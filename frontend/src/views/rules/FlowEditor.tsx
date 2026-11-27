import { useEffect, useRef, useState, type ReactNode } from "react";
import type { RuleStage, StageLibrary } from "../../types";
import { Alert, Button, Chip, Field, inputClass } from "../common";
import { TagInput } from "../TagInput";
import {
  insertUse,
  isOverridden,
  move,
  newStage,
  overrideFields,
  overrides,
  removeStage,
  renameStage,
  resetField,
  resolveDefinition,
  scopeLabels,
  stageKinds,
  stageName,
  uniqueName,
  usedStage,
  type Flow,
} from "./model";
import { StageChain } from "./RuleSummary";
import { StageFields } from "./StageFields";

/**
 * Ordered stages. Order matters twice: a stage may only depend on an earlier
 * one, and the last stage must be an approval. Go enforces both on save.
 * An entry is an inline stage or a use of a shared stage or group, which
 * shows what it inherits and edits only what it overrides.
 */
export function FlowEditor({
  label,
  flow,
  agents,
  remoteAgents = [],
  onChange,
  library = null,
  exclude = [],
  takenNames = [],
  onExtract,
  onPreview,
  previewOf,
  problem,
}: {
  label: string;
  flow: Flow;
  agents: string[];
  /** The names in agents that are remote advisors: read-only, no runtime. */
  remoteAgents?: string[];
  onChange(flow: Flow): void;
  /** The shared stages and groups a use can name, as Go listed them. */
  library?: StageLibrary | null;
  /** Names not offered for insertion, such as the group being edited. */
  exclude?: string[];
  /** Names a stage extracted to a shared stage may not take. */
  takenNames?: string[];
  /** Turns the inline stage at index into a shared stage called name. */
  onExtract?(index: number, name: string): void;
  /** The flow as planning sees it, expanded by Go. */
  onPreview?(): Promise<RuleStage[]>;
  /** The document the preview expands, by identity: any edit to it, in
   * this flow or elsewhere, makes a preview of it out of date. */
  previewOf?: unknown;
  /** The entry Go refused, with its reason. */
  problem?: { entry: number; message: string } | null;
}) {
  const ids = flow.stages.map(stageName);
  const [inserting, setInserting] = useState("");
  // Each preview, or its failure, records the document it was made from,
  // and is shown only while that document is still the one being edited.
  const [shown, setShown] = useState<{
    of: unknown;
    stages?: RuleStage[];
    error?: string;
  } | null>(null);
  const current = useRef(previewOf);
  useEffect(() => {
    current.current = previewOf;
  }, [previewOf]);
  const fresh = shown && shown.of === previewOf ? shown : null;
  const preview = fresh?.stages ?? null;
  const previewError = fresh?.error ?? "";
  const insertable = (library?.definitions ?? []).filter(
    (definition) =>
      !definition.overridden &&
      !definition.ambiguous &&
      !exclude.includes(definition.name),
  );
  function update(index: number, stage: RuleStage) {
    onChange({
      stages: flow.stages.map((entry, position) =>
        position === index ? stage : entry,
      ),
    });
  }
  // Feedback can only go upstream, so earlier agent stages are the ones to
  // offer; Go checks which of them is upstream and can write.
  function agentsBefore(index: number) {
    return flow.stages
      .slice(0, index)
      .filter((stage) => stage.kind === "agent")
      .map((stage) => stage.id);
  }
  function add() {
    // New stages go before the closing approval so the flow stays valid.
    const last = flow.stages[flow.stages.length - 1];
    const at =
      last?.kind === "approval" ? flow.stages.length - 1 : flow.stages.length;
    const stages = [...flow.stages];
    stages.splice(at, 0, newStage(ids));
    onChange({ stages });
  }
  async function showPreview() {
    if (!onPreview) return;
    const of = previewOf;
    setShown(null);
    let next: { of: unknown; stages?: RuleStage[]; error?: string };
    try {
      next = { of, stages: await onPreview() };
    } catch (reason) {
      next = { of, error: String(reason) };
    }
    // A result for a document edited since would show what it no longer
    // expands to, and must not replace one made for the current one.
    if (current.current === of) setShown(next);
  }
  return (
    <div className="grid min-w-0 gap-3" role="group" aria-label={label}>
      <ol className="m-0 grid min-w-0 list-none gap-3 p-0">
        {flow.stages.map((stage, index) => {
          const name = stageName(stage);
          const refused = problem?.entry === index;
          const problemID = `${label}-problem-${index}`.replace(/\W+/g, "-");
          return (
            <li
              key={index}
              aria-label={`Stage ${name || index + 1}`}
              aria-invalid={refused || undefined}
              aria-describedby={refused ? problemID : undefined}
              className={`min-w-0 border bg-field p-3.5 ${refused ? "border-danger" : "border-line"}`}
            >
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <Chip tone="accent">{index + 1}</Chip>
                <b className="min-w-0 text-xs wrap-anywhere">
                  {name || "(no id)"}
                </b>
                {stage.use && <Chip>uses {stage.use}</Chip>}
                <span className="ml-auto flex flex-none flex-wrap gap-1.5">
                  {!stage.use && onExtract && (
                    <ExtractButton
                      stage={stage}
                      taken={takenNames}
                      onExtract={(shared) => onExtract(index, shared)}
                    />
                  )}
                  <Button
                    aria-label={`Move stage ${name} earlier`}
                    disabled={index === 0}
                    onClick={() =>
                      onChange({ stages: move(flow.stages, index, index - 1) })
                    }
                  >
                    ↑
                  </Button>
                  <Button
                    aria-label={`Move stage ${name} later`}
                    disabled={index === flow.stages.length - 1}
                    onClick={() =>
                      onChange({ stages: move(flow.stages, index, index + 1) })
                    }
                  >
                    ↓
                  </Button>
                  <Button
                    variant="danger"
                    aria-label={`Remove stage ${name}`}
                    disabled={flow.stages.length === 1}
                    onClick={() => onChange(removeStage(flow, index))}
                  >
                    Remove
                  </Button>
                </span>
              </div>
              {refused && (
                <p id={problemID} className="mt-2 mb-0 text-xs text-danger">
                  Go refused this entry: {problem.message}
                </p>
              )}
              {stage.use ? (
                <UseEditor
                  use={stage}
                  library={library}
                  agents={agents}
                  remoteAgents={remoteAgents}
                  targets={agentsBefore(index)}
                  dependencies={ids.slice(0, index)}
                  onRename={(id) => onChange(renameStage(flow, index, id))}
                  onChange={(next) => update(index, next)}
                />
              ) : (
                <StageFields
                  stage={stage}
                  update={(patch) => update(index, { ...stage, ...patch })}
                  agents={agents}
                  remoteAgents={remoteAgents}
                  targets={agentsBefore(index)}
                  leading={
                    <>
                      <Field label="Stage id">
                        <input
                          className={inputClass}
                          value={stage.id}
                          onChange={(event) =>
                            onChange(
                              renameStage(flow, index, event.target.value),
                            )
                          }
                        />
                      </Field>
                      <Field label="Kind">
                        <select
                          className={inputClass}
                          value={stage.kind}
                          onChange={(event) =>
                            update(index, {
                              ...stage,
                              kind: event.target.value,
                            })
                          }
                        >
                          {stageKinds.map((kind) => (
                            <option key={kind}>{kind}</option>
                          ))}
                        </select>
                      </Field>
                    </>
                  }
                  trailing={
                    <DependsOn
                      stage={stage}
                      earlier={ids.slice(0, index)}
                      onChange={(dependsOn) =>
                        update(index, { ...stage, dependsOn })
                      }
                    />
                  }
                />
              )}
            </li>
          );
        })}
      </ol>
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <Button onClick={add}>+ Add stage</Button>
        {insertable.length > 0 && (
          <>
            <select
              aria-label={`Shared stage or group to insert into ${label}`}
              className={`${inputClass} w-auto max-w-full`}
              value={inserting}
              onChange={(event) => setInserting(event.target.value)}
            >
              <option value="">Shared stage or group…</option>
              {insertable.map((definition) => (
                <option key={definition.name} value={definition.name}>
                  {definition.name} (
                  {definition.kind === "group"
                    ? "group"
                    : definition.stage?.kind}
                  , {scopeLabels[definition.scope]})
                </option>
              ))}
            </select>
            <Button
              disabled={!inserting}
              onClick={() => {
                onChange(insertUse(flow, inserting));
                setInserting("");
              }}
            >
              Insert
            </Button>
          </>
        )}
        {onPreview && (
          <Button className="ml-auto" onClick={() => void showPreview()}>
            Preview as planned
          </Button>
        )}
      </div>
      {previewError && <Alert flush>{previewError}</Alert>}
      {preview && (
        <section
          aria-label={`${label} as planned`}
          className="grid gap-2 border border-line p-3"
        >
          <div className="flex items-center gap-2">
            <b className="text-xs">As planned, with every use expanded</b>
            <Button
              className="ml-auto"
              variant="ghost"
              onClick={() => setShown(null)}
            >
              Hide
            </Button>
          </div>
          <StageChain stages={preview} />
        </section>
      )}
    </div>
  );
}

function DependsOn({
  stage,
  earlier,
  onChange,
}: {
  stage: RuleStage;
  earlier: string[];
  onChange(dependsOn: string[]): void;
}) {
  return (
    <Field label="Depends on (earlier stages)">
      <TagInput
        label={`Dependencies of ${stageName(stage)}`}
        values={stage.dependsOn}
        placeholder={earlier.join(", ") || "none"}
        onChange={onChange}
      />
    </Field>
  );
}

/**
 * A use of a shared stage or group. Its id and dependsOn wire it in; on a
 * shared stage each field Go lets a use set shows the definition's value
 * until the use overrides it, and can be reset to that value.
 */
function UseEditor({
  use,
  library,
  agents,
  remoteAgents,
  targets,
  dependencies,
  onRename,
  onChange,
}: {
  use: RuleStage;
  library: StageLibrary | null;
  agents: string[];
  remoteAgents: string[];
  targets: string[];
  dependencies: string[];
  onRename(id: string): void;
  onChange(use: RuleStage): void;
}) {
  const name = use.use ?? "";
  const definition = resolveDefinition(library, name);
  const wiring = (
    <>
      <Field label="Stage id">
        <input
          className={inputClass}
          value={use.id}
          placeholder={name}
          onChange={(event) => onRename(event.target.value)}
        />
      </Field>
      <DependsOn
        stage={use}
        earlier={dependencies}
        onChange={(dependsOn) => onChange({ ...use, dependsOn })}
      />
    </>
  );
  const grid = (children: ReactNode) => (
    <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-3">
      {children}
    </div>
  );
  // Until Go has listed the library there is nothing to show inherited.
  if (!library) return grid(wiring);
  if (!definition) {
    return (
      <>
        {grid(wiring)}
        <p className="m-0 text-xs text-warn">
          No shared stage or group named {name} is in effect here, so Go will
          refuse the save until one is defined or this use is removed.
        </p>
      </>
    );
  }
  const origin = (
    <p className="mt-2 mb-0 text-xs text-muted">
      Uses the {scopeLabels[definition.scope]}{" "}
      {definition.kind === "group" ? "group" : "shared stage"}{" "}
      <b className="text-ink">{name}</b>
      {definition.file && (
        <>
          {" from "}
          <span className="font-mono wrap-anywhere">{definition.file}</span>
        </>
      )}
      .
    </p>
  );
  if (definition.group) {
    return (
      <>
        {origin}
        {grid(wiring)}
        <p className="m-0 mb-2 text-xs text-muted">
          Its stages arrive wired among themselves, and dependsOn goes on the
          ones that depend on nothing in it. A use of a group overrides nothing.
        </p>
        <StageChain stages={definition.group.stages} />
      </>
    );
  }
  const base = definition.stage ?? { id: "", kind: "" };
  const names = (library.overridable ?? [])
    .filter((field) => overrides(field, base.kind))
    .map((field) => field.name);
  return (
    <>
      {origin}
      <StageFields
        stage={usedStage(use, base)}
        update={(patch) => onChange(overrideFields(use, patch, names))}
        agents={agents}
        remoteAgents={remoteAgents}
        targets={targets}
        leading={wiring}
        show={(field) => names.includes(field)}
        decorate={(field, node) => (
          <Overridable
            field={field}
            overridden={isOverridden(use, field)}
            onReset={() => onChange(resetField(use, field))}
          >
            {node}
          </Overridable>
        )}
      />
    </>
  );
}

/** One field of a use: inherited from its shared stage, or overridden
 * for this use, with a way back to the inherited value. */
function Overridable({
  field,
  overridden,
  onReset,
  children,
}: {
  field: string;
  overridden: boolean;
  onReset(): void;
  children: ReactNode;
}) {
  return (
    <div
      className={`min-w-0 border-l-2 pl-2.5 ${overridden ? "border-accent" : "border-transparent"}`}
    >
      {children}
      <div className="mb-2 flex items-center gap-2 text-[11px]">
        {overridden ? (
          <>
            <Chip tone="accent">{field} overridden</Chip>
            <button
              type="button"
              aria-label={`Reset ${field} to inherited`}
              className="cursor-pointer border-0 bg-transparent p-0 text-accent hover:underline"
              onClick={onReset}
            >
              Reset to inherited
            </button>
          </>
        ) : (
          <Chip tone="muted">{field} inherited</Chip>
        )}
      </div>
    </div>
  );
}

/** Extract to shared stage: asks for the shared stage's name first. */
function ExtractButton({
  stage,
  taken,
  onExtract,
}: {
  stage: RuleStage;
  taken: string[];
  onExtract(name: string): void;
}) {
  const [name, setName] = useState<string | null>(null);
  if (name === null) {
    return (
      <Button
        aria-label={`Extract stage ${stage.id} to a shared stage`}
        onClick={() => setName(uniqueName(stage.id || "stage", taken))}
      >
        Extract to shared stage
      </Button>
    );
  }
  // A name already in use would change what its uses mean, so it is not
  // offered; the view knows its own names.
  const clash = taken.includes(name.trim());
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <input
        aria-label={`Shared stage name for ${stage.id}`}
        className={`${inputClass} w-40 p-1.5`}
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      <Button
        variant="primary"
        disabled={!name.trim() || clash}
        title={
          clash
            ? `${name.trim()} already names a shared stage or group`
            : undefined
        }
        onClick={() => {
          onExtract(name.trim());
          setName(null);
        }}
      >
        Extract
      </Button>
      <Button variant="ghost" onClick={() => setName(null)}>
        Cancel
      </Button>
    </span>
  );
}
