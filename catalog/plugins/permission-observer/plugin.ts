// permission-observer — OpenCode plugin (ADR 0029)
//
// Keeps approved Bash commands in memory, per session and project, and
// suggests the second explicit approval as a bash_allow_add candidate —
// exactly once per candidate, then forgets it. Nothing is ever written by
// this plugin: `homonto permissions suggest` renders the snippet and exits,
// and the user pastes it into homonto.toml.
//
// Authoritative decisions come from the correlated permission events
// (permission.asked carries the full request incl. metadata.command;
// permission.replied carries the user's decision), verified at pinned
// revision 50efc055. Execution alone is never treated as approval.
//
// Requires Bun's spawn; the plugin refuses to run without it.

import { PluginInput } from "@opencode-ai/plugin"

interface Asked {
  id: string
  sessionID: string
  permission: string
  metadata?: Record<string, unknown>
}

interface Replied {
  sessionID: string
  requestID: string
  reply: "once" | "always" | "reject"
}

interface Candidate {
  sessionID: string
  command: string
  allows: number
  denied: boolean
}

export const permissionObserver = (input: PluginInput) => {
  const pending = new Map<string, Asked>()
  const candidates = new Map<string, Candidate>() // sessionID \u0000 command -> candidate
  let suggested = new Set<string>()

  function recordDecision(replied: Replied) {
    const ask = pending.get(replied.requestID)
    if (!ask) return
    pending.delete(replied.requestID)
    if (ask.permission !== "bash") return
    const command = typeof ask.metadata?.command === "string" ? ask.metadata.command : ""
    if (!command) return
    const key = replied.sessionID + "\u0000" + command
    const cur = candidates.get(key) ?? { sessionID: replied.sessionID, command, allows: 0, denied: false }
    if (replied.reply === "reject") {
      cur.denied = true
      candidates.set(key, cur)
      return
    }
    if (cur.denied) return // a later deny is authoritative; the candidate stays dead
    cur.allows += 1
    if (cur.allows >= 2 && !suggested.has(key)) {
      suggested.add(key)
      candidates.delete(key) // exactly one suggestion per candidate, then forget
      void suggest(command)
    } else {
      candidates.set(key, cur)
    }
  }

  async function suggest(command: string) {
    if (!input.$) {
      console.warn("permission-observer: Bun.$ unavailable; cannot render a suggestion")
      return
    }
    try {
      const proc = input.$.sync`homonto permissions suggest`
      proc.stdin.write(command + "\n")
      proc.stdin.end()
      const out = await new Response(proc.stdout).text()
      process.stdout.write(out)
    } catch (err) {
      console.warn("permission-observer: suggest failed", String(err))
    }
  }

  return {
    name: "permission-observer",
    event: async (event: { type: string; properties: any }) => {
      if (event.type === "permission.asked") {
        const props = event.properties as Asked
        if (props.id && props.sessionID && props.permission) pending.set(props.id, props)
        return
      }
      if (event.type === "permission.replied") {
        recordDecision(event.properties as Replied)
      }
    },
  }
}

export default permissionObserver