import assert from "node:assert/strict"
import { readFile, writeFile, realpath, rm } from "node:fs/promises"
import { dirname, join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

// Import the actual engine-projected TS entrypoint using Node's native type
// stripping. No rewritten source/module or synthetic binding is substituted.
const projected = await realpath(process.argv[2])
const root = resolve(dirname(projected), "../../../..")
const config = process.argv[3]
const backendSnapshot = JSON.parse(await readFile(process.argv[4], "utf8"))
const bindingPath = join(dirname(projected), "binding.json")
const bindingBytes = await readFile(bindingPath, "utf8")
assert.deepEqual(JSON.parse(bindingBytes), { version: 1, configPath: config, configRoot: dirname(config) })
const { default: plugin } = await import(pathToFileURL(process.argv[2]).href)
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
const event = (type) => ({ event: { type } })
let live = 0, maxLive = 0, calls = 0, killed = 0, stderrDrained = 0
let responses = [], held
let toasts = []
const timers = new Map()
const realTimeout = globalThis.setTimeout, realClearTimeout = globalThis.clearTimeout
globalThis.setTimeout = (fn, ms, ...args) => {
  const timer = realTimeout(() => { timers.delete(timer); fn(...args) }, ms)
  timers.set(timer, { fn, ms })
  return timer
}
globalThis.clearTimeout = (timer) => { timers.delete(timer); realClearTimeout(timer) }
const change = (status = "active", extra = {}) => ({
  workflow: "onto", identity: "id-one", path: "changes/one", name: "one", status,
  phase: "build", tasksCompleted: 1, tasksTotal: 2, pending: ["complete tasks"], ...extra,
})
const snapshot = (changes = [], findings = []) => ({ configPath: config, changes, findings })
globalThis.Bun = {
  spawn(argv, options) {
    assert.deepEqual(argv, ["homonto", "workflow", "snapshot", "--json", "--config", config])
    assert.equal(options.cwd, join(root, "source/nested"))
    assert.equal(options.stderr, "pipe")
    calls++; live++; maxLive = Math.max(maxLive, live)
    const response = responses.shift()
    assert.ok(response, "unexpected subprocess")
    let exited, done = false, stdout
    const proc = {
      stdout: new ReadableStream({ start(c) { stdout = c } }),
      stderr: new ReadableStream({ pull(c) { stderrDrained++; c.enqueue(new TextEncoder().encode("diagnostic")); c.close() } }, { highWaterMark: 0 }),
      exited: new Promise((resolve) => { exited = resolve }),
      kill() { if (!done) { killed++; finish("", 137) } },
    }
    function finish(text = JSON.stringify(response.value), code = response.code || 0) {
      if (done) return
      done = true; live--
      stdout.enqueue(new TextEncoder().encode(text)); stdout.close(); exited(code)
    }
    if (response.hold) held = finish
    else queueMicrotask(() => finish(response.raw))
    return proc
  },
}
const input = { directory: process.cwd(), client: { tui: { async showToast({ body }) { toasts.push(body) } } } }
const hooks = await plugin(input)
try {
  responses.push({ value: backendSnapshot })
  await hooks.event(event("session.idle"))
  assert.match(toasts.at(-1).message, /1\/2 tasks/)
  assert.match(toasts.at(-1).message, /pending: complete tasks/)

  // Claiming close is not completion; archive with pending integration stays visible.
  responses.push({ value: snapshot([change("active", { phase: "close", pending: [] })]) })
  await hooks.event(event("session.idle"))
  assert.equal(toasts.at(-1).variant, "info")
  responses.push({ value: snapshot([change("integration-pending", { phase: "close", pending: ["complete integration"] })]) })
  await hooks.event(event("session.idle"))
  assert.notEqual(toasts.at(-1).variant, "success")
  responses.push({ value: snapshot([change("completed", { phase: "close", pending: [] })]) })
  await hooks.event(event("session.idle"))
  assert.equal(toasts.at(-1).variant, "success")

  // Reuse of a name is a new generation; disappearance is not a completion.
  responses.push({ value: snapshot([change("active", { identity: "id-two" })]) })
  await hooks.event(event("session.idle"))
  responses.push({ value: snapshot() })
  await hooks.event(event("session.idle"))
  assert.match(toasts.at(-1).message, /completion is not established/)
  assert.equal(toasts.at(-1).variant, "warning")

  responses.push({ value: snapshot([change("abandoned")]) })
  await hooks.event(event("session.idle"))
  assert.equal(toasts.at(-1).variant, "warning")

  // Triggers while a subprocess is outstanding produce one trailing refresh.
  responses.push({ hold: true, value: snapshot([change("active", { tasksCompleted: 0 })]) })
  const first = hooks.event(event("session.idle"))
  while (!held) await delay(1)
  const count = calls
  const second = hooks.event(event("session.idle"))
  await hooks.event(event("file.watcher.updated"))
  responses.push({ value: snapshot([change("active", { tasksCompleted: 2, pending: [] })]) })
  held(); held = undefined
  await Promise.all([first, second])
  assert.equal(calls, count + 1)
  assert.equal(maxLive, 1)
  assert.match(toasts.at(-1).message, /2\/2 tasks/)

  const finding = { workflow: "onto", change: "one", message: "invalid evidence" }
  responses.push({ value: snapshot([change("active", { verifyResult: "fail", pending: ["repair tests"] })], [finding]) })
  const output = { context: [] }
  await hooks["experimental.session.compacting"]({}, output)
  assert.match(output.context.join("\n"), /verify: fail/)
  assert.match(output.context.join("\n"), /pending: repair tests/)
  assert.match(output.context.join("\n"), /invalid evidence/)
  responses.push({ value: snapshot([], [finding]) })
  const errorsOnly = { context: [] }
  await hooks["experimental.session.compacting"]({}, errorsOnly)
  assert.match(errorsOnly.context.join("\n"), /invalid evidence/)

  responses.push({ raw: "not json" })
  const failed = { context: [] }
  await hooks["experimental.session.compacting"]({}, failed)
  assert.match(failed.context.join("\n"), /observation failed/)

  const terminalOutput = { context: [] }
  const terminalToasts = toasts.length
  responses.push({ value: snapshot(["completed", "abandoned", "bypassed"].map((status) => change(status, { identity: status, verifyResult: "fail", pending: ["do not revive terminal work"] }))) })
  await hooks["experimental.session.compacting"]({}, terminalOutput)
  assert.deepEqual(terminalOutput.context, [], "historical verification failures must not revive terminal work")
  assert.ok(toasts.slice(terminalToasts).every((t) => t.variant !== "success"))

  // Bound execution even when the child never exits; drain stderr concurrently.
  responses.push({ hold: true })
  const stuck = hooks.event(event("session.idle"))
  while (!held) await delay(1)
  const deadline = [...timers.values()].find((t) => t.ms === 5000)
  assert.ok(deadline)
  deadline.fn()
  await stuck
  held = undefined
  assert.equal(killed, 1)
  assert.match(toasts.at(-1).message, /timed out/)
  assert.equal(stderrDrained, calls)
  assert.equal(timers.size, 0)

  await hooks.event(event("file.watcher.updated"))
  assert.ok([...timers.values()].some((t) => t.ms === 250))
  await hooks.dispose()
  await hooks.dispose()
  assert.equal(timers.size, 0)
  const before = calls
  await hooks.event(event("session.idle"))
  await delay(300)
  assert.equal(calls, before)

  const secondHooks = await plugin(input)
  responses.push({ hold: true })
  const disposing = secondHooks.event(event("session.idle"))
  while (!held) await delay(1)
  const toastCount = toasts.length
  const disposingOutput = { context: [] }
  const disposingCompaction = secondHooks["experimental.session.compacting"]({}, disposingOutput)
  await secondHooks.dispose()
  await Promise.all([disposing, disposingCompaction])
  assert.deepEqual(disposingOutput.context, [])
  assert.equal(killed, 2)
  assert.equal(toasts.length, toastCount)
  assert.equal(timers.size, 0)

  // Events continue arriving in both generations. Awaiting compaction must
  // settle after two reads, with newer progress/findings, not join a third.
  const streaming = await plugin(input)
  responses.push({ hold: true, value: snapshot([change("active", { tasksCompleted: 0 })]) })
  held = undefined
  const streamingIdle = streaming.event(event("session.idle"))
  while (!held) await delay(1)
  const startCalls = calls
  const streamingOutput = { context: [] }
  const compacting = streaming["experimental.session.compacting"]({}, streamingOutput)
  for (let i = 0; i < 50; i++) await streaming.event(event("file.watcher.updated"))
  responses.push({ hold: true, value: snapshot([change("active", { tasksCompleted: 2, pending: [] })], [{ workflow: "onto", message: "latest health finding" }]) })
  held(); held = undefined
  while (!held) await delay(1)
  for (let i = 0; i < 50; i++) await streaming.event(event("file.watcher.updated"))
  held(); held = undefined
  await Promise.all([streamingIdle, compacting])
  assert.equal(calls, startCalls + 1, "one flight exceeded two generations")
  assert.match(streamingOutput.context.join("\n"), /2\/2 tasks/)
  assert.match(streamingOutput.context.join("\n"), /latest health finding/)
  assert.ok([...timers.values()].some((t) => t.ms === 250), "remaining events need an independent scheduled flight")
  await streaming.dispose()
  assert.equal(timers.size, 0)

  // Compaction also has a wall-clock budget independent of subprocess or
  // watcher activity and retains the most recently completed snapshot/findings.
  const slow = await plugin(input)
  responses.push({ value: snapshot([change("active", { tasksCompleted: 2, pending: [] })], [{ workflow: "onto", message: "retained finding" }]) })
  await slow.event(event("session.idle"))
  responses.push({ hold: true })
  const slowIdle = slow.event(event("session.idle"))
  while (!held) await delay(1)
  const slowOutput = { context: [] }
  const slowCompaction = slow["experimental.session.compacting"]({}, slowOutput)
  const budget = [...timers.values()].find((t) => t.ms === 1500)
  assert.ok(budget)
  budget.fn()
  await slowCompaction
  assert.match(slowOutput.context.join("\n"), /2\/2 tasks/)
  assert.match(slowOutput.context.join("\n"), /retained finding/)
  assert.match(slowOutput.context.join("\n"), /Refresh still in progress/)
  const beforeDisposeToasts = toasts.length
  await slow.dispose()
  await slowIdle
  held = undefined
  assert.equal(toasts.length, beforeDisposeToasts)
  assert.equal(timers.size, 0)
  assert.equal(maxLive, 1)

  assert.equal(await readFile(bindingPath, "utf8"), bindingBytes, "observer wrote projection metadata")
  // Missing or invalid metadata is an observer error, not a TOML/Markdown
  // discovery fallback. No subprocess may run for any of these bindings.
  for (const invalid of [undefined, "{", "null", JSON.stringify({ version: 2, configPath: config, configRoot: root }),
    JSON.stringify({ version: 1, configPath: "relative.toml", configRoot: root }),
    JSON.stringify({ version: 1, configPath: join(root, "nested/custom.toml"), configRoot: root }),
    JSON.stringify({ version: 1, configPath: join(dirname(root), "other.toml"), configRoot: dirname(root) })]) {
    if (invalid === undefined) await rm(bindingPath)
    else await writeFile(bindingPath, invalid)
    const h = await plugin(input)
    const count = calls
    await h.event(event("session.idle"))
    assert.equal(calls, count)
    assert.equal(toasts.at(-1).variant, "error")
    assert.match(toasts.at(-1).message, /binding/)
    await h.dispose()
  }
} finally {
  globalThis.setTimeout = realTimeout; globalThis.clearTimeout = realClearTimeout
  for (const timer of timers.keys()) realClearTimeout(timer)
  await writeFile(bindingPath, bindingBytes)
}
