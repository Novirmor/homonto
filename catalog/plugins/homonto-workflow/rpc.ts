export const WorkflowPanelRPC = {
  id: "homonto.workflow",
  methods: {
    identity: {
      input: { type: "object", additionalProperties: false },
      output: {
        type: "object", properties: { token: { type: "string" } }, required: ["token"], additionalProperties: false,
      },
    },
    snapshot: {
      input: {
        type: "object",
        properties: { sessionID: { type: "string" } },
        required: ["sessionID"],
        additionalProperties: false,
      },
      output: {
        type: "object",
        properties: { text: { type: "string" } },
        required: ["text"],
        additionalProperties: false,
      },
    },
  },
  events: {
    updated: { schema: {
      type: "object", properties: {
        sessionID: { type: "string" }, message: { type: "string" },
        variant: { type: "string", enum: ["info", "success", "warning", "error"] },
      }, required: ["sessionID", "message", "variant"], additionalProperties: false,
    } },
  },
} as const
