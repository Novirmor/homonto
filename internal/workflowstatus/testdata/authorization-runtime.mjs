import assert from "node:assert/strict"
import { resolve, join } from "node:path"
import { pathToFileURL } from "node:url"
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises"
import { tmpdir } from "node:os"

const { createAuthorization } = await import(pathToFileURL(resolve(process.argv[2])).href)
const events = [], replies = []
let listener, stopped = false, result = "ask", identity = "same"
const state = await mkdtemp(join(tmpdir(), "homonto-v2-authorization-"))
const previousState = process.env.XDG_STATE_HOME
process.env.XDG_STATE_HOME = state
await mkdir(join(state, "opencode"))
await writeFile(join(state, "opencode", "service.json"), JSON.stringify({
  url: "http://127.0.0.1:1234/", pid: process.pid, version: "2.0.16", password: "sandbox",
}))
const originalFetch = globalThis.fetch
globalThis.fetch = async (url, options) => {
  const target = new URL(url)
  assert.equal(target.origin, "http://127.0.0.1:1234")
  assert.equal(options.headers.authorization, "Basic " + Buffer.from("opencode:sandbox").toString("base64"))
  assert.equal(options.redirect, "error")
  const reply = (body, status = 200) => new Response(body === undefined ? null : JSON.stringify(body), {
    status, headers: { "content-type": "application/json" },
  })
  if (target.pathname === "/api/info") return reply({ version: "2.0.16", pid: process.pid })
  if (target.pathname === "/api/rpc/homonto.workflow/identity") {
    assert.equal(target.searchParams.get("location[directory]"), "/workspace")
    return reply({ output: { token: identity } })
  }
  const permission = /^\/api\/session\/ses_one\/permission$/.exec(target.pathname)
  if (permission) {
    const input = JSON.parse(options.body)
    assert.equal(input.action, "shell")
    assert.deepEqual(input.resources, ["git status"])
    assert.deepEqual(input.save, [])
    assert.deepEqual(input.source, { type: "tool", messageID: "msg", id: "call" })
    events.push(input)
    return reply({ data: { id: input.id, effect: result } })
  }
  if (/^\/api\/session\/ses_one\/permission\/per_[\w-]+\/reply$/.test(target.pathname)) {
    replies.push({ requestID: target.pathname.split("/").at(-2), ...JSON.parse(options.body) })
    return reply(undefined, 204)
  }
  throw new Error("unexpected OpenCode request: " + target.pathname)
}
const ctx = {
  app: { version: "2.0.16" }, location: { directory: "/workspace", project: { id: "project" } },
  event: { subscribe: ({ signal }) => ({ async *[Symbol.asyncIterator]() {
    while (!stopped) {
      const event = await new Promise(resolve => {
        listener = resolve
        signal.addEventListener("abort", () => { stopped = true; resolve(undefined) }, { once: true })
      })
      if (event) yield event
    }
  } }) },
}
const call = (signal = new AbortController().signal) => ({ sessionID: "ses_one", messageID: "msg", id: "call", agent: "build", signal })
const request = { permission: "bash", patterns: ["git status"], metadata: { tool: "publish" } }
async function until(check) {
  for (let n = 0; n < 100; n++) { if (check()) return; await new Promise(done => setTimeout(done, 1)) }
  assert.fail("permission request did not arrive")
}
try {
  const auth = createAuthorization(ctx, "same")
  identity = "other"
  await assert.rejects(() => auth.ask(call(), request), /does not host/)
  assert.equal(events.length, 0)
  identity = "same"
  result = "deny"
  await assert.rejects(() => auth.ask(call(), request), /denied/)
  result = "allow"
  await auth.ask(call(), request)
  result = "ask"
  const approved = auth.ask(call(), request)
  await until(() => events.length === 3 && listener)
  const current = events.at(-1)
  const deliver = listener; listener = undefined
  deliver({ type: "permission.replied", data: { sessionID: "ses_one", requestID: current.id, reply: "once" } })
  await approved
  const rejected = auth.ask(call(), request)
  await until(() => events.length === 4 && listener)
  const wrong = events.at(-1)
  const mismatch = listener; listener = undefined
  mismatch({ type: "permission.replied", data: { sessionID: "another", requestID: wrong.id, reply: "once" } })
  const abort = new AbortController()
  const cancelled = auth.ask(call(abort.signal), request)
  await until(() => events.length === 5)
  abort.abort()
  await assert.rejects(cancelled, /aborted/)
  auth.dispose()
  await assert.rejects(rejected)
  assert.ok(replies.some(reply => reply.requestID === wrong.id && reply.decision === "reject"))
  assert.ok(replies.some(reply => reply.requestID === events.at(-1).id && reply.decision === "reject"))
} finally {
  globalThis.fetch = originalFetch
  if (previousState === undefined) delete process.env.XDG_STATE_HOME
  else process.env.XDG_STATE_HOME = previousState
  await rm(state, { recursive: true })
}
