import type { AgentDefinition, AgentScope } from "../../types";

export type AgentDraft = {
  /** The name the definition had when editing began; "" for a new agent. */
  originalName: string;
  scope: AgentScope;
  /** A file-backed agent stays in its scope; a new or copied one can pick. */
  scopeLocked: boolean;
  definition: AgentDefinition;
};

export function blankDefinition(): AgentDefinition {
  return {
    name: "",
    description: "",
    permission: "read-only",
    instructions: "",
    source: "",
  };
}
