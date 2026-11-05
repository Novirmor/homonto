// Harness shim for the hosting draft/publication core (ADR 0090). The core —
// sessions, drafts, approval correlation, TTL, capacities, freshness and
// uncertain-send reconciliation, and the host providers — runs in Go behind
// `homonto runtime serve`. This file relays harness events through the shared
// core client (core.ts) and mirrors per-session draft state for synchronous
// context injection.
import { check, createCore } from "./core.ts"
import type { Definition, Run } from "./core.ts"

type SessionState = { sessionID: string; deleted: boolean; drafts: { draftID: string; expiresAt: number; items: { itemID: string; status: string }[] }[] }

export const HOSTS = { github: "GitHub", gitlab: "GitLab", bitbucket: "Bitbucket" } as const
export type Host = keyof typeof HOSTS
const RELAYED = new Set(["session.deleted", "form.created", "form.replied", "form.cancelled", "question.asked", "question.rejected", "question.replied"])

const draftArgs = { items: { type: "array", minItems: 1, maxItems: 10, items: { type: "object", additionalProperties: false,
  required: ["kind", "url", "body", "baseOID", "headOID", "reviewEvent"], properties: {
    kind: { type: "string", enum: ["issue_comment", "pr_comment", "pr_review"] }, url: { type: "string" }, body: { type: "string", maxLength: 8192 },
    baseOID: { type: "string" }, headOID: { type: "string" }, reviewEvent: { type: "string", enum: ["", "COMMENT", "REQUEST_CHANGES"] },
  } } } }
const idArgs = { draftID: { type: "string" } }
// Mirrors hostruntime.ToolDefinitions; the shim checks the core's tools/list against it.
function definitionsFor(host: Host): Record<string, Definition> {
  const label = HOSTS[host]
  return {
    [`homonto_${host}_draft`]: { description: `Stage an immutable, memory-only ${label} batch. PR bodies gain reviewed OIDs before preview. Formal reviews support COMMENT/REQUEST_CHANGES only. Submit the returned question arguments unchanged to native question; approval is workflow confirmation, not human-only attestation. New batches invalidate earlier session drafts.`, args: draftArgs },
    [`homonto_${host}_status`]: { description: "Read the session's draft statuses, exact full preview and native question arguments.", args: idArgs },
    [`homonto_${host}_publish`]: { description: "Publish only items approved through correlated native question events, after permission and fresh scope/actor/destination checks. Uncertain outcomes are never blindly retried.", args: idArgs },
  }
}

type Options = { run: Run; configPath: string; command?: string[] }

export function createGithubDrafts(options: Options) { return createHostDrafts({ ...options, host: "github" }) }

export function createHostDrafts({ run, configPath, host, command = ["homonto", "runtime", "serve"] }: Options & { host: Host }) {
  check(Object.hasOwn(HOSTS, host), "unknown hosting provider")
  const LABEL = HOSTS[host]
  const states = new Map<string, SessionState>()
  const core = createCore({ run, configPath, provider: host, label: LABEL, definitions: definitionsFor(host), command,
    onNotification(method, params) {
      if (method !== "homonto/state") return
      for (const s of (Array.isArray(params.sessions) ? params.sessions : []) as SessionState[]) states.set(s.sessionID, s)
    } })

  function before(input: { tool: string; sessionID: string; callID: string }, output: { args: unknown }) {
    if (core.disposed || input.tool !== "question") return
    core.notify("homonto/before", { tool: input.tool, sessionID: input.sessionID, callID: input.callID, args: output.args })
  }
  function event({ event: e }: { event: { type: string; properties?: unknown; data?: unknown } }) {
    if (e.type === "server.instance.disposed") { core.dispose(); return }
    if (core.disposed || !RELAYED.has(e.type)) return
    core.notify("homonto/event", { event: { type: e.type, properties: e.properties, data: e.data } })
  }

  return {
    host,
    tools: core.tools,
    event,
    before,
    dispose: core.dispose,
    context(sessionID: string): string {
      const s = states.get(sessionID)
      if (core.disposed || s?.deleted) return `${LABEL} drafts: disposed`
      const at = Date.now()
      return JSON.stringify({ drafts: (s?.drafts ?? []).map(d => ({ draftID: d.draftID, items: d.items.map(i => ({ itemID: i.itemID,
        status: at >= d.expiresAt && (i.status === "pending" || i.status === "approved") ? "expired" : i.status })) })) })
    },
  }
}
