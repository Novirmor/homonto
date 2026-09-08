import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { default: plugin } = await import(pathToFileURL(resolve(process.argv[2])).href)
const events = (await readFile(process.argv[3], "utf8")).trim().split("\n").map(JSON.parse)
const sent = []
let stderrDrained = 0
globalThis.Bun = { spawn(argv, opts) {
  assert.deepEqual(argv, ["homonto", "permissions", "suggest"])
  assert.equal(opts.cwd, "/workspace")
  return {
    stdin: { write(text) { sent.push(text) }, end() {} },
    stdout: new ReadableStream({ start(c) { c.close() } }),
    stderr: new ReadableStream({ pull(c) { stderrDrained++; c.close() } }, { highWaterMark: 0 }),
    exited: Promise.resolve(0), kill() {},
  }
} }
const hooks = await plugin({ directory: "/workspace" })
const feed = async (event) => hooks.event({ event })
for (const event of events) await feed(event)
assert.deepEqual(sent, [], "one approval is not a suggestion")
for (const event of events) await feed(event)
await new Promise((resolve) => setImmediate(resolve))
assert.deepEqual(sent.sort(), ["git status\n", "go test ./...\n"])
assert.equal(stderrDrained, 2)
for (const event of events) await feed(event)
assert.equal(sent.length, 2, "suggest only once per session/command")

// Runtime producer shapes, including the often-confused permission/reply keys.
const ask = (id, sessionID, command, permission = "bash") => ({ type: "permission.asked", properties: { id, sessionID, permission, patterns: [command], metadata: { command }, always: [] } })
const reply = (requestID, sessionID, reply) => ({ type: "permission.replied", properties: { requestID, sessionID, reply } })
await feed(ask("one", "s", "nope")); await feed(reply("one", "s", "reject"))
for (const id of ["two", "three"]) { await feed(ask(id, "s", "nope")); await feed(reply(id, "s", "once")) }
assert.equal(sent.length, 2, "denial is authoritative")
for (const id of ["one", "two"]) {
  await feed(ask(id, "s", "mismatch")); await feed(reply(id, "other", "once"))
  await feed(ask(id, "s", "not bash", "edit")); await feed(reply(id, "s", "always"))
  await feed(ask(id, "s", "unknown")); await feed(reply(id, "s", "maybe"))
  await feed({ type: "permission.updated", properties: { id, sessionID: "s", type: "bash", metadata: { command: "stale" } } })
  await feed({ type: "permission.replied", properties: { permissionID: id, sessionID: "s", response: "once" } })
}
assert.equal(sent.length, 2, "unknown, stale, cross-session and non-bash events cannot suggest")
await hooks.dispose()
await hooks.dispose()
for (const event of events) await feed(event)
assert.equal(sent.length, 2)

const realTimeout = globalThis.setTimeout, realClear = globalThis.clearTimeout
const timers = new Map()
globalThis.setTimeout = (fn, ms) => { assert.equal(ms, 5000); const t = { unref() {} }; timers.set(t, fn); return t }
globalThis.clearTimeout = (t) => timers.delete(t)
let kills = 0
const realWrite = process.stdout.write, realWarn = console.warn
const lateOutput = []
process.stdout.write = (text) => { lateOutput.push(String(text)); return true }
console.warn = (...args) => { lateOutput.push(args.join(" ")) }
globalThis.Bun.spawn = () => {
  let stdout, stderr, exited, stopped = false
  return {
    stdin: { write() {}, end() {} },
    stdout: new ReadableStream({ start(c) { stdout = c } }),
    stderr: new ReadableStream({ start(c) { stderr = c } }),
    exited: new Promise((r) => { exited = r }),
    kill() { if (stopped) return; stopped = true; kills++; stdout.enqueue(new TextEncoder().encode("late suggestion")); stdout.close(); stderr.close(); exited(137) },
  }
}
try {
  for (const dispose of [false, true]) {
    const h = await plugin({ directory: "/workspace" })
    for (const id of ["one", "two"]) {
      await h.event({ event: ask(id, "s", "stuck command") })
      await h.event({ event: reply(id, "s", "once") })
    }
    assert.equal(timers.size, 1)
    const outputBeforeDispose = lateOutput.length
    if (dispose) await h.dispose()
    else [...timers.values()][0]()
    await new Promise((resolve) => setImmediate(resolve))
    assert.equal(timers.size, 0)
    if (dispose) {
      assert.equal(lateOutput.length, outputBeforeDispose, "disposed observer emitted late output")
      await h.dispose()
      for (const event of events) await h.event({ event })
      assert.equal(timers.size, 0, "disposed observer started a suggestion")
    }
  }
  assert.equal(kills, 2, "timeout and disposal both stop an outstanding child")
} finally {
  globalThis.setTimeout = realTimeout; globalThis.clearTimeout = realClear
  process.stdout.write = realWrite; console.warn = realWarn
}
