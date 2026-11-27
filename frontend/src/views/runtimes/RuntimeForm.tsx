import { useState } from "react";
import type { Runtime, RuntimeConfig } from "../../types";
import { blankTemplate, type RuntimeDraft } from "./draft";
import { Alert, Button, Field, Notice, Panel, inputClass } from "../common";
import { UserServersPicker } from "./UserServersPicker";

const presets: Array<{ id: Runtime; label: string }> = [
  { id: "claude", label: "Claude Code" },
  { id: "codex", label: "Codex" },
  { id: "copilot", label: "GitHub Copilot" },
];

/**
 * One argument per line, with no shell or quoting. Lines are trimmed: an
 * invisible trailing space or a pasted carriage return would otherwise become
 * part of a flag. An argument that needs surrounding whitespace can be written
 * in inkuna.yaml by hand.
 */
function ArgumentsField({
  label,
  hint,
  required = false,
  value,
  onChange,
}: {
  label: string;
  hint: string;
  required?: boolean;
  value: string[] | undefined;
  onChange(value: string[] | undefined): void;
}) {
  // Blank lines are kept while typing; what is saved has them dropped and
  // each line trimmed.
  const [text, setText] = useState((value ?? []).join("\n"));
  return (
    <Field label={label}>
      <textarea
        className={`${inputClass} min-h-[92px] resize-y font-mono text-xs leading-relaxed`}
        aria-label={label}
        required={required}
        spellCheck={false}
        value={text}
        onChange={(event) => {
          setText(event.target.value);
          const lines = event.target.value
            .split("\n")
            .map((line) => line.trim())
            .filter(Boolean);
          onChange(lines.length > 0 ? lines : undefined);
        }}
      />
      <small className="text-[11px] text-muted">{hint}</small>
    </Field>
  );
}

export function RuntimeForm({
  draft,
  taken,
  secrets,
  onCancel,
  onSave,
}: {
  draft: RuntimeDraft;
  /** Names already declared, which a new runtime cannot reuse. */
  taken: Runtime[];
  secrets: string[];
  onCancel(): void;
  onSave(draft: RuntimeDraft): Promise<void>;
}) {
  const [value, setValue] = useState(draft);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const config = value.config;
  const usesAdapter = value.builtIn || Boolean(config.preset);
  // The CLI whose servers userMcp names. Listing asks the runtime as saved,
  // so it lists only while the form still has the saved CLI.
  const adapter = value.builtIn ? value.name : config.preset;
  const savedAdapter = draft.builtIn ? draft.name : draft.config.preset;
  function patch(next: Partial<RuntimeConfig>) {
    setValue((current) => ({
      ...current,
      config: { ...current.config, ...next },
    }));
  }
  // A name the opened entry exports but Settings no longer declares stays
  // listed, so it can be unticked; saving with it ticked is refused.
  const undeclared = (draft.config.secrets ?? []).filter(
    (name) => !secrets.includes(name),
  );
  const secretOptions = [...secrets, ...undeclared];
  const title = draft.isNew ? "Add runtime" : `Edit ${draft.name}`;
  const nameTaken = draft.isNew && taken.includes(value.name.trim());

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
        {error && (
          <div className="mt-4">
            <Alert flush>{error}</Alert>
          </div>
        )}
        <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-2">
          <Field label="Name (lowercase letters, digits, dashes): agents, rules, and projects refer to it">
            <input
              required
              pattern="[a-z][a-z0-9\-]{0,31}"
              className={inputClass}
              aria-label="Runtime name"
              disabled={!draft.isNew}
              value={value.name}
              onChange={(event) =>
                setValue((current) => ({
                  ...current,
                  name: event.target.value,
                }))
              }
            />
            {nameTaken && (
              <small role="alert" className="text-[11px] text-danger">
                {value.name.trim()} is already declared; edit it instead.
              </small>
            )}
          </Field>
          <Field label="Label shown in the app (optional)">
            <input
              className={inputClass}
              aria-label="Runtime label"
              value={config.label ?? ""}
              onChange={(event) =>
                patch({ label: event.target.value || undefined })
              }
            />
          </Field>
          {!value.builtIn && (
            <Field label="Start from">
              <select
                className={inputClass}
                aria-label="Start from"
                value={config.preset ?? ""}
                onChange={(event) => {
                  const preset = event.target.value;
                  // An adapter builds its own arguments, so a template cannot
                  // be kept beside it; switching back starts a fresh one.
                  setValue((current) => ({
                    ...current,
                    config: preset
                      ? {
                          label: current.config.label,
                          binary: current.config.binary,
                          secrets: current.config.secrets,
                          userConfig: current.config.userConfig,
                          // Server names are the CLI's own, so another CLI's
                          // would name servers it does not have.
                          userMcp:
                            preset === current.config.preset
                              ? current.config.userMcp
                              : undefined,
                          preset,
                        }
                      : {
                          ...blankTemplate,
                          label: current.config.label,
                          binary: current.config.binary,
                          secrets: current.config.secrets,
                        },
                  }));
                }}
              >
                <option value="">Blank argument template</option>
                {presets.map((preset) => (
                  <option key={preset.id} value={preset.id}>
                    {preset.label} adapter (another copy of that CLI)
                  </option>
                ))}
              </select>
            </Field>
          )}
          <Field label="Binary: a name found on PATH, or an absolute path">
            <input
              required={!usesAdapter}
              className={inputClass}
              aria-label="Runtime binary"
              placeholder={
                usesAdapter
                  ? (config.preset ?? value.name)
                  : "/opt/tool/bin/tool"
              }
              value={config.binary ?? ""}
              onChange={(event) =>
                patch({ binary: event.target.value || undefined })
              }
            />
          </Field>
        </div>
        {usesAdapter ? (
          <>
            <Notice>
              Inkuna builds this runtime&apos;s arguments itself, with the
              CLI&apos;s sandbox and tool limits for read-only and
              worktree-write steps. Only the binary, the exported secrets,
              whether workers load your own CLI setup, and which of your own MCP
              servers they get are configurable.
            </Notice>
            <div className="mt-4 grid gap-1">
              <label className="flex min-w-0 items-center gap-1.5 text-xs">
                <input
                  type="checkbox"
                  checked={config.userConfig ?? false}
                  onChange={(event) =>
                    patch({ userConfig: event.target.checked || undefined })
                  }
                />
                Load my own CLI setup in workers
              </label>
              <small className="text-[11px] text-muted">
                Your settings, hooks, skills, memory, and plugins, and for Codex
                its config.toml; your MCP servers start only if ticked below.
                Off, workers start from the CLI&apos;s defaults and the
                repository&apos;s instructions, which keeps every call smaller.
                Turn it on only when workers need that setup, such as a Codex
                model provider or a team&apos;s hooks.
              </small>
            </div>
            <UserServersPicker
              key={adapter}
              runtime={value.name.trim()}
              saved={!draft.isNew && adapter === savedAdapter}
              value={config.userMcp}
              onChange={(userMcp) => patch({ userMcp })}
            />
          </>
        ) : (
          <>
            <Notice>
              One argument per line; Inkuna starts the binary directly, never
              through a shell. Placeholders: {"{prompt}"}, {"{model}"},{" "}
              {"{reasoning_effort}"}, {"{schema}"} (the JSON schema text), and{" "}
              {"{schema_path}"} (a file holding it). A placeholder with no value
              drops its argument, and the flag before it. Flags that switch a
              CLI&apos;s permission checks off are refused.
            </Notice>
            <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-2">
              <div className="col-span-full">
                <ArgumentsField
                  label="Arguments for every run"
                  hint="For example: run, --model, {model}"
                  value={config.args}
                  onChange={(args) => patch({ args })}
                />
              </div>
              <ArgumentsField
                label="Added for read-only steps (required)"
                hint="The flags that keep the CLI from changing files. Inkuna cannot enforce a read-only step without them."
                required
                value={config.readOnlyArgs}
                onChange={(readOnlyArgs) => patch({ readOnlyArgs })}
              />
              <ArgumentsField
                label="Added for worktree-write steps"
                hint="Leave empty and this runtime only runs read-only steps."
                value={config.writeArgs}
                onChange={(writeArgs) => patch({ writeArgs })}
              />
              <Field label="Prompt delivery">
                <select
                  className={inputClass}
                  aria-label="Prompt delivery"
                  value={config.stdin ?? "prompt"}
                  onChange={(event) => patch({ stdin: event.target.value })}
                >
                  <option value="prompt">Written to standard input</option>
                  <option value="none">
                    Passed as {"{prompt}"} in the arguments
                  </option>
                </select>
              </Field>
              <Field label="Output format">
                <select
                  className={inputClass}
                  aria-label="Output format"
                  value={config.output ?? "text"}
                  onChange={(event) => patch({ output: event.target.value })}
                >
                  <option value="text">
                    Plain text (the last JSON object is the result)
                  </option>
                  <option value="claude-stream-json">
                    Claude Code stream-json events
                  </option>
                  <option value="codex-json">Codex JSON events</option>
                  <option value="copilot-json">Copilot JSON events</option>
                </select>
              </Field>
              <ArgumentsField
                label="Version probe arguments"
                hint="Used by Test connection and the health check. Defaults to --version."
                value={config.versionArgs}
                onChange={(versionArgs) => patch({ versionArgs })}
              />
            </div>
          </>
        )}
        <fieldset className="m-0 mt-4 min-w-0 border-0 p-0">
          <legend className="mb-1.5 text-xs text-ink-soft">
            Secrets exported to this runtime&apos;s worker processes
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
                    checked={config.secrets?.includes(name) ?? false}
                    onChange={(event) => {
                      const current = config.secrets ?? [];
                      const next = event.target.checked
                        ? [...current, name]
                        : current.filter((item) => item !== name);
                      patch({ secrets: next.length > 0 ? next : undefined });
                    }}
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
        <div className="mt-5">
          <Notice>
            Runs already in progress keep the runtimes they started with; the
            next plan uses this declaration.
          </Notice>
        </div>
        <div className="mt-5 flex flex-wrap justify-end gap-2">
          <Button onClick={onCancel}>Cancel</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={saving || nameTaken}
          >
            {saving ? "Saving…" : "Save runtime"}
          </Button>
        </div>
      </form>
    </Panel>
  );
}
