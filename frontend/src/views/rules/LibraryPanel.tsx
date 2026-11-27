import { useEffect, useState } from "react";
import { globalDefinitionUsers, saveGlobalDefinition } from "../../bridge";
import type {
  DefinitionUser,
  LibraryDefinition,
  RuleGroup,
  RuleStage,
  RulesDocument,
  StageLibrary,
} from "../../types";
import {
  Alert,
  Button,
  Chip,
  Field,
  Notice,
  Panel,
  inputClass,
} from "../common";
import { FlowEditor } from "./FlowEditor";
import {
  copyDefinition,
  definitionNames,
  newGroup,
  newSharedStage,
  removeDefinition,
  scopeLabels,
  setDefinition,
  stageKinds,
} from "./model";
import { StageFields } from "./StageFields";

type Value = { stage: RuleStage } | { group: RuleGroup };

// The repository's own definitions are one per name, so their key is
// scope and name alone: one just created opens before Go lists its file.
const key = (definition: Pick<LibraryDefinition, "scope" | "file" | "name">) =>
  definition.scope === "repository"
    ? `repository\n${definition.name}`
    : `${definition.scope}\n${definition.file ?? ""}\n${definition.name}`;

/**
 * The shared stages and groups the project's rules can use, in every scope,
 * as Go listed them for the rules being edited. The repository's own are
 * part of the rules and saved with them; a global one is saved to its
 * library file on its own, after saying which repositories use it; a
 * built-in one is read-only and can be copied to the repository instead.
 */
export function LibraryPanel({
  library,
  document,
  agents,
  remoteAgents,
  onChange,
  onLibraryChanged,
  onAddFromCatalog,
  problem,
  loadError = "",
}: {
  library: StageLibrary | null;
  /** Why Go's listing could not be loaded, when it could not. */
  loadError?: string;
  document: RulesDocument;
  agents: string[];
  remoteAgents: string[];
  onChange(document: RulesDocument): void;
  /** A global definition was saved, so Go's listing is out of date. */
  onLibraryChanged(): void;
  /** Opens the catalog's shared stages and groups to add one. */
  onAddFromCatalog?(): void;
  /** The repository definition Go refused, with its reason. */
  problem?: { definition: string; message: string } | null;
}) {
  const [open, setOpen] = useState<string | null>(null);
  const [name, setName] = useState("");
  const taken = definitionNames(library, document);
  const own = (definition: string) =>
    Boolean(document.stages?.[definition] || document.groups?.[definition]);
  function create(value: Value) {
    const trimmed = name.trim();
    onChange(setDefinition(document, trimmed, value));
    setOpen(key({ scope: "repository", name: trimmed }));
    setName("");
  }
  const definitions = library?.definitions ?? [];
  return (
    <Panel>
      <section aria-label="Shared stages and groups">
        <h3 className="m-0 text-[15px] font-semibold">
          Shared stages and groups
        </h3>
        <p className="mt-2 mb-4 text-sm text-muted">
          Define a stage, or a run of stages, once and insert it into any flow.
          A repository definition replaces a global one with the same name for
          this repository, and a global one replaces a built-in one.
        </p>
        {loadError ? (
          <Alert flush>
            The stage library could not be loaded: {loadError}
          </Alert>
        ) : (
          !library && (
            <p className="m-0 text-xs text-muted">Loading the stage library…</p>
          )
        )}
        {(library?.problems ?? []).length > 0 && (
          <Notice title="Library files with problems" role="status">
            <ul className="m-0 grid list-none gap-1.5 p-0">
              {(library?.problems ?? []).map((item) => (
                <li
                  key={`${item.file}\n${item.message}`}
                  className="text-warn wrap-anywhere"
                >
                  <span className="font-mono">{item.file}</span>: {item.message}
                </li>
              ))}
            </ul>
          </Notice>
        )}
        <ul
          aria-label="Shared definitions"
          className="m-0 mt-2 grid list-none gap-2 p-0"
        >
          {definitions.map((definition) => {
            const id = key(definition);
            const refused =
              definition.scope === "repository" &&
              problem?.definition === definition.name;
            return (
              <DefinitionRow
                key={id}
                definition={definition}
                document={document}
                library={library}
                agents={agents}
                remoteAgents={remoteAgents}
                open={open === id || refused}
                problem={refused ? problem.message : ""}
                canCopy={
                  definition.scope !== "repository" && !own(definition.name)
                }
                onToggle={() => setOpen(open === id ? null : id)}
                onChange={onChange}
                onLibraryChanged={onLibraryChanged}
              />
            );
          })}
        </ul>
        <div className="mt-4 flex min-w-0 flex-wrap items-center gap-2">
          <input
            aria-label="New shared definition name"
            className={`${inputClass} w-56 max-w-full`}
            placeholder="name, such as review"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
          <Button
            disabled={!name.trim() || own(name.trim())}
            onClick={() => create({ stage: newSharedStage() })}
          >
            + New shared stage
          </Button>
          <Button
            disabled={!name.trim() || own(name.trim())}
            onClick={() => create({ group: newGroup() })}
          >
            + New group
          </Button>
          {onAddFromCatalog && (
            <Button onClick={onAddFromCatalog}>Add from catalog…</Button>
          )}
          {name.trim() && !own(name.trim()) && taken.includes(name.trim()) && (
            <span className="text-xs text-warn">
              {name.trim()} is defined elsewhere; this one replaces it for this
              repository.
            </span>
          )}
        </div>
      </section>
    </Panel>
  );
}

function DefinitionRow({
  definition,
  document,
  library,
  agents,
  remoteAgents,
  open,
  problem,
  canCopy,
  onToggle,
  onChange,
  onLibraryChanged,
}: {
  definition: LibraryDefinition;
  document: RulesDocument;
  library: StageLibrary | null;
  agents: string[];
  remoteAgents: string[];
  open: boolean;
  problem: string;
  canCopy: boolean;
  onToggle(): void;
  onChange(document: RulesDocument): void;
  onLibraryChanged(): void;
}) {
  const scope = scopeLabels[definition.scope];
  const label = `${scope} ${definition.kind === "group" ? "group" : "stage"} ${definition.name}`;
  const usedBy = (definition.usedBy ?? []).map((flow) =>
    flow === "default" ? "the fallback flow" : flow,
  );
  // The repository's own are edited in the draft, which Go's listing
  // catches up with only after a pause, so they are read from it.
  const drafted =
    definition.scope === "repository"
      ? document.groups?.[definition.name]
        ? { group: document.groups[definition.name] }
        : document.stages?.[definition.name]
          ? { stage: document.stages[definition.name] }
          : undefined
      : undefined;
  const value: Value =
    drafted ??
    (definition.group
      ? { group: definition.group }
      : { stage: definition.stage ?? { id: "", kind: "" } });
  return (
    <li
      aria-label={label}
      aria-invalid={Boolean(problem) || undefined}
      className={`min-w-0 border bg-field p-3 ${problem ? "border-danger" : "border-line"} ${definition.overridden ? "opacity-70" : ""}`}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <b className="min-w-0 text-xs wrap-anywhere">{definition.name}</b>
        <Chip tone="accent">
          {definition.kind === "group" ? "group" : definition.stage?.kind}
        </Chip>
        <Chip tone={definition.scope === "repository" ? "green" : "neutral"}>
          {scope}
        </Chip>
        {definition.overridden && <Chip tone="muted">overridden</Chip>}
        {definition.ambiguous && (
          <Chip tone="warn">defined in two global files</Chip>
        )}
        <span className="ml-auto flex flex-none flex-wrap gap-1.5">
          {canCopy && (
            <Button
              aria-label={`Copy ${label} to the repository`}
              onClick={() => onChange(copyDefinition(document, definition))}
            >
              Copy to repository
            </Button>
          )}
          {definition.scope === "repository" && (
            <Button
              variant="danger"
              aria-label={`Delete ${label}`}
              onClick={() =>
                onChange(removeDefinition(document, definition.name))
              }
            >
              Delete
            </Button>
          )}
          <Button
            aria-label={`${open ? "Close" : definition.readOnly ? "View" : "Edit"} ${label}`}
            aria-expanded={open}
            onClick={onToggle}
          >
            {open ? "Done" : definition.readOnly ? "View" : "Edit"}
          </Button>
        </span>
      </div>
      {definition.file && (
        <p className="mt-1 mb-0 font-mono text-[11px] wrap-anywhere text-muted">
          {definition.file}
        </p>
      )}
      <p className="mt-1 mb-0 text-xs text-muted">
        {definition.overridden
          ? "Replaced here by a definition with the same name in a higher scope."
          : usedBy.length
            ? `Used by ${usedBy.join(", ")}.`
            : "Not used by any flow here."}
      </p>
      {problem && (
        <p className="mt-2 mb-0 text-xs text-danger">
          Go refused this definition: {problem}
        </p>
      )}
      {open && definition.scope === "repository" && (
        <DefinitionEditor
          name={definition.name}
          value={value}
          library={library}
          agents={agents}
          remoteAgents={remoteAgents}
          onChange={(next) =>
            onChange(setDefinition(document, definition.name, next))
          }
        />
      )}
      {open && definition.scope === "global" && (
        <GlobalEditor
          definition={definition}
          value={value}
          library={library}
          agents={agents}
          remoteAgents={remoteAgents}
          onSaved={() => {
            onToggle();
            onLibraryChanged();
          }}
        />
      )}
      {open && definition.readOnly && (
        <>
          <p className="mt-3 mb-0 text-xs text-ink-soft">
            {definition.readOnly}
          </p>
          {/* A disabled fieldset makes every control read-only at once. */}
          <fieldset
            disabled
            aria-label={`${definition.name} (read-only)`}
            className="m-0 min-w-0 border-0 p-0"
          >
            <DefinitionEditor
              name={definition.name}
              value={value}
              library={library}
              agents={agents}
              remoteAgents={remoteAgents}
              onChange={() => {}}
            />
          </fieldset>
        </>
      )}
    </li>
  );
}

/** A shared stage's fields, or a group's stages. */
function DefinitionEditor({
  name,
  value,
  library,
  agents,
  remoteAgents,
  onChange,
}: {
  name: string;
  value: Value;
  library: StageLibrary | null;
  agents: string[];
  remoteAgents: string[];
  onChange(value: Value): void;
}) {
  if ("group" in value) {
    return (
      <div className="mt-3">
        <FlowEditor
          label={`Stages of group ${name}`}
          flow={value.group}
          agents={agents}
          remoteAgents={remoteAgents}
          library={library}
          exclude={[name]}
          onChange={(flow) => onChange({ group: { stages: flow.stages } })}
        />
      </div>
    );
  }
  const stage = { ...value.stage, id: name };
  return (
    <StageFields
      stage={stage}
      update={(patch) =>
        onChange({ stage: { ...value.stage, ...patch, id: "" } })
      }
      agents={agents}
      remoteAgents={remoteAgents}
      targets={[]}
      leading={
        <Field label="Kind">
          <select
            aria-label={`Kind of ${name}`}
            className={inputClass}
            value={value.stage.kind}
            onChange={(event) =>
              onChange({ stage: { ...value.stage, kind: event.target.value } })
            }
          >
            {stageKinds.map((kind) => (
              <option key={kind}>{kind}</option>
            ))}
          </select>
        </Field>
      }
    />
  );
}

/**
 * A global definition, edited in place: what it changes is said first,
 * from the registered repositories whose rules use it, and saving writes
 * its library file through Go's checks.
 */
function GlobalEditor({
  definition,
  value,
  library,
  agents,
  remoteAgents,
  onSaved,
}: {
  definition: LibraryDefinition;
  value: Value;
  library: StageLibrary | null;
  agents: string[];
  remoteAgents: string[];
  onSaved(): void;
}) {
  const [draft, setDraft] = useState<Value>(() => structuredClone(value));
  const [users, setUsers] = useState<DefinitionUser[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    globalDefinitionUsers(definition.file ?? "", definition.name)
      .then((found) => active && setUsers(found))
      .catch((reason: unknown) => active && setError(String(reason)));
    return () => {
      active = false;
    };
  }, [definition.file, definition.name]);
  async function save() {
    setBusy(true);
    setError("");
    try {
      await saveGlobalDefinition(definition.file ?? "", definition.name, draft);
      onSaved();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="mt-3 grid gap-2">
      <Notice
        role="note"
        aria-label={`Who editing ${definition.name} changes`}
        title={`Editing ${definition.name} changes it for every repository that uses it`}
        className="mt-0"
      >
        {users === null ? (
          <p className="m-0">Finding the repositories that use it…</p>
        ) : users.length === 0 ? (
          <p className="m-0">No registered repository uses it now.</p>
        ) : (
          <ul className="m-0 list-disc pl-[18px] wrap-anywhere">
            {users.map((user) => (
              <li key={user.repository}>
                <b className="text-ink">{user.project}</b>{" "}
                {user.problem
                  ? `(its rules do not load, so it may use it: ${user.problem})`
                  : `(${(user.flows ?? [])
                      .map((flow) =>
                        flow === "default" ? "the fallback flow" : flow,
                      )
                      .join(", ")})`}
              </li>
            ))}
          </ul>
        )}
        <p className="mt-2 mb-0">
          Plans already made with it are refused at approval and planned again.
        </p>
      </Notice>
      {error && <Alert flush>{error}</Alert>}
      <DefinitionEditor
        name={definition.name}
        value={draft}
        library={library}
        agents={agents}
        remoteAgents={remoteAgents}
        onChange={setDraft}
      />
      <div className="flex gap-2">
        <Button variant="primary" disabled={busy} onClick={() => void save()}>
          {busy ? "Saving…" : `Save to ${definition.file}`}
        </Button>
      </div>
    </div>
  );
}
