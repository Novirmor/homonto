import assert from "node:assert/strict"
import { resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { default: plugin } = await import(pathToFileURL(resolve(process.argv[2])).href)
const sent = []
let before, after, next, finished = false
const queue = []
const subscription = {
  async *[Symbol.asyncIterator]() {
    while (!finished) {
      const event = queue.shift() ?? await new Promise((wake) => { next = wake })
      if (event) yield event
    }
  },
}
const feed = async (type, data) => {
  const event = { type, data }
  if (next) { const wake = next; next = undefined; wake(event) }
  else queue.push(event)
  await new Promise((done) => setImmediate(done))
}
globalThis.Bun = { spawn(argv, options) {
  assert.deepEqual(argv, ["homonto", "permissions", "suggest"])
  assert.equal(options.cwd, "/workspace")
  return {
    stdin: { write(text) { sent.push(text) }, end() {} },
    stdout: new ReadableStream({ start(c) { c.close() } }),
    stderr: new ReadableStream({ start(c) { c.close() } }),
    exited: Promise.resolve(0), kill() {},
  }
} }
const signal = new AbortController()
const dispose = await plugin.setup({
  location: { directory: "/workspace", project: { id: "project" } },
  session: { get: async ({ sessionID }) => ({ id: sessionID, projectID: sessionID === "foreign" ? "other" : "project" }) },
  event: { subscribe: ({ signal: abort }) => { abort.addEventListener("abort", () => {
    finished = true
    if (next) { const wake = next; next = undefined; wake(undefined) }
  }); return subscription } },
  tool: { hook: async (name, callback) => {
    if (name === "execute.before") before = callback
    if (name === "execute.after") after = callback
    return { dispose: async () => {} }
  } },
})
const command = (cmd, id, sessionID = "session") => {
  const call = { tool: "shell", sessionID, messageID: `msg_${id}`, id: `call_${id}`, input: { command: cmd } }
  before(call)
  return call
}
const ask = (call, id, resources = [call.input.command]) => feed("permission.asked", {
  id: `per_${id}`, sessionID: call.sessionID, action: "shell", resources,
  source: { type: "tool", messageID: call.messageID, id: call.id },
})
const reply = (call, id, effect) => feed("permission.replied", { sessionID: call.sessionID, requestID: `per_${id}`, reply: effect })
const approved = async (cmd, id, effect = "once", session = "session", resources) => {
  const call = command(cmd, id, session)
  await ask(call, id, resources)
  await reply(call, id, effect)
  after(call)
}
try {
  await approved("git status", "one", "once")
  assert.deepEqual(sent, [])
  await approved("git status", "two", "always")
  assert.deepEqual(sent, [], "always may be a V2 auto-reply")
  await approved("git status", "three", "once")
  assert.deepEqual(sent, ["git status\n"])
  await approved("git status", "four", "once")
  assert.equal(sent.length, 1)
  await approved("git diff", "deny", "reject")
  await approved("git diff", "five", "once")
  await approved("git diff", "six", "once")
  assert.equal(sent.length, 1, "rejection disqualifies a candidate")
  await approved("go test ./...", "multi", "once", "session", ["go test ./...", "go vet ./..."])
  await approved("go test ./...", "changed", "once", "session", ["go test ./... && go vet ./..."])
  await approved("go test ./...", "foreign1", "once", "foreign")
  await approved("go test ./...", "foreign2", "once", "foreign")
  assert.equal(sent.length, 1, "ambiguous resources or another project cannot suggest")
  await approved("go test ./...", "seventh", "once")
  await approved("go test ./...", "eighth", "once")
  assert.deepEqual(sent, ["git status\n", "go test ./...\n"])
  dispose(); dispose()
  await approved("new command", "post1", "once")
  await approved("new command", "post2", "once")
  assert.equal(sent.length, 2)
} finally {
  dispose()
}
