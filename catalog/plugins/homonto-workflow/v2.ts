import type { Plugin } from "@opencode/plugin/promise/plugin"
import type { SessionContext } from "@opencode/plugin/promise/session"
import type { ToolContext as Invocation } from "@opencode/plugin/promise/tool"
import { randomUUID } from "node:crypto"
import { lstat, readFile, realpath } from "node:fs/promises"
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path"
import { fileURLToPath } from "node:url"
import { createRunner } from "./runner.ts"
import { WorkflowPanelRPC } from "./rpc.ts"
import { createAuthorization } from "./authorization.ts"
import { HOSTS, createHostDrafts } from "./github.ts"
import type { Host } from "./github.ts"
import { requireCoordinator, requireLiveContext } from "./compat.ts"
import { CONTINUATION_POLICY, CONTEXT_SNAPSHOT_LIMIT, HANDBACK_LIMIT } from "./continuation.ts"
import { createWorkflowCore } from "./workflow.ts"
import { absolute, bounded, object, parseSnapshot, render, terminal, text } from "./snapshot.ts"
import type { Binding, Change, Snapshot } from "./snapshot.ts"

const HOST_FLAGS = { github: "githubEnabled", gitlab: "gitlabEnabled", bitbucket: "bitbucketEnabled" } as const
function within(root: string, directory: string) {
  const path = relative(root, directory)
  return path !== ".." && !path.startsWith(".." + sep) && !isAbsolute(path)
}

async function selectedBinding(signal?: AbortSignal): Promise<Binding> {
  const module = await realpath(fileURLToPath(import.meta.url))
  const plugin = dirname(module)
  const catalog = resolve(plugin, "../..")
  const control = dirname(catalog)
  if (basename(plugin) !== "homonto-workflow" || basename(dirname(plugin)) !== "plugins" ||
      basename(catalog) !== "catalog" || basename(control) !== ".homonto") {
    throw new Error("V2 workflow context requires a materialized homonto catalog")
  }
  const path = join(plugin, "binding.json")
  const info = await lstat(path)
  if (!info.isFile() || info.size > 16384) throw new Error("invalid workflow binding file")
  const binding: unknown = JSON.parse(await readFile(path, { encoding: "utf8", signal }))
  if (!object(binding) || binding.version !== 1 || !absolute(binding.configPath) || !absolute(binding.configRoot) ||
      dirname(binding.configPath) !== binding.configRoot || await realpath(binding.configRoot) !== dirname(control) ||
      (binding.coordinator !== undefined && !text(binding.coordinator)) ||
      Object.values(HOST_FLAGS).some(flag => binding[flag] !== undefined && typeof binding[flag] !== "boolean")) {
    throw new Error("invalid selected workflow config; run homonto apply")
  }
  return { version: 1, configPath: binding.configPath, configRoot: binding.configRoot,
    coordinator: typeof binding.coordinator === "string" ? binding.coordinator : "homonto",
    githubEnabled: binding.githubEnabled === true, gitlabEnabled: binding.gitlabEnabled === true,
    bitbucketEnabled: binding.bitbucketEnabled === true }
}

export default {
  id: "homonto-workflow-context",
  async setup(ctx) {
    const binding = await selectedBinding()
    const root = await realpath(binding.configRoot)
    const identity = randomUUID()
    if (!within(root, await realpath(ctx.location.directory))) throw new Error("workflow context loaded outside its bound workspace")
    const processes = createRunner(() => binding.configRoot)
    const lifetime = new AbortController()
    const requests = new Set<AbortController>()
    const registrations: { dispose(): Promise<void> }[] = []
    let disposed = false
    let flight: Promise<Snapshot> | undefined
    let previous = new Map<string, Change>()
    let previousFindings = ""
    let initialized = false
    const authorization = createAuthorization(ctx, identity)
    // One draft/publication service per enabled hosting workflow (ADR 0089, ADR 0090).
    const hosts = (Object.keys(HOSTS) as Host[]).filter(host => binding[HOST_FLAGS[host]]).map(host => createHostDrafts({
      host, configPath: binding.configPath, run: processes.at(() => binding.configRoot),
    }))
    // Status, handoff and the worker handback state run in the Go core (M4.1).
    const handback = createWorkflowCore({ configPath: binding.configPath, run: processes.at(() => binding.configRoot) })

    async function checkBinding(signal: AbortSignal) {
      const current = await selectedBinding(signal)
      if (current.configPath !== binding.configPath || current.configRoot !== binding.configRoot ||
          current.coordinator !== binding.coordinator || Object.values(HOST_FLAGS).some(flag => current[flag] !== binding[flag])) {
        throw new Error("workflow binding changed; reload the plugin")
      }
    }

    function snapshot() {
      flight ??= processes.run(["homonto", "workflow", "snapshot", "--json", "--config", binding.configPath],
        { signal: lifetime.signal, timeout: 1000 }).then((raw) => parseSnapshot(raw, binding)).finally(() => { flight = undefined })
      return flight
    }

    const dispose = () => {
      if (disposed) return
      disposed = true
      lifetime.abort()
      processes.dispose()
      authorization.dispose()
      for (const host of hosts) host.dispose()
      handback.dispose()
      for (const controller of requests) controller.abort()
      requests.clear()
      for (const registration of registrations) {
        try { void registration.dispose().catch(() => {}) } catch {}
      }
    }

    const panel = async (input: unknown, context: { signal: AbortSignal }) => {
      if (!object(input) || typeof input.sessionID !== "string" || !input.sessionID ||
          Object.keys(input).length !== 1 || disposed || context.signal.aborted) {
        throw new Error("workflow panel request refused")
      }
      const sessionID = input.sessionID
      const controller = new AbortController()
      requests.add(controller)
      const signal = controller.signal
      let interrupt!: () => void
      const interrupted = new Promise<never>((_resolve, reject) => {
        interrupt = () => reject(new Error("workflow panel request interrupted"))
        signal.addEventListener("abort", interrupt, { once: true })
      })
      const timer = setTimeout(() => controller.abort(), 1500)
      timer.unref?.()
      context.signal.addEventListener("abort", interrupt, { once: true })
      if (context.signal.aborted) controller.abort()
      const inScope = async () => {
        const session = await ctx.session.get({ sessionID }, { signal })
        if (signal.aborted || context.signal.aborted || disposed || session.id !== sessionID ||
            session.projectID !== ctx.location.project.id ||
            !within(root, await realpath(session.location.directory)) || signal.aborted ||
            context.signal.aborted || disposed) throw new Error("workflow panel session out of scope")
      }
      const work = async () => {
        await inScope()
        await checkBinding(signal)
        if (signal.aborted || context.signal.aborted || disposed) throw new Error("workflow panel request interrupted")
        let next: Snapshot
        try {
          next = await snapshot()
        } catch {
          await checkBinding(signal)
          await inScope()
          if (signal.aborted || context.signal.aborted || disposed) throw new Error("workflow panel request interrupted")
          return { text: "Workflow observation unavailable; completion is not established. Inspect the selected config and run homonto workflow snapshot --json --config <selected-config>. No stale workflow status was reused." }
        }
        await checkBinding(signal)
        await inScope()
        if (signal.aborted || context.signal.aborted || disposed) throw new Error("workflow panel request interrupted")
        const status = render(next, binding)
        return { text: bounded(next.changes.some((c) => !terminal(c))
          ? status ?? ""
          : ["No active workflow work observed; missing records do not establish completion.", status].filter(Boolean).join("\n"), 16384) }
      }
      try {
        return await Promise.race([work(), interrupted])
      } catch {
        throw new Error("workflow panel request refused")
      } finally {
        clearTimeout(timer)
        signal.removeEventListener("abort", interrupt)
        context.signal.removeEventListener("abort", interrupt)
        controller.abort()
        requests.delete(controller)
      }
    }

    const inject = async (event: SessionContext) => {
      if (disposed) return
      const controller = new AbortController()
      requests.add(controller)
      const signal = controller.signal
      let interrupt!: () => void
      const interrupted = new Promise<never>((_resolve, reject) => {
        interrupt = () => reject(new Error("workflow context interrupted"))
        signal.addEventListener("abort", interrupt, { once: true })
      })
      const timer = setTimeout(() => controller.abort(), 1500)
      timer.unref?.()
      const unavailable = "## Homonto workflow context\nWorkflow observation unavailable; completion is not established. Inspect the selected config and run homonto workflow snapshot --json --config <selected-config>. No stale workflow context was reused."
      const inScope = async () => {
        const session = await ctx.session.get({ sessionID: event.sessionID }, { signal })
        if (signal.aborted || disposed) return false
        return session.id === event.sessionID && session.projectID === ctx.location.project.id &&
          within(root, await realpath(session.location.directory))
      }
      const work = async () => {
        if (!await inScope()) return
        await checkBinding(signal)
        if (signal.aborted || disposed) return
        // A failed snapshot must not hide an independently observed refusal.
        // Revalidate scope and binding before releasing either kind of context.
        let next: Snapshot | undefined
        try { next = await snapshot() } catch {}
        await checkBinding(signal)
        if (signal.aborted || disposed || !await inScope()) return
        return { text: next ? render(next, binding) : unavailable, unavailable: !next }
      }
      try {
        const result = await Promise.race([work(), interrupted])
        if (!disposed && !signal.aborted && result) {
          const blocked = await handback.take(event.sessionID)
          if (disposed || signal.aborted) return
          if (!result.unavailable) event.system.push({ type: "text", text: CONTINUATION_POLICY })
          if (result.text) event.system.push({ type: "text", text: bounded(result.text, CONTEXT_SNAPSHOT_LIMIT - (blocked ? HANDBACK_LIMIT : 0)) })
          if (blocked) event.system.push({ type: "text", text: blocked })
        }
        if (!disposed && !signal.aborted) for (const host of hosts) event.system.push({ type: "text", text: host.context(event.sessionID) })
      } catch {
        if (!disposed) event.system.push({ type: "text", text: unavailable })
      } finally {
        clearTimeout(timer)
        signal.removeEventListener("abort", interrupt)
        controller.abort()
        requests.delete(controller)
      }
    }

    const agentName = (agent: unknown) => text(agent) ? agent : object(agent) && text(agent.name) ? agent.name
      : object(agent) && text(agent.id) ? agent.id : undefined

    // Creation events are not replayed after reload. Resolve a resumed worker
    // on demand, with a deadline so an unavailable host cannot stall the stream.
    async function recoverChild(sessionID: string) {
      if (await handback.hasSession(sessionID).catch(() => false)) return
      const controller = new AbortController()
      const signal = AbortSignal.any([controller.signal, lifetime.signal])
      const timer = setTimeout(() => controller.abort(), 1000)
      timer.unref?.()
      let interrupt!: () => void
      const interrupted = new Promise<never>((_resolve, reject) => {
        interrupt = () => reject(new Error("worker lookup interrupted"))
        signal.addEventListener("abort", interrupt, { once: true })
      })
      try {
        signal.throwIfAborted()
        const session = await Promise.race([ctx.session.get({ sessionID }, { signal }), interrupted])
        if (disposed || signal.aborted || session.id !== sessionID || session.projectID !== ctx.location.project.id ||
            !text(session.parentID)) return
        handback.session(sessionID, session.parentID, agentName(session.agent))
      } catch {} finally {
        clearTimeout(timer)
        signal.removeEventListener("abort", interrupt)
        controller.abort()
      }
    }

    // Only child sessions' rejected prompts are kept. Await discovery before
    // correlating asked/replied events so replies cannot overtake a lookup.
    async function observeHandback(event: { type: string; data?: unknown }) {
      const data = event.data
      if (!object(data)) return
      if (event.type === "session.created" && text(data.sessionID) && text(data.parentID) &&
          (data.projectID === undefined || data.projectID === ctx.location.project.id)) {
        handback.session(data.sessionID, data.parentID, agentName(data.agent))
      } else if (event.type === "permission.asked" && text(data.sessionID) && text(data.id) && text(data.action) &&
          Array.isArray(data.resources)) {
        await recoverChild(data.sessionID)
        if (!disposed) handback.asked(data.sessionID, data.id, data.action, data.resources.filter(text))
      } else if (event.type === "permission.replied" && text(data.sessionID) && text(data.requestID) && text(data.reply)) {
        handback.replied(data.sessionID, data.requestID, data.reply)
      } else if (event.type === "session.deleted") {
        const id = object(data.info) ? data.info.id : data.sessionID
        if (text(id)) handback.deleted(id)
      }
    }

    function progress(call: Invocation, stage: string) {
      try { void Promise.resolve(call.progress?.({ title: stage })).catch(() => {}) } catch {}
    }

    async function validateSession(sessionID: string, parent: AbortSignal, report: (stage: string) => void = () => {}) {
      const controller = new AbortController()
      const signal = AbortSignal.any([parent, lifetime.signal, controller.signal])
      let stage = "session lookup", timedOut = false
      const stopped = () => new Error(timedOut
        ? `[preflight_timeout] ${stage} exceeded 5 seconds; check the OpenCode service and retry in the selected workspace`
        : "[invocation_cancelled] workflow preflight aborted or plugin disposed; resume only in a new authorized invocation")
      const active = () => { if (signal.aborted || disposed) throw stopped() }
      let interrupt!: () => void
      const interrupted = new Promise<never>((_resolve, reject) => {
        interrupt = () => reject(stopped())
        signal.addEventListener("abort", interrupt, { once: true })
      })
      const timer = setTimeout(() => { timedOut = true; controller.abort() }, 5000)
      timer.unref?.()
      const work = async () => {
        active()
        report("Checking OpenCode session")
        const session = await ctx.session.get({ sessionID }, { signal }).catch(() => {
          throw new Error("[session_unavailable] OpenCode session lookup failed; resume in a live session in the selected workspace")
        })
        active()
        if (!session || !text(session.id) || !text(session.projectID) || !session.location || !text(session.location.directory)) {
          throw new Error("[session_unavailable] OpenCode returned incomplete session context; use a compatible live OpenCode session in the selected workspace")
        }
        if (session.id !== sessionID) throw new Error("[session_mismatch] OpenCode returned another session; restart OpenCode in the selected workspace")
        if (session.projectID !== ctx.location.project.id) throw new Error("[project_mismatch] session belongs to another project; resume in the selected workspace")
        stage = "workspace validation"
        const directory = await realpath(session.location.directory).catch(() => {
          throw new Error("[workspace_unavailable] session directory cannot be resolved; reopen the selected workspace")
        })
        active()
        if (!within(root, directory)) throw new Error("[workspace_mismatch] workflow session outside selected config; resume in the workspace where this integration was applied")
        stage = "binding validation"
        report("Checking workflow binding")
        await checkBinding(signal).catch(() => {
          throw new Error("[binding_changed] workflow binding unavailable or changed; run homonto apply with the selected --config and restart OpenCode")
        })
        active()
      }
      try { await Promise.race([work(), interrupted]) } finally {
        clearTimeout(timer)
        signal.removeEventListener("abort", interrupt)
        controller.abort()
      }
    }

    async function permitted(call: Invocation) {
      requireLiveContext(toolContext(call))
      if (disposed) throw new Error("[plugin_disposed] workflow plugin disposed; restart OpenCode before retrying")
      if (!text(call.id) || call.id.length > 256) throw new Error("[missing_call_id] tool invocation ID is missing or invalid; use a compatible live OpenCode session")
      await validateSession(call.sessionID, call.signal, stage => progress(call, stage))
    }

    function toolContext(call: Invocation) {
      return {
        sessionID: call?.sessionID, messageID: call?.messageID, agent: call?.agent, abort: call?.signal,
        progress: (stage: string) => progress(call, stage),
        ask: async (request: { permission: string; patterns: string[]; metadata: Record<string, unknown> }) => {
          if (!Object.values(request.metadata).every(v => typeof v === "string")) throw new Error("invalid permission metadata")
          await permitted(call)
          await authorization.ask(call, { permission: request.permission, patterns: request.patterns,
            metadata: request.metadata as Record<string, string> }, stage => progress(call, stage))
          await permitted(call)
        },
      }
    }

    const hostTool = async (host: (typeof hosts)[number], name: string, input: unknown, call: Invocation) => {
      await permitted(call)
      const result = await host.tools[name].execute(input, toolContext(call))
      await permitted(call)
      return result
    }

    try {
      registrations.push(await ctx.session.hook("context", inject))
      registrations.push(await ctx.session.hook("compaction", inject))
      registrations.push(await ctx.tool.transform(editor => {
        for (const [name, definition] of Object.entries(handback.tools)) {
          editor.add({ name, description: definition.description, options: { codemode: false },
            input: { type: "object", properties: definition.args, required: Object.keys(definition.args), additionalProperties: false },
            execute: async (input, call) => {
              requireCoordinator(toolContext(call), binding)
              await permitted(call)
              const result = await definition.execute(input, toolContext(call))
              await permitted(call)
              return { content: result }
            } })
        }
        for (const host of hosts) {
          for (const [name, definition] of Object.entries(host.tools)) {
            editor.add({ name, description: definition.description, options: { codemode: false },
              input: { type: "object", properties: definition.args, required: Object.keys(definition.args), additionalProperties: false },
              execute: async (input, call) => {
                const result = await hostTool(host, name, input, call)
                return { content: result }
              } })
          }
        }
      }))
      if (hosts.length) {
        registrations.push(await ctx.tool.hook("execute.before", (event) => {
          for (const host of hosts) host.before({ tool: event.tool, sessionID: event.sessionID, callID: event.id }, { args: event.input })
        }))
      }
      const rpc = await ctx.rpc.register(WorkflowPanelRPC, {
        identity: async () => ({ token: identity }),
        snapshot: panel,
      })
      registrations.push(rpc)
      const subscriber = ctx.event.subscribe({ signal: lifetime.signal })
      void (async () => {
        try {
          for await (const event of subscriber) {
            if (disposed) return
            for (const host of hosts) host.event({ event })
            await observeHandback(event)
            if (event.type !== "session.idle" || !object(event.data) || typeof event.data.sessionID !== "string") continue
            const sessionID = event.data.sessionID
            try {
              await validateSession(sessionID, lifetime.signal)
              const next = await snapshot()
              if (disposed) return
              const changes = new Map(next.changes.map(c => [`${c.workflow}\u0000${c.identity}`, c]))
              for (const [id, c] of changes) {
                const before = previous.get(id)
                if (!initialized && terminal(c)) continue
                if (JSON.stringify(before) === JSON.stringify(c)) continue
                const unhealthy = c.verifyResult === "fail" || next.findings.some(f => f.workflow === c.workflow && (!f.change || f.change === c.name))
                const message = bounded(`${c.workflow}: ${c.name} is ${c.status} at ${c.phase}; ${c.tasksCompleted}/${c.tasksTotal} tasks; pending: ${c.pending.join(", ")}`, 1000)
                await rpc.events.emit("updated", { sessionID, message, variant: unhealthy ? "error" : c.status === "completed" && !c.pending.length ? "success" : c.status === "abandoned" ? "warning" : "info" })
              }
              for (const [id, c] of previous) {
                if (!changes.has(id) && !terminal(c)) await rpc.events.emit("updated", {
                  sessionID, message: bounded(`${c.workflow}: ${c.name} is no longer observable; completion is not established`, 1000), variant: "warning",
                })
              }
              const findings = JSON.stringify(next.findings)
              if (findings !== previousFindings && next.findings.length) await rpc.events.emit("updated", {
                sessionID, message: `Workflow health: ${next.findings.length} finding(s)`, variant: "error",
              })
              if (!disposed) { previous = changes; previousFindings = findings; initialized = true }
            } catch {
              if (!disposed) await rpc.events.emit("updated", {
                sessionID, message: "Workflow observation unavailable; completion is not established", variant: "error",
              }).catch(() => {})
            }
          }
        } catch {}
      })()
    } catch (error) {
      dispose()
      throw error
    }
    return dispose
  },
} satisfies Plugin
