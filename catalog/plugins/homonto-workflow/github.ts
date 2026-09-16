import { randomUUID } from "node:crypto"
import { dirname, isAbsolute, normalize } from "node:path"

type Run = (argv: string[], options?: { signal?: AbortSignal; stdin?: string; timeout?: number }) => Promise<string>
type ToolContext = {
  sessionID: string
  messageID: string
  agent: string
  abort: AbortSignal
  ask(permission: { permission: string; patterns: string[]; always: string[]; metadata: Record<string, unknown> }): Promise<void>
}
type InputItem = {
  kind: "issue_comment" | "pr_comment" | "pr_review"
  url: string
  body: string
  baseOID: string
  headOID: string
  reviewEvent: "" | "COMMENT" | "APPROVE" | "REQUEST_CHANGES"
}
type Target = { host: string; repo: string; number: number; type: "issues" | "pull"; url: string }
type Repo = { host: string; name: string; id: number }
type Snapshot = {
  repo: Repo
  actor: { id: number; login: string }
  id: number
  number: number
  state: string
  title: string
  body: string | null
  updated_at: string
  baseOID: string
  headOID: string
  locked: boolean
  merged: boolean
  draft: boolean
}
type Status = "pending" | "approved" | "declined" | "revision-requested" | "invalidated" | "expired" | "publishing" | "published" | "stale" | "blocked" | "uncertain"
type Item = InputItem & { itemID: string; target: Target; snapshot: Snapshot; payload: string; status: Status; receipt?: { id: number; url: string }; reason?: string }
type Question = { header: string; question: string; options: { label: string; description: string }[]; multiple: false }
type Session = { deleted: boolean; staging: boolean; current?: string; cancel: AbortController }
type Draft = { draftID: string; sessionID: string; expiresAt: number; invalidated: boolean; items: Item[]; question: { questions: Question[] }; callID?: string; requestID?: string; answered: boolean }

const TTL = 15 * 60 * 1000
const MAX_SESSIONS = 64
const MAX_DRAFTS = 128
const MAX_REQUESTS = 512
const bytes = (s: string) => new TextEncoder().encode(s).length
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v)
const id = (v: unknown): v is number => Number.isSafeInteger(v) && (v as number) > 0
const text = (v: unknown): v is string => typeof v === "string"
const oid = (v: unknown): v is string => text(v) && /^[a-f0-9]{40}$/.test(v)
const pathOK = (v: unknown): v is string => text(v) && isAbsolute(v) && normalize(v) === v && !/[\x00-\x1f\x7f*?]/.test(v)
const nameOK = (v: unknown): v is string => text(v) && /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9_][A-Za-z0-9_.-]*$/.test(v)
const hostOK = (v: string) => v.length <= 253 && v.includes(".") && v.split(".").every(x => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(x)) && !/^\d+(?:\.\d+)+$/.test(v)
function check(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message) }
function exact(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (Array.isArray(b)) return Array.isArray(a) && a.length === b.length && b.every((v, i) => exact(a[i], v))
  if (!record(a) || !record(b)) return false
  const keys = Object.keys(b)
  return Object.keys(a).length === keys.length && keys.every(k => Object.hasOwn(a, k) && exact(a[k], b[k]))
}
function keys(v: unknown, expected: string[]): asserts v is Record<string, unknown> {
  check(record(v) && Object.keys(v).length === expected.length && expected.every(k => Object.hasOwn(v, k)), "invalid arguments")
}
function parseTarget(value: unknown): Target {
  check(text(value) && bytes(value) <= 2048, "invalid GitHub URL")
  const m = /^https:\/\/([^/]+)\/([^/]+\/[^/]+)\/(issues|pull)\/([1-9][0-9]*)$/.exec(value)
  check(m && hostOK(m[1]) && nameOK(m[2]) && id(Number(m[4])), "expected a plain HTTPS issue or pull URL")
  return { host: m[1], repo: m[2], type: m[3] as Target["type"], number: Number(m[4]), url: value }
}
function origin(value: string): { host: string; repo: string } {
  const raw = value.trim()
  const m = /^(?:https:\/\/|ssh:\/\/git@|git@)([a-z0-9.-]+)[/:]([^\s/:]+\/[^\s/]+?)(?:\.git)?$/.exec(raw)
  check(m && hostOK(m[1]) && nameOK(m[2]) && !/[?#@%]/.test(m[2]), "unsupported origin URL")
  return { host: m[1], repo: m[2] }
}
function inputItems(args: unknown): InputItem[] {
  keys(args, ["items"])
  check(Array.isArray(args.items) && args.items.length > 0 && args.items.length <= 10, "draft requires 1–10 items")
  let total = 0
  const items = args.items.map(v => {
    keys(v, ["kind", "url", "body", "baseOID", "headOID", "reviewEvent"])
    check(["issue_comment", "pr_comment", "pr_review"].includes(v.kind as string), "unsupported operation")
    const t = parseTarget(v.url)
    check(text(v.body) && v.body.trim() && bytes(v.body) <= 8192 && !v.body.includes("\u0000"), "body must contain 1–8192 UTF-8 bytes")
    check(text(v.baseOID) && text(v.headOID) && text(v.reviewEvent), "invalid review fields")
    if (v.kind === "issue_comment") {
      check(t.type === "issues" && v.baseOID === "" && v.headOID === "" && v.reviewEvent === "", "issue fields must be empty")
    } else {
      check(t.type === "pull" && oid(v.baseOID) && oid(v.headOID), "PR requires reviewed base/head OIDs")
      check(v.kind === "pr_review" ? ["COMMENT", "REQUEST_CHANGES"].includes(v.reviewEvent) : v.reviewEvent === "", "formal reviews support COMMENT or REQUEST_CHANGES only")
    }
    total += bytes(v.body)
    return { ...v } as InputItem
  })
  check(total <= 32768, "batch bodies exceed 32 KiB")
  return items
}

export function createGithubDrafts({ run, configPath }: { run: Run; configPath: string }) {
  check(typeof run === "function" && pathOK(configPath), "runner and bound absolute configPath are required")
  const sessions = new Map<string, Session>()
  const drafts = new Map<string, Draft>()
  const requests = new Set<string>()
  const lifetime = new AbortController()
  let disposed = false

  function expire() {
    for (const d of drafts.values()) {
      if (Date.now() >= d.expiresAt) {
        d.answered = true
        for (const i of d.items) if (["pending", "approved"].includes(i.status)) i.status = "expired"
      }
    }
  }
  function invalidate(d: Draft) {
    d.invalidated = true
    d.answered = true
    for (const i of d.items) if (["pending", "approved"].includes(i.status)) i.status = "invalidated"
  }
  function guard(ctx: ToolContext): Session {
    expire()
    check(!disposed && ctx && text(ctx.agent) && ctx.agent.length > 0 && ctx.agent.length <= 256 &&
      text(ctx.sessionID) && ctx.sessionID.length > 0 && ctx.sessionID.length <= 256 && text(ctx.messageID) && ctx.messageID.length > 0 &&
      ctx.abort instanceof AbortSignal && !ctx.abort.aborted && typeof ctx.ask === "function", "GitHub tools require a live session context")
    let s = sessions.get(ctx.sessionID)
    if (!s) {
      check(sessions.size < MAX_SESSIONS, "GitHub session capacity reached")
      s = { deleted: false, staging: false, cancel: new AbortController() }
      sessions.set(ctx.sessionID, s)
    }
    check(!s.deleted, "session deleted")
    return s
  }
  function live(ctx: ToolContext, d?: Draft) {
    const s = guard(ctx)
    if (d) check(!d.invalidated && s.current === d.draftID && Date.now() < d.expiresAt, "draft invalidated or expired")
  }
  async function command(argv: string[], ctx: ToolContext, stdin?: string): Promise<string> {
    const s = guard(ctx)
    const result = await run(argv, { signal: AbortSignal.any([ctx.abort, lifetime.signal, s.cancel.signal]), timeout: 15000, ...(stdin === undefined ? {} : { stdin }) })
    guard(ctx)
    check(text(result) && bytes(result) <= 8 * 1024 * 1024, "command output exceeded limit")
    return result
  }
  function argv(host: string, endpoint: string, method = "GET", paginated = false): string[] {
    return ["gh", "api", "--hostname", host, "--method", method, "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28", ...(paginated ? ["--paginate", "--slurp"] : []), endpoint, ...(method === "POST" ? ["--input", "-"] : [])]
  }
  async function api(host: string, endpoint: string, ctx: ToolContext, paginated = false): Promise<unknown> {
    return JSON.parse(await command(argv(host, endpoint, "GET", paginated), ctx))
  }
  async function repo(host: string, name: string, ctx: ToolContext): Promise<Repo> {
    const v = await api(host, `repos/${name}`, ctx)
    check(record(v) && id(v.id) && nameOK(v.full_name) && v.html_url === `https://${host}/${v.full_name}`, "invalid repository identity")
    return { host, id: v.id, name: v.full_name }
  }
  async function scope(ctx: ToolContext): Promise<Repo[]> {
    const v: unknown = JSON.parse(await command(["homonto", "workspace", "inspect", "--json", "--config", configPath], ctx))
    check(record(v) && [0, 1, 2].includes(v.schema_version as number) && v.config_path === configPath && v.config_root === dirname(configPath) && record(v.repos), "invalid workspace inspect contract")
    const paths = Object.values(v.repos)
    if ((v.schema_version as number) < 2) paths.push(v.config_root)
    check(paths.length > 0 && paths.length <= 64 && paths.every(pathOK) && new Set(paths).size === paths.length, "invalid or empty declared source scope")
    const result: Repo[] = []
    for (const p of paths) {
      const o = origin(await command(["git", "-C", p as string, "remote", "get-url", "origin"], ctx))
      const r = await repo(o.host, o.repo, ctx)
      check(!result.some(other => other.host === r.host && other.id === r.id), "ambiguous repository aliases")
      result.push(r)
    }
    return result
  }
  async function snapshot(t: Target, ctx: ToolContext): Promise<Snapshot> {
    const allowed = await scope(ctx)
    check(allowed.some(r => r.host === t.host), "target host is outside selected source scope")
    const r = await repo(t.host, t.repo, ctx)
    check(allowed.filter(a => a.host === r.host && a.id === r.id).length === 1, "target is outside selected source scope")
    const actor = await api(t.host, "user", ctx)
    check(record(actor) && id(actor.id) && text(actor.login) && /^[A-Za-z0-9][A-Za-z0-9-]*$/.test(actor.login), "invalid authenticated actor")
    const v = await api(t.host, `repos/${r.name}/${t.type === "pull" ? "pulls" : "issues"}/${t.number}`, ctx)
    check(record(v) && id(v.id) && v.number === t.number && ["open", "closed"].includes(v.state as string) && text(v.title) && bytes(v.title) <= 8192 &&
      (v.body === null || (text(v.body) && bytes(v.body) <= 262144)) && text(v.updated_at) && Number.isFinite(Date.parse(v.updated_at)) &&
      typeof v.locked === "boolean" && v.html_url === `https://${t.host}/${r.name}/${t.type}/${t.number}`, "invalid destination snapshot")
    let baseOID = "", headOID = "", merged = false, draft = false
    if (t.type === "issues") check(!Object.hasOwn(v, "pull_request"), "issue URL resolves to a pull request")
    else {
      check(record(v.base) && oid(v.base.sha) && record(v.head) && oid(v.head.sha) && record(v.base.repo) && v.base.repo.id === r.id &&
        typeof v.merged === "boolean" && typeof v.draft === "boolean", "invalid pull snapshot")
      baseOID = v.base.sha; headOID = v.head.sha; merged = v.merged; draft = v.draft
    }
    return { repo: r, actor: { id: actor.id, login: actor.login }, id: v.id, number: t.number, state: v.state as string,
      title: v.title, body: v.body as string | null, updated_at: v.updated_at, locked: v.locked, baseOID, headOID, merged, draft }
  }
  function endpoint(i: Item) { return `repos/${i.snapshot.repo.name}/${i.kind === "pr_review" ? "pulls" : "issues"}/${i.target.number}/${i.kind === "pr_review" ? "reviews" : "comments"}` }
  function receipt(v: unknown, i: Item): { id: number; url: string } {
    check(record(v) && id(v.id) && record(v.user) && v.user.id === i.snapshot.actor.id && v.user.login === i.snapshot.actor.login && v.body === i.body, "invalid receipt actor or body")
    const base = `https://${i.target.host}/${i.snapshot.repo.name}/${i.target.type}/${i.target.number}`
    check(v.html_url === `${base}#${i.kind === "pr_review" ? "pullrequestreview-" : "issuecomment-"}${v.id}`, "invalid receipt destination URL")
    if (i.kind === "pr_review") {
      check(v.commit_id === i.headOID && v.state === (i.reviewEvent === "COMMENT" ? "COMMENTED" : "CHANGES_REQUESTED"), "invalid review receipt")
      const apiRoot = i.target.host === "github.com" ? "https://api.github.com" : `https://${i.target.host}/api/v3`
      check(v.pull_request_url === `${apiRoot}/repos/${i.snapshot.repo.name}/pulls/${i.target.number}`, "invalid review destination")
    } else {
      const apiRoot = i.target.host === "github.com" ? "https://api.github.com" : `https://${i.target.host}/api/v3`
      check(v.issue_url === `${apiRoot}/repos/${i.snapshot.repo.name}/issues/${i.target.number}`, "invalid comment destination")
    }
    return { id: v.id, url: v.html_url as string }
  }
  async function existing(i: Item, ctx: ToolContext): Promise<{ id: number; url: string }[]> {
    const pages = await api(i.target.host, `${endpoint(i)}?per_page=100`, ctx, true)
    check(Array.isArray(pages) && pages.length > 0 && pages.length <= 1000 && pages.every(p => Array.isArray(p) && p.length <= 100), "invalid paginated destination response")
    const found = new Map<number, { id: number; url: string }>()
    for (const v of pages.flat()) {
      check(record(v) && id(v.id) && record(v.user) && id(v.user.id) && text(v.user.login) && text(v.body), "invalid remote publication record")
      if (v.body !== i.body || v.user.id !== i.snapshot.actor.id || v.user.login !== i.snapshot.actor.login) continue
      if (i.kind === "pr_review" && (v.commit_id !== i.headOID || v.state !== (i.reviewEvent === "COMMENT" ? "COMMENTED" : "CHANGES_REQUESTED"))) continue
      const r = receipt(v, i)
      found.set(r.id, r)
    }
    return [...found.values()]
  }
  function preview(i: Item) {
    return { itemID: i.itemID, kind: i.kind, url: i.target.url, repository: i.snapshot.repo, actor: i.snapshot.actor,
      body: i.body, baseOID: i.baseOID, headOID: i.headOID, reviewEvent: i.reviewEvent }
  }
  function view(d: Draft) {
    expire()
    return JSON.stringify({ draftID: d.draftID, expiresAt: d.expiresAt, items: d.items.map(i => ({ ...preview(i), status: i.status, ...(i.receipt ? { receipt: i.receipt } : {}), ...(i.reason ? { reason: i.reason } : {}) })), question: d.question })
  }
  function get(args: unknown, ctx: ToolContext) {
    guard(ctx)
    keys(args, ["draftID"])
    check(text(args.draftID), "draftID required")
    const d = drafts.get(args.draftID)
    check(d && d.sessionID === ctx.sessionID, "unknown draft for this session")
    return d
  }
  async function stage(args: unknown, ctx: ToolContext) {
    const s = guard(ctx)
    const inputs = inputItems(args)
    check(!s.staging, "draft staging already in progress")
    check(![...drafts.values()].some(d => d.sessionID === ctx.sessionID && d.items.some(i => i.status === "publishing" || i.status === "uncertain")), "publication pending or uncertain; reconcile it before staging another draft")
    for (const [key, d] of drafts) if (Date.now() >= d.expiresAt && !d.items.some(i => i.status === "publishing")) drafts.delete(key)
    check(drafts.size < MAX_DRAFTS, "GitHub draft capacity reached")
    if (s.current) { const old = drafts.get(s.current); if (old) invalidate(old) }
    s.current = undefined
    s.staging = true
    const draftID = randomUUID()
    const d: Draft = { draftID, sessionID: ctx.sessionID, expiresAt: Date.now() + TTL, invalidated: false, items: [], question: { questions: [] }, answered: false }
    drafts.set(draftID, d)
    try {
      for (const input of inputs) {
        await ctx.ask({ permission: "homonto_github_read", patterns: [input.url], always: [], metadata: { url: input.url } })
        live(ctx)
        const t = parseTarget(input.url)
        const snap = await snapshot(t, ctx)
        check(input.baseOID === snap.baseOID && input.headOID === snap.headOID, "reviewed base/head OIDs differ from remote")
        check(input.kind !== "pr_review" || (snap.state === "open" && !snap.merged), "formal review requires an open unmerged PR")
        const body = input.kind === "issue_comment" ? input.body : `${input.body}\n\nReviewed base: ${input.baseOID}\nReviewed head: ${input.headOID}`
        check(bytes(body) <= 8192, "body with reviewed OIDs exceeds 8 KiB")
        const payload = JSON.stringify(input.kind === "pr_review" ? { commit_id: input.headOID, event: input.reviewEvent, body } : { body })
        d.items.push({ ...input, body, itemID: randomUUID(), target: { ...t, repo: snap.repo.name, url: `https://${t.host}/${snap.repo.name}/${t.type}/${t.number}` }, snapshot: snap, payload, status: "pending" })
      }
      check(d.items.reduce((n, i) => n + bytes(i.body), 0) <= 32768, "batch bodies with OIDs exceed 32 KiB")
      check(new Set(d.items.map(i => JSON.stringify([i.kind, i.target.url, i.payload]))).size === d.items.length, "duplicate items in batch")
      d.question.questions = d.items.map((i, n) => ({ header: `GitHub ${n + 1}`, question: JSON.stringify(preview(i), null, 2), options: [
        { label: "Decline", description: "Do not publish this item." },
        { label: "Revise", description: "Request a new immutable draft." },
        { label: `Publish ${randomUUID()}`, description: "Approve this exact destination, actor, payload and reviewed commits." },
      ], multiple: false }))
      live(ctx)
      check(Date.now() < d.expiresAt, "draft expired while staging")
      s.current = draftID
      return view(d)
    } catch (error) {
      invalidate(d)
      drafts.delete(draftID)
      throw error
    } finally { s.staging = false }
  }
  async function publish(args: unknown, ctx: ToolContext) {
    const d = get(args, ctx)
    for (const i of d.items.filter(i => i.status === "uncertain")) {
      try {
        const matches = await existing(i, ctx)
        if (matches.length === 1) { i.receipt = matches[0]; i.status = "published"; delete i.reason }
      } catch { }
    }
    const selected = d.items.filter(i => i.status === "approved")
    if (!selected.length) return view(d)
    live(ctx, d)
    for (const i of selected) i.status = "publishing"
    for (const i of selected) {
      let attempted = false
      try {
        live(ctx, d)
        const cmd = argv(i.target.host, endpoint(i), "POST")
        const pattern = cmd.map(a => /^[A-Za-z0-9_./:-]+$/.test(a) ? a : `'${a.replaceAll("'", "'\\''")}'`).join(" ")
        await ctx.ask({ permission: "bash", patterns: [pattern], always: [], metadata: { draftID: d.draftID, itemID: i.itemID, url: i.target.url, payload: i.payload } })
        live(ctx, d)
        const matches = await existing(i, ctx)
        live(ctx, d)
        const fresh = await snapshot(i.target, ctx)
        live(ctx, d)
        if (!exact(fresh, i.snapshot)) { i.status = "stale"; i.reason = "destination snapshot or authenticated actor changed"; continue }
        if (matches.length > 1) { i.status = "uncertain"; i.reason = "multiple matching remote publications"; continue }
        if (matches.length === 1) { i.receipt = matches[0]; i.status = "published"; continue }
        live(ctx, d)
        attempted = true
        const v: unknown = JSON.parse(await command(cmd, ctx, i.payload))
        i.receipt = receipt(v, i)
        i.status = "published"
      } catch {
        i.status = attempted ? "uncertain" : "blocked"
        i.reason = attempted ? "publication outcome uncertain; never resend this item" : "publication preflight denied, invalidated or unavailable; create a new draft"
        if (attempted) {
          try {
            live(ctx, d)
            const matches = await existing(i, ctx)
            live(ctx, d)
            if (matches.length === 1) { i.receipt = matches[0]; i.status = "published"; delete i.reason }
          } catch { }
        }
      }
    }
    return view(d)
  }
  function before(input: { tool: string; sessionID: string; callID: string }, output: { args: unknown }) {
    expire()
    if (disposed || input.tool !== "question" || !text(input.callID) || !input.callID || input.callID.length > 256) return
    const s = sessions.get(input.sessionID)
    const d = s?.current ? drafts.get(s.current) : undefined
    if (!s || s.deleted || !d || d.answered || d.callID || !d.items.every(i => i.status === "pending") || !exact(output.args, d.question)) return
    d.callID = input.callID
  }
  function event({ event: e }: { event: { type: string; properties?: unknown } }) {
    expire()
    if (e.type === "server.instance.disposed") { dispose(); return }
    if (disposed || !record(e.properties)) return
    const p = e.properties
    if (e.type === "session.deleted") {
      const sessionID = record(p.info) ? p.info.id : p.sessionID
      if (!text(sessionID)) return
      const s = sessions.get(sessionID)
      if (s) {
        s.deleted = true
        s.cancel.abort()
        for (const d of drafts.values()) if (d.sessionID === sessionID) invalidate(d)
      } else if (sessions.size < MAX_SESSIONS) sessions.set(sessionID, { deleted: true, staging: false, cancel: new AbortController() })
      return
    }
    if (!text(p.sessionID)) return
    const s = sessions.get(p.sessionID)
    const d = s?.current ? drafts.get(s.current) : undefined
    if (!s || s.deleted || !d || d.answered || !d.items.every(i => i.status === "pending")) return
    if (e.type === "question.asked") {
      if (!d.callID || d.requestID || !text(p.id) || !p.id || p.id.length > 256 || requests.has(p.id) || requests.size >= MAX_REQUESTS ||
        !record(p.tool) || p.tool.callID !== d.callID || !text(p.tool.messageID) || !p.tool.messageID || !exact(p.questions, d.question.questions)) return
      d.requestID = p.id
      requests.add(p.id)
      return
    }
    if (!d.requestID || p.requestID !== d.requestID) return
    if (e.type === "question.rejected") { d.answered = true; for (const i of d.items) i.status = "declined"; return }
    if (e.type !== "question.replied") return
    d.answered = true
    const answers = p.answers
    if (!Array.isArray(answers) || answers.length !== d.items.length || answers.some((a, n) => !Array.isArray(a) || a.length !== 1 || !d.question.questions[n].options.some(o => o.label === a[0]))) {
      for (const i of d.items) i.status = "declined"
      return
    }
    d.items.forEach((i, n) => { i.status = answers[n][0] === "Decline" ? "declined" : answers[n][0] === "Revise" ? "revision-requested" : "approved" })
  }
  function dispose() {
    if (disposed) return
    disposed = true
    lifetime.abort()
    for (const d of drafts.values()) invalidate(d)
  }
  const draftArgs = { items: { type: "array", minItems: 1, maxItems: 10, items: { type: "object", additionalProperties: false,
    required: ["kind", "url", "body", "baseOID", "headOID", "reviewEvent"], properties: {
      kind: { type: "string", enum: ["issue_comment", "pr_comment", "pr_review"] }, url: { type: "string" }, body: { type: "string", maxLength: 8192 },
      baseOID: { type: "string" }, headOID: { type: "string" }, reviewEvent: { type: "string", enum: ["", "COMMENT", "REQUEST_CHANGES"] },
    } } } }
  const idArgs = { draftID: { type: "string" } }
  return {
    tools: {
      homonto_github_draft: { description: "Stage an immutable, memory-only GitHub batch. PR bodies gain reviewed OIDs before preview. Formal reviews support COMMENT/REQUEST_CHANGES only. Submit the returned question arguments unchanged to native question; approval is workflow confirmation, not human-only attestation. New batches invalidate earlier session drafts.", args: draftArgs, execute: stage },
      homonto_github_status: { description: "Read the session's draft statuses, exact full preview and native question arguments.", args: idArgs, execute: async (args: unknown, ctx: ToolContext) => view(get(args, ctx)) },
      homonto_github_publish: { description: "Publish only items approved through correlated native question events, after permission and fresh scope/actor/destination checks. Uncertain outcomes are never blindly retried.", args: idArgs, execute: publish },
    },
    event,
    before,
    dispose,
    context(sessionID: string): string {
      expire()
      if (disposed || sessions.get(sessionID)?.deleted) return "GitHub drafts: disposed"
      return JSON.stringify({ drafts: [...drafts.values()].filter(d => d.sessionID === sessionID).map(d => ({ draftID: d.draftID, items: d.items.map(i => ({ itemID: i.itemID, status: i.status })) })) })
    },
  }
}
