import assert from "node:assert/strict"
import { readFile, writeFile, realpath, rm, symlink, mkdir } from "node:fs/promises"
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
const { CONTINUATION_POLICY } = await import(pathToFileURL(join(dirname(projected), "continuation.ts")).href)
assert.equal(typeof CONTINUATION_POLICY, "string")
for (const phrase of [
  "Before ending", "next authorized, in-scope action", "progress summary", "subagent report", "compaction",
  "explicit pause", "permission denial", "bounded permitted recovery", "Delegated workers", "research-only",
  "plan-only", "does not select", "anticipated budget pressure",
]) assert.ok(CONTINUATION_POLICY.includes(phrase), `continuation policy missing ${phrase}`)
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
const realBun = globalThis.Bun, realFetch = globalThis.fetch, realStateHome = process.env.XDG_STATE_HOME
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

let responses = [], calls = 0, live = 0, maxLive = 0, killed = 0, drained = 0, held, githubSpawn
globalThis.Bun = {
  spawn(argv, options) {
    calls++; live++; maxLive = Math.max(maxLive, live)
    if (!githubSpawn) assert.deepEqual(argv, ["homonto", "workflow", "snapshot", "--json", "--config", config])
    assert.equal(options.cwd, root)
    if (!githubSpawn) assert.equal(options.stdin, "ignore")
    assert.equal(options.stderr, "pipe")
    const response = githubSpawn ? githubSpawn(argv, options) : responses.shift()
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
let sessionValue = session, getSession, lastSignal, sessionGets = 0
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
          if (event) { yield event; entry.last = event }
        }
      } finally { consumers.delete(entry); signal.removeEventListener("abort", stop) }
    } }),
    push(event) { for (const entry of consumers) {
      if (entry.wake) { const wake = entry.wake; entry.wake = undefined; wake(event) }
      else entry.queue.push(event)
    } },
    async flush() {
      const marker = { type: "fixture.flush" }
      this.push(marker)
      await until(() => [...consumers].every(entry => entry.last === marker))
    },
  }
}
async function setup(overrides = {}) {
  const hooks = new Map()
  const toolHooks = new Map(), nestedTools = []
  const definitions = []
  const notifications = []
  const githubEnabled = await readFile(bindingPath, "utf8").then(text => {
    try { return JSON.parse(text).githubEnabled === true } catch { return false }
  }).catch(() => false)
  let handler, identityToken, rpcDisposed = 0
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
    }, async hook(name, callback) {
      assert.equal(name, "execute.before")
      assert.equal(toolHooks.has(name), false)
      toolHooks.set(name, callback)
      return { async dispose() {} }
    }, async list() { nestedTools.push("list"); assert.fail("draft must return question arguments, not look up host tools") },
    async execute() { nestedTools.push("execute"); assert.fail("draft must not execute a nested host question") } },
    session: {
      async get(input, { signal } = {}) {
        sessionGets++
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
        identityToken = identity.token
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
  return { hooks, toolHooks, nestedTools, identityToken, definitions, notifications, dispose, snapshot(input = { sessionID: "ses_one" }, signal = new AbortController().signal) {
    assert.ok(handler)
    return handler(input, { signal })
  }, get rpcDisposed() { return rpcDisposed } }
}
const request = (agent = "custom-agent") => ({
  sessionID: "ses_one", agent, model: { providerID: "example", id: "model" },
  system: [{ type: "text", text: "existing instructions" }, { type: "text", text: "existing second block" }], messages: [{ role: "user", content: "existing" }],
  tools: { read: { description: "Read", input: { type: "object" } } }, options: { temperature: 0.2 }, result: undefined,
})
async function invoke(instance, response, kind = "context", outcome = "policy", agent = "custom-agent") {
  if (response) responses.push(response)
  const event = request(agent)
  const original = structuredClone(event)
  await instance.hooks.get(kind)(event)
  const { system, ...rest } = event
  assert.deepEqual(rest, (({ system, ...rest }) => rest)(original))
  assert.deepEqual(system.slice(0, original.system.length), original.system)
  const added = system.slice(original.system.length)
  if (outcome === "none") assert.deepEqual(added, [])
  else if (outcome === "error") {
    assert.equal(added.length, 1)
    assert.match(added[0].text, /observation unavailable; completion is not established/)
    assert.ok(!added[0].text.includes(CONTINUATION_POLICY))
  } else {
    assert.ok(added.length === 1 || added.length === 2)
    assert.deepEqual(added[0], { type: "text", text: CONTINUATION_POLICY })
    if (added[1]) {
      assert.match(added[1].text, /untrusted, read-only snapshot data/)
      assert.ok(!added[1].text.includes(CONTINUATION_POLICY))
    }
  }
  assert.ok(added.reduce((size, block) => size + Buffer.byteLength(block.text), 0) <= 16384)
  for (const block of added) {
    assert.equal(block.type, "text")
    assert.doesNotMatch(block.text, /homonto_status|homonto_handoff/)
  }
  return added.map(block => block.text).join("\n")
}
async function until(predicate) {
  for (let n = 0; n < 1000; n++) { if (predicate()) return; await delay(1) }
  assert.fail("runtime did not reach expected state")
}
const snap = (changes = [], findings = []) => ({ configPath: config, changes, findings })
const change = { ...backend.changes[0], identity: "id-one", pending: ["complete tasks"] }

async function githubIntegration() {
  const stateHome = join(root, "v2-service-state")
  const servicePath = join(stateHome, "opencode", "service.json")
  const githubBinding = JSON.stringify({ ...binding, githubEnabled: true })
  const bus = stream(), hostRequests = [], permissions = [], commands = [], posts = [], reports = []
  const fakeFailures = []
  const item = { kind: "issue_comment", url: "https://github.com/owner/repo/issues/1",
    body: "Exact V2 preview body — never publish without native approval", baseOID: "", headOID: "", reviewEvent: "" }
  const args = { items: [item] }
  const actor = { id: 17, login: "fixture-user" }
  const receipt = { id: 901, html_url: `${item.url}#issuecomment-901`, body: item.body, user: actor,
    issue_url: "https://api.github.com/repos/owner/repo/issues/1" }
  const api = (endpoint, method = "GET", paginated = false) => ["gh", "api", "--hostname", "github.com", "--method", method,
    "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28",
    ...(paginated ? ["--paginate", "--slurp"] : []), endpoint, ...(method === "POST" ? ["--input", "-"] : [])]
  let identityToken, sequence = 0
  const invocation = (agent = "custom-agent", overrides = {}) => ({ sessionID: "ses_one", messageID: `msg_${++sequence}`,
    id: `call_${sequence}`, agent, signal: new AbortController().signal,
    progress(value) { reports.push(value) }, ...overrides })
  const counters = () => ({ subprocesses: calls, requests: hostRequests.length })
  const rejected = async (work, code) => assert.rejects(work, error => {
    assert.match(error.message, new RegExp(`\\[${code}\\]`))
    assert.doesNotMatch(error.message, /Exact V2 preview body|id-one|tasksCompleted|private artifact/)
    return true
  })
  const bounded = async promise => {
    let settled = false
    const result = promise.then(value => ({ value }), error => ({ error })).finally(() => { settled = true })
    await until(() => settled)
    const outcome = await result
    if (outcome.error) throw outcome.error
    return outcome.value
  }
  try {
    process.env.XDG_STATE_HOME = stateHome
    await mkdir(dirname(servicePath), { recursive: true })
    await writeFile(bindingPath, githubBinding)
    globalThis.fetch = async (url, options) => {
      try {
        const target = new URL(url)
        hostRequests.push({ url: target.href, options })
        assert.equal(target.origin, "http://127.0.0.1:43123")
        assert.equal(options.redirect, "error")
        assert.equal(options.headers.authorization, "Basic " + Buffer.from("opencode:fixture-password").toString("base64"))
        assert.equal(options.signal.aborted, false)
        let value
        if (target.pathname === "/api/info") {
          assert.equal(options.method, "GET")
          value = { pid: process.pid, version: "2.0.16" }
        } else {
          assert.equal(options.method, "POST")
          const body = JSON.parse(options.body)
          if (target.pathname === "/api/rpc/homonto.workflow/identity") {
            assert.equal(target.searchParams.get("location[directory]"), process.cwd())
            assert.deepEqual(body, { input: {} })
            assert.equal(typeof identityToken, "string")
            value = { output: { token: identityToken } }
          } else {
            assert.equal(target.pathname, "/api/session/ses_one/permission")
            assert.equal(body.sessionID, "ses_one")
            assert.match(body.id, /^per_/)
            assert.equal(body.source.type, "tool")
            assert.ok(body.source.id && body.source.messageID)
            assert.ok(["homonto_github_read", "shell"].includes(body.action))
            permissions.push(body)
            value = { data: { id: body.id, effect: "allow" } }
          }
        }
        return new Response(JSON.stringify(value), { status: 200, headers: { "content-type": "application/json" } })
      } catch (error) { fakeFailures.push(error); throw error }
    }
    githubSpawn = (argv, options) => {
      try {
        commands.push(argv)
        const reads = [
          [["homonto", "workspace", "inspect", "--json", "--config", config], { value: {
            schema_version: 2, config_path: config, config_root: root, repos: { source: process.cwd() },
          } }],
          [["git", "-C", process.cwd(), "remote", "get-url", "origin"], { raw: "git@github.com:owner/repo.git\n" }],
          [api("repos/owner/repo"), { value: { id: 42, full_name: "owner/repo", html_url: "https://github.com/owner/repo" } }],
          [api("user"), { value: actor }],
          [api("repos/owner/repo/issues/1"), { value: { id: 81, number: 1, state: "open", title: "Fixture issue",
            body: "private artifact", updated_at: "2026-09-01T00:00:00Z", locked: false, html_url: item.url } }],
          [api("repos/owner/repo/issues/1/comments?per_page=100", "GET", true), { value: [[]] }],
        ]
        if (JSON.stringify(argv) === JSON.stringify(api("repos/owner/repo/issues/1/comments", "POST"))) {
          assert.deepEqual(JSON.parse(new TextDecoder().decode(options.stdin)), { body: item.body })
          posts.push(argv)
          return { value: receipt }
        }
        assert.equal(options.stdin, "ignore")
        const match = reads.find(([expected]) => JSON.stringify(expected) === JSON.stringify(argv))
        assert.ok(match, `unexpected GitHub subprocess: ${JSON.stringify(argv)}`)
        return match[1]
      } catch (error) { fakeFailures.push(error); throw error }
    }
    const runtime = await setup({ event: bus })
    identityToken = runtime.identityToken
    const tool = (name, instance = runtime) => instance.definitions.find(d => d.name === name)
    const execute = async (name, input, call = invocation(), instance = runtime) => {
      const result = await bounded(tool(name, instance).execute(input, call))
      assert.deepEqual(Object.keys(result), ["content"])
      return JSON.parse(result.content)
    }
    const stage = (call = invocation()) => execute("homonto_github_draft", args, call)
    const status = (draft, call = invocation("build")) => execute("homonto_github_status", { draftID: draft.draftID }, call)
    const publish = (draft, call = invocation("build")) => execute("homonto_github_publish", { draftID: draft.draftID }, call)
    const noPublish = async (draft, expected = "pending") => {
      const before = counters()
      assert.equal((await publish(draft)).items[0].status, expected)
      assert.deepEqual(counters(), before, "unapproved drafts must not request publication permission or run gh")
      assert.equal(posts.length, 0)
    }
    const before = runtime.toolHooks.get("execute.before")
    assert.equal(typeof before, "function")
    const questionCall = draft => {
      const call = invocation("build")
      before({ tool: "question", sessionID: call.sessionID, id: call.id, input: structuredClone(draft.question) })
      return call
    }
    const form = (draft, call) => ({ id: `form_${++sequence}`, sessionID: call.sessionID,
      metadata: { kind: "question", tool: { id: call.id, messageID: call.messageID } },
      fields: draft.question.questions.map((q, n) => ({ key: `q${n}`, title: q.header, description: q.question,
        type: "string", custom: true, options: q.options.map(o => ({ value: o.label, label: o.label, description: o.description })) })) })
    const reply = (draft, nativeForm) => ({ type: "form.replied", data: { sessionID: "ses_one", id: nativeForm.id,
      answer: { q0: draft.question.questions[0].options[2].label } } })
    const send = async (...events) => { for (const event of events) bus.push(event); await bus.flush() }

    await assert.rejects(() => tool("homonto_github_draft").execute({ items: [] }, invocation()), /1–10 items/)
    const noService = counters()
    await rejected(() => stage(), "service_metadata_missing")
    assert.deepEqual(counters(), noService)
    await writeFile(servicePath, JSON.stringify({ url: "http://127.0.0.1:43123", pid: process.pid,
      version: "2.0.16", password: "fixture-password" }))

    for (const [field, code] of [["agent", "missing_agent"], ["sessionID", "missing_sessionID"],
      ["messageID", "missing_messageID"], ["signal", "missing_abort"], ["id", "missing_call_id"]]) {
      const call = invocation(), before = counters(), lookups = sessionGets
      delete call[field]
      await rejected(() => stage(call), code)
      assert.deepEqual(counters(), before)
      assert.equal(sessionGets, lookups, `${field} must be validated before host session lookup`)
    }
    for (const [name, input] of [["homonto_status", {}], ["homonto_handoff", { workflow: "onto", change: "one", identity: "id-one" }]]) {
      const before = counters(), lookups = sessionGets
      await assert.rejects(() => tool(name).execute(input, invocation("build")), error => {
        assert.match(error.message, /\[wrong_agent\]/)
        assert.ok(error.message.includes(`expected=${JSON.stringify(binding.coordinator ?? "homonto")}`))
        assert.match(error.message, /observed="build"; select the configured coordinator in OpenCode and retry/)
        assert.doesNotMatch(error.message, /id-one|private artifact|tasksCompleted/)
        return true
      })
      assert.deepEqual(counters(), before)
      assert.equal(sessionGets, lookups)
    }
    for (const [value, code] of [
      [undefined, "session_unavailable"],
      [{ ...session, location: undefined }, "session_unavailable"],
      [{ ...session, id: "other" }, "session_mismatch"],
      [{ ...session, projectID: "other" }, "project_mismatch"],
      [{ ...session, location: { directory: dirname(root) } }, "workspace_mismatch"],
      [{ ...session, location: { directory: join(root, "missing-session-directory") } }, "workspace_unavailable"],
    ]) {
      const before = counters()
      sessionValue = value
      await rejected(() => stage(), code)
      assert.deepEqual(counters(), before)
    }
    sessionValue = session
    const beforeScope = counters()
    getSession = async () => { throw new Error("private artifact lookup failure") }
    await rejected(() => stage(), "session_unavailable")
    getSession = undefined
    await writeFile(bindingPath, JSON.stringify({ ...binding, githubEnabled: true, configPath: join(root, "other.toml") }))
    await rejected(() => stage(), "binding_changed")
    await writeFile(bindingPath, githubBinding)
    assert.deepEqual(counters(), beforeScope)

    for (const reason of ["timeout", "cancel", "dispose"]) {
      const instance = reason === "dispose" ? await setup({ event: stream() }) : runtime
      let release
      getSession = () => new Promise(resolve => { release = resolve })
      const controller = new AbortController(), before = counters()
      const call = invocation("custom-agent", { signal: controller.signal })
      const pending = rejected(() => bounded(tool("homonto_github_draft", instance).execute(args, call)),
        reason === "timeout" ? "preflight_timeout" : "invocation_cancelled")
      await until(() => release)
      if (reason === "timeout") expire(5000)
      else if (reason === "cancel") controller.abort()
      else instance.dispose()
      await pending
      assert.equal(lastSignal.aborted, true)
      release(session)
      getSession = undefined
      await delay(10)
      assert.deepEqual(counters(), before, "late session resolution must not request permission or spawn")
      assert.equal(timers.size, 0)
      assert.equal((await stage()).items[0].status, "pending")
    }

    for (const behavior of [() => new Promise(() => {}), () => { throw new Error("progress threw") },
      () => Promise.reject(new Error("progress rejected"))]) {
      const draft = await stage(invocation("custom-agent", { progress(value) { reports.push(value); return behavior() } }))
      assert.equal(draft.items[0].status, "pending")
    }

    const controller = new AbortController(), draftCall = invocation("custom-agent", { signal: controller.signal })
    const draft = await stage(draftCall)
    assert.equal(draft.items[0].status, "pending")
    assert.equal(draft.items[0].body, item.body)
    assert.equal(draft.items[0].url, item.url)
    assert.deepEqual(draft.items[0].actor, actor)
    assert.deepEqual(draft.items[0].repository, { host: "github.com", id: 42, name: "owner/repo" })
    assert.equal(draft.question.questions.length, 1)
    const question = draft.question.questions[0]
    assert.equal(question.header, "GitHub 1")
    assert.equal(question.multiple, false)
    assert.deepEqual(JSON.parse(question.question), (({ status, ...preview }) => preview)(draft.items[0]))
    assert.deepEqual(question.options.slice(0, 2).map(o => o.label), ["Decline", "Revise"])
    assert.match(question.options[2].label, /^Publish [0-9a-f-]{36}$/)
    assert.ok(question.options.every(o => typeof o.description === "string" && o.description))
    assert.deepEqual(runtime.nestedTools, [])
    assert.equal(posts.length, 0)
    controller.abort()
    assert.deepEqual(await status(draft), draft)
    await noPublish(draft)

    const tampered = structuredClone(draft.question)
    tampered.questions[0].question += "tampered"
    const wrongCall = invocation()
    before({ tool: "question", sessionID: "ses_one", id: wrongCall.id, input: tampered })
    const unboundForm = form(draft, wrongCall)
    await send({ type: "form.created", data: { form: unboundForm } }, reply(draft, unboundForm))
    await noPublish(draft)
    const nativeCall = questionCall(draft), nativeForm = form(draft, nativeCall)
    assert.notEqual(nativeCall.id, draftCall.id)
    for (const mutate of [
      f => { f.metadata.tool.id = draftCall.id },
      f => { f.sessionID = "ses_other" },
      f => { f.metadata.kind = "other" },
      f => { f.fields[0].description += "tampered" },
      f => { f.fields[0].options[2].value = "Publish forged" },
    ]) {
      const invalid = structuredClone(nativeForm)
      mutate(invalid)
      await send({ type: "form.created", data: { form: invalid } }, reply(draft, nativeForm))
      await noPublish(draft)
    }
    await send({ type: "form.created", data: { form: nativeForm } },
      { ...reply(draft, nativeForm), data: { ...reply(draft, nativeForm).data, id: "wrong-form" } },
      { ...reply(draft, nativeForm), data: { ...reply(draft, nativeForm).data, sessionID: "ses_other" } })
    await noPublish(draft)
    await send({ type: "form.cancelled", data: { sessionID: "ses_one", id: nativeForm.id } }, reply(draft, nativeForm))
    await noPublish(draft, "declined")

    const forgedDraft = await stage(), forgedCall = questionCall(forgedDraft), forgedForm = form(forgedDraft, forgedCall)
    await send({ type: "form.created", data: { form: forgedForm } },
      { type: "form.replied", data: { sessionID: "ses_one", id: forgedForm.id, answer: { q0: "Publish forged" } } },
      reply(forgedDraft, forgedForm))
    await noPublish(forgedDraft, "declined")

    const approvedDraft = await stage(), approvedCall = questionCall(approvedDraft), approvedForm = form(approvedDraft, approvedCall)
    let releaseIdle
    getSession = () => new Promise(resolve => { releaseIdle = resolve })
    const beforeIdle = counters()
    bus.push({ type: "session.idle", data: { sessionID: "ses_one" } })
    await until(() => releaseIdle)
    bus.push({ type: "form.created", data: { form: approvedForm } })
    bus.push(reply(approvedDraft, approvedForm))
    expire(5000)
    await bus.flush()
    assert.equal(lastSignal.aborted, true)
    assert.equal(runtime.notifications.at(-1).variant, "error")
    releaseIdle(session)
    getSession = undefined
    await delay(10)
    assert.deepEqual(counters(), beforeIdle, "expired idle lookup must not launch a late snapshot")
    assert.equal((await status(approvedDraft)).items[0].status, "approved")
    assert.equal(posts.length, 0)
    const publishCall = invocation("build")
    const published = await publish(approvedDraft, publishCall)
    assert.equal(published.items[0].status, "published")
    assert.deepEqual(published.items[0].receipt, { id: receipt.id, url: receipt.html_url })
    assert.equal(posts.length, 1)
    const publicationPermission = permissions.at(-1)
    assert.equal(publicationPermission.action, "shell")
    assert.equal(publicationPermission.agent, "build")
    assert.deepEqual(publicationPermission.source, { type: "tool", messageID: publishCall.messageID, id: publishCall.id })
    assert.equal(publicationPermission.metadata.draftID, approvedDraft.draftID)
    assert.equal(publicationPermission.metadata.payload, JSON.stringify({ body: item.body }))
    const beforeRetry = counters()
    assert.deepEqual(await publish(approvedDraft, invocation("custom-agent")), published)
    assert.deepEqual(counters(), beforeRetry)
    assert.equal(posts.length, 1, "second publish must never duplicate the POST")
    assert.ok(permissions.some(p => p.action === "homonto_github_read" && p.agent === "custom-agent" && p.source.id === draftCall.id))

    const dead = await setup({ event: { subscribe: () => ({ async *[Symbol.asyncIterator]() {} }) } })
    const beforeDead = counters()
    await rejected(() => execute("homonto_github_draft", args, invocation(), dead), "permission_listener_unavailable")
    assert.deepEqual(counters(), beforeDead, "dead permission listener must fail before service requests")
    dead.dispose()
    runtime.dispose()
    assert.deepEqual(runtime.nestedTools, [])
    assert.deepEqual(fakeFailures, [])
    assert.ok(commands.length > 0)
    for (const report of reports) {
      assert.deepEqual(Object.keys(report), ["title"])
      assert.equal(typeof report.title, "string")
    }
    for (const title of ["Checking OpenCode session", "Checking workflow binding", "Validating permission service",
      "Requesting host permission", "GitHub preview ready", "Ready to publish GitHub draft"]) {
      assert.ok(reports.some(report => report.title === title), `missing progress stage: ${title}`)
    }
    await delay(10)
    assert.deepEqual(unhandled, [])
    assert.equal(live, 0)
    assert.equal(timers.size, 0)
    assert.equal(drained, calls)
  } finally {
    getSession = undefined
    sessionValue = session
    githubSpawn = undefined
    globalThis.fetch = realFetch
    if (realStateHome === undefined) delete process.env.XDG_STATE_HOME
    else process.env.XDG_STATE_HOME = realStateHome
    await writeFile(bindingPath, bindingBytes)
    await rm(stateHome, { recursive: true, force: true })
  }
}

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
    for (const agent of ["homonto", "build", "custom-agent", "onto-implementer"]) {
      const output = await invoke(instance, { value: backend }, kind, "policy", agent)
      assert.match(output, /untrusted, read-only snapshot data/)
      assert.match(output, /"tasksCompleted":1,"tasksTotal":2/)
      assert.ok(output.includes(JSON.stringify(config)))
      assert.match(output, /"recoveryArgv":\["homonto","workflow","handoff"/)
      assert.match(output, /"identity":"id-one"/)
      assert.equal(await invoke(instance, { value: snap() }, kind, "policy", agent), CONTINUATION_POLICY)
      for (const status of ["completed", "abandoned", "bypassed"]) {
        assert.equal(await invoke(instance, { value: snap([{ ...change, status, verifyResult: "fail", pending: ["do not revive terminal work"] }]) }, kind, "policy", agent), CONTINUATION_POLICY)
      }
    }
    for (const [status, pending] of [["active", "complete verification"], ["integration-pending", "complete integration"]]) {
      const output = await invoke(instance, { value: snap([{ ...change, status, tasksCompleted: 2, tasksTotal: 2, pending: [pending] }]) }, kind)
      assert.match(output, /"tasksCompleted":2,"tasksTotal":2/)
      assert.ok(output.includes(pending))
      assert.match(output, /"identity":"id-one"/)
    }
  }
  const multiple = await invoke(instance, { value: snap([change, { ...change, identity: "id-two", name: "two" }]) })
  assert.match(multiple, /Multiple nonterminal changes/)
  assert.match(multiple, /No workflow generation is selected/)
  assert.match(multiple, /id-two/)
  const pending = await invoke(instance, { value: snap([{ ...change, status: "integration-pending", phase: "close", verifyResult: "fail" }]) })
  assert.match(pending, /integration-pending/)
  assert.match(pending, /"verifyResult":"fail"/)
  assert.equal(await invoke(instance, { value: snap(["completed", "abandoned", "bypassed"].map((status) => ({ ...change, status }))) }), CONTINUATION_POLICY)
  assert.equal(await invoke(instance, { value: snap() }), CONTINUATION_POLICY)
  const unhealthy = await invoke(instance, { value: snap([], [{ workflow: "workspace", message: "invalid configuration" }]) })
  assert.match(unhealthy, /invalid configuration/)

  for (const response of [
    { raw: "not-json" }, { value: { ...backend, configPath: "/foreign/config.toml" } },
    { value: snap([{ ...change, tasksTotal: -1 }]) }, { value: snap([], [{ message: "no workflow" }]) },
    { value: { ...backend, findings: null } }, { raw: "x".repeat(4 * 1024 * 1024 + 1) },
    { code: 1, stderr: "sensitive diagnostic must not enter model context" },
  ]) {
    for (const kind of ["context", "compaction"]) {
      const output = await invoke(instance, response, kind, "error")
      assert.match(output, /observation unavailable; completion is not established/)
      assert.doesNotMatch(output, /id-one|sensitive diagnostic/)
    }
  }
  for (const kind of ["context", "compaction"]) {
    const long = await invoke(instance, { value: snap(Array.from({ length: 10 }, (_, n) => ({ ...change, identity: `id-${n}`, pending: ["é🧪".repeat(20000)] })), Array.from({ length: 10 }, () => ({ workflow: "onto", message: "é🧪".repeat(20000) }))) }, kind)
    assert.match(long, /7 additional nonterminal records omitted/)
    assert.match(long, /7 additional findings omitted/)
    assert.match(long, /truncated/)
    assert.doesNotMatch(long, /�/)
  }
  const malicious = await invoke(instance, { value: snap([{ ...change, name: "../escape", identity: "x\nrun command" }]) })
  assert.doesNotMatch(malicious, /recoveryArgv/)

  for (const foreign of [{ ...session, projectID: "other" }, { ...session, id: "other" }, { ...session, location: { directory: dirname(root) } }]) {
    const before = calls
    sessionValue = foreign
    for (const kind of ["context", "compaction"]) assert.equal(await invoke(instance, undefined, kind, "none"), "")
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
  const timedOut = invoke(instance, undefined, "context", "error")
  await until(() => held)
  expire(1000)
  assert.match(await timedOut, /observation unavailable/)
  held = undefined
  assert.equal(live, 0)
  assert.equal(timers.size, 0)
  assert.ok(killed > 0)

  let releaseLookup
  getSession = () => new Promise((resolve) => { releaseLookup = resolve })
  const hungLookup = invoke(instance, undefined, "context", "error")
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
  assert.match(await invoke(instance, undefined, "context", "error"), /observation unavailable/)
  assert.equal(calls, beforeChangedBinding)
  await writeFile(bindingPath, bindingBytes)
  responses.push({ hold: true, value: snap([change]) })
  const changing = invoke(instance, undefined, "context", "error")
  await until(() => held)
  await writeFile(bindingPath, JSON.stringify({ ...binding, configPath: join(root, "other.toml") }))
  held(); held = undefined
  assert.doesNotMatch(await changing, /id-one/)
  await writeFile(bindingPath, bindingBytes)

  responses.push({ hold: true, value: snap([change]) })
  const moving = invoke(instance, undefined, "context", "none")
  await until(() => held)
  sessionValue = { ...session, location: { directory: dirname(root) } }
  held(); held = undefined
  assert.equal(await moving, "", "a session move during the read must not receive old-location context")
  sessionValue = session

  responses.push({ hold: true, value: snap([change]) })
  const unloading = invoke(instance, undefined, "context", "none")
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
  for (const kind of ["context", "compaction"]) assert.equal(await invoke(instance, undefined, kind, "none"), "")
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
  await githubIntegration()
  const afterGithub = calls

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
  assert.equal(calls, afterGithub)
  process.stdout.write("V2 context runtime: hooks, RPC, binding, scope, budgets, coalescing, GitHub native approval, preflight and cleanup passed\n")
} finally {
  for (const dispose of cleanups) dispose()
  await writeFile(bindingPath, bindingBytes)
  globalThis.setTimeout = realTimeout
  globalThis.clearTimeout = realClearTimeout
  globalThis.Bun = realBun
  globalThis.fetch = realFetch
  if (realStateHome === undefined) delete process.env.XDG_STATE_HOME
  else process.env.XDG_STATE_HOME = realStateHome
  process.off("unhandledRejection", onUnhandled)
}
