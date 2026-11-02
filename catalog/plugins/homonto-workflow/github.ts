// Harness shim for the hosting draft/publication core (ADR 0090). The core —
// sessions, drafts, approval correlation, TTL, capacities, freshness and
// uncertain-send reconciliation, and the GitHub provider — runs in Go behind
// `homonto runtime serve` (MCP-shaped JSON-RPC over stdio). This file only
// relays tool calls, harness events, native permission requests and progress,
// executes the core's commands through the plugin's own runner, and mirrors
// per-session draft state for synchronous context injection.
import { spawn } from "node:child_process"
import { isAbsolute, normalize } from "node:path"
import { requireLiveContext } from "./compat.ts"
import type { ToolContext } from "./compat.ts"

type Run = (argv: string[], options?: { signal?: AbortSignal; stdin?: string; timeout?: number }) => Promise<string>
type Pending = { resolve(v: unknown): void; reject(e: Error): void }
type Call = { ctx: ToolContext; runs: Map<number, AbortController> }
type SessionState = { sessionID: string; deleted: boolean; drafts: { draftID: string; expiresAt: number; items: { itemID: string; status: string }[] }[] }

export const HOSTS = { github: "GitHub", gitlab: "GitLab", bitbucket: "Bitbucket" } as const
export type Host = keyof typeof HOSTS
const RELAYED = new Set(["session.deleted", "form.created", "form.replied", "form.cancelled", "question.asked", "question.rejected", "question.replied"])
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v)
const pathOK = (v: unknown): v is string => typeof v === "string" && isAbsolute(v) && normalize(v) === v && !/[\x00-\x1f\x7f*?]/.test(v)
function check(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message) }

const draftArgs = { items: { type: "array", minItems: 1, maxItems: 10, items: { type: "object", additionalProperties: false,
  required: ["kind", "url", "body", "baseOID", "headOID", "reviewEvent"], properties: {
    kind: { type: "string", enum: ["issue_comment", "pr_comment", "pr_review"] }, url: { type: "string" }, body: { type: "string", maxLength: 8192 },
    baseOID: { type: "string" }, headOID: { type: "string" }, reviewEvent: { type: "string", enum: ["", "COMMENT", "REQUEST_CHANGES"] },
  } } } }
const idArgs = { draftID: { type: "string" } }
type Definition = { description: string; args: Record<string, unknown> }
// Mirrors hostruntime.ToolDefinitions; the shim checks the core's tools/list against it.
function definitionsFor(host: Host): Record<string, Definition> {
  const label = HOSTS[host]
  return {
    [`homonto_${host}_draft`]: { description: `Stage an immutable, memory-only ${label} batch. PR bodies gain reviewed OIDs before preview. Formal reviews support COMMENT/REQUEST_CHANGES only. Submit the returned question arguments unchanged to native question; approval is workflow confirmation, not human-only attestation. New batches invalidate earlier session drafts.`, args: draftArgs },
    [`homonto_${host}_status`]: { description: "Read the session's draft statuses, exact full preview and native question arguments.", args: idArgs },
    [`homonto_${host}_publish`]: { description: "Publish only items approved through correlated native question events, after permission and fresh scope/actor/destination checks. Uncertain outcomes are never blindly retried.", args: idArgs },
  }
}

function canonical(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`
  if (record(v)) return `{${Object.keys(v).sort().map(k => `${JSON.stringify(k)}:${canonical(v[k])}`).join(",")}}`
  return JSON.stringify(v)
}

type Options = { run: Run; configPath: string; command?: string[] }

export function createGithubDrafts(options: Options) { return createHostDrafts({ ...options, host: "github" }) }

export function createHostDrafts({ run, configPath, host, command = ["homonto", "runtime", "serve"] }: Options & { host: Host }) {
  check(typeof run === "function" && pathOK(configPath), "runner and bound absolute configPath are required")
  check(Object.hasOwn(HOSTS, host), "unknown hosting provider")
  const PROVIDER = host, LABEL = HOSTS[host], definitions = definitionsFor(host)
  const lifetime = new AbortController()
  const pending = new Map<number, Pending>()
  const calls = new Map<number, Call>()
  const states = new Map<string, SessionState>()
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
    if (message.method === "homonto/state") {
      for (const s of (Array.isArray(params.sessions) ? params.sessions : []) as SessionState[]) states.set(s.sessionID, s)
      return
    }
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
      capabilities: { experimental: { "homonto/runtime": { configPath, provider: PROVIDER } } }, _meta: now() }).done
    notify("notifications/initialized", {})
    const listed = await request("tools/list", { _meta: now() }).done
    const expected = Object.entries(definitions).map(([name, d]) => ({ name, description: d.description,
      inputSchema: { type: "object", properties: d.args, required: Object.keys(d.args), additionalProperties: false } }))
    check(record(listed) && canonical(listed.tools) === canonical(expected), `[runtime_mismatch] ${LABEL} runtime tools differ from the workflow plugin; install matching homonto and plugin versions`)
  })()
  ready.catch(() => {})

  async function execute(name: string, args: unknown, ctx: ToolContext): Promise<string> {
    requireLiveContext(ctx)
    check(!disposed, `[plugin_disposed] ${LABEL} plugin disposed; restart OpenCode in a compatible live session and stage a new draft`)
    await ready
    check(!disposed, `[plugin_disposed] ${LABEL} plugin disposed; restart OpenCode in a compatible live session and stage a new draft`)
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

  function dispose() {
    if (disposed) return
    disposed = true
    lifetime.abort()
    notify("homonto/dispose", { _meta: now() })
    fail(new Error(`[plugin_disposed] ${LABEL} plugin disposed; restart OpenCode in a compatible live session and stage a new draft`))
    child.stdin.end()
  }
  function before(input: { tool: string; sessionID: string; callID: string }, output: { args: unknown }) {
    if (disposed || input.tool !== "question") return
    notify("homonto/before", { tool: input.tool, sessionID: input.sessionID, callID: input.callID, args: output.args, _meta: now() })
  }
  function event({ event: e }: { event: { type: string; properties?: unknown; data?: unknown } }) {
    if (e.type === "server.instance.disposed") { dispose(); return }
    if (disposed || !RELAYED.has(e.type)) return
    notify("homonto/event", { event: { type: e.type, properties: e.properties, data: e.data }, _meta: now() })
  }

  return {
    host,
    tools: Object.fromEntries(Object.entries(definitions).map(([name, d]) => [name, { description: d.description, args: d.args,
      execute: (args: unknown, ctx: ToolContext) => execute(name, args, ctx) }])) as
      Record<string, { description: string; args: Record<string, unknown>; execute(args: unknown, ctx: ToolContext): Promise<string> }>,
    event,
    before,
    dispose,
    context(sessionID: string): string {
      const s = states.get(sessionID)
      if (disposed || s?.deleted) return `${LABEL} drafts: disposed`
      const at = Date.now()
      return JSON.stringify({ drafts: (s?.drafts ?? []).map(d => ({ draftID: d.draftID, items: d.items.map(i => ({ itemID: i.itemID,
        status: at >= d.expiresAt && (i.status === "pending" || i.status === "approved") ? "expired" : i.status })) })) })
    },
  }
}
