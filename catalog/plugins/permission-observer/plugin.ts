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
// permission.replied carries the user's decision), verified against OpenCode
// v1.18.29. Execution alone is never treated as approval.
//
// Requires Bun's spawn; the plugin refuses to run without it.

import type { Plugin } from "@opencode-ai/plugin"

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

// The pinned runtime producer differs from the generated v1 SDK Event union.
// Accept unknown properties and narrow them, rather than casting stale SDK
// payloads to an incompatible request/reply interface.
interface RuntimeEvent { type: string; properties?: unknown }

export const permissionObserver = (async (input: Parameters<Plugin>[0]) => {
  const pending = new Map<string, Asked>()
  const candidates = new Map<string, Candidate>() // sessionID \u0000 command -> candidate
  const suggested = new Set<string>()
  let disposed = false
  const running = new Set<() => void>()

  async function dispose() {
    if (disposed) return
    disposed = true
    for (const cancel of running) cancel()
    running.clear()
    pending.clear(); candidates.clear(); suggested.clear()
  }

  function recordDecision(replied: Replied) {
    const requestKey = replied.sessionID + "\u0000" + replied.requestID
    const ask = pending.get(requestKey)
    if (!ask) return
    pending.delete(requestKey)
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
    if (disposed) return
    let timer: ReturnType<typeof setTimeout> | undefined
    let cancel: (() => void) | undefined
    let kill: (() => void) | undefined
    try {
      const proc = Bun.spawn(["homonto", "permissions", "suggest"], {
        stdin: "pipe",
        stdout: "pipe",
        stderr: "pipe",
        cwd: input.directory,
      })
      kill = () => { try { proc.kill() } catch { /* already exited */ } }
      const interrupted = new Promise<never>((_resolve, reject) => {
        cancel = () => { if (timer) clearTimeout(timer); kill?.(); reject(new Error("suggest timed out or disposed")) }
        running.add(cancel)
        timer = setTimeout(cancel, 5000)
        timer.unref?.()
      })
      proc.stdin.write(command + "\n")
      proc.stdin.end()
      async function drain(stream: ReadableStream<Uint8Array>) {
        const reader = stream.getReader()
        const decoder = new TextDecoder()
        let text = "", size = 0
        try {
          while (true) {
            const { value, done } = await reader.read()
            if (done) return text + decoder.decode()
            size += value.byteLength
            if (size > 64 * 1024) throw new Error("suggest output exceeded limit")
            text += decoder.decode(value, { stream: true })
          }
        } finally { reader.releaseLock() }
      }
      const [out, err, exitCode] = await Promise.race([Promise.all([
        drain(proc.stdout),
        drain(proc.stderr),
        proc.exited,
      ]), interrupted])
      if (exitCode !== 0) throw new Error(err || `homonto exited ${exitCode}`)
      if (!disposed) process.stdout.write(out)
    } catch (err) {
      if (!disposed) console.warn("permission-observer: suggest failed", String(err))
    } finally {
      if (timer) clearTimeout(timer)
      if (cancel) running.delete(cancel)
      kill?.()
    }
  }

  return {
    dispose,
    event: async ({ event }: { event: RuntimeEvent }) => {
      if (event.type === "server.instance.disposed") {
        await dispose()
        return
      }
      if (disposed) return
      const props = event.properties
      if (!props || typeof props !== "object") return
      if (event.type === "permission.asked") {
        if (!("id" in props) || typeof props.id !== "string" || !props.id ||
            !("sessionID" in props) || typeof props.sessionID !== "string" || !props.sessionID ||
            !("permission" in props) || typeof props.permission !== "string") return
        const metadata = "metadata" in props && props.metadata && typeof props.metadata === "object" ? props.metadata : undefined
        pending.set(props.sessionID + "\u0000" + props.id, {
          id: props.id, sessionID: props.sessionID, permission: props.permission,
          metadata: metadata && "command" in metadata ? { command: metadata.command } : undefined,
        })
        return
      }
      if (event.type === "permission.replied") {
        if (!("sessionID" in props) || typeof props.sessionID !== "string" ||
            !("requestID" in props) || typeof props.requestID !== "string" ||
            !("reply" in props) || (props.reply !== "once" && props.reply !== "always" && props.reply !== "reject")) return
        recordDecision({ sessionID: props.sessionID, requestID: props.requestID, reply: props.reply })
      }
    },
  }
}) satisfies Plugin

export default permissionObserver
