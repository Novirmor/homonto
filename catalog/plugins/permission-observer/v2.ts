import type { Plugin } from "@opencode/plugin/promise/plugin"

const value = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v)
const key = (...parts: string[]) => parts.join("\u0000")

export default {
  id: "homonto.permission-observer",
  async setup(ctx) {
    const controller = new AbortController()
    const calls = new Map<string, string>()
    const pending = new Map<string, { sessionID: string; command: string }>()
    const candidates = new Map<string, { allows: number; denied: boolean }>()
    const suggested = new Set<string>()
    const running = new Set<() => void>()
    let disposed = false

    function cleanup() {
      if (disposed) return
      disposed = true
      controller.abort()
      for (const cancel of running) cancel()
      running.clear()
      calls.clear(); pending.clear(); candidates.clear(); suggested.clear()
    }

    async function suggest(command: string) {
      if (disposed) return
      let timer: ReturnType<typeof setTimeout> | undefined
      let cancel: (() => void) | undefined
      let kill: (() => void) | undefined
      try {
        const proc = Bun.spawn(["homonto", "permissions", "suggest"], {
          stdin: "pipe", stdout: "pipe", stderr: "pipe", cwd: ctx.location.directory,
        })
        kill = () => { try { proc.kill() } catch {} }
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
        const [out, err, code] = await Promise.race([Promise.all([
          drain(proc.stdout), drain(proc.stderr), proc.exited,
        ]), interrupted])
        if (code !== 0) throw new Error(err || `homonto exited ${code}`)
        if (!disposed) process.stdout.write(out)
      } catch (err) {
        if (!disposed) console.warn("permission-observer: suggest failed", String(err))
      } finally {
        if (timer) clearTimeout(timer)
        if (cancel) running.delete(cancel)
        kill?.()
      }
    }

    async function observe(e: { type: string; data?: unknown }) {
      if (disposed || !value(e.data)) return
      const p = e.data
      if (e.type === "session.deleted") {
        const id = value(p.info) ? p.info.id : p.sessionID
        if (typeof id !== "string") return
        for (const k of calls.keys()) if (k.startsWith(id + "\u0000")) calls.delete(k)
        for (const k of pending.keys()) if (k.startsWith(id + "\u0000")) pending.delete(k)
        for (const k of candidates.keys()) if (k.startsWith(id + "\u0000")) candidates.delete(k)
        return
      }
      if (e.type === "permission.asked") {
        if (p.action !== "shell" || typeof p.id !== "string" || typeof p.sessionID !== "string" ||
            !value(p.source) || p.source.type !== "tool" || typeof p.source.messageID !== "string" ||
            typeof p.source.id !== "string" || !Array.isArray(p.resources) || p.resources.length !== 1 ||
            typeof p.resources[0] !== "string") return
        const command = calls.get(key(p.sessionID, p.source.messageID, p.source.id))
        if (!command || p.resources[0] !== command || pending.size >= 4096) return
        try {
          const session = await ctx.session.get({ sessionID: p.sessionID })
          if (disposed || session.projectID !== ctx.location.project.id) return
        } catch { return }
        pending.set(key(p.sessionID, p.id), { sessionID: p.sessionID, command })
        return
      }
      if (e.type === "permission.replied") {
        if (typeof p.sessionID !== "string" || typeof p.requestID !== "string") return
        const request = pending.get(key(p.sessionID, p.requestID))
        if (!request) return
        pending.delete(key(p.sessionID, p.requestID))
        const id = key(request.sessionID, request.command)
        const current = candidates.get(id) ?? { allows: 0, denied: false }
        if (p.reply === "reject") {
          candidates.set(id, { ...current, denied: true })
          return
        }
        if (p.reply !== "once" || current.denied || suggested.has(id)) return
        current.allows++
        if (current.allows < 2) { candidates.set(id, current); return }
        candidates.delete(id)
        suggested.add(id)
        void suggest(request.command)
      }
    }

    await ctx.tool.hook("execute.before", (event) => {
      if (disposed || event.tool !== "shell" || !value(event.input) || typeof event.input.command !== "string" ||
          !event.input.command || calls.size >= 4096) return
      calls.set(key(event.sessionID, event.messageID, event.id), event.input.command)
    })
    await ctx.tool.hook("execute.after", (event) => {
      calls.delete(key(event.sessionID, event.messageID, event.id))
    })
    void (async () => {
      try {
        for await (const e of ctx.event.subscribe({ signal: controller.signal })) await observe(e)
      } catch (error) {
        if (!disposed) console.warn("permission-observer: events unavailable", String(error))
      }
    })()
    return cleanup
  },
} satisfies Plugin
