import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

// Every hosting provider's shim must start the Go core, agree with its
// tools/list (version-skew check), and route tool calls to it.
const { createHostDrafts, HOSTS } = await import(pathToFileURL(process.argv[2]).href)
const context = { sessionID: 'session-1', messageID: 'message-1', agent: 'coordinator', abort: new AbortController().signal, async ask() {} }
for (const host of Object.keys(HOSTS)) {
  const service = createHostDrafts({ host, configPath: '/workspace/control/selected.toml', run: async () => { throw new Error('no commands expected') } })
  assert.deepEqual(Object.keys(service.tools), [`homonto_${host}_draft`, `homonto_${host}_status`, `homonto_${host}_publish`])
  await assert.rejects(() => service.tools[`homonto_${host}_status`].execute({ draftID: 'missing' }, context), /unknown draft for this session/)
  await assert.rejects(() => service.tools[`homonto_${host}_draft`].execute({ items: [] }, context), /draft requires 1–10 items/)
  assert.equal(service.context('session-1'), JSON.stringify({ drafts: [] }))
  service.dispose()
  await assert.rejects(() => service.tools[`homonto_${host}_status`].execute({ draftID: 'missing' }, context), new RegExp(`\\[plugin_disposed\\] ${HOSTS[host]} plugin disposed`))
}
console.log('host runtimes passed')
