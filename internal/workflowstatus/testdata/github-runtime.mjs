import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const { createGithubDrafts } = await import(pathToFileURL(process.argv[2]).href)
const A = 'a'.repeat(40), B = 'b'.repeat(40)
const configPath = '/workspace/control/selected.toml'
const url = 'https://github.com/owner/repo/issues/1'
const item = (overrides = {}) => ({ kind: 'issue_comment', url, body: 'Research only.\nNo implementation.', baseOID: '', headOID: '', reviewEvent: '', ...overrides })
const context = (overrides = {}) => ({ sessionID: 'session-1', messageID: 'message-1', agent: 'coordinator', abort: new AbortController().signal, async ask() {}, ...overrides })

function setup() {
  const calls = [], comments = []
  let mutateError = '', state = 'open', head = B, actor = 7, denyScope = false
  let nextID = 100, beforeMutation
  const run = async (argv, options = {}) => {
    calls.push({ argv, options })
    assert.ok(!options.signal?.aborted)
    if (argv[0] === 'homonto') {
      assert.deepEqual(argv, ['homonto', 'workspace', 'inspect', '--json', '--config', configPath])
      return JSON.stringify({ schema_version: 2, config_path: configPath, config_root: '/workspace/control', repos: { app: '/workspace/source' } })
    }
    if (argv[0] === 'git') {
      assert.deepEqual(argv, ['git', '-C', '/workspace/source', 'remote', 'get-url', 'origin'])
      return 'git@github.com:owner/repo.git\n'
    }
    assert.equal(argv[0], 'gh')
    assert.equal(argv[argv.indexOf('--hostname') + 1], 'github.com')
    const endpoint = argv.find(a => a === 'user' || a.startsWith('repos/'))
    const post = argv[argv.indexOf('--method') + 1] === 'POST'
    if (endpoint === 'user') return JSON.stringify({ id: actor, login: 'author' })
    if (endpoint === 'repos/owner/repo') return JSON.stringify({ id: denyScope ? 999 : 20, full_name: 'owner/repo', html_url: 'https://github.com/owner/repo' })
    if (post) {
      assert.deepEqual(argv.slice(-2), ['--input', '-'])
      const body = JSON.parse(options.stdin)
      const isReview = endpoint.includes('/reviews')
      const number = endpoint.includes('/2/') ? 2 : 1
      const type = isReview || number === 2 ? 'pull' : 'issues'
      const id = nextID++
      const record = { id, user: { id: actor, login: 'author' }, body: body.body,
        html_url: `https://github.com/owner/repo/${type}/${number}#${isReview ? 'pullrequestreview-' : 'issuecomment-'}${id}`,
        issue_url: `https://api.github.com/repos/owner/repo/issues/${number}`,
        pull_request_url: `https://api.github.com/repos/owner/repo/pulls/${number}`,
        commit_id: body.commit_id, state: body.event === 'COMMENT' ? 'COMMENTED' : 'CHANGES_REQUESTED' }
      if (beforeMutation) await beforeMutation()
      if (mutateError !== 'lost-without-record') comments.push(record)
      if (mutateError) throw new Error(mutateError)
      return JSON.stringify(record)
    }
    if (endpoint.includes('?per_page=100')) {
      assert.ok(argv.includes('--paginate') && argv.includes('--slurp'))
      return JSON.stringify([comments])
    }
    const pr = endpoint.includes('/pulls/')
    return JSON.stringify({ id: pr ? 31 : 30, number: pr ? 2 : 1, state, title: 'Research issue', body: 'Original context', updated_at: '2026-09-13T00:00:00Z', locked: false,
      html_url: `https://github.com/owner/repo/${pr ? 'pull/2' : 'issues/1'}`,
      ...(pr ? { base: { sha: A, repo: { id: 20 } }, head: { sha: head }, merged: false, draft: false } : {}) })
  }
  const service = createGithubDrafts({ run, configPath, coordinator: 'coordinator' })
  return { service, calls, comments, postCount: () => calls.filter(c => c.argv.includes('POST')).length,
    setState: value => state = value, setHead: value => head = value, setActor: value => actor = value,
    setError: value => mutateError = value, setBeforeMutation: value => beforeMutation = value }
}
const invoke = async (s, name, args, ctx = context()) => JSON.parse(await s.service.tools[`homonto_github_${name}`].execute(args, ctx))
const stage = (s, items = [item()], ctx) => invoke(s, 'draft', { items }, ctx)
function asked(s, d, ctx = context(), mutate = x => x) {
  s.service.before({ tool: 'question', sessionID: ctx.sessionID, callID: 'call-1' }, { args: d.question })
  s.service.event({ event: { type: 'question.asked', properties: mutate({ id: `que_${d.draftID}`, sessionID: ctx.sessionID, questions: d.question.questions, tool: { messageID: 'question-message', callID: 'call-1' } }) } })
}
function reply(s, d, choices = ['Publish'], ctx = context()) {
  const answers = choices.map((choice, n) => [choice === 'Publish' ? d.question.questions[n].options[2].label : choice])
  s.service.event({ event: { type: 'question.replied', properties: { sessionID: ctx.sessionID, requestID: `que_${d.draftID}`, answers } } })
}
function approve(s, d, choices, ctx) { asked(s, d, ctx); reply(s, d, choices, ctx) }

{
  const s = setup(), d = await stage(s)
  assert.equal(d.items[0].status, 'pending')
  assert.equal((await invoke(s, 'publish', { draftID: d.draftID })).items[0].status, 'pending')
  s.service.event({ event: { type: 'permission.replied', properties: { sessionID: 'session-1', requestID: `que_${d.draftID}`, reply: 'always' } } })
  assert.equal(s.postCount(), 0)
  approve(s, d)
  const out = await invoke(s, 'publish', { draftID: d.draftID })
  assert.equal(out.items[0].status, 'published')
  assert.equal(out.items[0].receipt.url, `${url}#issuecomment-100`)
  assert.deepEqual(JSON.parse(s.calls.find(c => c.argv.includes('POST')).options.stdin), { body: item().body })
  await invoke(s, 'publish', { draftID: d.draftID })
  assert.equal(s.postCount(), 1)
}
for (const choice of ['Decline', 'Revise', 'unrecognized']) {
  const s = setup(), d = await stage(s)
  asked(s, d); reply(s, d, [choice])
  const out = await invoke(s, 'publish', { draftID: d.draftID })
  assert.equal(out.items[0].status, choice === 'Revise' ? 'revision-requested' : 'declined')
  assert.equal(s.postCount(), 0)
}
for (const tamper of [p => ({ ...p, sessionID: 'other' }), p => ({ ...p, tool: { ...p.tool, callID: 'other' } }), p => ({ ...p, questions: [{ ...p.questions[0], question: 'Approve?' }] })]) {
  const s = setup(), d = await stage(s)
  asked(s, d, context(), tamper); reply(s, d)
  assert.equal((await invoke(s, 'publish', { draftID: d.draftID })).items[0].status, 'pending')
  assert.equal(s.postCount(), 0)
}
{
  const s = setup(), d = await stage(s)
  await assert.rejects(() => stage(s, [item()], context({ agent: 'h-review' })), /coordinator/)
  await assert.rejects(() => invoke(s, 'publish', { draftID: d.draftID, approved: true }), /invalid arguments/)
  await assert.rejects(() => invoke(s, 'status', { draftID: d.draftID }, context({ sessionID: 'other' })), /unknown draft/)
  approve(s, d)
  const d2 = await stage(s, [item({ body: 'Revised draft' })])
  assert.equal((await invoke(s, 'status', { draftID: d.draftID })).items[0].status, 'invalidated')
  await invoke(s, 'publish', { draftID: d2.draftID })
  assert.equal(s.postCount(), 0)
}
for (const mutate of [s => s.setState('closed'), s => s.setActor(8), s => s.setHead('c'.repeat(40))]) {
  const s = setup(), d = await stage(s, [item({ kind: 'pr_comment', url: 'https://github.com/owner/repo/pull/2', baseOID: A, headOID: B })])
  approve(s, d); mutate(s)
  assert.equal((await invoke(s, 'publish', { draftID: d.draftID })).items[0].status, 'stale')
  assert.equal(s.postCount(), 0)
}
{
  const s = setup(), d = await stage(s)
  approve(s, d)
  const denied = context({ async ask() { throw new Error('denied') } })
  assert.equal((await invoke(s, 'publish', { draftID: d.draftID }, denied)).items[0].status, 'blocked')
  assert.equal(s.postCount(), 0)
}
{
  const s = setup(), controller = new AbortController(), d = await stage(s, [item()], context({ abort: controller.signal }))
  controller.abort()
  approve(s, d)
  const out = await invoke(s, 'publish', { draftID: d.draftID })
  assert.equal(out.items[0].status, 'published')
  assert.equal(s.postCount(), 1)
}
for (const error of ['lost-response', 'lost-without-record']) {
  const s = setup(), d = await stage(s)
  approve(s, d); s.setError(error)
  const out = await invoke(s, 'publish', { draftID: d.draftID })
  assert.equal(out.items[0].status, error === 'lost-response' ? 'published' : 'uncertain')
  await invoke(s, 'publish', { draftID: d.draftID })
  assert.equal(s.postCount(), 1)
  if (error === 'lost-without-record') await assert.rejects(() => stage(s), /uncertain/)
}
{
  const s = setup(), d = await stage(s, [item({ kind: 'pr_review', url: 'https://github.com/owner/repo/pull/2', baseOID: A, headOID: B, reviewEvent: 'COMMENT' })])
  approve(s, d)
  const out = await invoke(s, 'publish', { draftID: d.draftID })
  assert.equal(out.items[0].status, 'published')
  assert.deepEqual(JSON.parse(s.calls.find(c => c.argv.includes('POST')).options.stdin), { commit_id: B, event: 'COMMENT', body: d.items[0].body })
}
{
  const s = setup(), d = await stage(s, [item(), item({ kind: 'pr_comment', url: 'https://github.com/owner/repo/pull/2', baseOID: A, headOID: B })])
  approve(s, d, ['Publish', 'Decline'])
  const out = await invoke(s, 'publish', { draftID: d.draftID })
  assert.deepEqual(out.items.map(i => i.status), ['published', 'declined'])
  assert.equal(s.postCount(), 1)
}
{
  const s = setup(), d = await stage(s)
  approve(s, d)
  s.service.event({ event: { type: 'session.deleted', properties: { info: { id: 'session-1' } } } })
  await assert.rejects(() => invoke(s, 'publish', { draftID: d.draftID }), /deleted/)
  assert.equal(s.postCount(), 0)
}
{
  const s = setup(), d = await stage(s)
  approve(s, d); s.service.dispose()
  await assert.rejects(() => invoke(s, 'publish', { draftID: d.draftID }), /coordinator/)
  const restarted = setup()
  await assert.rejects(() => invoke(restarted, 'publish', { draftID: d.draftID }), /unknown draft/)
}
for (const input of [item({ url: 'https://github.com/owner/repo/issues/1?x=1' }), item({ body: 'x'.repeat(8193) }), item({ kind: 'pr_review', url: 'https://github.com/owner/repo/pull/2', baseOID: A, headOID: B, reviewEvent: 'APPROVE' })]) {
  const s = setup()
  await assert.rejects(() => stage(s, [input]))
  assert.equal(s.calls.length, 0)
}
console.log('GitHub draft runtime passed')
