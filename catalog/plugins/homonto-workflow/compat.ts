// OpenCode v1.18.29/v1.18.30 accepts raw JSON-schema-shaped argument maps
// (wraps them as object properties, all required). Public ToolDefinition still
// advertises Zod. Keep this runtime contract explicit and validate in execute.
export type ArgumentSchema = Record<string, unknown>
export type ToolContext = {
  sessionID: string
  messageID: string
  agent: string
  abort: AbortSignal
  ask(input: { permission: string; patterns: string[]; always: string[]; metadata: Record<string, unknown> }): Promise<void>
}
export type ToolDefinition = {
  description: string
  args: Record<string, ArgumentSchema>
  execute(args: unknown, context: ToolContext): Promise<string>
}

export type Binding = {
  version: 1
  configPath: string
  configRoot: string
  coordinator: string
  githubEnabled: boolean
}

export function requireCoordinator(context: ToolContext, binding: Binding) {
  if (!context || context.agent !== binding.coordinator) throw new Error("homonto tools are coordinator-only")
  if (!context.abort || context.abort.aborted) throw new Error("tool aborted or missing abort context")
  if (typeof context.ask !== "function") throw new Error("tool permission context unavailable")
}

export function strictArgs(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).length !== keys.length || keys.some((key) => !Object.hasOwn(value, key))) {
    throw new Error(`invalid arguments; required keys: ${keys.join(", ") || "none"}`)
  }
  return value as Record<string, unknown>
}
