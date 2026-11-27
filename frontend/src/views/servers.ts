import type { MCPServer } from "../types";

/**
 * A step's granted server names as the declarations describe them; a name
 * no longer declared is shown by name alone.
 */
export function grantedServers(
  names: string[] | undefined,
  declared: MCPServer[] | undefined,
): MCPServer[] {
  return (names ?? []).map(
    (name) =>
      declared?.find((server) => server.name === name) ?? {
        name,
      },
  );
}

const userServerStatuses: Record<
  string,
  { label: string; tone: "green" | "muted" | "warn" | "error" }
> = {
  ready: { label: "ready", tone: "green" },
  unknown: { label: "starts with each call", tone: "muted" },
  "needs-sign-in": { label: "needs sign-in", tone: "warn" },
  failed: { label: "not starting", tone: "error" },
  missing: { label: "not configured", tone: "error" },
  disabled: { label: "switched off", tone: "muted" },
};

/** How a CLI's word on one of the user's servers is shown, or null for none. */
export function userServerStatus(
  status: string | undefined,
): { label: string; tone: "green" | "muted" | "warn" | "error" } | null {
  if (!status) return null;
  return userServerStatuses[status] ?? { label: status, tone: "muted" };
}
