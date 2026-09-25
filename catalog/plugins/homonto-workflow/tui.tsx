/** @jsxImportSource @opentui/solid */
import { Plugin, usePlugin } from "@opencode/plugin/tui"
import type { PanelInput } from "@opencode/plugin/tui/context"
import { Show, createEffect, createSignal, onCleanup } from "solid-js"
import { WorkflowPanelRPC } from "./rpc.ts"

type View = { sessionID: string; status: "loading" | "empty" | "ready" | "failed"; text: string }

function WorkflowPanel(props: { panel: PanelInput }) {
  const ctx = usePlugin()
  const rpc = ctx.client.rpc(WorkflowPanelRPC)
  const [revision, setRevision] = createSignal(0)
  const [view, setView] = createSignal<View>({ sessionID: "", status: "loading", text: "" })

  ctx.keymap.layer(() => ({
    enabled: () => props.panel.focused,
    commands: [{
      id: "homonto.workflow.refresh",
      title: "Refresh workflow status",
      group: "Homonto",
      bind: "r",
      run: () => { setRevision((value) => value + 1) },
    }],
  }))

  createEffect(() => {
    const sessionID = props.panel.sessionID
    revision()
    const controller = new AbortController()
    let active = true
    onCleanup(() => {
      active = false
      controller.abort()
    })
    setView({ sessionID, status: "loading", text: "" })
    void rpc.snapshot({ sessionID }, { signal: controller.signal, location: ctx.location ?? ctx.data.location.default() }).then((result) => {
      if (!active) return
      const text = result && typeof result === "object" && "text" in result ? result.text : undefined
      if (typeof text !== "string" || !text || new TextEncoder().encode(text).length > 16384) {
        setView({ sessionID, status: "failed", text: "Failed to load workflow status." })
        return
      }
      if (text.startsWith("Workflow observation unavailable;")) {
        setView({ sessionID, status: "failed", text })
        return
      }
      setView({ sessionID, status: text.startsWith("No active workflow work observed;") ? "empty" : "ready", text })
    }).catch(() => {
      if (active) setView({ sessionID, status: "failed", text: "Failed to load workflow status." })
    })
  })

  return (
    <box flexDirection="column" width="100%" height="100%" padding={1}>
      <text fg={ctx.theme.text.base}>Workflow status · r refresh</text>
      <scrollbox flexGrow={1} width="100%">
        <text fg={ctx.theme.text.base}>
          {view().sessionID !== props.panel.sessionID || view().status === "loading" ? "Loading workflow status…" :
            view().status === "empty" ? `No active work\n${view().text}` :
            view().status === "failed" ? `Failed to load\n${view().text}` : view().text}
        </text>
      </scrollbox>
    </box>
  )
}

function WorkflowCommand() {
  const ctx = usePlugin()
  ctx.keymap.layer(() => ({
    mode: "global",
    commands: [{
      id: "homonto.workflow",
      title: "Open workflow status",
      group: "Homonto",
      palette: true,
      run: () => {
        if (!ctx.ui.panel.open("homonto.workflow")) {
          ctx.ui.toast.show({ message: "Open a session to view workflow status.", variant: "info" })
        }
      },
    }],
  }))
  return null
}

export default Plugin.define({
  id: "homonto-workflow-tui",
  setup(ctx) {
    const stopNotifications = ctx.client.rpc(WorkflowPanelRPC).events.on("updated", (event) => {
      const route = ctx.ui.router.current()
      if (route.type !== "session" || route.sessionID !== event.data.sessionID) return
      const message = event.data.message
      if (typeof message !== "string" || !message || new TextEncoder().encode(message).length > 1024) return
      const variant = event.data.variant
      if (variant !== "info" && variant !== "success" && variant !== "warning" && variant !== "error") return
      ctx.ui.toast.show({ title: "Workflow", message, variant, sessionID: event.data.sessionID })
    })
    const removePanel = ctx.ui.slot({
      append: "session.panel",
      render: (panel) => (
        <Show when={panel.name === "homonto.workflow"}>
          <WorkflowPanel panel={panel} />
        </Show>
      ),
    })
    const removeCommand = ctx.ui.slot({ append: "app", render: () => <WorkflowCommand /> })
    return () => {
      stopNotifications()
      removeCommand()
      removePanel()
      if (ctx.ui.panel.current()?.name === "homonto.workflow") ctx.ui.panel.close()
    }
  },
})
