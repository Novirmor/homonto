// Hosting runtime against a REAL sandbox host: the projected workflow plugin's
// shim (createHostDrafts) drives the Go core (`homonto runtime serve`), which
// runs the real host CLI. This script plays the user: it answers the native
// question exactly as OpenCode would relay it, then publishes one uniquely
// marked PR comment, confirms the receipt by reconciliation, and deletes it.
//
// Usage: node runtime.mjs <github.ts> <config path> <source dir>
// Env:   E2E_HOST (github|gitlab|bitbucket), HOSTING_PR_URL, HOSTING_ISSUE_URL (optional)
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { pathToFileURL } from 'node:url'

const [shim, configPath, source] = process.argv.slice(2)
const host = process.env.E2E_HOST
const prURL = process.env.HOSTING_PR_URL
assert.ok(shim && configPath && source && host && prURL, 'usage: runtime.mjs <github.ts> <config> <source>; E2E_HOST and HOSTING_PR_URL required')
const { createHostDrafts } = await import(pathToFileURL(shim).href)

function run(argv, { stdin, timeout = 30000, signal } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(argv[0], argv.slice(1), { cwd: source, stdio: ['pipe', 'pipe', 'pipe'], signal })
    let out = '', err = ''
    const timer = setTimeout(() => child.kill(), timeout)
    child.stdout.on('data', d => { out += d })
    child.stderr.on('data', d => { err += d })
    child.on('error', reject)
    child.on('close', code => { clearTimeout(timer); code === 0 ? resolve(out) : reject(new Error(`process exited ${code}: ${err.trim()}`)) })
    child.stdin.end(stdin ?? '')
  })
}
const json = async argv => JSON.parse(await run(argv))

// Reviewed OIDs as each host defines them (docs/guides/hosting-differences.md).
async function oids() {
  if (host === 'github') {
    const m = /^https:\/\/([^/]+)\/([^/]+\/[^/]+)\/pull\/(\d+)$/.exec(prURL)
    const pr = await json(['gh', 'api', '--hostname', m[1], `repos/${m[2]}/pulls/${m[3]}`])
    return { base: pr.base.sha, head: pr.head.sha }
  }
  if (host === 'gitlab') {
    const m = /^https:\/\/gitlab\.com\/(.+)\/-\/merge_requests\/(\d+)$/.exec(prURL)
    const mr = await json(['glab', 'api', '--hostname', 'gitlab.com', `projects/${encodeURIComponent(m[1])}/merge_requests/${m[2]}`])
    assert.ok(mr.diff_refs, 'fixture MR diff_refs not ready')
    return { base: mr.diff_refs.start_sha, head: mr.diff_refs.head_sha }
  }
  const m = /^https:\/\/bitbucket\.org\/([^/]+\/[^/]+)\/pull-requests\/(\d+)$/.exec(prURL)
  const api = endpoint => json(['homonto', 'host', 'bitbucket', 'api', endpoint])
  const pr = await api(`repositories/${m[1]}/pullrequests/${m[2]}`)
  const full = async (repo, hash) => hash.length === 40 ? hash : (await api(`repositories/${repo}/commit/${hash}`)).hash
  return { base: await full(m[1], pr.destination.commit.hash), head: await full(pr.source.repository.full_name, pr.source.commit.hash) }
}

async function remove(item) {
  const id = item.receipt.id
  if (host === 'github') {
    const m = /^https:\/\/([^/]+)\/([^/]+\/[^/]+)\//.exec(item.url)
    await run(['gh', 'api', '--hostname', m[1], '--method', 'DELETE', `repos/${m[2]}/issues/comments/${id}`])
  } else if (host === 'gitlab') {
    const m = /\/-\/merge_requests\/(\d+)$/.exec(item.url)
    await run(['glab', 'api', '--hostname', 'gitlab.com', '--method', 'DELETE', `projects/${item.repository.id}/merge_requests/${m[1]}/notes/${id}`])
  } else {
    const m = /^https:\/\/bitbucket\.org\/([^/]+\/[^/]+)\/pull-requests\/(\d+)$/.exec(item.url)
    await run(['homonto', 'host', 'bitbucket', 'api', '--method', 'DELETE', `repositories/${m[1]}/pullrequests/${m[2]}/comments/${id}`])
  }
}

const service = createHostDrafts({ host, configPath, run })
const tool = name => service.tools[`homonto_${host}_${name}`]
const ctx = { sessionID: `e2e-${randomUUID()}`, messageID: 'e2e-message', agent: 'homonto', abort: new AbortController().signal, async ask() {} }
const call = async (name, args) => JSON.parse(await tool(name).execute(args, ctx))

try {
  const { base, head } = await oids()
  const marker = `homonto hosting sandbox check ${randomUUID()}`
  const draft = await call('draft', { items: [{ kind: 'pr_comment', url: prURL, body: marker, baseOID: base, headOID: head, reviewEvent: '' }] })
  assert.equal(draft.items[0].status, 'pending')
  console.log(`  staged ${draft.items[0].url} as ${draft.items[0].actor.login} (repository ${draft.items[0].repository.name})`)

  // Unapproved: publishing must not write.
  assert.equal((await call('publish', { draftID: draft.draftID })).items[0].status, 'pending')

  // Approve through the native question events, as OpenCode relays them.
  service.before({ tool: 'question', sessionID: ctx.sessionID, callID: 'e2e-call' }, { args: draft.question })
  service.event({ event: { type: 'question.asked', properties: { id: `e2e-${draft.draftID}`, sessionID: ctx.sessionID,
    questions: draft.question.questions, tool: { callID: 'e2e-call', messageID: 'e2e-question' } } } })
  service.event({ event: { type: 'question.replied', properties: { sessionID: ctx.sessionID, requestID: `e2e-${draft.draftID}`,
    answers: [[draft.question.questions[0].options[2].label]] } } })

  const published = await call('publish', { draftID: draft.draftID })
  const item = published.items[0]
  assert.equal(item.status, 'published', JSON.stringify(item))
  console.log(`  published ${item.receipt.url}`)

  // Republishing reconciles against the host and never sends twice.
  assert.equal((await call('publish', { draftID: draft.draftID })).items[0].status, 'published')
  await remove(item)
  console.log('  removed the sandbox comment')
  console.log(`RUNTIME PASS: ${host}`)
} finally {
  service.dispose()
}
