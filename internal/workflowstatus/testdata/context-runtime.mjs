import assert from "node:assert/strict"
import { readFile, writeFile, realpath, rm, symlink } from "node:fs/promises"
import { dirname, join } from "node:path"
import { pathToFileURL } from "node:url"

const projected = await realpath(process.argv[2])
const config = process.argv[3]
const root = dirname(config)
const backend = JSON.parse(await readFile(process.argv[4], "utf8"))
const bindingPath = join(dirname(projected), "binding.json")
const bindingBytes = await readFile(bindingPath, "utf8")
const binding = JSON.parse(bindingBytes)
const { default: plugin } = await import(pathToFileURL(process.argv[2]).href)
const { WorkflowPanelRPC } = await import(pathToFileURL(join(dirname(projected), "rpc.ts")).href)
assert.deepEqual(WorkflowPanelRPC, {
  id: "homonto.workflow", methods: { identity: {
    input: { type: "object", additionalProperties: false },
    output: { type: "object", properties: { token: { type: "string" } }, required: ["token"], additionalProperties: false },
  }, snapshot: {
    input: { type: "object", properties: { sessionID: { type: "string" } }, required: ["sessionID"], additionalProperties: false },
    output: { type: "object", properties: { text: { type: "string" } }, required: ["text"], additionalProperties: false },
  } }, events: { updated: { schema: { type: "object", properties: {
    sessionID: { type: "string" }, message: { type: "string" },
    variant: { type: "string", enum: ["info", "success", "warning", "error"] },
  }, required: ["sessionID", "message", "variant"], additionalProperties: false } } },
})
assert.equal(plugin.id, "homonto-workflow-context")
assert.deepEqual(Object.keys(plugin).sort(), ["id", "setup"])

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
const unhandled = []
const onUnhandled = (error) => { unhandled.push(error) }
process.on("unhandledRejection", onUnhandled)
const realTimeout = globalThis.setTimeout, realClearTimeout = globalThis.clearTimeout
const timers = new Map()
globalThis.setTimeout = (fn, ms, ...args) => {
  const timer = realTimeout(() => { timers.delete(timer); fn(...args) }, ms)
  timers.set(timer, { fn, ms })
  return timer
}
globalThis.clearTimeout = (timer) => { timers.delete(timer); realClearTimeout(timer) }
const expire = (ms) => {
  const timer = [...timers.values()].find((t) => t.ms === ms)
  assert.ok(timer, `missing ${ms}ms deadline`)
  timer.fn()
}

let responses = [], calls = 0, live = 0, maxLive = 0, killed = 0, drained = 0, held
globalThis.Bun = {
  spawn(argv, options) {
    calls++; live++; maxLive = Math.max(maxLive, live)
    assert.deepEqual(argv, ["homonto", "workflow", "snapshot", "--json", "--config", config])
    assert.equal(options.cwd, root)
    assert.equal(options.stdin, "ignore")
    assert.equal(options.stderr, "pipe")
    const response = responses.shift()
    assert.ok(response, "unexpected read-only subprocess")
    let done = false, stdout, exited
    const proc = {
      stdout: new ReadableStream({ start(controller) { stdout = controller } }),
      stderr: new ReadableStream({ pull(controller) { drained++; controller.enqueue(new TextEncoder().encode(response.stderr ?? "")); controller.close() } }, { highWaterMark: 0 }),
      exited: new Promise((resolve) => { exited = resolve }),
      kill() { if (!done) { killed++; finish("", 137) } },
    }
    function finish(raw = response.raw ?? JSON.stringify(response.value), code = response.code ?? 0) {
      if (done) return
      done = true; live--
      stdout.enqueue(new TextEncoder().encode(raw)); stdout.close(); exited(code)
    }
    if (response.hold) held = finish
    else queueMicrotask(() => finish())
    return proc
  },
}

const session = { id: "ses_one", projectID: "prj_one", location: { directory: process.cwd() } }
let sessionValue = session, getSession, lastSignal
const cleanups = []
function stream() {
  const consumers = new Set()
  return {
    subscribe: ({ signal }) => ({ async *[Symbol.asyncIterator]() {
      const entry = { wake: undefined, queue: [] }
      consumers.add(entry)
      const stop = () => { if (entry.wake) entry.wake(undefined) }
      signal.addEventListener("abort", stop, { once: true })
      try {
        while (!signal.aborted) {
          const event = entry.queue.shift() ?? await new Promise(resolve => { entry.wake = resolve })
          entry.wake = undefined
          if (event) yield event
        }
      } finally { consumers.delete(entry); signal.removeEventListener("abort", stop) }
    } }),
    push(event) { for (const entry of consumers) {
      if (entry.wake) entry.wake(event)
      else entry.queue.push(event)
    } },
  }
}
async function setup(overrides = {}) {
  const hooks = new Map()
  const definitions = []
  const notifications = []
  const githubEnabled = await readFile(bindingPath, "utf8").then(text => {
    try { return JSON.parse(text).githubEnabled === true } catch { return false }
  }).catch(() => false)
  let handler, rpcDisposed = 0
  const context = new Proxy({
    app: { version: "2.0.16" },
    location: { directory: process.cwd(), project: { id: "prj_one", directory: root, canonical: root } },
    event: { subscribe: ({ signal }) => ({ async *[Symbol.asyncIterator]() {
      await new Promise(resolve => signal.addEventListener("abort", resolve, { once: true }))
    } }) },
    tool: { async transform(callback) {
      callback({ add: definition => definitions.push(definition) })
      assert.deepEqual(definitions.map(d => d.name), ["homonto_status", "homonto_handoff",
        ...(githubEnabled ? ["homonto_github_draft", "homonto_github_status", "homonto_github_publish"] : [])])
      return { async dispose() {} }
    }, async hook() { return { async dispose() {} } } },
    session: {
      async get(input, { signal } = {}) {
        assert.equal(input.sessionID, "ses_one")
        lastSignal = signal
        return getSession ? getSession(input, signal) : sessionValue
      },
      async hook(name, callback) {
        assert.ok(["context", "compaction"].includes(name))
        assert.equal(hooks.has(name), false)
        hooks.set(name, callback)
        return { async dispose() {} }
      },
    },
    rpc: {
      async register(definition, handlers) {
        assert.equal(definition, WorkflowPanelRPC)
        assert.deepEqual(Object.keys(handlers), ["identity", "snapshot"])
        const identity = await handlers.identity({})
        assert.equal(typeof identity.token, "string")
        handler = handlers.snapshot
        return { async dispose() { rpcDisposed++ }, events: { async emit(name, data) {
          assert.equal(name, "updated")
          notifications.push(data)
        } } }
      },
    },
    ...overrides,
  }, { get(target, key) { assert.ok(key in target, `unexpected plugin capability: ${String(key)}`); return target[key] } })
  const dispose = await plugin.setup(context)
  assert.equal(typeof dispose, "function")
  cleanups.push(dispose)
  assert.deepEqual([...hooks.keys()], ["context", "compaction"])
  return { hooks, definitions, notifications, dispose, snapshot(input = { sessionID: "ses_one" }, signal = new AbortController().signal) {
    assert.ok(handler)
    return handler(input, { signal })
  }, get rpcDisposed() { return rpcDisposed } }
}
const request = () => ({
  sessionID: "ses_one", agent: "custom-agent", model: { providerID: "example", id: "model" },
  system: [{ type: "text", text: "existing instructions" }], messages: [{ role: "user", content: "existing" }],
  tools: { read: { description: "Read", input: { type: "object" } } }, options: { temperature: 0.2 }, result: undefined,
})
async function invoke(instance, response, kind = "context") {
  if (response) responses.push(response)
  const event = request()
  const original = structuredClone(event)
  await instance.hooks.get(kind)(event)
  const { system, ...rest } = event
  assert.deepEqual(rest, (({ system, ...rest }) => rest)(original))
  assert.deepEqual(system[0], original.system[0])
  assert.ok(system.length <= 2)
  if (system[1]) {
    assert.equal(system[1].type, "text")
    assert.ok(Buffer.byteLength(system[1].text) <= 16384)
    assert.doesNotMatch(system[1].text, /homonto_status|homonto_handoff/)
  }
  return system[1]?.text ?? ""
}
async function until(predicate) {
  for (let n = 0; n < 1000; n++) { if (predicate()) return; await delay(1) }
  assert.fail("runtime did not reach expected state")
}
const snap = (changes = [], findings = []) => ({ configPath: config, changes, findings })
const change = { ...backend.changes[0], identity: "id-one", pending: ["complete tasks"] }

try {
  const instance = await setup()
  assert.equal(calls, 0, "setup must not read workflow state")
  const panel = async (response, expected = /./) => {
    if (response) responses.push(response)
    const result = await instance.snapshot()
    assert.deepEqual(Object.keys(result), ["text"])
    assert.ok(Buffer.byteLength(result.text) <= 16384)
    assert.match(result.text, expected)
    return result.text
  }
  const refuse = async (input = { sessionID: "ses_one" }) => {
    await assert.rejects(() => instance.snapshot(input), /workflow panel request refused/)
  }
  assert.match(await panel({ value: backend }, /"tasksCompleted":1,"tasksTotal":2/), /"recoveryArgv":\["homonto","workflow","handoff"/)
  assert.match(await panel({ value: snap() }, /No active workflow work observed/), /missing records do not establish completion/)
  for (const status of ["completed", "abandoned", "bypassed"]) {
    assert.match(await panel({ value: snap([{ ...change, status }]) }, /No active workflow work observed/), /missing records do not establish completion/)
  }
  assert.match(await panel({ value: snap([], [{ workflow: "onto", message: "health issue" }]) }, /health issue/), /No active workflow work observed/)
  assert.match(await panel({ value: snap(Array.from({ length: 10 }, (_, n) => ({ ...change, identity: `long-${n}`, pending: ["é🧪".repeat(20000)] }))) }, /truncated/), /additional nonterminal records omitted/)
  for (const response of [{ raw: "not-json" }, { code: 1, stderr: "private stderr" }]) {
    const text = await panel(response, /Workflow observation unavailable/)
    assert.doesNotMatch(text, /private stderr|id-one/)
  }
  for (const invalid of [{}, { sessionID: 42 }, { sessionID: "ses_one", extra: true }, { sessionID: "" }]) await refuse(invalid)
  for (const foreign of [{ ...session, projectID: "other" }, { ...session, id: "other" }, { ...session, location: { directory: dirname(root) } }]) {
    const before = calls
    sessionValue = foreign
    await refuse()
    assert.equal(calls, before)
  }
  sessionValue = session
  const beforeUnknown = calls
  await refuse({ sessionID: "ses_other" })
  assert.equal(calls, beforeUnknown)
  let rpcReleaseLookup
  getSession = () => new Promise((resolve) => { rpcReleaseLookup = resolve })
  const slowLookup = instance.snapshot()
  await until(() => rpcReleaseLookup)
  expire(1500)
  await assert.rejects(slowLookup, /workflow panel request refused/)
  assert.equal(lastSignal.aborted, true)
  rpcReleaseLookup(session)
  getSession = undefined
  await delay(10)
  const cancelled = new AbortController()
  cancelled.abort()
  await assert.rejects(() => instance.snapshot({ sessionID: "ses_one" }, cancelled.signal), /workflow panel request refused/)
  assert.equal(timers.size, 0)
  const cancelledDuringLookup = new AbortController()
  getSession = () => new Promise((resolve) => { rpcReleaseLookup = resolve })
  const cancelledCall = instance.snapshot({ sessionID: "ses_one" }, cancelledDuringLookup.signal)
  await until(() => rpcReleaseLookup)
  cancelledDuringLookup.abort()
  await assert.rejects(cancelledCall, /workflow panel request refused/)
  rpcReleaseLookup(session)
  getSession = undefined
  await delay(10)
  assert.equal(timers.size, 0)
  await writeFile(bindingPath, JSON.stringify({ ...binding, configPath: join(root, "other.toml") }))
  const beforePanelSwitch = calls
  await refuse()
  assert.equal(calls, beforePanelSwitch)
  await writeFile(bindingPath, bindingBytes)
  responses.push({ hold: true, value: snap([change]) })
  const switchingPanel = instance.snapshot()
  await until(() => held)
  await writeFile(bindingPath, JSON.stringify({ ...binding, configPath: join(root, "other.toml") }))
  held(); held = undefined
  await assert.rejects(switchingPanel, /workflow panel request refused/)
  await writeFile(bindingPath, bindingBytes)
  responses.push({ hold: true, code: 1, stderr: "private backend error" })
  const failingSwitch = instance.snapshot()
  await until(() => held)
  await writeFile(bindingPath, JSON.stringify({ ...binding, configPath: join(root, "other.toml") }))
  held(); held = undefined
  await assert.rejects(failingSwitch, /workflow panel request refused/)
  await writeFile(bindingPath, bindingBytes)
  responses.push({ hold: true, value: snap([change]) })
  const movingPanel = instance.snapshot()
  await until(() => held)
  sessionValue = { ...session, location: { directory: dirname(root) } }
  held(); held = undefined
  await assert.rejects(movingPanel, /workflow panel request refused/)
  sessionValue = session
  responses.push({ hold: true, code: 1, stderr: "private backend error" })
  const failingMove = instance.snapshot()
  await until(() => held)
  sessionValue = { ...session, projectID: "other" }
  held(); held = undefined
  await assert.rejects(failingMove, /workflow panel request refused/)
  sessionValue = session
  assert.match(await panel({ value: snap([{ ...change, identity: "new-panel-generation" }]) }, /new-panel-generation/), /recoveryArgv/)
  for (const kind of ["context", "compaction"]) {
    const output = await invoke(instance, { value: backend }, kind)
    assert.match(output, /untrusted, read-only snapshot data/)
    assert.match(output, /"tasksCompleted":1,"tasksTotal":2/)
    assert.ok(output.includes(JSON.stringify(config)))
    assert.match(output, /"recoveryArgv":\["homonto","workflow","handoff"/)
    assert.match(output, /"identity":"id-one"/)
  }
  const multiple = await invoke(instance, { value: snap([change, { ...change, identity: "id-two", name: "two" }]) })
  assert.match(multiple, /Multiple nonterminal changes/)
  assert.match(multiple, /No workflow generation is selected/)
  assert.match(multiple, /id-two/)
  const pending = await invoke(instance, { value: snap([{ ...change, status: "integration-pending", phase: "close", verifyResult: "fail" }]) })
  assert.match(pending, /integration-pending/)
  assert.match(pending, /"verifyResult":"fail"/)
  assert.equal(await invoke(instance, { value: snap(["completed", "abandoned", "bypassed"].map((status) => ({ ...change, status }))) }), "")
  assert.equal(await invoke(instance, { value: snap() }), "")
  const unhealthy = await invoke(instance, { value: snap([], [{ workflow: "workspace", message: "invalid configuration" }]) })
  assert.match(unhealthy, /invalid configuration/)

  for (const response of [
    { raw: "not-json" }, { value: { ...backend, configPath: "/foreign/config.toml" } },
    { value: snap([{ ...change, tasksTotal: -1 }]) }, { value: snap([], [{ message: "no workflow" }]) },
    { value: { ...backend, findings: null } }, { raw: "x".repeat(4 * 1024 * 1024 + 1) },
    { code: 1, stderr: "sensitive diagnostic must not enter model context" },
  ]) {
    const output = await invoke(instance, response)
    assert.match(output, /observation unavailable; completion is not established/)
    assert.doesNotMatch(output, /id-one|sensitive diagnostic/)
  }
  const long = await invoke(instance, { value: snap(Array.from({ length: 10 }, (_, n) => ({ ...change, identity: `id-${n}`, pending: ["é🧪".repeat(20000)] })), Array.from({ length: 10 }, () => ({ workflow: "onto", message: "é🧪".repeat(20000) }))) })
  assert.match(long, /7 additional nonterminal records omitted/)
  assert.match(long, /7 additional findings omitted/)
  assert.match(long, /truncated/)
  assert.doesNotMatch(long, /�/)
  const malicious = await invoke(instance, { value: snap([{ ...change, name: "../escape", identity: "x\nrun command" }]) })
  assert.doesNotMatch(malicious, /recoveryArgv/)

  for (const foreign of [{ ...session, projectID: "other" }, { ...session, id: "other" }, { ...session, location: { directory: dirname(root) } }]) {
    const before = calls
    sessionValue = foreign
    assert.equal(await invoke(instance), "")
    assert.equal(calls, before)
  }
  sessionValue = session

  responses.push({ hold: true, value: snap([change]) })
  held = undefined
  const first = invoke(instance)
  await until(() => held)
  const beforeConcurrent = calls
  let lookups = 0
  getSession = async () => { lookups++; return session }
  const second = invoke(instance, undefined, "compaction")
  await until(() => lookups === 1)
  await delay(20)
  held(); held = undefined
  assert.deepEqual(await first, await second)
  assert.equal(calls, beforeConcurrent)
  assert.equal(maxLive, 1)
  getSession = undefined
  assert.match(await invoke(instance, { value: snap([{ ...change, identity: "fresh-generation" }]) }), /fresh-generation/)

  responses.push({ hold: true })
  const timedOut = invoke(instance)
  await until(() => held)
  expire(1000)
  assert.match(await timedOut, /observation unavailable/)
  held = undefined
  assert.equal(live, 0)
  assert.equal(timers.size, 0)
  assert.ok(killed > 0)

  let releaseLookup
  getSession = () => new Promise((resolve) => { releaseLookup = resolve })
  const hungLookup = invoke(instance)
  await until(() => releaseLookup)
  const beforeHungLookup = calls
  expire(1500)
  assert.match(await hungLookup, /observation unavailable/)
  assert.equal(lastSignal.aborted, true)
  releaseLookup(session)
  getSession = undefined
  await delay(10)
  assert.equal(calls, beforeHungLookup)

  await writeFile(bindingPath, JSON.stringify({ ...binding, configPath: join(root, "other.toml") }))
  const beforeChangedBinding = calls
  assert.match(await invoke(instance), /observation unavailable/)
  assert.equal(calls, beforeChangedBinding)
  await writeFile(bindingPath, bindingBytes)
  responses.push({ hold: true, value: snap([change]) })
  const changing = invoke(instance)
  await until(() => held)
  await writeFile(bindingPath, JSON.stringify({ ...binding, configPath: join(root, "other.toml") }))
  held(); held = undefined
  assert.doesNotMatch(await changing, /id-one/)
  await writeFile(bindingPath, bindingBytes)

  responses.push({ hold: true, value: snap([change]) })
  const moving = invoke(instance)
  await until(() => held)
  sessionValue = { ...session, location: { directory: dirname(root) } }
  held(); held = undefined
  assert.equal(await moving, "", "a session move during the read must not receive old-location context")
  sessionValue = session

  responses.push({ hold: true, value: snap([change]) })
  const unloading = invoke(instance)
  await until(() => held)
  const unloadingPanel = instance.snapshot()
  await until(() => lastSignal && !lastSignal.aborted)
  instance.dispose(); instance.dispose()
  assert.equal(instance.rpcDisposed, 1)
  assert.equal(await unloading, "")
  await assert.rejects(unloadingPanel, /workflow panel request refused/)
  await refuse()
  held = undefined
  const beforeDisposed = calls
  assert.equal(await invoke(instance), "")
  assert.equal(calls, beforeDisposed)
  assert.equal(live, 0)
  assert.equal(timers.size, 0)
  assert.equal(drained, calls)
  await delay(10)
  assert.deepEqual(unhandled, [])

  const bus = stream()
  const observing = await setup({ event: bus })
  responses.push({ value: snap([change]) })
  bus.push({ type: "session.idle", data: { sessionID: "ses_one" } })
  await until(() => observing.notifications.length === 1)
  assert.equal(observing.notifications[0].variant, "info")
  responses.push({ value: snap([{ ...change, status: "completed", pending: [] }]) })
  bus.push({ type: "session.idle", data: { sessionID: "ses_one" } })
  await until(() => observing.notifications.length === 2)
  assert.equal(observing.notifications[1].variant, "success")
  observing.dispose()
  const beforeGithub = calls

  await writeFile(bindingPath, JSON.stringify({ ...binding, githubEnabled: true }))
  const githubRuntime = await setup()
  const call = { sessionID: "ses_one", messageID: "msg", id: "call", agent: "custom-agent", signal: new AbortController().signal,
    async progress() {} }
  const draft = githubRuntime.definitions.find(d => d.name === "homonto_github_draft")
  assert.ok(draft)
  await assert.rejects(() => draft.execute({ items: [] }, call), /1–10 items/)
  const beforeUnauthorized = calls
  await assert.rejects(() => draft.execute({ items: [{ kind: "issue_comment", url: "https://github.com/owner/repo/issues/1",
    body: "test", baseOID: "", headOID: "", reviewEvent: "" }] }, call))
  assert.equal(calls, beforeUnauthorized, "GitHub preview must fail before network or subprocess when bound service is unavailable")
  githubRuntime.dispose()
  await writeFile(bindingPath, bindingBytes)

  for (const value of ["invalid JSON", JSON.stringify({ ...binding, configRoot: dirname(root) }), " ".repeat(16385)]) {
    await writeFile(bindingPath, value)
    await assert.rejects(() => setup())
  }
  await rm(bindingPath)
  await assert.rejects(() => setup())
  await symlink(config, bindingPath)
  await assert.rejects(() => setup(), /binding file/)
  await rm(bindingPath)
  await writeFile(bindingPath, bindingBytes)
  await assert.rejects(() => setup({ location: { directory: dirname(root), project: { id: "other" } } }), /outside its bound workspace/)
  assert.equal(calls, beforeGithub)
  process.stdout.write("V2 context runtime: hooks, RPC, binding, scope, budgets, coalescing and cleanup passed\n")
} finally {
  for (const dispose of cleanups) dispose()
  await writeFile(bindingPath, bindingBytes)
  globalThis.setTimeout = realTimeout
  globalThis.clearTimeout = realClearTimeout
  process.off("unhandledRejection", onUnhandled)
}
