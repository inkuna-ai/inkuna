import { useState } from "react";
import type { MCPServerConfig, SecretRef } from "../../types";
import { TagInput } from "../TagInput";
import {
  Button,
  Chip,
  TableScroll,
  inputClass,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import { formatCommands, parseCommands } from "../rules/model";
import {
  FieldErrors,
  LinkButton,
  SetupPanel,
  formGridClass,
  type UpdateConfig,
} from "./shared";

/** The form's text for one server; Go checks the server on save. */
type ServerForm = {
  name: string;
  description: string;
  transport: "command" | "url";
  command: string;
  url: string;
  /** VARIABLE=SECRET pairs. */
  env: string[];
  bearer: string;
  tools: string[];
  tracker: string;
};

const emptyForm: ServerForm = {
  name: "",
  description: "",
  transport: "command",
  command: "",
  url: "",
  env: [],
  bearer: "",
  tools: [],
  tracker: "",
};

function formOf(server: MCPServerConfig): ServerForm {
  return {
    name: server.name,
    description: server.description ?? "",
    transport: server.url ? "url" : "command",
    command: formatCommands(server.command ? [server.command] : []),
    url: server.url ?? "",
    env: Object.entries(server.env ?? {}).map(
      ([variable, secret]) => `${variable}=${secret}`,
    ),
    bearer: server.bearer ?? "",
    tools: server.tools ?? [],
    tracker: server.tracker ?? "",
  };
}

function serverOf(form: ServerForm): MCPServerConfig {
  const server: MCPServerConfig = { name: form.name.trim() };
  if (form.description.trim()) server.description = form.description.trim();
  if (form.transport === "command") {
    // Parsed as argv, the same way the rules editor parses commands.
    server.command = parseCommands(form.command)[0] ?? [];
    const env: Record<string, string> = {};
    for (const pair of form.env) {
      const [variable, ...secret] = pair.split("=");
      env[variable.trim()] = secret.join("=").trim();
    }
    if (form.env.length > 0) server.env = env;
  } else {
    server.url = form.url.trim();
    if (form.bearer) server.bearer = form.bearer;
  }
  if (form.tools.length > 0) server.tools = form.tools;
  if (form.tracker) server.tracker = form.tracker;
  return server;
}

/**
 * The MCP servers this machine offers workers. A rule stage or an agent
 * grants one by name; its secrets are named here and resolved from the
 * keychain or the environment when a step starts.
 */
export function MCPServersPanel({
  servers,
  secrets,
  update,
}: {
  servers: MCPServerConfig[];
  secrets: SecretRef[];
  update: UpdateConfig;
}) {
  const [form, setForm] = useState<ServerForm>(emptyForm);
  // The row of the declared server loaded into the form, if any. Rows, not
  // names, identify servers here: until Go refuses the save, a rename can
  // leave two rows with one name.
  const [editing, setEditing] = useState<number | null>(null);

  function save() {
    const next = serverOf(form);
    update((draft) => {
      // Only the server being edited is replaced. A new server, or a rename,
      // onto another server's name keeps both, so Go refuses the save rather
      // than one silently replacing the other.
      const servers = draft.mcpServers ?? [];
      const kept =
        editing === null
          ? servers
          : servers.filter((_, row) => row !== editing);
      draft.mcpServers = [...kept, next];
    });
    setForm(emptyForm);
    setEditing(null);
  }

  const ready =
    form.name.trim() !== "" &&
    (form.transport === "command"
      ? form.command.trim() !== ""
      : form.url.trim() !== "");

  return (
    <SetupPanel
      title="MCP servers"
      wide
      description="Servers a rule stage or an agent can give its steps by name. A step starts only the servers it is granted, and a server's secrets reach the server, never the model."
    >
      <FieldErrors field="mcpServers" />
      {servers.length === 0 ? (
        <p className="mt-4 mb-0 text-xs text-muted">No MCP servers declared.</p>
      ) : (
        <TableScroll>
          <table className={tableClass} aria-label="Declared MCP servers">
            <thead>
              <tr>
                <th className={thClass}>Name</th>
                <th className={thClass}>Reaches</th>
                <th className={thClass}>Secrets</th>
                <th className={thClass}>Tools</th>
                <th className={thClass}></th>
              </tr>
            </thead>
            <tbody>
              {servers.map((server, index) => (
                <tr key={index}>
                  <td className={tdClass}>
                    <b className="font-mono font-semibold wrap-anywhere text-ink-strong">
                      {server.name}
                    </b>
                    {server.tracker && (
                      <>
                        {" "}
                        <Chip tone="muted">{server.tracker}</Chip>
                      </>
                    )}
                    {server.description && (
                      <div className="text-muted wrap-anywhere">
                        {server.description}
                      </div>
                    )}
                  </td>
                  <td className={tdClass}>
                    <span className="font-mono wrap-anywhere text-muted">
                      {server.url || formatCommands([server.command ?? []])}
                    </span>
                  </td>
                  <td className={tdClass}>
                    <span className="font-mono wrap-anywhere text-muted">
                      {[
                        ...Object.entries(server.env ?? {}).map(
                          ([variable, secret]) => `${variable} ← ${secret}`,
                        ),
                        ...(server.bearer ? [`bearer ← ${server.bearer}`] : []),
                      ].join(", ") || "—"}
                    </span>
                  </td>
                  <td className={tdClass}>
                    {server.tools?.length ? server.tools.join(", ") : "all"}
                  </td>
                  <td className={`${tdClass} text-right whitespace-nowrap`}>
                    <LinkButton
                      aria-label={`Edit MCP server ${server.name}`}
                      onClick={() => {
                        setForm(formOf(server));
                        setEditing(index);
                      }}
                    >
                      Edit
                    </LinkButton>{" "}
                    <LinkButton
                      aria-label={`Remove MCP server ${server.name}`}
                      onClick={() => {
                        update((draft) => {
                          draft.mcpServers = (draft.mcpServers ?? []).filter(
                            (_, row) => row !== index,
                          );
                        });
                        // The form's row moves up with the rows after it. If
                        // it was this one, saving the form adds it back.
                        if (editing === index) setEditing(null);
                        else if (editing !== null && editing > index)
                          setEditing(editing - 1);
                      }}
                    >
                      Remove
                    </LinkButton>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      <div className={formGridClass}>
        <input
          className={inputClass}
          aria-label="MCP server name"
          placeholder="name"
          value={form.name}
          onChange={(event) => setForm({ ...form, name: event.target.value })}
        />
        <input
          className={inputClass}
          aria-label="MCP server description"
          placeholder="what it is for (shown to the worker)"
          value={form.description}
          onChange={(event) =>
            setForm({ ...form, description: event.target.value })
          }
        />
        <select
          className={inputClass}
          aria-label="MCP server transport"
          value={form.transport}
          onChange={(event) =>
            setForm({
              ...form,
              transport: event.target.value as ServerForm["transport"],
            })
          }
        >
          <option value="command">command Inkuna starts</option>
          <option value="url">URL Inkuna connects to</option>
        </select>
        {form.transport === "command" ? (
          <>
            <input
              className={`${inputClass} font-mono`}
              aria-label="MCP server command"
              placeholder='npx -y "@example/docs-mcp@2.1.0"'
              value={form.command}
              onChange={(event) =>
                setForm({ ...form, command: event.target.value })
              }
            />
            <TagInput
              label="MCP server environment"
              values={form.env}
              placeholder="VARIABLE=SECRET, ..."
              onChange={(env) => setForm({ ...form, env })}
            />
          </>
        ) : (
          <>
            <input
              className={`${inputClass} font-mono`}
              aria-label="MCP server URL"
              placeholder="https://mcp.example.com/mcp"
              value={form.url}
              onChange={(event) =>
                setForm({ ...form, url: event.target.value })
              }
            />
            <select
              className={inputClass}
              aria-label="MCP server bearer secret"
              value={form.bearer}
              onChange={(event) =>
                setForm({ ...form, bearer: event.target.value })
              }
            >
              <option value="">no bearer token</option>
              {secrets.map((secret) => (
                <option key={secret.name} value={secret.name}>
                  bearer: {secret.name}
                </option>
              ))}
            </select>
          </>
        )}
        <TagInput
          label="MCP server tools"
          values={form.tools}
          placeholder="tools allowed (empty: all)"
          onChange={(tools) => setForm({ ...form, tools })}
        />
        <select
          className={inputClass}
          aria-label="MCP server tracker"
          value={form.tracker}
          onChange={(event) =>
            setForm({ ...form, tracker: event.target.value })
          }
        >
          <option value="">not a tracker's server</option>
          <option value="linear">Linear's server (read tools only)</option>
          <option value="github">GitHub's server (read tools only)</option>
        </select>
        <Button disabled={!ready} onClick={save}>
          {editing !== null ? "Update server" : "Add server"}
        </Button>
      </div>
    </SetupPanel>
  );
}
