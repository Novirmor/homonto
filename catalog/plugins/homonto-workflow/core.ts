// Harness shim client for the Go runtime core (ADR 0090, M4.1). One client
// per core binding — the workflow core (status, handoff, handback) or one
// hosting provider's draft/publication core — speaking MCP-shaped JSON-RPC 2.0
// over the stdio of `homonto runtime serve`. The client relays tool calls,
// executes the core's commands through the plugin's own runner, forwards
// native permission requests and progress, and fails closed on skew.
import { spawn } from "node:child_process"
import { isAbsolute, normalize } from "node:path"
import { requireLiveContext } from "./compat.ts"
import type { ToolContext } from "./compat.ts"

export type Run = (argv: string[], options?: { signal?: AbortSignal; stdin?: string; timeout?: number }) => Promise<string>
type Pending = { resolve(v: unknown): void; reject(e: Error): void }
type Call = { ctx: ToolContext; runs: Map<number, AbortController> }
export type Definition = { description: string; args: Record<string, unknown> }
export const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v)
export const pathOK = (v: unknown): v is string => typeof v === "string" && isAbsolute(v) && normalize(v) === v && !/[\x00-\x1f\x7f*?]/.test(v)
export function check(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message) }

function canonical(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`
  if (record(v)) return `{${Object.keys(v).sort().map(k => `${JSON.stringify(k)}:${canonical(v[k])}`).join(",")}}`
  return JSON.stringify(v)
}

export type CoreOptions = {
  run: Run; configPath: string; provider: string; label: string; definitions: Record<string, Definition>
  command?: string[]; onNotification?: (method: string, params: Record<string, unknown>) => void
}

export function createCore({ run, configPath, provider, label, definitions, command = ["homonto", "runtime", "serve"], onNotification }: CoreOptions) {
  check(typeof run === "function" && pathOK(configPath), "runner and bound absolute configPath are required")
  const LABEL = label
  const lifetime = new AbortController()
  const pending = new Map<number, Pending>()
  const calls = new Map<number, Call>()
  let nextID = 0, disposed = false, failure: Error | undefined
  const child = spawn(command[0], command.slice(1), { stdio: ["pipe", "pipe", "ignore"] })
  child.stdin.on("error", () => {})
  // Hold the event loop only while the core owes an answer; an idle runtime
  // must not keep the host process alive.
  const handles = [child, child.stdout, child.stdin] as unknown as { ref?(): void; unref?(): void }[]
  const hold = () => { for (const h of handles) (pending.size ? h.ref : h.unref)?.call(h) }

  const unavailable = () => new Error(`[runtime_unavailable] ${LABEL} runtime is unavailable (homonto runtime serve); install a homonto binary that matches the workflow plugin and restart OpenCode`)
  function fail(error: Error) {
    failure ??= error
    for (const p of pending.values()) p.reject(failure)
    pending.clear()
    hold()
  }
  child.on("error", () => fail(unavailable()))
  child.on("exit", () => fail(unavailable()))

  function send(message: Record<string, unknown>) {
    if (failure || child.stdin.destroyed) return
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", ...message }) + "\n")
  }
  const now = () => ({ now: Date.now() })
  function request(method: string, params: Record<string, unknown>): { id: number; done: Promise<unknown> } {
    const id = ++nextID
    const done = new Promise<unknown>((resolve, reject) => {
      if (failure) return reject(failure)
      pending.set(id, { resolve, reject })
      hold()
      send({ id, method, params })
    })
    return { id, done }
  }
  const notify = (method: string, params: Record<string, unknown>) => send({ method, params })

  async function answer(id: unknown, work: () => Promise<Record<string, unknown>>) {
    try {
      const result = await work()
      send({ id, result: { ...result, _meta: now() } })
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      send({ id, error: { code: -32000, message, data: now() } })
    }
  }
  function callFor(params: Record<string, unknown>): Call {
    const c = calls.get(params.call as number)
    check(c, "unknown invocation")
    return c
  }
  function handle(message: Record<string, unknown>) {
    const params = record(message.params) ? message.params : {}
    if (message.method === "homonto/progress") {
      const c = calls.get(params.call as number)
      try { void Promise.resolve(c?.ctx.progress?.(params.stage as string)).catch(() => {}) } catch { }
      return
    }
    if (message.method === "notifications/cancelled") {
      for (const c of calls.values()) c.runs.get(params.requestId as number)?.abort()
      return
    }
    if (message.method === "homonto/run") {
      void answer(message.id, async () => {
        const c = callFor(params)
        check(!c.ctx.abort.aborted, "process aborted or disposed")
        const controller = new AbortController()
        c.runs.set(message.id as number, controller)
        try {
          const stdout = await run(params.argv as string[], {
            signal: AbortSignal.any([c.ctx.abort, lifetime.signal, controller.signal]), timeout: params.timeout as number,
            ...(typeof params.stdin === "string" ? { stdin: params.stdin } : {}),
          })
          return { stdout }
        } finally { c.runs.delete(message.id as number) }
      })
      return
    }
    if (message.method === "homonto/ask") {
      void answer(message.id, async () => {
        const c = callFor(params)
        await c.ctx.ask({ permission: params.permission as string, patterns: params.patterns as string[],
          always: params.always as string[], metadata: params.metadata as Record<string, unknown> })
        return {}
      })
      return
    }
    if (typeof message.method === "string" && message.id === undefined) {
      onNotification?.(message.method, params)
      return
    }
    if (message.method === undefined && typeof message.id === "number") {
      const p = pending.get(message.id)
      if (!p) return
      pending.delete(message.id)
      hold()
      if (record(message.error)) p.reject(new Error(String(message.error.message)))
      else p.resolve(message.result)
    }
  }
  let buffer = ""
  child.stdout.setEncoding("utf8")
  child.stdout.on("data", (chunk: string) => {
    buffer += chunk
    for (let n = buffer.indexOf("\n"); n >= 0; n = buffer.indexOf("\n")) {
      const line = buffer.slice(0, n)
      buffer = buffer.slice(n + 1)
      let message: unknown
      try { message = JSON.parse(line) } catch { fail(unavailable()); continue }
      if (record(message)) handle(message)
    }
  })

  const ready = (async () => {
    await request("initialize", { protocolVersion: "2025-06-18", clientInfo: { name: "homonto-workflow", version: "1" },
      capabilities: { experimental: { "homonto/runtime": { configPath, provider } } }, _meta: now() }).done
    notify("notifications/initialized", {})
    const listed = await request("tools/list", { _meta: now() }).done
    const expected = Object.entries(definitions).map(([name, d]) => ({ name, description: d.description,
      inputSchema: { type: "object", properties: d.args, required: Object.keys(d.args), additionalProperties: false } }))
    check(record(listed) && canonical(listed.tools) === canonical(expected), `[runtime_mismatch] ${LABEL} runtime tools differ from the workflow plugin; install matching homonto and plugin versions`)
  })()
  ready.catch(() => {})

  const disposedError = () => new Error(`[plugin_disposed] ${LABEL} plugin disposed; restart OpenCode in a compatible live session and stage a new draft`)

  async function execute(name: string, args: unknown, ctx: ToolContext): Promise<string> {
    requireLiveContext(ctx)
    check(!disposed, disposedError().message)
    await ready
    check(!disposed, disposedError().message)
    const { id, done } = request("tools/call", { name, arguments: args,
      _meta: { ...now(), sessionID: ctx.sessionID, messageID: ctx.messageID, agent: ctx.agent } })
    const c: Call = { ctx, runs: new Map() }
    calls.set(id, c)
    const cancel = () => notify("notifications/cancelled", { requestId: id, reason: "aborted", _meta: now() })
    ctx.abort.addEventListener("abort", cancel, { once: true })
    try {
      const result = await done
      check(record(result) && Array.isArray(result.content) && record(result.content[0]) && typeof result.content[0].text === "string", "invalid runtime result")
      if (result.isError === true) throw new Error(result.content[0].text as string)
      return result.content[0].text as string
    } finally {
      ctx.abort.removeEventListener("abort", cancel)
      calls.delete(id)
    }
  }

  // A plain request outside any tool call (handback queries). It waits for
  // the core to be ready and fails closed when it is not.
  async function query(method: string, params: Record<string, unknown>): Promise<Record<string, unknown>> {
    check(!disposed, disposedError().message)
    await ready
    const result = await request(method, { ...params, _meta: now() }).done
    check(record(result), "invalid runtime result")
    return result
  }

  function dispose() {
    if (disposed) return
    disposed = true
    lifetime.abort()
    notify("homonto/dispose", { _meta: now() })
    fail(disposedError())
    child.stdin.end()
  }

  return {
    tools: Object.fromEntries(Object.entries(definitions).map(([name, d]) => [name, { description: d.description, args: d.args,
      execute: (args: unknown, ctx: ToolContext) => execute(name, args, ctx) }])) as
      Record<string, { description: string; args: Record<string, unknown>; execute(args: unknown, ctx: ToolContext): Promise<string> }>,
    notify: (method: string, params: Record<string, unknown>) => { if (!disposed) notify(method, { ...params, _meta: now() }) },
    query,
    dispose,
    get disposed() { return disposed },
  }
}
