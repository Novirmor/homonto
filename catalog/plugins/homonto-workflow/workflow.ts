// Shim for the Go workflow core (M4.1): the coordinator's homonto_status and
// homonto_handoff tools and the worker permission handback state (ADR 0077)
// run in `homonto runtime serve`. The plugin keeps what only OpenCode can do —
// session scope, the coordinator check, native permission prompts, context
// injection and the status panel — and relays everything else.
import { createCore } from "./core.ts"
import type { Definition, Run } from "./core.ts"

// Mirrors hostruntime.WorkflowToolDefinitions; the client checks the core's
// tools/list against it at startup.
export const WORKFLOW_TOOLS: Record<string, Definition> = {
  homonto_status: { description: "Read the selected workflow snapshot. Coordinator only; no arguments.", args: {} },
  homonto_handoff: { description: "Read exact-generation recovery artifacts. Coordinator only.", args: {
    workflow: { type: "string", enum: ["onto", "to"] }, change: { type: "string" }, identity: { type: "string" },
  } },
}

export const HANDBACK_UNAVAILABLE = "## Homonto worker permission blocks\nWorker permission handback is unavailable; a worker's refused request may be unreported. Ask delegated workers for their permission blocks before treating their results as complete."

export function createWorkflowCore({ run, configPath, command }: { run: Run; configPath: string; command?: string[] }) {
  const core = createCore({ run, configPath, provider: "workflow", label: "Workflow", definitions: WORKFLOW_TOOLS, command })
  return {
    tools: core.tools,
    dispose: core.dispose,
    session(sessionID: string, parentID: string, agent: string | undefined) {
      core.notify("homonto/handback/session", { sessionID, parentID, agent: agent ?? "" })
    },
    asked(sessionID: string, requestID: string, action: string, resources: string[]) {
      core.notify("homonto/handback/asked", { sessionID, requestID, action, resources })
    },
    replied(sessionID: string, requestID: string, reply: string) {
      core.notify("homonto/handback/replied", { sessionID, requestID, reply })
    },
    deleted(sessionID: string) { core.notify("homonto/handback/deleted", { sessionID }) },
    async hasSession(sessionID: string) { return (await core.query("homonto/handback/known", { sessionID })).known === true },
    // Consumes the parent's pending refusals. A core that cannot answer yields
    // an explicit notice rather than silence.
    async take(parentID: string): Promise<string | undefined> {
      try {
        const notice = (await core.query("homonto/handback/take", { sessionID: parentID })).notice
        return typeof notice === "string" && notice ? notice : undefined
      } catch { return HANDBACK_UNAVAILABLE }
    },
  }
}
