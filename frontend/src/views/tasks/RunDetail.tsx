import { useCallback, useEffect, useState } from "react";
import { getRunRounds } from "../../bridge";
import {
  isTerminal,
  type PlanSuggestion,
  type RunRound,
  type StepRun,
  type WorkflowRun,
} from "../../types";
import {
  Button,
  Chip,
  Eyebrow,
  inputClass,
  Metric,
  Metrics,
  Panel,
  StateChip,
  TableScroll,
  TabPanel,
  Tabs,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import { bestCostLabel, costBreakdown } from "../cost";
import { StepOriginBadge } from "../StepOrigin";
import { ArtifactsTab } from "./ArtifactsTab";
import { RunTaskDetails } from "../TaskDetails";
import { ChangesTab, type ChangesView } from "./changes/ChangesTab";
import { checksTone } from "./checks";
import { ContextTab } from "./ContextTab";
import { CostTab } from "./CostTab";
import { MessagesTab } from "./MessagesTab";
import { ResumeBar } from "./ResumeBar";
import { RunFailurePanel } from "./RunFailurePanel";
import { ReviewBar } from "./ReviewBar";
import { automatic, ranIn, shownRounds, type RoundPick } from "./rounds";
import { Timeline } from "./Timeline";
import { runtimeLine, type Tab } from "./run";
import { RunStory } from "./RunStory";
import { TrackerChanges } from "./TrackerChanges";
import { RunWorkspace } from "./RunWorkspace";
import { InlineAlert } from "./shared";
import { UsagePanel } from "./UsagePanel";

const tabs: Array<{ id: Tab; label: string }> = [
  { id: "timeline", label: "Timeline" },
  { id: "context", label: "Context" },
  { id: "changes", label: "Changes" },
  { id: "messages", label: "Messages" },
  { id: "artifacts", label: "Artifacts" },
  { id: "cost", label: "Cost" },
];

const nowrapCell = `${tdClass} whitespace-nowrap`;

export function RunDetail({
  run,
  tab,
  onTab,
  onCancel,
  onRemoveWorktree,
  onDelete,
  onReviewed,
  onOpenThread,
  onRerun,
}: {
  run: WorkflowRun;
  tab: Tab;
  onTab(tab: Tab): void;
  onCancel(): void;
  /** Removes the finished run's worktree; rejects with Go's reason. */
  onRemoveWorktree(): Promise<void>;
  /** Opens the delete confirmation for this ended run, or its task. */
  onDelete?(): void;
  /**
   * The run as Go returned it after an approval, a request for changes, or
   * a resume.
   */
  onReviewed?(run: WorkflowRun): void;
  onOpenThread?(taskID: string): void;
  /**
   * Opens the approval screen to rerun this stopped run, with the
   * suggested fixes the person ticked made.
   */
  onRerun?(suggestions: PlanSuggestion[]): void;
}) {
  const terminal = isTerminal(run.state);
  // The checks summary opens the Changes tab on its Checks view; the view
  // is kept per run, so another run opens on its own.
  const [changesView, setChangesView] = useState<{
    runID: string;
    view: ChangesView;
  }>({ runID: run.id, view: "files" });
  const view = changesView.runID === run.id ? changesView.view : "files";
  // A comment written in the Changes tab is sent by the review bar, which
  // counts the drafts again when one changes.
  const [commentsVersion, setCommentsVersion] = useState(0);
  const onCommentsChanged = useCallback(
    () => setCommentsVersion((version) => version + 1),
    [],
  );
  const steps = run.steps ?? [];
  const events = run.events ?? [];
  const reviewing = run.state === "reviewing";
  // The gate's thread, while the run is in memory; after a restart Messages
  // still lists it.
  const review = steps.find(
    (step) => step.state === "reviewing" && step.a2aTaskId,
  );
  const current = run.round ?? 1;

  // Earlier rounds never change, so they are read again only when the run
  // moves to another round or state; the current one is the run view's.
  const [stored, setStored] = useState<{
    runID: string;
    rounds: RunRound[];
  } | null>(null);
  const [roundsError, setRoundsError] = useState<{
    runID: string;
    message: string;
  } | null>(null);
  useEffect(() => {
    let active = true;
    getRunRounds(run.id)
      .then((rounds) => {
        if (!active) return;
        setStored({ runID: run.id, rounds });
        setRoundsError(null);
      })
      .catch((reason: unknown) => {
        if (active) setRoundsError({ runID: run.id, message: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [run.id, current, run.state]);
  const rounds = shownRounds(
    run,
    stored?.runID === run.id ? stored.rounds : [],
  );
  // The current round is shown until another is picked, and a new round
  // is followed into unless one was.
  const [picked, setPicked] = useState<{
    runID: string;
    round: RoundPick;
  } | null>(null);
  let pick: RoundPick = picked?.runID === run.id ? picked.round : current;
  if (pick !== "all" && !rounds.some((round) => round.round === pick))
    pick = current;
  const shown =
    pick === "all" ? rounds : rounds.filter((round) => round.round === pick);
  // Go decides what an ended or paused run can do, and says why not.
  const reviewable = reviewing || terminal;
  return (
    <Panel>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 flex-1 basis-[240px]">
          <div className="min-w-0 wrap-anywhere">
            <Eyebrow>Run · {run.id}</Eyebrow>
          </div>
          {/* A run from before titles carries its whole objective as its
              title, so the heading keeps to two lines; the details hold
              the rest. */}
          <h2
            className="mt-1.5 mb-1 line-clamp-2 text-xl font-semibold break-words"
            title={run.title}
          >
            {run.title}
          </h2>
          <p className="m-0 text-[11px] wrap-anywhere text-muted">
            {runtimeLine(run)}
            {run.branch ? ` · ${run.branch}` : ""}
            {run.rerunOf ? ` · started over from ${run.rerunOf}` : ""}
          </p>
          <RunTaskDetails key={run.id} runID={run.id} />
        </div>
        <div className="flex flex-none flex-wrap items-center gap-2.5">
          {run.taskCost && (
            <button
              type="button"
              title={`What every run of this task and its planning cost: ${costBreakdown(run.taskCost)}`}
              className="cursor-pointer border-0 bg-transparent p-0 text-left text-[11px] text-muted hover:text-ink-strong"
              onClick={() => onTab("cost")}
            >
              Task cost{" "}
              <b className="font-mono font-medium text-ink-strong">
                {bestCostLabel(run.taskCost)}
              </b>
            </button>
          )}
          {run.checks && (
            <button
              type="button"
              title={`${run.checks.passed} passed, ${run.checks.failed} failed, ${run.checks.running} running: open the checks`}
              className="cursor-pointer border-0 bg-transparent p-0"
              onClick={() => {
                setChangesView({ runID: run.id, view: "checks" });
                onTab("changes");
              }}
            >
              <Chip tone={checksTone(run.checks)}>
                {run.checks.passed} of {run.checks.total} checks passed
              </Chip>
            </button>
          )}
          <StateChip state={run.state} />
          {/* A run paused for review is cancelled from storage, so it can be
              cancelled after a restart too. */}
          <Button
            variant="danger"
            disabled={
              terminal ||
              run.state === "cancelling" ||
              (!run.live && !reviewing)
            }
            onClick={onCancel}
          >
            {run.state === "cancelling" ? "Cancelling…" : "Cancel run"}
          </Button>
          {/* Go decides whether it can go, and the dialog shows why not. */}
          {terminal && onDelete && (
            <Button variant="danger" onClick={onDelete}>
              Delete…
            </Button>
          )}
        </div>
      </div>
      {run.error && (
        <div className="mt-4">
          <InlineAlert>{run.error}</InlineAlert>
        </div>
      )}
      {/* Go says whether an ended run is one that stopped, which Resume is
          for, and why it stopped, which a rerun fixes. */}
      {terminal && (
        <RunFailurePanel
          key={`failure-${run.id}`}
          run={run}
          onRerun={onRerun}
        />
      )}
      {terminal && (
        <ResumeBar
          key={`resume-${run.id}`}
          run={run}
          onResumed={(next) => onReviewed?.(next)}
        />
      )}
      {reviewable && (
        <ReviewBar
          key={`review-${run.id}`}
          run={run}
          commentsVersion={commentsVersion}
          onChanged={(next) => onReviewed?.(next)}
          onOpenThread={
            review?.a2aTaskId && onOpenThread
              ? () => onOpenThread(review.a2aTaskId!)
              : undefined
          }
        />
      )}
      {/* Keyed by run so a workspace or usage panel starts over for another
          run. The two are siblings, so their keys must differ: React tells
          siblings apart by key, and two alike leave a stale copy behind on
          every refresh. */}
      <RunWorkspace
        key={`workspace-${run.id}`}
        run={run}
        onRemoveWorktree={onRemoveWorktree}
      />
      <RunStory key={`story-${run.id}`} run={run} />
      {(run.trackerChanges?.length ?? 0) > 0 && (
        <div className="mt-4 min-w-0">
          <TrackerChanges
            key={`tracker-${run.id}`}
            runId={run.id}
            changes={run.trackerChanges ?? []}
            label="Tracker changes"
            retry
          />
        </div>
      )}
      <div className="mt-5 min-w-0 [&_strong]:text-lg">
        <Metrics flush>
          <Metric value={String(steps.length)} label="Steps" />
          <Metric value={String(events.length)} label="Events" />
          <Metric
            value={String(run.pendingInputs ?? 0)}
            label="Pending inputs"
          />
          <Metric
            value={run.lastCommit?.shortHash ?? "—"}
            label="Latest commit"
          />
        </Metrics>
      </div>
      {rounds.length > 1 && (
        <div className="mt-5 flex min-w-0 flex-wrap items-center gap-2.5">
          <label className="flex items-center gap-2 text-xs text-ink-soft">
            Round
            <select
              aria-label="Round"
              className={`${inputClass} w-auto py-1 text-xs`}
              value={String(pick)}
              onChange={(event) =>
                setPicked({
                  runID: run.id,
                  round:
                    event.target.value === "all"
                      ? "all"
                      : Number(event.target.value),
                })
              }
            >
              {rounds.map((round) => (
                <option key={round.round} value={round.round}>
                  Round {round.round}
                  {automatic(round) ? " · check" : ""}
                  {round.resumed
                    ? round.edited?.length
                      ? ` · resumed with ${round.edited.join(", ")} edited`
                      : " · resumed"
                    : ""}
                  {round.round === current ? " (current)" : ""}
                </option>
              ))}
              <option value="all">All rounds</option>
            </select>
          </label>
        </div>
      )}
      {roundsError?.runID === run.id && current > 1 && (
        <div className="mt-3">
          <InlineAlert>
            Earlier rounds could not be read: {roundsError.message}
          </InlineAlert>
        </div>
      )}
      {pick === "all"
        ? rounds.map((round) => (
            <StepTable
              key={round.round}
              caption={`Round ${round.round} steps`}
              steps={ranIn(round)}
              round={round.round}
            />
          ))
        : shown.map((round) => (
            <StepTable
              key={round.round}
              caption={
                rounds.length > 1
                  ? `Round ${round.round} steps`
                  : "Workflow steps"
              }
              steps={round.steps}
              round={round.round}
            />
          ))}
      <UsagePanel key={`usage-${run.id}`} run={run} />
      <div className="mt-6 min-w-0">
        <Tabs
          tabs={tabs}
          active={tab}
          onChange={onTab}
          label="Run details"
          idPrefix="run"
        />
      </div>
      {/* Keyed by run so a tab never shows the previous run's fetched data. */}
      <TabPanel idPrefix="run" tab={tab}>
        {tab === "timeline" && <Timeline rounds={shown} />}
        {tab === "context" && <ContextTab key={run.id} run={run} />}
        {tab === "changes" && (
          <ChangesTab
            key={run.id}
            run={run}
            rounds={rounds}
            view={view}
            onView={(next) => setChangesView({ runID: run.id, view: next })}
            onCommentsChanged={onCommentsChanged}
          />
        )}
        {tab === "messages" && (
          <MessagesTab key={run.id} run={run} onOpenThread={onOpenThread} />
        )}
        {tab === "artifacts" && <ArtifactsTab key={run.id} run={run} />}
        {tab === "cost" && <CostTab key={run.id} run={run} />}
      </TabPanel>
    </Panel>
  );
}

/**
 * A round's steps. A step whose result the round kept from an earlier one
 * says which round that was.
 */
function StepTable({
  caption,
  steps,
  round,
}: {
  caption: string;
  steps: StepRun[];
  round: number;
}) {
  if (steps.length === 0) return null;
  return (
    <TableScroll>
      <table className={tableClass}>
        <caption className="mb-2 text-left text-muted">{caption}</caption>
        <thead>
          <tr>
            <th className={thClass}>Step</th>
            <th className={thClass}>Title</th>
            <th className={thClass}>Kind</th>
            <th className={thClass}>Agent</th>
            <th className={thClass}>Model</th>
            <th className={thClass}>State</th>
            <th className={thClass}>Summary</th>
          </tr>
        </thead>
        <tbody>
          {steps.map((step) => {
            const from = step.round ?? round;
            return (
              <tr key={step.id}>
                <td className={nowrapCell}>{step.id}</td>
                <td className={nowrapCell}>
                  {step.title}
                  {step.from && (
                    <>
                      {" "}
                      <StepOriginBadge origin={step.from} step={step.title} />
                    </>
                  )}
                </td>
                <td className={nowrapCell}>{step.kind}</td>
                <td className={nowrapCell}>{step.agent ?? "—"}</td>
                <td className={nowrapCell}>{step.model ?? "—"}</td>
                <td className={nowrapCell}>
                  <span className="inline-flex flex-wrap items-center gap-1.5">
                    <StateChip state={step.state} />
                    {from > 0 && from < round && (
                      <Chip tone="muted">
                        {step.state === "completed" ? "kept from" : "from"}{" "}
                        round {from}
                      </Chip>
                    )}
                  </span>
                </td>
                <td className={`${tdClass} min-w-[220px] text-muted`}>
                  {step.summary ?? ""}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableScroll>
  );
}
