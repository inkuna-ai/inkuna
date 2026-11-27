import type { StepOrigin } from "../types";

const scopes: Record<string, string> = {
  builtin: "built-in",
  global: "global",
  repository: "repository",
};

/** The definition a step came through first: its outermost group, or the
 * shared stage itself. */
function sharedName(origin: StepOrigin): string {
  return origin.groups?.[0] ?? origin.stage ?? "";
}

/** "shared: verify-and-approve (global)", for a step planned from a shared
 * stage or group; empty for one written inline. */
export function originLabel(origin?: StepOrigin | null): string {
  if (!origin) return "";
  return `shared: ${sharedName(origin)} (${scopeName(origin.scope)})`;
}

/** How a definition's scope is named on screen. */
export function scopeName(scope: string): string {
  return scopes[scope] ?? scope;
}
