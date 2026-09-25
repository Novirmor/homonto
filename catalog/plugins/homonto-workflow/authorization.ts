import { randomUUID } from "node:crypto"
import { readFile } from "node:fs/promises"
import { homedir } from "node:os"
import { join } from "node:path"
import type { Context } from "@opencode/plugin/promise/plugin"

type Invocation = { sessionID: string; messageID: string; id: string; agent: string; signal: AbortSignal }
type Request = { permission: string; patterns: string[]; metadata: Record<string, string> }

async function service(ctx: Context, signal: AbortSignal) {
  const path = join(process.env.XDG_STATE_HOME ?? join(homedir(), ".local", "state"), "opencode", "service.json")
  const value: unknown = JSON.parse(await readFile(path, { encoding: "utf8", signal }))
  if (!value || typeof value !== "object" || !("url" in value) || typeof value.url !== "string" ||
      !("pid" in value) || !Number.isSafeInteger(value.pid) || (value.pid as number) <= 0 ||
      !("version" in value) || value.version !== ctx.app.version ||
      ("password" in value && typeof value.password !== "string")) throw new Error("bound OpenCode service unavailable")
  const url = new URL(value.url)
  if (url.protocol !== "http:" || !["127.0.0.1", "[::1]"].includes(url.hostname) ||
      url.username || url.password || url.pathname !== "/" || url.search || url.hash) throw new Error("unsupported service endpoint")
  const password = "password" in value ? value.password : undefined
  const authorization = typeof password === "string"
    ? "Basic " + Buffer.from("opencode:" + password).toString("base64") : undefined
  const headers = { ...(authorization ? { authorization } : {}), "content-type": "application/json" }
  async function request(path: string, body?: unknown, requestSignal: AbortSignal = signal) {
    const response = await fetch(new URL(path, url), { method: body === undefined ? "GET" : "POST", headers,
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.any([requestSignal, AbortSignal.timeout(5_000)]), redirect: "error" })
    if (response.status === 204) return undefined
    if (response.status !== 200 || !response.headers.get("content-type")?.includes("application/json")) {
      throw new Error("OpenCode service request refused")
    }
    const text = await response.text()
    if (text.length > 65536) throw new Error("OpenCode service response too large")
    return JSON.parse(text) as unknown
  }
  const info = await request("/api/info")
  if (!info || typeof info !== "object" || !("pid" in info) || info.pid !== value.pid ||
      !("version" in info) || info.version !== ctx.app.version) throw new Error("registered OpenCode service changed")
  return { request }
}

export function createAuthorization(ctx: Context, token: string) {
  const lifetime = new AbortController()
  const pending = new Map<string, { sessionID: string; reply: (value: string) => void }>()
  let disposed = false
  const stream = ctx.event.subscribe({ signal: lifetime.signal })[Symbol.asyncIterator]()
  void (async () => {
    try {
      while (!disposed) {
        const { value, done } = await stream.next()
        if (done) break
        if (value.type !== "permission.replied") continue
        const data = value.data
        if (!data || typeof data.requestID !== "string" || typeof data.sessionID !== "string") continue
        const request = pending.get(data.requestID)
        if (request?.sessionID === data.sessionID) request.reply(data.reply)
      }
    } catch {} finally {
      for (const [id, request] of pending) { pending.delete(id); request.reply("reject") }
    }
  })()

  async function ask(call: Invocation, input: Request) {
    if (disposed || call.signal.aborted || !call.id || !call.messageID || !call.sessionID || !call.agent ||
        !input.patterns.length || input.patterns.some(p => !p)) throw new Error("permission context unavailable")
    const client = await service(ctx, call.signal)
    const endpoint = new URL("/api/rpc/homonto.workflow/identity", "http://127.0.0.1")
    endpoint.searchParams.set("location[directory]", ctx.location.directory)
    const identity = await client.request(endpoint.pathname + endpoint.search, { input: {} })
    if (!identity || typeof identity !== "object" || !("output" in identity) || !identity.output ||
        typeof identity.output !== "object" || !("token" in identity.output) || identity.output.token !== token ||
        disposed || call.signal.aborted) {
      throw new Error("OpenCode service does not host this workflow plugin")
    }
    const id = "per_" + randomUUID()
    if (pending.size >= 4096) throw new Error("permission request capacity reached")
    let resolve!: (value: string) => void
    const answer = new Promise<string>(done => { resolve = done })
    pending.set(id, { sessionID: call.sessionID, reply: resolve })
    const timeout = AbortSignal.timeout(5 * 60 * 1000)
    const signal = AbortSignal.any([call.signal, lifetime.signal, timeout])
    let stop!: () => void
    const interrupted = new Promise<never>((_done, reject) => {
      stop = () => reject(new Error("permission request aborted or expired"))
      signal.addEventListener("abort", stop, { once: true })
    })
    let asked = false, answered = false
    try {
      if (signal.aborted) throw new Error("permission request aborted")
      const response = await client.request(`/api/session/${encodeURIComponent(call.sessionID)}/permission`, {
        id, sessionID: call.sessionID, agent: call.agent,
        action: input.permission === "bash" ? "shell" : input.permission,
        resources: input.patterns, save: [], metadata: input.metadata,
        source: { type: "tool", messageID: call.messageID, id: call.id },
      }, signal)
      const result = response && typeof response === "object" && "data" in response ? response.data : undefined
      if (!result || typeof result !== "object" || !("id" in result) || !("effect" in result)) throw new Error("invalid permission response")
      if (result.id !== id || result.effect === "deny") throw new Error("permission denied")
      if (result.effect === "allow") return
      if (result.effect !== "ask") throw new Error("invalid permission decision")
      asked = true
      const reply = await Promise.race([answer, interrupted])
      answered = true
      if (reply !== "once" && reply !== "always") throw new Error("permission declined")
    } finally {
      pending.delete(id)
      signal.removeEventListener("abort", stop)
      if (asked && !answered) {
        await client.request(`/api/session/${encodeURIComponent(call.sessionID)}/permission/${encodeURIComponent(id)}/reply`,
          { decision: "reject" }, AbortSignal.timeout(2_000)).catch(() => {})
      }
    }
  }

  return { ask, dispose() { disposed = true; lifetime.abort(); void stream.return?.() } }
}
