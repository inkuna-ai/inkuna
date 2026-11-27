import { useState } from "react";
import { listUserMCPServers, signInUserMCPServer } from "../../bridge";
import type { Runtime, UserMCPServer } from "../../types";
import { Alert, Button, Chip } from "../common";
import { userServerStatus } from "../servers";

/**
 * The user's own MCP servers, as the runtime's CLI lists them, to tick for
 * every step on the runtime. Listing is asked for rather than run on open:
 * Claude Code connects to every server to list them. A ticked server the
 * CLI no longer lists stays, so it can be unticked.
 */
export function UserServersPicker({
  runtime,
  saved,
  value,
  onChange,
}: {
  runtime: Runtime;
  /** Whether the runtime is declared yet; a new one cannot be asked. */
  saved: boolean;
  value: string[] | undefined;
  onChange(value: string[] | undefined): void;
}) {
  const [listed, setListed] = useState<UserMCPServer[] | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const ticked = value ?? [];

  async function list() {
    setBusy("list");
    setError("");
    try {
      setListed(await listUserMCPServers(runtime));
    } catch (reason: unknown) {
      setError(String(reason));
    } finally {
      setBusy("");
    }
  }

  async function signIn(server: string) {
    setBusy(server);
    setError("");
    try {
      await signInUserMCPServer(runtime, server);
      setListed(await listUserMCPServers(runtime));
    } catch (reason: unknown) {
      setError(String(reason));
    } finally {
      setBusy("");
    }
  }

  function toggle(name: string, on: boolean) {
    const next = on
      ? [...ticked, name]
      : ticked.filter((item) => item !== name);
    onChange(next.length > 0 ? next : undefined);
  }

  const rows: UserMCPServer[] = [...(listed ?? [])];
  for (const name of ticked) {
    if (!rows.some((server) => server.name === name)) {
      rows.push({
        name,
        status: listed ? "missing" : "",
        detail: listed ? "the CLI no longer lists it" : undefined,
      });
    }
  }

  return (
    <fieldset
      aria-label="Your MCP servers"
      className="m-0 mt-4 grid min-w-0 gap-1.5 border-0 p-0"
    >
      <legend className="mb-1.5 text-xs text-ink-soft">
        Your own MCP servers given to every step on this runtime
      </legend>
      <small className="text-[11px] text-muted">
        A ticked server is given with all its tools, beside the servers a step
        is granted, and nothing reviews what it changes: a tracker&apos;s own
        server can write to the tracker. To limit a server to some of its tools,
        declare it under MCP servers in Settings instead. Unticked, no server of
        yours starts, whether or not workers load your CLI setup.
      </small>
      {error && <Alert flush>{error}</Alert>}
      {rows.length > 0 && (
        <ul className="m-0 grid list-none gap-1.5 p-0">
          {rows.map((server) => {
            const status = userServerStatus(server.status);
            return (
              <li
                key={server.name}
                className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs"
              >
                <label className="flex min-w-0 items-center gap-1.5 wrap-anywhere">
                  <input
                    type="checkbox"
                    checked={ticked.includes(server.name)}
                    onChange={(event) =>
                      toggle(server.name, event.target.checked)
                    }
                  />
                  <b className="font-mono">{server.name}</b>
                </label>
                {status && <Chip tone={status.tone}>{status.label}</Chip>}
                {(server.url || server.command || server.source) && (
                  <span className="min-w-0 text-[11px] wrap-anywhere text-muted">
                    {[server.source, server.url || server.command]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                )}
                {server.detail && server.status !== "needs-sign-in" && (
                  <span className="text-[11px] text-muted">
                    {server.detail}
                  </span>
                )}
                {server.signIn && server.status === "needs-sign-in" && (
                  <Button
                    className="px-2 py-0.5 text-xs"
                    disabled={busy !== ""}
                    onClick={() => void signIn(server.name)}
                  >
                    {busy === server.name ? "Signing in…" : "Sign in"}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div>
        <Button
          className="px-2.5 py-1 text-xs"
          disabled={!saved || busy !== ""}
          onClick={() => void list()}
        >
          {busy === "list"
            ? "Listing…"
            : listed
              ? "List again"
              : "List my servers"}
        </Button>
        {!saved && (
          <small className="ml-2 text-[11px] text-muted">
            Save the runtime first to list its CLI&apos;s servers.
          </small>
        )}
        {listed && listed.length === 0 && (
          <small className="ml-2 text-[11px] text-muted">
            The CLI lists no servers of yours.
          </small>
        )}
      </div>
    </fieldset>
  );
}
