import { useEffect, useState } from "react";
import type {
  AgentDefinition,
  AgentLocations,
  AgentScope,
  Runtime,
  TaskKind,
} from "../../types";
import { RuntimeOptions } from "../runtimes/RuntimeOptions";
import { useRuntimes } from "../runtimes/useRuntimes";
import { Alert, Button, Field, Notice, Panel, inputClass } from "../common";
import { TagInput } from "../TagInput";
import { TrackerKindsField } from "../TrackerKindsField";
import type { AgentDraft } from "./draft";

const taskKinds: TaskKind[] = [
  "research",
  "bugfix",
  "implementation",
  "review",
  "documentation",
  "skill-authoring",
];

export function AgentForm({
  draft,
  locations,
  secrets,
  onCancel,
  onSave,
  onDirtyChange,
}: {
  draft: AgentDraft;
  locations: AgentLocations;
  secrets: string[];
  onCancel(): void;
  onSave(draft: AgentDraft): Promise<void>;
  /** Says whether the form differs from the agent it opened with. */
  onDirtyChange?(dirty: boolean): void;
}) {
  const runtimes = useRuntimes();
  const [value, setValue] = useState(draft);
  const dirty = JSON.stringify(value) !== JSON.stringify(draft);
  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange]);
  // Closed, by a save or Cancel, the form holds nothing to lose.
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const definition = value.definition;
  function patch(next: Partial<AgentDefinition>) {
    setValue((current) => ({
      ...current,
      definition: { ...current.definition, ...next },
    }));
  }
  function toggle<T>(list: T[] | undefined, item: T): T[] {
    const items = list ?? [];
    return items.includes(item)
      ? items.filter((entry) => entry !== item)
      : [...items, item];
  }
  const directory =
    value.scope === "repository" ? locations.repository : locations.global;
  const title = draft.originalName ? `Edit ${draft.originalName}` : "New agent";
  // A name the opened definition carries but Settings no longer declares
  // stays listed, so it can be unchecked; saving with it checked is refused.
  const undeclared = (draft.definition.secrets ?? []).filter(
    (name) => !secrets.includes(name),
  );
  const secretOptions = [...secrets, ...undeclared];

  return (
    <Panel>
      <form
        aria-label={title}
        onSubmit={(event) => {
          event.preventDefault();
          setSaving(true);
          setError("");
          onSave(value)
            .catch((reason: unknown) => setError(String(reason)))
            .finally(() => setSaving(false));
        }}
      >
        <h2 className="m-0 text-lg font-semibold">{title}</h2>
        <p className="mt-1 mb-0 text-xs wrap-anywhere text-muted">
          {directory
            ? `Saved as ${directory}/${definition.name || "<name>"}.md`
            : "Choose a project to save into its repository."}
        </p>
        {error && (
          <div className="mt-4">
            <Alert flush>{error}</Alert>
          </div>
        )}
        <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-2">
          <Field label="Name (lowercase letters, digits, dashes)">
            <input
              required
              className={inputClass}
              value={definition.name}
              onChange={(event) => patch({ name: event.target.value })}
            />
          </Field>
          <Field label="Save to">
            <select
              aria-label="Save to"
              className={inputClass}
              disabled={value.scopeLocked}
              value={value.scope}
              onChange={(event) =>
                setValue((current) => ({
                  ...current,
                  scope: event.target.value as AgentScope,
                }))
              }
            >
              <option value="repository" disabled={!locations.repository}>
                This repository (.inkuna/agents)
              </option>
              <option value="global" disabled={!locations.global}>
                All projects (global agents directory)
              </option>
            </select>
          </Field>
          <div className="col-span-full">
            <Field label="Description: the orchestrator reads this to decide when to use the agent">
              <input
                required
                className={inputClass}
                value={definition.description}
                onChange={(event) => patch({ description: event.target.value })}
              />
            </Field>
          </div>
          <Field label="Permission">
            <select
              className={inputClass}
              value={definition.permission}
              onChange={(event) => patch({ permission: event.target.value })}
            >
              <option value="read-only">read-only</option>
              <option value="worktree-write">
                worktree-write (may edit files in the task worktree)
              </option>
            </select>
          </Field>
          <Field label="Runtime">
            <select
              className={inputClass}
              value={definition.runtime ?? ""}
              onChange={(event) =>
                patch({
                  runtime: (event.target.value || undefined) as
                    | Runtime
                    | undefined,
                })
              }
            >
              <option value="">project default</option>
              <RuntimeOptions
                runtimes={runtimes}
                value={definition.runtime}
                writeOnly={definition.permission === "worktree-write"}
              />
            </select>
          </Field>
          <Field label="Model (blank lets routing choose by complexity)">
            <input
              className={inputClass}
              value={definition.model ?? ""}
              onChange={(event) =>
                patch({ model: event.target.value || undefined })
              }
            />
          </Field>
          <Field label="Reasoning effort (optional)">
            <input
              className={inputClass}
              value={definition.reasoningEffort ?? ""}
              placeholder="low, medium, high, xhigh"
              onChange={(event) =>
                patch({ reasoningEffort: event.target.value || undefined })
              }
            />
          </Field>
          <Field label="Max turns (blank for the default)">
            <input
              type="number"
              min={0}
              className={`${inputClass} max-w-28`}
              value={definition.maxTurns || ""}
              onChange={(event) =>
                patch({ maxTurns: Number(event.target.value) || undefined })
              }
            />
          </Field>
          <Field label="Tags (comma separated)">
            <TagInput
              label="Tags"
              values={definition.tags}
              onChange={(tags) => patch({ tags })}
            />
          </Field>
          <div className="col-span-full">
            <Field label="Skills (comma separated ids from .inkuna/skills)">
              <TagInput
                label="Skills"
                values={definition.skills}
                onChange={(skills) => patch({ skills })}
              />
            </Field>
          </div>
          <div className="col-span-full">
            <Field label="MCP servers (comma separated names declared in inkuna.yaml)">
              <TagInput
                label="MCP servers"
                values={definition.mcp}
                onChange={(mcp) => patch({ mcp })}
              />
            </Field>
          </div>
          <div className="col-span-full">
            <TrackerKindsField
              label="Tracker changes its steps may ask for, as far as each repository's tracker settings allow"
              values={definition.tracker}
              onChange={(tracker) => patch({ tracker })}
            />
          </div>
        </div>
        <fieldset className="m-0 mt-2 min-w-0 border-0 p-0">
          <legend className="mb-1.5 text-xs text-ink-soft">
            Task kinds (none selected makes it a generalist)
          </legend>
          <div className="flex flex-wrap gap-x-4 gap-y-1.5">
            {taskKinds.map((kind) => (
              <label key={kind} className="flex items-center gap-1.5 text-xs">
                <input
                  type="checkbox"
                  checked={definition.taskKinds?.includes(kind) ?? false}
                  onChange={() =>
                    patch({ taskKinds: toggle(definition.taskKinds, kind) })
                  }
                />
                {kind}
              </label>
            ))}
          </div>
        </fieldset>
        <fieldset className="m-0 mt-4 min-w-0 border-0 p-0">
          <legend className="mb-1.5 text-xs text-ink-soft">
            Secrets exported to this agent&apos;s process
          </legend>
          {secretOptions.length === 0 ? (
            <p className="m-0 text-xs text-muted">
              No secrets are declared. Declare them in Settings first; values
              stay in the keychain.
            </p>
          ) : (
            <div className="flex flex-wrap gap-x-4 gap-y-1.5">
              {secretOptions.map((name) => (
                <label
                  key={name}
                  className="flex min-w-0 items-center gap-1.5 text-xs wrap-anywhere"
                >
                  <input
                    type="checkbox"
                    checked={definition.secrets?.includes(name) ?? false}
                    onChange={() =>
                      patch({ secrets: toggle(definition.secrets, name) })
                    }
                  />
                  {name}
                  {undeclared.includes(name) && (
                    <span className="text-muted">(not declared)</span>
                  )}
                </label>
              ))}
            </div>
          )}
        </fieldset>
        <Field label="Instructions (Markdown): what the agent is told on every task">
          <textarea
            required
            className={`${inputClass} min-h-[220px] resize-y font-mono text-xs leading-relaxed`}
            value={definition.instructions}
            onChange={(event) => patch({ instructions: event.target.value })}
          />
        </Field>
        <Notice>
          Runs already in progress keep the agents they started with; the next
          plan picks up this definition.
        </Notice>
        <div className="mt-5 flex flex-wrap justify-end gap-2">
          <Button onClick={onCancel}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={saving}>
            {saving ? "Saving…" : "Save agent"}
          </Button>
        </div>
      </form>
    </Panel>
  );
}
