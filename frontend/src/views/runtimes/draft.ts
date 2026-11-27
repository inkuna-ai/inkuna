import type { Runtime, RuntimeConfig } from "../../types";

export interface RuntimeDraft {
  name: Runtime;
  isNew: boolean;
  builtIn: boolean;
  config: RuntimeConfig;
}

// A starting point for a CLI Inkuna has no adapter for: the prompt on stdin,
// plain-text output, and no write arguments, which keeps it read-only. The
// read-only arguments are left for the user: only they know the CLI's flag.
export const blankTemplate: RuntimeConfig = {
  args: ["--model", "{model}"],
  stdin: "prompt",
  output: "text",
};
