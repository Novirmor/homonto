// OpenCode v1.18.29/v1.18.30 accepts raw JSON-schema-shaped argument maps
// (wraps them as object properties, all required). Public ToolDefinition still
// advertises Zod. Keep this runtime contract explicit and validate in execute.
export type ArgumentSchema = Record<string, unknown>
export type ToolContext = {
  sessionID: string
  messageID: string
  agent: string
  abort: AbortSignal
  progress?: (stage: string) => void
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

export function requireLiveContext(context: ToolContext): void {
  const fail = (reason: string, detail: string): never => {
    throw new Error(`[${reason}] ${detail}; use a compatible live OpenCode session and retry the tool invocation`)
  }
  if (context == null) fail("missing_context", "tool context is missing")
  if (typeof context !== "object" || Array.isArray(context)) fail("invalid_context", "tool context is invalid")
  for (const field of ["agent", "sessionID", "messageID"] as const) {
    const value = context[field]
    if (value == null || value === "") fail(`missing_${field}`, `tool context ${field} is missing`)
    if (typeof value !== "string" || !value.trim() || value.length > 256) fail(`invalid_${field}`, `tool context ${field} must be a nonblank string of at most 256 characters`)
  }
  if (context.abort == null) fail("missing_abort", "tool abort context is missing")
  if (!(context.abort instanceof AbortSignal)) fail("invalid_abort", "tool abort context must be an AbortSignal")
  if (context.abort.aborted) fail("invocation_cancelled", "tool invocation was aborted; start a new invocation")
  if (context.ask == null) fail("missing_ask", "tool permission context is missing")
  if (typeof context.ask !== "function") fail("invalid_ask", "tool permission context must provide ask")
}

export function requireCoordinator(context: ToolContext, binding: Binding): void {
  const quoted = (value: unknown) => JSON.stringify(typeof value === "string"
    ? value.length > 128 ? `${value.slice(0, 128)}…` : value
    : value == null ? "<missing>" : "<invalid>")
    .replaceAll("\u2028", "\\u2028").replaceAll("\u2029", "\\u2029")
  const identities = `expected=${quoted(binding.coordinator)}, observed=${quoted(context?.agent)}`
  try { requireLiveContext(context) } catch (error) {
    if (error instanceof Error) error.message += `; ${identities}`
    throw error
  }
  if (context.agent !== binding.coordinator) {
    throw new Error(`[wrong_agent] homonto tools are coordinator-only: ${identities}; select the configured coordinator in OpenCode and retry`)
  }
}

export function strictArgs(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).length !== keys.length || keys.some((key) => !Object.hasOwn(value, key))) {
    throw new Error(`invalid arguments; required keys: ${keys.join(", ") || "none"}`)
  }
  return value as Record<string, unknown>
}
