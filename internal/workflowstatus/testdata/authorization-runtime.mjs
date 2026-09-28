import assert from "node:assert/strict"
import { resolve, join } from "node:path"
import { pathToFileURL } from "node:url"
import fs, { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises"
import { syncBuiltinESMExports } from "node:module"
import { getEventListeners } from "node:events"
import { tmpdir } from "node:os"

const { createAuthorization } = await import(pathToFileURL(resolve(process.argv[2])).href)
const events = [], replies = [], requests = [], timeouts = [], authorizations = []
let result = "ask", identity = "same", intercept
const state = await mkdtemp(join(tmpdir(), "homonto-v2-authorization-"))
const previousState = process.env.XDG_STATE_HOME
process.env.XDG_STATE_HOME = state
await mkdir(join(state, "opencode"))
const metadataPath = join(state, "opencode", "service.json")
const metadata = {
  url: "http://127.0.0.1:1234/", pid: process.pid, version: "2.0.16", password: "sandbox",
}
const saveMetadata = (value = metadata) => writeFile(metadataPath, JSON.stringify(value))
await saveMetadata()
const originalFetch = globalThis.fetch
const originalTimeout = AbortSignal.timeout
AbortSignal.timeout = ms => {
  const controller = new AbortController()
  timeouts.push({ ms, controller })
  return controller.signal
}
const reply = (body, status = 200) => new Response(body === undefined ? null : JSON.stringify(body), {
  status, headers: { "content-type": "application/json" },
})
globalThis.fetch = async (url, options) => {
  const target = new URL(url)
  requests.push({ target, options })
  assert.equal(target.origin, "http://127.0.0.1:1234")
  assert.equal(options.headers.authorization, "Basic " + Buffer.from("opencode:sandbox").toString("base64"))
  assert.equal(options.redirect, "error")
  assert.equal(options.signal.aborted, false)
  const intercepted = intercept?.(target, options)
  if (intercepted !== undefined) return intercepted
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
function authorization() {
  let listener
  const ctx = {
    app: { version: "2.0.16" }, location: { directory: "/workspace", project: { id: "project" } },
    event: { subscribe: ({ signal }) => ({ async *[Symbol.asyncIterator]() {
      while (!signal.aborted) {
        let stop
        const event = await new Promise(resolve => {
          listener = resolve
          stop = () => resolve(undefined)
          signal.addEventListener("abort", stop, { once: true })
        })
        signal.removeEventListener("abort", stop)
        listener = undefined
        if (!event) return
        if (event instanceof Error) throw event
        yield event
      }
    } }) },
  }
  const auth = createAuthorization(ctx, "same")
  authorizations.push(auth)
  return { ...auth, async deliver(event) {
    await until(() => listener)
    const send = listener
    listener = undefined
    send(event)
  } }
}
const call = (signal = new AbortController().signal) => ({ sessionID: "ses_one", messageID: "msg", id: "call", agent: "build", signal })
const request = { permission: "bash", patterns: ["git status"], metadata: { tool: "publish" } }
async function until(check) {
  for (let n = 0; n < 500; n++) { if (check()) return; await new Promise(done => setTimeout(done, 1)) }
  assert.fail("authorization condition did not arrive")
}
const safe = error => {
  assert.doesNotMatch(String(error) + JSON.stringify(error), /sandbox|sensitive-marker|127\.0\.0\.1|1234|Basic /)
}
const reason = code => error => {
  safe(error)
  assert.equal(error.code, code)
  assert.ok(error.message.startsWith(`[${code}]`))
  return true
}
async function rejectedSoon(promise, code) {
  let settled = false
  const checked = assert.rejects(promise, reason(code)).finally(() => { settled = true })
  await until(() => settled)
  await checked
}
const stages = ["Validating permission service", "Requesting host permission", "Waiting for permission answer"]
async function waiting(auth, signal, report = () => {}) {
  const seen = []
  const promise = auth.ask(call(signal), request, stage => { seen.push(stage); return report(stage) })
  promise.catch(() => {})
  await until(() => seen.includes(stages[2]))
  assert.deepEqual(seen, stages)
  return { promise, id: events.at(-1).id }
}
const answer = (auth, id, value = "once", sessionID = "ses_one") => auth.deliver({
  type: "permission.replied", data: { sessionID, requestID: id, reply: value },
})
try {
  const auth = authorization()
  await rm(metadataPath)
  await assert.rejects(auth.ask(call(), request), reason("service_metadata_missing"))
  await writeFile(metadataPath, '{"sensitive-marker":')
  await assert.rejects(auth.ask(call(), request), reason("service_metadata_invalid"))
  for (const value of [null, {}, { ...metadata, password: 123 }, { ...metadata, pid: 0 },
    { ...metadata, version: undefined }, { ...metadata, url: "sensitive-marker" },
    { ...metadata, url: "http://user:sensitive-marker@127.0.0.1:1234/" },
    { ...metadata, url: "https://sensitive-marker.invalid/" },
    { ...metadata, url: "http://127.0.0.1:1234/?sensitive-marker" }]) {
    await saveMetadata(value)
    await assert.rejects(auth.ask(call(), request), reason("service_metadata_invalid"))
  }
  assert.equal(requests.length, 0)
  await saveMetadata({ ...metadata, version: "sensitive-marker" })
  await assert.rejects(auth.ask(call(), request), reason("service_binding_mismatch"))
  await saveMetadata()
  intercept = target => target.pathname === "/api/info" ? reply({ pid: process.pid + 1, version: "2.0.16" }) : undefined
  await assert.rejects(auth.ask(call(), request), reason("service_binding_mismatch"))
  intercept = () => { throw new Error("sensitive-marker http://127.0.0.1:1234/") }
  await assert.rejects(auth.ask(call(), request), reason("service_unavailable"))
  intercept = () => new Response("sensitive-marker", { headers: { "content-type": "application/json" } })
  await assert.rejects(auth.ask(call(), request), reason("service_unavailable"))
  intercept = undefined
  identity = "other"
  await assert.rejects(auth.ask(call(), request), reason("service_binding_mismatch"))
  assert.equal(events.length, 0)
  identity = "same"
  result = "deny"
  const deniedStages = []
  await assert.rejects(auth.ask(call(), request, stage => deniedStages.push(stage)), reason("permission_denied"))
  assert.deepEqual(deniedStages, stages.slice(0, 2))
  result = "allow"
  await auth.ask(call(), request)
  for (const report of [() => { throw new Error("sensitive-marker") },
    () => Promise.reject(new Error("sensitive-marker")), () => new Promise(() => {})]) {
    await auth.ask(call(), request, report)
  }
  result = "ask"
  const approved = await waiting(auth)
  let approvedSettled = false
  approved.promise.finally(() => { approvedSettled = true })
  await answer(auth, approved.id, "once", "another")
  await answer(auth, "per_wrong")
  for (const timeout of timeouts.filter(t => t.ms === 5000)) timeout.controller.abort()
  await new Promise(done => setTimeout(done, 5))
  assert.equal(approvedSettled, false)
  await answer(auth, approved.id)
  await approved.promise
  const always = await waiting(auth, undefined, () => Promise.reject(new Error("sensitive-marker")))
  await answer(auth, always.id, "always")
  await always.promise
  const denied = await waiting(auth)
  await answer(auth, denied.id, "reject")
  await rejectedSoon(denied.promise, "permission_denied")
  assert.equal(replies.length, 0)
  const rejected = await waiting(auth)
  const abort = new AbortController()
  const cancelled = await waiting(auth, abort.signal)
  abort.abort(new Error("sensitive-marker"))
  await rejectedSoon(cancelled.promise, "invocation_cancelled")
  assert.ok(replies.some(reply => reply.requestID === cancelled.id && reply.decision === "reject"))
  const retry = await waiting(auth)
  await answer(auth, retry.id)
  await retry.promise
  auth.dispose()
  await rejectedSoon(rejected.promise, "invocation_cancelled")
  assert.ok(replies.some(reply => reply.requestID === rejected.id && reply.decision === "reject"))
  await rejectedSoon(auth.ask(call(), request), "invocation_cancelled")

  for (const end of [undefined, new Error("sensitive-marker")]) {
    const dead = authorization()
    const first = await waiting(dead)
    const second = await waiting(dead)
    await dead.deliver(end)
    await rejectedSoon(first.promise, "permission_listener_unavailable")
    await rejectedSoon(second.promise, "permission_listener_unavailable")
    for (const { id } of [first, second]) {
      assert.ok(replies.some(reply => reply.requestID === id && reply.decision === "reject"))
    }
    const count = requests.length
    const progress = []
    await rejectedSoon(dead.ask(call(), request, stage => progress.push(stage)), "permission_listener_unavailable")
    await assert.rejects(dead.ask(call(), request), /restart OpenCode/)
    assert.equal(requests.length, count)
    assert.deepEqual(progress, [])
    dead.dispose()
  }

  for (const phase of ["/api/info", "/api/rpc/homonto.workflow/identity"]) {
    for (const stop of ["cancel", "dispose", "stream", "timeout"]) {
      const preflight = authorization()
      const controller = new AbortController()
      let release, requestSignal
      intercept = (target, options) => {
        if (target.pathname !== phase) return undefined
        requestSignal = options.signal
        return new Promise(resolve => { release = resolve })
      }
      const count = events.length
      const promise = preflight.ask(call(controller.signal), request)
      promise.catch(() => {})
      await until(() => release)
      const requestsBefore = requests.length
      if (stop === "cancel") controller.abort(new Error("sensitive-marker"))
      if (stop === "dispose") preflight.dispose()
      if (stop === "stream") await preflight.deliver(undefined)
      if (stop === "timeout") timeouts.at(-1).controller.abort()
      await rejectedSoon(promise, stop === "stream" ? "permission_listener_unavailable"
        : stop === "timeout" ? "service_timeout" : "invocation_cancelled")
      assert.equal(requestSignal.aborted, true)
      release(reply(phase === "/api/info" ? { pid: process.pid, version: "2.0.16" } : { output: { token: "same" } }))
      await new Promise(done => setTimeout(done, 5))
      assert.equal(events.length, count)
      assert.equal(requests.length, requestsBefore)
      intercept = undefined
      if (stop === "cancel" || stop === "timeout") {
        result = "allow"
        await preflight.ask(call(), request)
        result = "ask"
      }
      preflight.dispose()
    }
  }

  const originalReadFile = fs.readFile
  const originalSetTimeout = globalThis.setTimeout
  const originalClearTimeout = globalThis.clearTimeout
  const metadataTimers = []
  globalThis.setTimeout = (run, ms, ...args) => {
    if (ms !== 5000) return originalSetTimeout(run, ms, ...args)
    const timer = { run, cleared: false, unref() {} }
    metadataTimers.push(timer)
    return timer
  }
  globalThis.clearTimeout = timer => {
    if (metadataTimers.includes(timer)) timer.cleared = true
    else originalClearTimeout(timer)
  }
  try {
    for (const stop of ["timeout", "cancel", "dispose", "stream_end", "stream_throw"]) {
      const discovery = authorization()
      const controller = new AbortController()
      let release, readSignal
      fs.readFile = (path, options) => {
        assert.equal(path, metadataPath)
        assert.equal(options.encoding, "utf8")
        readSignal = options.signal
        return new Promise(resolve => { release = resolve })
      }
      syncBuiltinESMExports()
      const count = requests.length
      const timerCount = metadataTimers.length
      const seen = []
      const promise = discovery.ask(call(controller.signal), request, stage => seen.push(stage))
      promise.catch(() => {})
      await until(() => release)
      assert.equal(metadataTimers.length, timerCount + 1)
      const timer = metadataTimers.at(-1)
      assert.equal(timer.cleared, false)
      assert.equal(readSignal.aborted, false)
      assert.ok(getEventListeners(readSignal, "abort").length > 0)
      if (stop === "timeout") timer.run()
      if (stop === "cancel") controller.abort(new Error("sensitive-marker"))
      if (stop === "dispose") discovery.dispose()
      if (stop === "stream_end") await discovery.deliver(undefined)
      if (stop === "stream_throw") await discovery.deliver(new Error("sensitive-marker"))
      await rejectedSoon(promise, stop === "timeout" ? "service_metadata_timeout"
        : stop.startsWith("stream_") ? "permission_listener_unavailable" : "invocation_cancelled")
      assert.equal(readSignal.aborted, true)
      assert.equal(timer.cleared, true)
      assert.equal(getEventListeners(readSignal, "abort").length, 0)
      assert.equal(requests.length, count)
      assert.deepEqual(seen, stages.slice(0, 1))
      release(JSON.stringify(metadata))
      await new Promise(done => originalSetTimeout(done, 5))
      assert.equal(requests.length, count)
      fs.readFile = originalReadFile
      syncBuiltinESMExports()
      const recovered = stop === "timeout" || stop === "cancel" ? discovery : authorization()
      const approved = await waiting(recovered)
      const recoveredTimer = metadataTimers.at(-1)
      assert.notEqual(recoveredTimer, timer)
      assert.equal(recoveredTimer.cleared, true)
      recoveredTimer.run()
      await answer(recovered, approved.id)
      await approved.promise
      recovered.dispose()
      discovery.dispose()
    }
    assert.ok(metadataTimers.every(timer => timer.cleared))
  } finally {
    fs.readFile = originalReadFile
    syncBuiltinESMExports()
    globalThis.setTimeout = originalSetTimeout
    globalThis.clearTimeout = originalClearTimeout
  }

  const expiring = authorization()
  const expired = await waiting(expiring)
  timeouts.findLast(t => t.ms === 5 * 60 * 1000).controller.abort()
  await rejectedSoon(expired.promise, "permission_timeout")
  assert.ok(replies.some(reply => reply.requestID === expired.id && reply.decision === "reject"))
  expiring.dispose()

  const cleanup = authorization()
  const cleanupAbort = new AbortController()
  const cleaning = await waiting(cleanup, cleanupAbort.signal)
  let cleanupSignal
  intercept = (target, options) => {
    if (!target.pathname.endsWith("/reply")) return undefined
    assert.equal(JSON.parse(options.body).decision, "reject")
    cleanupSignal = options.signal
    return new Promise(() => {})
  }
  cleanupAbort.abort()
  await until(() => cleanupSignal)
  assert.equal(cleanupSignal.aborted, false)
  timeouts.findLast(t => t.ms === 2000).controller.abort()
  await rejectedSoon(cleaning.promise, "invocation_cancelled")
  assert.equal(cleanupSignal.aborted, true)
  cleanup.dispose()
  intercept = undefined
  assert.deepEqual([...new Set(timeouts.map(t => t.ms))].sort((a, b) => a - b), [2000, 5000, 300000])
} finally {
  for (const auth of authorizations) auth.dispose()
  globalThis.fetch = originalFetch
  AbortSignal.timeout = originalTimeout
  if (previousState === undefined) delete process.env.XDG_STATE_HOME
  else process.env.XDG_STATE_HOME = previousState
  await rm(state, { recursive: true })
}
