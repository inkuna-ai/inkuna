import { Fragment, type ReactNode } from "react";
import type { Runtime, RuleStage } from "../../types";
import { RuntimeOptions } from "../runtimes/RuntimeOptions";
import { useRuntimes } from "../runtimes/useRuntimes";
import { Field, inputClass } from "../common";
import { TagInput } from "../TagInput";
import { TrackerKindsField } from "../TrackerKindsField";
import { CommandsInput } from "./CommandsInput";
import { ExitCodesInput } from "./ExitCodesInput";
import { systemCodesSummary } from "./model";

/**
 * The fields of one stage, by its kind: an inline stage's, a shared
 * stage's, or what a use of one overrides. Each field is named as the
 * file writes it, so a use can show only those Go lets it override
 * (show) and mark each as inherited or overridden (decorate).
 */
export function StageFields({
  stage,
  update,
  agents,
  remoteAgents = [],
  targets,
  leading,
  trailing,
  show = () => true,
  decorate = (_field, node) => node,
}: {
  stage: RuleStage;
  update(patch: Partial<RuleStage>): void;
  agents: string[];
  /** The names in agents that are remote advisors: read-only, no runtime. */
  remoteAgents?: string[];
  /** Earlier agent stages feedback can go to. */
  targets: string[];
  /** Fields shown first in the grid, such as the stage id and kind. */
  leading?: ReactNode;
  /** Fields shown last in the grid, such as dependsOn. */
  trailing?: ReactNode;
  show?(field: string): boolean;
  decorate?(field: string, node: ReactNode): ReactNode;
}) {
  const runtimes = useRuntimes();
  const remote = Boolean(stage.agent && remoteAgents.includes(stage.agent));
  const field = (name: string, node: ReactNode) =>
    show(name) ? <Fragment key={name}>{decorate(name, node)}</Fragment> : null;
  return (
    <>
      <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-3">
        {leading}
        {field(
          "title",
          <Field label="Title (optional)">
            <input
              className={inputClass}
              value={stage.title ?? ""}
              onChange={(event) => update({ title: event.target.value })}
            />
          </Field>,
        )}
        {stage.kind === "agent" && (
          <>
            {field(
              "agent",
              <Field label="Agent">
                <select
                  className={inputClass}
                  value={stage.agent ?? ""}
                  onChange={(event) =>
                    update(
                      // A remote advisor only takes read-only stages and
                      // runs on no local runtime.
                      remoteAgents.includes(event.target.value)
                        ? {
                            agent: event.target.value,
                            permission: "read-only",
                            runtime: undefined,
                          }
                        : { agent: event.target.value },
                    )
                  }
                >
                  <option value="auto">auto (orchestrator staffs it)</option>
                  {stage.agent &&
                    stage.agent !== "auto" &&
                    !agents.includes(stage.agent) && (
                      <option value={stage.agent}>
                        {stage.agent} (not found)
                      </option>
                    )}
                  {agents.map((name) => (
                    <option key={name} value={name}>
                      {remoteAgents.includes(name)
                        ? `${name} (remote, read-only)`
                        : name}
                    </option>
                  ))}
                </select>
              </Field>,
            )}
            {field(
              "runtime",
              <Field label="Runtime">
                <select
                  className={inputClass}
                  value={stage.runtime ?? ""}
                  // Nothing to choose for a remote advisor; a runtime left
                  // over in the file can still be cleared.
                  disabled={remote && !stage.runtime}
                  onChange={(event) =>
                    update({
                      runtime: (event.target.value || undefined) as
                        | Runtime
                        | undefined,
                    })
                  }
                >
                  <option value="">
                    {remote
                      ? "none: a remote agent runs elsewhere"
                      : "agent or project default"}
                  </option>
                  <RuntimeOptions
                    runtimes={runtimes}
                    value={stage.runtime}
                    writeOnly={stage.permission === "worktree-write"}
                  />
                </select>
              </Field>,
            )}
            {field(
              "permission",
              <Field label="Permission">
                <select
                  className={inputClass}
                  value={stage.permission ?? ""}
                  // Fixed for a remote advisor; a value from the file that
                  // breaks the contract can still be corrected.
                  disabled={remote && stage.permission === "read-only"}
                  onChange={(event) =>
                    update({ permission: event.target.value || undefined })
                  }
                >
                  <option value="">agent default</option>
                  <option>read-only</option>
                  <option disabled={remote}>worktree-write</option>
                </select>
              </Field>,
            )}
            {field(
              "skills",
              <Field label="Skills (comma separated)">
                <TagInput
                  label={`Skills for ${stage.id}`}
                  values={stage.skills}
                  onChange={(skills) => update({ skills })}
                />
              </Field>,
            )}
            {field(
              "mcp",
              <Field label="MCP servers (comma separated, declared in inkuna.yaml)">
                <TagInput
                  label={`MCP servers for ${stage.id}`}
                  values={stage.mcp}
                  onChange={(mcp) => update({ mcp })}
                />
              </Field>,
            )}
            {field(
              "tracker",
              <TrackerKindsField
                label={`Tracker changes ${stage.id} may ask for (needs a review gate after it)`}
                values={stage.tracker}
                onChange={(tracker) => update({ tracker })}
              />,
            )}
            {field(
              "allowedPaths",
              <Field label="Allowed paths (comma separated)">
                <TagInput
                  label={`Allowed paths for ${stage.id}`}
                  values={stage.allowedPaths}
                  placeholder="."
                  onChange={(allowedPaths) => update({ allowedPaths })}
                />
              </Field>,
            )}
          </>
        )}
        {trailing}
      </div>
      {(stage.kind === "agent" || stage.kind === "human-input") &&
        field(
          "prompt",
          <Field
            label={
              stage.kind === "human-input"
                ? "Question for the user (required)"
                : "Extra instructions (optional)"
            }
          >
            <textarea
              className={`${inputClass} min-h-20 resize-y`}
              value={stage.prompt ?? ""}
              onChange={(event) => update({ prompt: event.target.value })}
            />
          </Field>,
        )}
      {stage.kind === "agent" &&
        field(
          "commands",
          <Field label="Commands its worker may run without asking: one per line, plain words, each allowed exactly as written">
            <CommandsInput
              label={`Commands for ${stage.id}`}
              commands={stage.commands}
              onChange={(commands) => update({ commands })}
            />
          </Field>,
        )}
      {(stage.kind === "script" || stage.kind === "evaluation") &&
        field(
          "commands",
          <Field label='Commands: one per line, no shell; quote arguments with spaces ("like this"), escape quotes as \" and write "" for an empty argument'>
            <CommandsInput
              label={`Commands for ${stage.id}`}
              commands={stage.commands}
              onChange={(commands) => update({ commands })}
            />
          </Field>,
        )}
      {stage.kind === "evaluation" &&
        field(
          "continueOnFailure",
          <label className="flex items-center gap-1.5 text-xs text-ink-soft">
            <input
              type="checkbox"
              checked={stage.continueOnFailure ?? false}
              onChange={(event) =>
                update({ continueOnFailure: event.target.checked })
              }
            />
            Run every command, even after one fails (the stage still fails)
          </label>,
        )}
      {stage.kind === "evaluation" && (
        <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-3">
          {field(
            "onFailure",
            <Field label="When a command fails">
              <select
                className={inputClass}
                value={stage.onFailure ?? "fail"}
                onChange={(event) =>
                  update(
                    event.target.value === "feedback"
                      ? { onFailure: "feedback" }
                      : { onFailure: undefined, feedback: undefined },
                  )
                }
              >
                <option value="fail">Fail the run</option>
                <option value="feedback">
                  Send the failures back as feedback
                </option>
              </select>
            </Field>,
          )}
          {stage.onFailure === "feedback" &&
            field(
              "feedback",
              <>
                <Field label="Send failures to">
                  <TargetSelect
                    value={stage.feedback?.target}
                    candidates={targets}
                    onChange={(target) =>
                      update({ feedback: { ...stage.feedback, target } })
                    }
                  />
                </Field>
                <Field label="Automatic rounds before the run fails">
                  <input
                    className={inputClass}
                    type="number"
                    placeholder="2"
                    value={stage.feedback?.maxRounds ?? ""}
                    onChange={(event) =>
                      update({
                        feedback: {
                          ...stage.feedback,
                          maxRounds:
                            event.target.value === ""
                              ? undefined
                              : Number(event.target.value),
                        },
                      })
                    }
                  />
                </Field>
                <Field label="Exit codes that pause the run for you">
                  <ExitCodesInput
                    label="Exit codes that pause the run for you"
                    codes={stage.feedback?.systemExitCodes}
                    onChange={(codes) =>
                      update({
                        feedback: {
                          ...stage.feedback,
                          systemExitCodes: codes.length ? codes : undefined,
                        },
                      })
                    }
                  />
                  <small className="text-[11px] text-muted">
                    {stage.feedback?.systemExitCodes?.length
                      ? `${systemCodesSummary(stage.feedback.systemExitCodes)}, as does a check that could not start or was stopped`
                      : "None: every failure goes back to the writer"}
                  </small>
                </Field>
              </>,
            )}
        </div>
      )}
      {stage.kind === "approval" && (
        <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-3">
          {field(
            "review",
            <label className="flex items-center gap-1.5 text-xs text-ink-soft">
              <input
                type="checkbox"
                checked={stage.review ?? false}
                onChange={(event) => update({ review: event.target.checked })}
              />
              Pause for review: approve or request changes
            </label>,
          )}
          {field(
            "feedback",
            <Field label="Send feedback to">
              <TargetSelect
                value={stage.feedback?.target}
                candidates={targets}
                onChange={(target) =>
                  update({ feedback: { ...stage.feedback, target } })
                }
              />
            </Field>,
          )}
        </div>
      )}
      {stage.kind !== "approval" &&
        field(
          "optional",
          <label className="flex items-center gap-1.5 text-xs text-ink-soft">
            <input
              type="checkbox"
              checked={stage.optional ?? false}
              onChange={(event) => update({ optional: event.target.checked })}
            />
            Optional stage (recorded on the planned step)
          </label>,
        )}
    </>
  );
}

function TargetSelect({
  value,
  candidates,
  onChange,
}: {
  value?: string;
  candidates: string[];
  onChange(target: string | undefined): void;
}) {
  return (
    <select
      className={inputClass}
      value={value ?? ""}
      onChange={(event) => onChange(event.target.value || undefined)}
    >
      <option value="">nearest writable agent stage upstream</option>
      {value && !candidates.includes(value) && (
        <option value={value}>{value} (not an earlier agent stage)</option>
      )}
      {candidates.map((id) => (
        <option key={id} value={id}>
          {id}
        </option>
      ))}
    </select>
  );
}
