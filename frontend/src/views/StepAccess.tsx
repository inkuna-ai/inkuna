import { useEffect, useState } from "react";
import type { AccessEdit, WorkflowNode } from "../types";
import { formatCommands, parseCommands } from "./rules/model";

// Compact variant of the shared input look for the narrow inspector column.
export const compactInputClass =
  "w-full min-w-0 rounded border border-line-strong bg-field p-1.5 font-mono text-xs text-ink-strong disabled:opacity-60";
export const routeLabel = "grid min-w-0 gap-1 text-[10px] text-muted";

/** What the approval screen may change about what a step may do. */
export interface AccessPatch {
  permission?: string;
  commands?: string[][];
  allowedPaths?: string[];
}

const splitPaths = (text: string) =>
  text
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
const joinPaths = (paths: string[]) => paths.join(", ");

/**
 * Text that is sent to Go when it loses focus, not on every keystroke, so a
 * half-typed command is never checked. The text follows the step whenever
 * Go's value changes to something it does not already say (a revision, a
 * new plan); a refused edit stays as typed, beside Go's reason, to be fixed.
 */
function DraftText<T>({
  label,
  value,
  parse,
  format,
  onCommit,
  disabled,
  multiline,
  placeholder,
}: {
  label: string;
  value: T;
  parse(text: string): T;
  format(value: T): string;
  onCommit(value: T): void;
  disabled: boolean;
  multiline?: boolean;
  placeholder?: string;
}) {
  const formatted = format(value);
  const [text, setText] = useState(formatted);
  useEffect(() => {
    setText((current) =>
      format(parse(current)) === formatted ? current : formatted,
    );
  }, [formatted, format, parse]);
  const props = {
    "aria-label": label,
    className: compactInputClass,
    value: text,
    disabled,
    placeholder,
    onBlur: () => {
      if (format(parse(text)) !== formatted) onCommit(parse(text));
    },
  };
  return multiline ? (
    <textarea
      {...props}
      className={`${compactInputClass} min-h-14 resize-y`}
      onChange={(event) => setText(event.target.value)}
    />
  ) : (
    <input {...props} onChange={(event) => setText(event.target.value)} />
  );
}

function CommandsText({
  label,
  commands,
  onCommit,
  disabled,
}: {
  label: string;
  commands: string[][] | undefined;
  onCommit(commands: string[][]): void;
  disabled: boolean;
}) {
  return (
    <DraftText
      label={label}
      value={commands ?? []}
      parse={parseCommands}
      format={formatCommands}
      onCommit={onCommit}
      disabled={disabled}
      multiline
      placeholder="none"
    />
  );
}

/**
 * Go's notes on what approval changed about a step's access. A change that
 * lets the step do more than its rule did is a warning; any other is a
 * plain note, so whoever reads the plan later sees the run left its rule.
 */
function AccessNotes({ edits }: { edits: AccessEdit[] | undefined }) {
  if (!edits?.length) return null;
  const widening = edits.filter((edit) => edit.widens);
  const other = edits.filter((edit) => !edit.widens);
  return (
    <>
      {widening.length > 0 && (
        <section
          aria-label="Widened access"
          className="grid min-w-0 gap-1 border-l-2 border-warn-line bg-raised p-2 text-[11px] text-warn"
        >
          <b className="font-semibold">
            Widens what this step may do beyond its rule
          </b>
          {widening.map((edit) => (
            <span className="wrap-anywhere" key={edit.field}>
              {edit.note}
            </span>
          ))}
        </section>
      )}
      {other.map((edit) => (
        <p
          className="m-0 text-[11px] wrap-anywhere text-muted"
          key={edit.field}
        >
          {edit.note}
        </p>
      ))}
    </>
  );
}

/**
 * An agent step's access on the approval screen: its permission, the
 * commands its worker may run without asking, and, when it writes, the
 * paths it keeps its writes to. Go checks every edit as it checks a rule's
 * stage and says what changed.
 */
export function StepAccess({
  step,
  remote,
  onEdit,
  disabled,
}: {
  step: WorkflowNode;
  /** A remote advisor, which Go keeps read-only. */
  remote: boolean;
  onEdit(patch: AccessPatch): void;
  disabled: boolean;
}) {
  return (
    <>
      <label className={routeLabel}>
        Permission
        <select
          className={compactInputClass}
          aria-label={`Permission for ${step.title}`}
          value={step.permission}
          disabled={disabled || remote}
          title={remote ? "A remote agent is a read-only advisor" : undefined}
          onChange={(event) => onEdit({ permission: event.target.value })}
        >
          <option value="read-only">read-only</option>
          <option value="worktree-write">worktree-write</option>
        </select>
      </label>
      <label className={routeLabel}>
        Commands it may run without asking, one per line
        <CommandsText
          label={`Commands for ${step.title}`}
          commands={step.commands}
          disabled={disabled}
          onCommit={(commands) => onEdit({ commands })}
        />
      </label>
      {step.permission === "worktree-write" && (
        <label className={routeLabel}>
          Allowed paths (comma separated)
          <DraftText
            label={`Allowed paths for ${step.title}`}
            value={step.allowedPaths ?? []}
            parse={splitPaths}
            format={joinPaths}
            onCommit={(allowedPaths) => onEdit({ allowedPaths })}
            disabled={disabled}
            placeholder="."
          />
        </label>
      )}
      <AccessNotes edits={step.accessEdits} />
    </>
  );
}

/** An evaluation step's commands on the approval screen. */
export function StepChecks({
  step,
  onEdit,
  disabled,
}: {
  step: WorkflowNode;
  onEdit(patch: AccessPatch): void;
  disabled: boolean;
}) {
  return (
    <div className="grid min-w-0 gap-2 border-t border-line pt-3">
      <div className="flex min-w-0 items-baseline justify-between gap-2">
        <b className="min-w-0 text-[11px] wrap-anywhere">{step.title}</b>
        <small className="shrink-0 font-mono text-[9px] text-muted uppercase">
          checks
        </small>
      </div>
      <label className={routeLabel}>
        Commands, one per line, run in order
        <CommandsText
          label={`Commands for ${step.title}`}
          commands={step.commands}
          disabled={disabled}
          onCommit={(commands) => onEdit({ commands })}
        />
      </label>
      <AccessNotes edits={step.accessEdits} />
    </div>
  );
}
