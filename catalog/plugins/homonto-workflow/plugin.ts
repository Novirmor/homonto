// homonto-workflow observes the read-only `homonto workflow snapshot` command
// and turns changes in that snapshot into OpenCode toasts. It never invokes a
// mutating workflow command and retains its comparison state only in memory.

import type { Plugin } from "@opencode-ai/plugin"
import { readFile, lstat, realpath } from "node:fs/promises"
import { basename, dirname, isAbsolute, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

type Change = {
  identity: string
  status: string
  path: string
  workflow: string
  name: string
  phase: string
  derivedPhase?: string
  tasksCompleted: number
  tasksTotal: number
  verifyResult?: string
  integration?: string
  pending: string[]
}

type Snapshot = {
  configPath: string
  changes: Change[]
  findings: { workflow: string; change?: string; message: string }[]
}

interface RuntimeEvent { type: string; properties?: unknown }

export const homontoWorkflow = (async ({ client, directory }: Parameters<Plugin>[0]) => {
  let previous = new Map<string, Change>()
  let previousFindings = ""
  let timer: ReturnType<typeof setTimeout> | undefined
  let flight: Promise<Snapshot | undefined> | undefined
  let requested = false
  let disposed = false
  let cancelSpawn: (() => void) | undefined
  let observerError = ""
  let initialized = false
  let latestCompleted: Snapshot | undefined
  const compactions = new Set<() => void>()

  async function dispose() {
    if (disposed) return
    disposed = true
    requested = false
    if (timer) clearTimeout(timer)
    timer = undefined
    cancelSpawn?.()
    for (const cancel of compactions) cancel()
    compactions.clear()
    previous.clear()
    previousFindings = ""
    latestCompleted = undefined
    observerError = ""
  }

  // OpenCode may launch in a nested directory or a source checkout. Resolve the
  // materialized module, never the launch cwd. Every projection carries the
  // exact selected config; other TOML files and workflow references are irrelevant.
  async function selectedConfig() {
    const module = await realpath(fileURLToPath(import.meta.url))
    const plugin = dirname(module)
    const catalog = resolve(plugin, "../..")
    const control = dirname(catalog)
    if (basename(plugin) !== "homonto-workflow" || basename(dirname(plugin)) !== "plugins" ||
        basename(catalog) !== "catalog" || basename(control) !== ".homonto") {
      throw new Error("workflow plugin is not in a materialized homonto catalog")
    }
    const root = dirname(control)
    let binding: { version: number; configPath: string; configRoot: string }
    try {
      const path = join(plugin, "binding.json")
      if (!(await lstat(path)).isFile()) throw new Error("binding must be a regular file")
      binding = JSON.parse(await readFile(path, "utf8"))
    } catch (err) {
      throw new Error(`workflow binding unavailable; run homonto apply with the selected --config: ${String(err)}`)
    }
    if (!binding || binding.version !== 1 || typeof binding.configPath !== "string" || typeof binding.configRoot !== "string" ||
        !isAbsolute(binding.configPath) || resolve(binding.configPath) !== binding.configPath ||
        !isAbsolute(binding.configRoot) || resolve(binding.configRoot) !== binding.configRoot ||
        dirname(binding.configPath) !== binding.configRoot || await realpath(binding.configRoot) !== root) {
      throw new Error("invalid workflow config binding; run homonto apply with the selected --config")
    }
    return binding.configPath
  }

  async function toast(message: string, variant: "info" | "success" | "warning" | "error") {
    if (disposed) return
    try {
      await client.tui.showToast({ body: { title: "Workflow", message, variant } })
    } catch {
      // A headless server has no TUI recipient. Observation must not affect the
      // workflow or prevent the rest of OpenCode's event pipeline from running.
    }
  }

  async function snapshot(): Promise<Snapshot> {
    const config = await selectedConfig()
    if (disposed) throw new Error("observer disposed")
    const proc = Bun.spawn(["homonto", "workflow", "snapshot", "--json", "--config", config], {
      cwd: directory,
      stdout: "pipe",
      stderr: "pipe",
    })
    let deadline: ReturnType<typeof setTimeout> | undefined
    async function drain(stream: ReadableStream<Uint8Array>, limit: number) {
      const reader = stream.getReader()
      const decoder = new TextDecoder()
      let text = "", size = 0
      try {
        while (true) {
          const { value, done } = await reader.read()
          if (done) return text + decoder.decode()
          size += value.byteLength
          if (size > limit) throw new Error("workflow snapshot output exceeded limit")
          text += decoder.decode(value, { stream: true })
        }
      } finally { reader.releaseLock() }
    }
    try {
      const interrupted = new Promise<never>((_resolve, reject) => {
        cancelSpawn = () => {
          if (deadline) clearTimeout(deadline)
          try { proc.kill() } catch { /* already exited */ }
          reject(new Error("workflow snapshot timed out or disposed"))
        }
        deadline = setTimeout(cancelSpawn, 5000)
        deadline.unref?.()
      })
      const [out, stderr, code] = await Promise.race([
        Promise.all([drain(proc.stdout, 4 * 1024 * 1024), drain(proc.stderr, 64 * 1024), proc.exited]), interrupted,
      ])
      if (code !== 0) throw new Error(`workflow snapshot exited ${code}: ${stderr.trim()}`)
      const value: Snapshot = JSON.parse(out)
      if (value.configPath !== config || !Array.isArray(value.changes) || !Array.isArray(value.findings) ||
          value.changes.some((c) => !c || typeof c.identity !== "string" || !c.identity ||
            typeof c.workflow !== "string" || typeof c.name !== "string" || typeof c.status !== "string" ||
            typeof c.phase !== "string" || !Array.isArray(c.pending))) throw new Error("invalid workflow snapshot contract")
      return value
    } finally {
      if (deadline) clearTimeout(deadline)
      cancelSpawn = undefined
      try { proc.kill() } catch { /* already exited */ }
    }
  }

  function describe(change: Change) {
    const phase = change.derivedPhase && change.derivedPhase !== change.phase
      ? `${change.phase} (working: ${change.derivedPhase})`
      : change.phase
    const progress = change.tasksTotal > 0 ? `, ${change.tasksCompleted}/${change.tasksTotal} tasks` : ""
    const pending = change.pending.length ? `; pending: ${change.pending.join(", ")}` : ""
    const verify = change.verifyResult ? `; verify: ${change.verifyResult}` : ""
    return `${change.workflow}: ${change.name} is ${change.status} at ${phase}${progress}${verify}${pending}`
  }

  async function publish(next: Snapshot) {
    const key = (c: Change) => `${c.workflow}\u0000${c.identity}`
    const nextChanges = new Map(next.changes.map((change) => [key(change), change]))
    for (const change of next.changes) {
      if (disposed) return
      const before = previous.get(key(change))
      if (!initialized && ["completed", "abandoned", "bypassed"].includes(change.status)) continue
      if (JSON.stringify(before) !== JSON.stringify(change)) {
        const unhealthy = change.verifyResult === "fail" || next.findings.some((f) => f.workflow === change.workflow && (!f.change || f.change === change.name))
        const completed = change.status === "completed" && !unhealthy && change.pending.length === 0
        await toast(describe(change), unhealthy ? "error" : completed ? "success" : change.status === "abandoned" ? "warning" : "info")
      }
    }
    for (const [key, old] of previous) {
      if (!nextChanges.has(key) && !["completed", "abandoned", "bypassed"].includes(old.status)) {
        await toast(`${old.workflow}: ${old.name} is no longer observable; completion is not established`, "warning")
      }
    }
    if (disposed) return
    previous = nextChanges
    initialized = true

    const findings = JSON.stringify(next.findings)
    if (findings !== previousFindings && next.findings.length > 0) {
      await toast(`Workflow health: ${next.findings.length} finding(s)`, "error")
    }
    if (!disposed) previousFindings = findings
  }

  // One flight has at most two generations. Further watcher events schedule a
  // separate flight instead of keeping every awaiting hook alive indefinitely.
  function refresh(): Promise<Snapshot | undefined> {
    if (disposed) return Promise.resolve(undefined)
    requested = true
    if (flight) return flight
    flight = (async () => {
      let latest: Snapshot | undefined
      for (let generation = 0; generation < 2 && !disposed; generation++) {
        requested = false
        try {
          latest = await snapshot()
          if (disposed) return undefined
          latestCompleted = latest
          observerError = ""
          await publish(latest)
        } catch (err) {
          if (disposed) return undefined
          latest = undefined
          const message = `Workflow observation failed: ${String(err)}`
          const changed = message !== observerError
          observerError = message
          if (changed) await toast(message, "error")
        }
        if (!requested) break
      }
      return latest
    })().finally(() => {
      flight = undefined
      if (requested && !disposed) schedule()
    })
    return flight
  }

  function schedule() {
    if (disposed) return
    if (flight) { requested = true; return }
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => {
      timer = undefined
      void refresh()
    }, 250)
    timer.unref?.()
  }

  return {
    dispose,
    event: async ({ event }: { event: RuntimeEvent }) => {
      if (event.type === "server.instance.disposed") {
        await dispose()
        return
      }
      if (event.type === "session.idle") {
        if (timer) clearTimeout(timer)
        timer = undefined
        await refresh()
        return
      }
      if (event.type === "file.watcher.updated") schedule()
    },
    "experimental.session.compacting": async (_input, output) => {
      if (disposed) return
      let deadline: ReturnType<typeof setTimeout> | undefined
      let cancel: (() => void) | undefined
      let timedOut = false
      const bounded = new Promise<void>((resolve) => {
        cancel = () => { if (deadline) clearTimeout(deadline); resolve() }
        compactions.add(cancel)
        deadline = setTimeout(() => { timedOut = true; resolve() }, 1500)
        deadline.unref?.()
      })
      try { await Promise.race([refresh(), bounded]) } finally {
        if (deadline) clearTimeout(deadline)
        if (cancel) compactions.delete(cancel)
      }
      if (disposed) return
      const next = latestCompleted
      if (!next) { output.context.push(`## Workflow Status\n${observerError || "Workflow observation unavailable; do not infer completion."}`); return }
      const lines = next.changes.filter((c) => !["completed", "abandoned", "bypassed"].includes(c.status)).map(describe)
      lines.push(...next.findings.map((f) => `Health: ${f.workflow}${f.change ? ": " + f.change : ""}: ${f.message}`))
      if (observerError) lines.push(observerError)
      if (timedOut) lines.push("Refresh still in progress; using the latest completed observation, not proof of completion.")
      if (lines.length) output.context.push(`## Workflow Status\n${lines.join("\n")}`)
    },
  }
}) satisfies Plugin

export default homontoWorkflow
