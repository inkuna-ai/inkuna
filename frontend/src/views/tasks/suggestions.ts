import type { PlanSuggestion } from "../../types";

/**
 * A plan's steps with suggestions made: a command added to a step's
 * commands, unless it lists it already, or a permission given.
 */
export function withSuggestions<
  Step extends { id: string; permission: string; commands?: string[][] },
>(steps: Step[], suggestions: PlanSuggestion[]): Step[] {
  return steps.map((step) => {
    let next = step;
    for (const suggestion of suggestions) {
      if (suggestion.stepId !== step.id) continue;
      if (suggestion.permission)
        next = { ...next, permission: suggestion.permission };
      const command = suggestion.command;
      if (
        command &&
        !(next.commands ?? []).some(
          (listed) => listed.join("\u0000") === command.join("\u0000"),
        )
      )
        next = { ...next, commands: [...(next.commands ?? []), command] };
    }
    return next;
  });
}
