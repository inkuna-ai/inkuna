import { useEffect, useState } from "react";
import { getRunContext } from "../../bridge";
import { useUsageVersion } from "../../events";
import { formatTime, pretty } from "../../format";
import {
  isTerminal,
  type ContextEnvelope,
  type DocBrief,
  type FeedbackRound,
  type StepContext,
  type StepDocRead,
  type WorkflowRun,
} from "../../types";
import { Chip, Code, Field, inputClass } from "../common";
import { ServerList, UserServerList } from "../ServerList";
import { docStatusTones } from "../docs/tree";
import {
  CheckTail,
  CommentExcerpt,
  CommentPlace,
} from "../messages/Conversation";
import { truncatedPartLabel } from "../usage";
import { PromptBreakdown } from "./PromptBreakdown";
import { InlineAlert, SubHeading, Summary, TabBody } from "./shared";

const trackerLabels: Record<string, string> = {
  linear: "Linear",
  github: "GitHub issues",
  jira: "Jira",
};

function trackerLabel(tracker: string): string {
  return trackerLabels[tracker] ?? tracker;
}

export function ContextTab({ run }: { run: WorkflowRun }) {
  const agentSteps = (run.steps ?? []).filter((step) => step.kind === "agent");
  const [selectedStepID, setStepID] = useState(agentSteps[0]?.id ?? "");
  // Steps can arrive after the tab opens, and a selected step can disappear.
  // The effective selection is derived so it is always a step that exists.
  const stepID = agentSteps.some((step) => step.id === selectedStepID)
    ? selectedStepID
    : (agentSteps[0]?.id ?? "");
  // Results and errors remember the step they belong to: a failed refresh
  // keeps the last good context for that step, and another step's context is
  // never shown under the wrong selection.
  const [loaded, setLoaded] = useState<{
    stepID: string;
    context: StepContext;
  } | null>(null);
  const [failure, setFailure] = useState<{
    stepID: string;
    message: string;
  } | null>(null);
  // An active run is refreshed as it goes, and each refresh can bring the
  // step's context and new reads; a finished run is fetched once and left.
  const activeRun = isTerminal(run.state) ? null : run;
  // A step's call can be stored after its run ends, so a new usage row is
  // also a reason to fetch again.
  const stored = useUsageVersion(run.id);
  useEffect(() => {
    if (!stepID) return;
    let active = true;
    getRunContext(run.id, stepID)
      .then((value) => {
        if (active) {
          setLoaded({ stepID, context: value });
          setFailure(null);
        }
      })
      .catch((reason: unknown) => {
        if (active) setFailure({ stepID, message: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [run.id, stepID, run.state, activeRun, stored]);
  if (agentSteps.length === 0) {
    return (
      <p className="mt-5 mb-0 text-sm text-muted">
        This run has no agent steps.
      </p>
    );
  }
  const context = loaded?.stepID === stepID ? loaded.context : null;
  const error = failure?.stepID === stepID ? failure.message : "";
  return (
    <TabBody>
      <div className="max-w-[420px] min-w-0">
        <Field label="Step" flush>
          <select
            aria-label="Context step"
            className={inputClass}
            value={stepID}
            onChange={(event) => setStepID(event.target.value)}
          >
            {agentSteps.map((step) => (
              <option value={step.id} key={step.id}>
                {step.id} · {step.agent ?? "agent"}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {error && <InlineAlert>{error}</InlineAlert>}
      {context && (
        <>
          <Summary
            label={<>Agent {context.agent}</>}
            meta={
              <>
                runtime {context.envelope.runtime} ·{" "}
                {context.envelope.acceptanceCriteria?.length ?? 0} acceptance
                criteria
                {context.envelope.constraints?.length
                  ? ` · ${context.envelope.constraints.length} constraints`
                  : ""}
                {context.envelope.humanResponses?.length
                  ? ` · ${context.envelope.humanResponses.length} human answers`
                  : ""}
                {context.envelope.peerMessages?.length
                  ? ` · ${context.envelope.peerMessages.length} peer messages`
                  : ""}
                {` · ${context.envelope.docs?.length ?? 0} docs`}
                {context.envelope.mcpServers?.length
                  ? ` · ${context.envelope.mcpServers.length} MCP servers`
                  : ""}
                {context.envelope.userMcpServers?.length
                  ? ` · ${context.envelope.userMcpServers.length} of your MCP servers`
                  : ""}
                {context.envelope.tracker ? " · tracker changes" : ""}
              </>
            }
          >
            {context.envelope.objective}
          </Summary>
          <StageBrief envelope={context.envelope} />
          {context.envelope.mcpServers?.length ||
          context.envelope.userMcpServers?.length ? (
            <div className="grid min-w-0 gap-3 border-l-2 border-line-strong bg-field p-3.5">
              <ServerList
                servers={context.envelope.mcpServers ?? []}
                readOnly={context.envelope.metadata?.permission === "read-only"}
              />
              <UserServerList servers={context.envelope.userMcpServers ?? []} />
            </div>
          ) : null}
          {context.envelope.tracker && (
            <p
              aria-label="Tracker changes"
              className="m-0 border-l-2 border-line-strong bg-field p-3.5 text-xs wrap-anywhere text-ink-soft"
            >
              The step could ask for {context.envelope.tracker.kinds.join(", ")}{" "}
              changes in {trackerLabel(context.envelope.tracker.tracker)}
              {context.envelope.tracker.story
                ? `, naming its story ${context.envelope.tracker.story} as "story"`
                : ", and the run has no story"}
              . Inkuna checks each change when the step ends and makes it only
              once the run is approved at its review gate.
            </p>
          )}
          {context.envelope.metadata?.truncated && (
            <p className="m-0 text-[11px] wrap-anywhere text-warn">
              Cut to fit the budgets:{" "}
              {context.envelope.metadata.truncated
                .split(",")
                .map(truncatedPartLabel)
                .join(", ")}
              . A cut part ends with a note saying how much was left out and
              where the full text is; a later turn&apos;s cuts show on its call
              below.
            </p>
          )}
          {context.handoff && (
            <div className="min-w-0 border-l-2 border-ok bg-field p-3.5">
              <h3 className="mt-0 mb-1.5 text-[13px] font-semibold wrap-anywhere text-ok">
                Handoff · {context.handoff.status}
              </h3>
              <p className="m-0 text-xs leading-relaxed wrap-anywhere whitespace-pre-wrap">
                {context.handoff.summary}
              </p>
              {context.handoff.decisions?.length ? (
                <HandoffList
                  title="Decisions"
                  items={context.handoff.decisions}
                />
              ) : null}
              {context.handoff.risks?.length ? (
                <HandoffList title="Risks" items={context.handoff.risks} />
              ) : null}
            </div>
          )}
          <FeedbackRounds rounds={context.feedback ?? []} />
          {/* Keyed by step so a call chosen for one step never carries over. */}
          <PromptBreakdown key={stepID} calls={context.usage ?? []} />
          <DocIndex
            docs={context.envelope.docs ?? []}
            truncated={context.envelope.metadata?.docsTruncated === "true"}
          />
          <DocsRead
            reads={context.docReads ?? []}
            delivered={context.envelope.docs?.length ?? 0}
          />
          <SubHeading>Context envelope</SubHeading>
          <p className="m-0 text-[11px] wrap-anywhere text-muted">
            Inkuna&apos;s record of the step as it started. The prompt sends
            only what the agent can use.
          </p>
          <Code>{pretty(context.envelope)}</Code>
          {context.prompt && (
            <>
              <SubHeading>Sent to the remote agent</SubHeading>
              <Code>{context.prompt}</Code>
            </>
          )}
        </>
      )}
    </TabBody>
  );
}

/**
 * What the worker was sent after the objective: its stage in the
 * repository's rules, which narrows the objective to this step, and the
 * limits the user set on the whole task.
 */
function StageBrief({ envelope }: { envelope: ContextEnvelope }) {
  const constraints = envelope.constraints ?? [];
  const stage = Boolean(envelope.stageTitle || envelope.stagePrompt);
  if (!stage && constraints.length === 0) return null;
  return (
    <div className="grid min-w-0 gap-1.5 border-l-2 border-accent bg-field p-3.5">
      {stage && (
        <section aria-label="Stage" className="grid min-w-0 gap-1">
          <h3 className="m-0 text-[13px] font-semibold wrap-anywhere text-accent">
            Stage{envelope.stageTitle ? ` · ${envelope.stageTitle}` : ""}
          </h3>
          <p className="m-0 text-xs leading-relaxed wrap-anywhere whitespace-pre-wrap">
            {envelope.stagePrompt || (
              <span className="text-muted">
                The rules give this stage no instructions.
              </span>
            )}
          </p>
        </section>
      )}
      {constraints.length > 0 && (
        <section aria-label="Constraints" className="min-w-0">
          <h4 className="mt-0 mb-1 text-[11px] font-semibold text-muted">
            Constraints
          </h4>
          <ul className="m-0 list-disc pl-5 text-xs leading-relaxed wrap-anywhere">
            {constraints.map((constraint, index) => (
              // Two constraints can say the same thing.
              <li key={index}>{constraint}</li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

function HandoffList({ title, items }: { title: string; items: string[] }) {
  return (
    <>
      <h4 className="mt-3 mb-1 text-[11px] font-semibold text-muted">
        {title}
      </h4>
      <ul className="m-0 list-disc pl-5 text-xs leading-relaxed wrap-anywhere">
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </>
  );
}

const feedbackTones = {
  open: "muted",
  fixed: "green",
  declined: "warn",
  question: "accent",
  "no-reply": "error",
} as const;

/**
 * What each round sent the step back with, newest first: each item as the
 * step received it, and its reply as Go recorded it.
 */
function FeedbackRounds({ rounds }: { rounds: FeedbackRound[] }) {
  if (rounds.length === 0) return null;
  return (
    <>
      <SubHeading>Feedback</SubHeading>
      {[...rounds].reverse().map((round) => (
        <section
          key={round.round}
          aria-label={`Feedback for round ${round.round}`}
          className="grid min-w-0 gap-px"
        >
          <h4 className="m-0 mb-1 text-[11px] font-semibold text-muted">
            Round {round.round}
          </h4>
          <ul className="m-0 grid min-w-0 list-none gap-px p-0">
            {round.items.map((item) => (
              <li
                key={item.id}
                className="grid min-w-0 gap-1 bg-field px-3 py-2"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="font-mono text-[11px] text-ink-soft">
                    {item.id}
                  </span>
                  <small className="text-[10px] text-muted">
                    from {item.from === "check" ? "a check" : "the user"}
                  </small>
                  <span className="ml-auto">
                    <Chip
                      tone={
                        feedbackTones[
                          item.status as keyof typeof feedbackTones
                        ] ?? "neutral"
                      }
                    >
                      {item.status === "no-reply" ? "no reply" : item.status}
                    </Chip>
                  </span>
                </div>
                <p className="m-0 text-xs leading-relaxed wrap-anywhere whitespace-pre-wrap">
                  <CommentPlace item={item} />
                  {item.text}
                </p>
                <CheckTail item={item} />
                <CommentExcerpt item={item} />
                {item.note && (
                  <p className="m-0 border-l-2 border-line-strong pl-2 text-[11px] leading-relaxed wrap-anywhere whitespace-pre-wrap text-ink-soft">
                    {item.note}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </>
  );
}

/** The doc index the step received, as Go delivered and cut it. */
function DocIndex({
  docs,
  truncated,
}: {
  docs: DocBrief[];
  truncated: boolean;
}) {
  return (
    <>
      <SubHeading>Doc index</SubHeading>
      {truncated && (
        <p className="m-0 text-[11px] wrap-anywhere text-warn">
          Cut to fit: deprecated and the least recently updated docs were left
          out.
        </p>
      )}
      {docs.length === 0 ? (
        <p className="m-0 text-xs text-muted">
          No docs were delivered to this step.
        </p>
      ) : (
        <ul
          aria-label="Delivered docs"
          className="m-0 grid min-w-0 list-none gap-px p-0"
        >
          {docs.map((doc) => (
            <li
              key={doc.path}
              className="grid min-w-0 gap-1 bg-field px-3 py-2"
            >
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="min-w-0 font-mono text-[11px] wrap-anywhere">
                  {doc.path}
                </span>
                {doc.status && (
                  <Chip tone={docStatusTones[doc.status] ?? "warn"}>
                    {doc.status}
                  </Chip>
                )}
              </div>
              <small className="min-w-0 text-[11px] wrap-anywhere text-muted">
                {[
                  doc.title || "Untitled",
                  doc.lastUpdated && `updated ${doc.lastUpdated}`,
                  doc.author,
                  doc.tags?.join(", "),
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </small>
              {doc.problems?.map((problem) => (
                <small
                  key={problem}
                  className="min-w-0 text-[11px] wrap-anywhere text-danger"
                >
                  {problem}
                </small>
              ))}
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

/** Where a read landed, as its path under the docs root reads. */
function readTarget(read: StepDocRead) {
  if (read.docPath === "/") return "searched the docs root";
  if (read.docPath.endsWith("/")) return `searched ${read.docPath}`;
  return read.docPath;
}

/**
 * The docs the step's agent opened, in the order it opened them, against
 * the index it was given: which delivered docs it used, and what it found
 * on its own.
 */
function DocsRead({
  reads,
  delivered,
}: {
  reads: StepDocRead[];
  delivered: number;
}) {
  // Docs count once however often they were opened; the list keeps every
  // read.
  const opened = new Set(
    reads.filter((read) => read.delivered).map((read) => read.docPath),
  ).size;
  const elsewhere = new Set(
    reads
      .filter((read) => !read.delivered && !read.docPath.endsWith("/"))
      .map((read) => read.docPath),
  ).size;
  return (
    <>
      <SubHeading>Docs read</SubHeading>
      <p className="m-0 text-[11px] wrap-anywhere text-muted">
        {reads.length === 0
          ? "The agent opened no docs that Inkuna could see."
          : [
              `Opened ${opened} of the ${delivered} ${delivered === 1 ? "doc" : "docs"} delivered`,
              elsewhere > 0 && `${elsewhere} not in the index`,
            ]
              .filter(Boolean)
              .join(" · ")}
      </p>
      {reads.length > 0 && (
        <ol
          aria-label="Docs read"
          className="m-0 grid min-w-0 list-none gap-px p-0"
        >
          {reads.map((read, index) => (
            <li
              key={`${read.openedAt}-${index}`}
              className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 bg-field px-3 py-2"
            >
              <time
                dateTime={read.openedAt}
                className="flex-none font-mono text-[10px] text-faint"
              >
                {formatTime(read.openedAt)}
              </time>
              <span className="min-w-0 font-mono text-[11px] wrap-anywhere">
                {readTarget(read)}
              </span>
              {!read.docPath.endsWith("/") && (
                <Chip tone={read.delivered ? "green" : "neutral"}>
                  {read.delivered ? "delivered" : "not delivered"}
                </Chip>
              )}
              <small className="ml-auto font-mono text-[10px] text-muted">
                {read.tool}
              </small>
            </li>
          ))}
        </ol>
      )}
    </>
  );
}
