import type { MCPServer, UserMCPServer } from "../types";
import { Chip } from "./common";
import { userServerStatus } from "./servers";

/**
 * The MCP servers a step is given: what each is, how Inkuna reaches it, and
 * the tools the step may call. A read-only step's permission covers only
 * its worktree, so the list says when its servers can change more.
 */
export function ServerList({
  servers,
  readOnly,
}: {
  servers: MCPServer[];
  readOnly: boolean;
}) {
  if (servers.length === 0) return null;
  return (
    <section aria-label="MCP servers" className="grid min-w-0 gap-1.5">
      <h4 className="m-0 text-[11px] font-semibold text-muted">MCP servers</h4>
      {/* A tracker's server too: that its tools only read is what whoever
          declared it said, not something Inkuna checked. */}
      {readOnly && (
        <p className="m-0 text-[11px] wrap-anywhere text-warn">
          Read-only covers the worktree only: through these servers the step can
          change systems outside it.
        </p>
      )}
      <ul className="m-0 grid list-none gap-1.5 p-0">
        {servers.map((server) => (
          <li key={server.name} className="min-w-0 text-xs wrap-anywhere">
            <b className="font-mono">{server.name}</b>
            {server.tracker && (
              <>
                {" "}
                <Chip tone="muted">{server.tracker} · declared read tools</Chip>
              </>
            )}
            {server.description && (
              <span className="text-ink-soft"> · {server.description}</span>
            )}
            <div className="text-[11px] text-muted">
              {server.tools?.length
                ? `Tools: ${server.tools.join(", ")}`
                : "All its tools"}
              {(server.url || server.command?.length) && (
                <span className="font-mono">
                  {" · "}
                  {server.url || server.command?.join(" ")}
                </span>
              )}
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * The user's own MCP servers a step is given, by its runtime's userMcp: on
 * the approval screen by name, and in the run's record with what the CLI
 * said of each when the step started. Every tool of each is allowed, and
 * nothing reviews what they change, so the list says so.
 */
export function UserServerList({ servers }: { servers: UserMCPServer[] }) {
  if (servers.length === 0) return null;
  return (
    <section aria-label="Your MCP servers" className="grid min-w-0 gap-1.5">
      <h4 className="m-0 text-[11px] font-semibold text-muted">
        Your MCP servers
      </h4>
      <p className="m-0 text-[11px] wrap-anywhere text-warn">
        From your own CLI setup, with every tool: nothing reviews what the step
        changes through them, a tracker included.
      </p>
      <ul className="m-0 grid list-none gap-1.5 p-0">
        {servers.map((server) => {
          const status = userServerStatus(server.status);
          return (
            <li key={server.name} className="min-w-0 text-xs wrap-anywhere">
              <b className="font-mono">{server.name}</b>{" "}
              <Chip tone="warn">yours · all tools</Chip>
              {status && (
                <>
                  {" "}
                  <Chip tone={status.tone}>{status.label}</Chip>
                </>
              )}
              {(server.url || server.command || server.detail) && (
                <div className="text-[11px] text-muted">
                  {[server.source, server.url || server.command, server.detail]
                    .filter(Boolean)
                    .join(" · ")}
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
