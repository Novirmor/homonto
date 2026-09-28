import { randomUUID } from "node:crypto"
import { readFile } from "node:fs/promises"
import { homedir } from "node:os"
import { join } from "node:path"
import type { Context } from "@opencode/plugin/promise/plugin"

type Invocation = { sessionID: string; messageID: string; id: string; agent: string; signal: AbortSignal }
type Request = { permission: string; patterns: string[]; metadata: Record<string, string> }

function failure(code: string, message: string) {
  return Object.assign(new Error(`[${code}] ${message}`), { code })
}

async function interruptible<T>(work: () => Promise<T>, signal: AbortSignal): Promise<T> {
  let stop!: () => void
  const interrupted = new Promise<never>((_resolve, reject) => {
    stop = () => reject(new Error("operation aborted"))
    signal.addEventListener("abort", stop, { once: true })
  })
  try {
    signal.throwIfAborted()
    return await Promise.race([work(), interrupted])
  } finally {
    signal.removeEventListener("abort", stop)
  }
}

async function service(ctx: Context, signal: AbortSignal) {
  const path = join(process.env.XDG_STATE_HOME ?? join(homedir(), ".local", "state"), "opencode", "service.json")
  const deadline = new AbortController()
  const reading = AbortSignal.any([signal, deadline.signal])
  const timer = setTimeout(() => deadline.abort(), 5_000)
  timer.unref?.()
  let text: string
  try {
    text = await interruptible(() => readFile(path, { encoding: "utf8", signal: reading }), reading)
    reading.throwIfAborted()
  } catch (error) {
    if (deadline.signal.aborted && !signal.aborted) {
      throw failure("service_metadata_timeout", "OpenCode service metadata read exceeded 5 seconds; check the service and restart OpenCode before retrying")
    }
    if (error && typeof error === "object" && "code" in error && error.code === "ENOENT") {
      throw failure("service_metadata_missing", "OpenCode service metadata is missing; restart OpenCode in the selected workspace")
    }
    throw failure("service_metadata_unavailable", "OpenCode service metadata cannot be read; restart OpenCode in the selected workspace")
  } finally {
    clearTimeout(timer)
    deadline.abort()
  }
  signal.throwIfAborted()
  const invalid = () => failure("service_metadata_invalid", "OpenCode service metadata is invalid; restart OpenCode in the selected workspace")
  let value: unknown
  try { value = JSON.parse(text) } catch { throw invalid() }
  if (!value || typeof value !== "object" || !("url" in value) || typeof value.url !== "string" ||
      !("pid" in value) || !Number.isSafeInteger(value.pid) || (value.pid as number) <= 0 ||
      !("version" in value) || typeof value.version !== "string" || !value.version ||
      ("password" in value && typeof value.password !== "string")) throw invalid()
  const mismatch = () => failure("service_binding_mismatch", "OpenCode service binding changed; restart OpenCode in the selected workspace")
  if (value.version !== ctx.app.version) throw mismatch()
  let url: URL
  try { url = new URL(value.url) } catch { throw invalid() }
  if (url.protocol !== "http:" || !["127.0.0.1", "[::1]"].includes(url.hostname) ||
      url.username || url.password || url.pathname !== "/" || url.search || url.hash) throw invalid()
  const password = "password" in value ? value.password : undefined
  const authorization = typeof password === "string"
    ? "Basic " + Buffer.from("opencode:" + password).toString("base64") : undefined
  const headers = { ...(authorization ? { authorization } : {}), "content-type": "application/json" }
  async function request(path: string, body?: unknown, requestSignal: AbortSignal = signal) {
    const timeout = AbortSignal.timeout(5_000)
    const bounded = AbortSignal.any([requestSignal, timeout])
    try {
      return await interruptible(async () => {
        const response = await fetch(new URL(path, url), { method: body === undefined ? "GET" : "POST", headers,
          ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: bounded, redirect: "error" })
        bounded.throwIfAborted()
        if (response.status === 204) return undefined
        if (response.status !== 200 || !response.headers.get("content-type")?.includes("application/json")) {
          throw new Error("service request refused")
        }
        const text = await response.text()
        bounded.throwIfAborted()
        if (text.length > 65536) throw new Error("service response too large")
        return JSON.parse(text) as unknown
      }, bounded)
    } catch {
      if (timeout.aborted && !requestSignal.aborted) {
        throw failure("service_timeout", "OpenCode permission-service request exceeded 5 seconds; check the service and retry")
      }
      throw failure("service_unavailable", "OpenCode permission-service request failed; check the service and restart OpenCode before retrying")
    }
  }
  const info = await request("/api/info")
  if (!info || typeof info !== "object" || !("pid" in info) || info.pid !== value.pid ||
      !("version" in info) || info.version !== ctx.app.version) throw mismatch()
  return { request }
}

export function createAuthorization(ctx: Context, token: string) {
  const lifetime = new AbortController()
  const pending = new Map<string, { sessionID: string; reply: (value: string) => void }>()
  let disposed = false, listening = true
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
      listening = false
      lifetime.abort()
    }
  })()

  async function ask(call: Invocation, input: Request, report: (stage: string) => void = () => {}) {
    const signal = AbortSignal.any([call.signal, lifetime.signal])
    const stopped = () => disposed || call.signal.aborted
      ? failure("invocation_cancelled", "permission request aborted or plugin disposed; retry in a new authorized invocation")
      : failure("permission_listener_unavailable", "OpenCode permission listener stopped; restart OpenCode before requesting permission again")
    const active = () => { if (signal.aborted || !listening) throw stopped() }
    const progress = (stage: string) => {
      try { void Promise.resolve(report(stage)).catch(() => {}) } catch {}
    }
    active()
    if (!call.id || !call.messageID || !call.sessionID || !call.agent ||
        !input.patterns.length || input.patterns.some(p => !p)) {
      throw failure("permission_context_unavailable", "permission context unavailable; retry from a live OpenCode tool invocation")
    }
    try {
      progress("Validating permission service")
      active()
      const client = await interruptible(() => service(ctx, signal), signal)
      active()
      const endpoint = new URL("/api/rpc/homonto.workflow/identity", "http://127.0.0.1")
      endpoint.searchParams.set("location[directory]", ctx.location.directory)
      const identity = await client.request(endpoint.pathname + endpoint.search, { input: {} })
      active()
      if (!identity || typeof identity !== "object" || !("output" in identity) || !identity.output ||
          typeof identity.output !== "object" || !("token" in identity.output) || identity.output.token !== token) {
        throw failure("service_binding_mismatch", "OpenCode service does not host this workflow plugin; restart OpenCode in the selected workspace")
      }
      const id = "per_" + randomUUID()
      if (pending.size >= 4096) throw failure("permission_capacity", "permission request capacity reached; finish pending permission requests before retrying")
      let resolve!: (value: string) => void
      const answer = new Promise<string>(done => { resolve = done })
      pending.set(id, { sessionID: call.sessionID, reply: resolve })
      const timeout = AbortSignal.timeout(5 * 60 * 1000)
      const waiting = AbortSignal.any([signal, timeout])
      let requested = false, answered = false
      try {
        progress("Requesting host permission")
        active()
        requested = true
        const response = await client.request(`/api/session/${encodeURIComponent(call.sessionID)}/permission`, {
          id, sessionID: call.sessionID, agent: call.agent,
          action: input.permission === "bash" ? "shell" : input.permission,
          resources: input.patterns, save: [], metadata: input.metadata,
          source: { type: "tool", messageID: call.messageID, id: call.id },
        }, waiting)
        active()
        const result = response && typeof response === "object" && "data" in response ? response.data : undefined
        if (!result || typeof result !== "object" || !("id" in result) || result.id !== id ||
            !("effect" in result) || !["allow", "deny", "ask"].includes(result.effect as string)) {
          throw failure("permission_response_invalid", "invalid host permission response; restart OpenCode before retrying")
        }
        answered = result.effect !== "ask"
        if (result.effect === "deny") throw failure("permission_denied", "host permission denied")
        if (result.effect === "allow") return
        progress("Waiting for permission answer")
        const reply = await interruptible(() => answer, waiting)
        active()
        answered = true
        if (reply !== "once" && reply !== "always") throw failure("permission_denied", "host permission declined")
      } catch (error) {
        active()
        if (timeout.aborted) throw failure("permission_timeout", "permission answer wait expired after 5 minutes; retry and answer the host permission request")
        throw error
      } finally {
        pending.delete(id)
        if (requested && !answered) {
          await client.request(`/api/session/${encodeURIComponent(call.sessionID)}/permission/${encodeURIComponent(id)}/reply`,
            { decision: "reject" }, AbortSignal.timeout(2_000)).catch(() => {})
        }
      }
    } catch (error) {
      active()
      throw error
    }
  }

  return { ask, dispose() { disposed = true; lifetime.abort(); void stream.return?.().catch(() => {}) } }
}
