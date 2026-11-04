// Prints the OpenCode shim's rendering of each fixture so the Go test can
// compare it with hostruntime.RenderContext and the continuation policy.
import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { pathToFileURL } from "node:url"

const plugin = process.argv[2]
const fixtures = JSON.parse(await readFile(process.argv[3], "utf8"))
const { render, bounded } = await import(pathToFileURL(join(plugin, "snapshot.ts")).href)
const { CONTINUATION_POLICY, CONTEXT_SNAPSHOT_LIMIT, HANDBACK_LIMIT } = await import(pathToFileURL(join(plugin, "continuation.ts")).href)
const out = {
  policy: CONTINUATION_POLICY, snapshotLimit: CONTEXT_SNAPSHOT_LIMIT, handbackLimit: HANDBACK_LIMIT,
  renders: fixtures.snapshots.map(s => render(s, { configPath: fixtures.configPath }) ?? ""),
  bounded: fixtures.bounded.map(([text, limit]) => bounded(text, limit)),
}
process.stdout.write(JSON.stringify(out))
