import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  Controls,
  ReactFlow,
  type Dimensions,
  type Edge,
  type Node,
  type NodeChange,
} from "@xyflow/react";
import { reviseWorkflow } from "../bridge";
import { formatTime } from "../format";
import type {
  AgentCandidate,
  AppDefaults,
  BranchPlan,
  MCPServer,
  Runtime,
  RunResume,
  RuntimeInfo,
  TaskSpec,
  WorkflowNode,
  WorkflowPlan,
} from "../types";
import { BranchPanel } from "./BranchPanel";
import { PlanTitle } from "./PlanTitle";
import { RerunPanel, type RerunControls } from "./RerunPanel";
import { ServerList, UserServerList } from "./ServerList";
import { StepOriginBadge } from "./StepOrigin";
import { originLabel } from "./origin";
import {
  StepAccess,
  StepChecks,
  compactInputClass,
  routeLabel,
  type AccessPatch,
} from "./StepAccess";
import { branchPanelVisible } from "./branching";
import { RuntimeOptions } from "./runtimes/RuntimeOptions";
import { useRuntimes } from "./runtimes/useRuntimes";
import { grantedServers } from "./servers";
import { systemCodesSummary } from "./rules/model";
import { ruleNames, useRules } from "./rules/useRules";
import { exact, usageLine } from "./usage";
import {
  Alert,
  Button,
  Chip,
  Empty,
  Header,
  TableScroll,
  tableClass,
  tdClass,
  thClass,
} from "./common";

const inspectorHeading = "text-xs font-semibold text-ink";
const inspectorText = "mt-1.5 text-[11px] leading-relaxed text-muted";
const inspectorRule = "my-5 border-0 border-t border-line";
// Free-text cells wrap, but never collapse to a one-word-per-line column.
const wideCell = `${tdClass} min-w-[180px]`;
const shortCell = `${tdClass} whitespace-nowrap`;

/**
 * What the approval screen may change: on an agent step its staffing and
 * access, on an evaluation step its commands.
 */
interface StepEdit extends AccessPatch {
  agent?: string;
  runtime?: Runtime;
  score?: number;
}

function Preview({
  title,
  children,
  foot,
}: {
  title: React.ReactNode;
  children: React.ReactNode;
  foot: React.ReactNode;
}) {
  return (
    <div className="grid min-w-0 gap-1.5 bg-field p-3">
      <b className="font-mono text-[9px] font-medium wrap-anywhere text-accent uppercase">
        {title}
      </b>
      <span className="min-w-0 text-[11px] wrap-anywhere">{children}</span>
      <small className="min-w-0 text-[11px] wrap-anywhere text-muted">
        {foot}
      </small>
    </div>
  );
}

function Check({ label }: { label: string }) {
  return (
    <div className="flex gap-2.5 border-b border-line py-2.5 text-xs text-ink-soft">
      <span className="text-ok">✓</span>
      {label}
    </div>
  );
}

/**
 * Agents a step may be given: any candidate for a read-only step, only
 * writable ones for a writable step, plus the current agent so a choice that
 * is no longer offered stays visible instead of silently changing.
 */
function candidatesFor(step: WorkflowNode, candidates: AgentCandidate[]) {
  const writable = step.permission === "worktree-write";
  const listed = candidates.filter(
    (candidate) => !writable || candidate.permission === "worktree-write",
  );
  if (
    step.agent &&
    !listed.some((candidate) => candidate.name === step.agent)
  ) {
    listed.push({
      name: step.agent,
      description: "",
      permission: step.permission,
      source: "plan",
    });
  }
  return listed;
}

/** What a planned step does with feedback, as Go resolved it: a review gate
 * and where its feedback goes, or a check loop, its cap, and the exit codes
 * that pause the run instead. */
function feedbackSummary(step: WorkflowNode): string | undefined {
  if (step.kind === "approval" && step.review)
    return step.feedbackTarget
      ? `pauses for review → ${step.feedbackTarget}`
      : "pauses for review · approve only";
  if (step.kind === "evaluation" && step.onFailure === "feedback") {
    const loop = `failures → ${step.feedbackTarget}, up to ${step.maxRounds} ${step.maxRounds === 1 ? "round" : "rounds"}`;
    const codes = step.systemExitCodes ?? [];
    return codes.length ? `${loop}; ${systemCodesSummary(codes)}` : loop;
  }
  return undefined;
}

/** The model routing gave a step, with its reasoning effort: neighbouring
 * scores can run the same model at different efforts. */
function modelLabel(step: WorkflowNode): string | undefined {
  const selection = step.modelSelection;
  if (!selection?.model) return undefined;
  return selection.reasoningEffort
    ? `${selection.model} · ${selection.reasoningEffort}`
    : selection.model;
}

function StepEditor({
  step,
  candidates,
  runtimes,
  servers,
  onEdit,
  disabled,
}: {
  step: WorkflowNode;
  candidates: AgentCandidate[];
  runtimes: RuntimeInfo[];
  /** The MCP servers inkuna.yaml declares. */
  servers?: MCPServer[];
  onEdit(edit: StepEdit): void;
  disabled: boolean;
}) {
  const agent = candidates.find((candidate) => candidate.name === step.agent);
  const remote = agent?.source === "remote" || step.runtime === "remote";
  const pinnedRuntime = remote ? "remote" : agent?.runtime;
  return (
    <div className="grid min-w-0 gap-2 border-t border-line pt-3">
      <div className="flex min-w-0 items-baseline justify-between gap-2">
        <b className="min-w-0 text-[11px] wrap-anywhere">{step.title}</b>
        <small className="shrink-0 font-mono text-[9px] text-muted uppercase">
          {step.permission}
        </small>
      </div>
      <label className={routeLabel}>
        Agent
        <select
          className={compactInputClass}
          aria-label={`Agent for ${step.title}`}
          value={step.agent ?? ""}
          disabled={disabled}
          onChange={(event) => onEdit({ agent: event.target.value })}
        >
          {candidatesFor(step, candidates).map((candidate) => (
            <option
              key={candidate.name}
              value={candidate.name}
              title={candidate.description}
            >
              {candidate.name}
              {candidate.permission === "read-only" ? " (read-only)" : ""}
              {candidate.source === "remote" ? " (remote)" : ""}
            </option>
          ))}
        </select>
      </label>
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)] gap-2">
        <label className={routeLabel}>
          Complexity score
          <input
            className={compactInputClass}
            aria-label={`Complexity score for ${step.title}`}
            type="number"
            disabled={disabled}
            value={step.complexity?.score ?? ""}
            onChange={(event) => {
              const score = Number(event.target.value);
              if (Number.isFinite(score) && event.target.value !== "")
                onEdit({ score });
            }}
          />
        </label>
        <label className={routeLabel}>
          Worker runtime
          <select
            className={compactInputClass}
            aria-label={`Runtime for ${step.title}`}
            value={step.runtime ?? ""}
            disabled={disabled || Boolean(pinnedRuntime)}
            title={
              remote
                ? "A remote agent runs on its own side"
                : pinnedRuntime
                  ? `Pinned by agent ${step.agent}`
                  : undefined
            }
            onChange={(event) =>
              onEdit({ runtime: event.target.value as Runtime })
            }
          >
            {remote ? (
              <option value="remote">remote</option>
            ) : (
              <RuntimeOptions
                runtimes={runtimes}
                value={step.runtime}
                writeOnly={step.permission === "worktree-write"}
              />
            )}
          </select>
        </label>
      </div>
      <StepAccess
        step={step}
        remote={remote}
        onEdit={onEdit}
        disabled={disabled}
      />
      <Preview
        title={<>Complexity {step.complexity?.score ?? "—"} / 10</>}
        foot={
          step.modelSelection?.rationale ??
          "Policy selected · editable before approval"
        }
      >
        {step.agent ? `${step.agent} · ` : ""}
        {step.runtime}
        {modelLabel(step) ? ` · ${modelLabel(step)}` : ""}
      </Preview>
      <ServerList
        servers={grantedServers(step.mcpServers, servers)}
        readOnly={step.permission === "read-only"}
      />
      <UserServerList
        servers={(step.userMcp ?? []).map((name) => ({ name, status: "" }))}
      />
      {step.trackerKinds?.length ? (
        <p className="m-0 text-[11px] wrap-anywhere text-muted">
          May ask for tracker changes: {step.trackerKinds.join(", ")}. Each
          waits for the review gate after it, and is made only when you approve
          the run there.
        </p>
      ) : null}
    </div>
  );
}

/**
 * What the Workflow screen holds for a plan that Go accepted: the steps as
 * last revised, a rerun's resume, and a saved title. The app keeps it while
 * another screen is open, so coming back shows, and approves, the plan as
 * edited rather than as first planned.
 */
export type WorkflowDraft = {
  workflowID: string;
  steps: WorkflowNode[];
  resume?: RunResume | null;
  retitled?: { task: TaskSpec; branching?: BranchPlan } | null;
};

export function WorkflowView({
  defaults,
  plan,
  onRun,
  onReplan,
  rerun,
  draft,
  onDraft,
}: {
  defaults: AppDefaults;
  plan: WorkflowPlan | null;
  onRun(workflow: WorkflowPlan["workflow"]): Promise<void>;
  /**
   * Plans the same request again under a rule; restaff asks the orchestrator
   * again instead of reusing a kept decision. Absent when unknown.
   */
  onReplan?(ruleName: string, restaff?: boolean): Promise<void>;
  /**
   * Set for a rerun of a stopped run: Go rebuilds each edit against the
   * plan the run ran under, and approving resumes or starts over the run.
   */
  rerun?: RerunControls;
  /** The edits kept for this plan, from when the screen was last open. */
  draft?: WorkflowDraft | null;
  /**
   * Keeps the edits Go accepted, for when the screen opens again. It is
   * given an update to the draft as the parent holds it now, so a title Go
   * saved after the screen closed does not undo edits made since.
   */
  onDraft?(update: (current: WorkflowDraft | null) => WorkflowDraft): void;
}) {
  // A draft of another plan, or of none, is not this plan's.
  const kept =
    plan && draft?.workflowID === plan.workflow.id ? draft : undefined;
  const [steps, setSteps] = useState<WorkflowNode[]>(
    () => kept?.steps ?? plan?.workflow.steps ?? [],
  );
  // What resuming under the plan as shown would do, as Go last said. A
  // rerun of another run is another screen, keyed by its run.
  const [resume, setResume] = useState(kept?.resume ?? rerun?.resume ?? null);
  const [discard, setDiscard] = useState(false);
  const [starting, setStarting] = useState(false);
  // A save to the rule hands over the plan as it stands, so nothing changes
  // it, and it is not handed over twice, until it is done.
  const [saving, setSaving] = useState(false);
  const [runError, setRunError] = useState("");
  const [revising, setRevising] = useState(false);
  const [reviseError, setReviseError] = useState("");
  const [replanning, setReplanning] = useState(false);
  const [replanError, setReplanError] = useState("");
  // A title changed here renames the task and its planned branch, which Go
  // saved; the override belongs to the plan it was made on.
  const [retitled, setRetitled] = useState<{
    workflowID: string;
    task: TaskSpec;
    branching?: BranchPlan;
  } | null>(() =>
    plan && kept?.retitled
      ? { workflowID: plan.workflow.id, ...kept.retitled }
      : null,
  );
  // Only what Go accepted is kept: a refused edit, or one still on its way,
  // would come back looking accepted.
  function keep(patch: Partial<Omit<WorkflowDraft, "workflowID">>) {
    if (!plan) return;
    const { id: workflowID, steps } = plan.workflow;
    onDraft?.((current) => ({
      ...(current?.workflowID === workflowID ? current : { workflowID, steps }),
      ...patch,
    }));
  }
  const current =
    plan && retitled?.workflowID === plan.workflow.id ? retitled : null;
  const branching = current ? current.branching : plan?.workflow.branching;
  // Edits can overlap; only the newest revision may update the steps.
  const revision = useRef(0);
  // A revision that comes back after the screen closed is dropped: Go saved
  // nothing, and the screen opened since never showed it, so keeping it
  // would bring back steps the person did not see when they approved.
  const open = useRef(true);
  useEffect(() => {
    open.current = true;
    return () => {
      open.current = false;
    };
  }, []);
  const rules = useRules(plan?.project.id);
  const runtimes = useRuntimes();
  // The steps start from the plan. A new plan, from a re-plan, replaces them
  // and drops any revision still in flight. Not on mount: an edit can land
  // before the first effects run, and resetting then would discard it.
  const shownPlan = useRef(plan);
  useEffect(() => {
    if (shownPlan.current === plan) return;
    shownPlan.current = plan;
    setSteps(plan?.workflow.steps ?? []);
    setDiscard(false);
    setReviseError("");
    revision.current += 1;
    setRevising(false);
  }, [plan]);
  async function edit(stepID: string, patch: StepEdit) {
    if (!plan) return;
    const previous = steps;
    const next = steps.map((step) =>
      step.id !== stepID
        ? step
        : {
            ...step,
            agent: patch.agent ?? step.agent,
            runtime: patch.runtime ?? step.runtime,
            permission: patch.permission ?? step.permission,
            commands: patch.commands ?? step.commands,
            allowedPaths: patch.allowedPaths ?? step.allowedPaths,
            complexity:
              patch.score === undefined || !step.complexity
                ? step.complexity
                : {
                    ...step.complexity,
                    // The score is a whole number; whether it is in range
                    // is Go's to say.
                    score: Math.round(patch.score),
                  },
          },
    );
    setSteps(next);
    const ticket = ++revision.current;
    setRevising(true);
    try {
      const edited = JSON.stringify({ ...plan.workflow, steps: next });
      if (rerun) {
        const revised = await rerun.revise(edited);
        if (ticket !== revision.current || !open.current) return;
        setSteps(revised.workflow.steps);
        setResume(revised.resume);
        keep({ steps: revised.workflow.steps, resume: revised.resume });
      } else {
        const revised = await reviseWorkflow(edited);
        if (ticket !== revision.current || !open.current) return;
        setSteps(revised.steps);
        keep({ steps: revised.steps });
      }
      setReviseError("");
    } catch (reason) {
      if (ticket !== revision.current) return;
      // A refused choice goes back to what Go last accepted. Typed commands
      // or paths stay as typed beside Go's reason, to be fixed rather than
      // typed again; approval waits until Go accepts them.
      const typed =
        patch.commands !== undefined || patch.allowedPaths !== undefined;
      setSteps(typed ? next : previous);
      setReviseError(String(reason));
    } finally {
      if (ticket === revision.current) setRevising(false);
    }
  }
  async function replan(ruleName: string, restaff = false) {
    if (!onReplan) return;
    setReplanning(true);
    setReplanError("");
    try {
      await onReplan(ruleName, restaff);
    } catch (reason) {
      setReplanError(String(reason));
    } finally {
      setReplanning(false);
    }
  }
  async function rerunAs(startOver: boolean) {
    if (!plan || !rerun || !resume) return;
    setStarting(true);
    setRunError("");
    try {
      const changes = resume.changes.map((change) => change.path);
      await rerun.onRerun(
        { ...plan.workflow, steps },
        {
          round: resume.round,
          startOver,
          discard: !startOver && discard && changes.length > 0,
          changes,
        },
      );
    } catch (reason) {
      setRunError(String(reason));
    } finally {
      setStarting(false);
    }
  }
  async function saveToRule() {
    if (!plan || !rerun?.saveToRule || !resume || saving) return;
    setSaving(true);
    setRunError("");
    try {
      await rerun.saveToRule({ ...plan.workflow, steps }, resume);
    } catch (reason) {
      setRunError(String(reason));
    } finally {
      setSaving(false);
    }
  }
  async function approveAndRun() {
    if (!plan) return;
    setStarting(true);
    setRunError("");
    try {
      await onRun({ ...plan.workflow, branching, steps });
    } catch (reason) {
      setRunError(String(reason));
    } finally {
      setStarting(false);
    }
  }
  // React Flow measures a node once and drops the measurement when it is
  // handed a new node object without one; a new object landing between a
  // measurement and its render left the nodes hidden for good. So only a
  // change of steps rebuilds them, not every render (the rules or runtimes
  // loading), and a rebuilt node carries the size React Flow last reported
  // for its step, because every edit rebuilds them twice: the edit, then
  // Go's revision.
  const [sizes, setSizes] = useState<ReadonlyMap<string, Dimensions>>(
    () => new Map(),
  );
  const measure = useCallback((changes: NodeChange[]) => {
    const measured = changes.flatMap((change) =>
      change.type === "dimensions" && change.dimensions
        ? [[change.id, change.dimensions] as const]
        : [],
    );
    if (measured.length > 0)
      setSizes((current) => new Map([...current, ...measured]));
  }, []);
  const nodes = useMemo<Node[]>(
    () =>
      steps.map((step, index) => ({
        id: step.id,
        position: { x: 80 + index * 245, y: index % 2 ? 180 : 40 },
        measured: sizes.get(step.id),
        data: {
          label: (
            <div className="grid min-w-0 gap-1 p-1">
              <small className="font-mono text-[8px] text-accent uppercase">
                {step.kind}
              </small>
              <b className="text-[11px] wrap-anywhere">{step.title}</b>
              <span className="text-[9px] wrap-anywhere text-muted">
                {step.agent ? `${step.agent} · ` : ""}
                {step.modelSelection
                  ? `${step.runtime} · ${modelLabel(step) ?? ""}`
                  : step.permission}
              </span>
              {step.complexity && (
                <em className="font-mono text-[8px] text-ok uppercase not-italic">
                  Complexity {step.complexity.score}/10
                </em>
              )}
              {feedbackSummary(step) && (
                <em className="font-mono text-[8px] wrap-anywhere text-warn uppercase not-italic">
                  {feedbackSummary(step)}
                </em>
              )}
              {step.from && (
                <em className="font-mono text-[8px] wrap-anywhere text-accent not-italic">
                  {originLabel(step.from)}
                </em>
              )}
            </div>
          ),
        },
      })),
    [steps, sizes],
  );
  const edges = useMemo<Edge[]>(
    () =>
      steps.flatMap((step) =>
        (step.dependsOn ?? []).map((dependency) => ({
          id: `${dependency}-${step.id}`,
          source: dependency,
          target: step.id,
          animated: false,
        })),
      ),
    [steps],
  );
  if (!plan) {
    return (
      <section>
        <Header eyebrow="Plan" title="Workflow" />
        <Empty title="No workflow planned yet">
          <p>Create a request for a local project before approving a run.</p>
        </Empty>
      </section>
    );
  }
  const task = current?.task ?? plan.task;
  const agentSteps = steps.filter((step) => step.kind === "agent");
  const checkSteps = steps.filter((step) => step.kind === "evaluation");
  const candidates = plan.candidates ?? [];
  const ruleName = plan.ruleName ?? plan.workflow.ruleName ?? "default";
  const orchestrated = plan.orchestrated ?? plan.workflow.orchestrated ?? false;
  const ruleOptions = ruleNames(rules);
  if (!ruleOptions.includes(ruleName)) ruleOptions.unshift(ruleName);
  const busy = starting || revising || replanning || saving;
  // Only a plan with auto stages has staffing to ask for again.
  const staffed = (plan.decision?.assignments?.length ?? 0) > 0;
  return (
    <section>
      <Header
        eyebrow={
          rerun ? "Rerun · Approval required" : "Plan · Approval required"
        }
        title="Workflow"
        actions={
          rerun && resume ? (
            <div className="flex flex-wrap gap-2">
              <Button
                disabled={busy || Boolean(reviseError)}
                onClick={() => void rerunAs(true)}
              >
                Start over
              </Button>
              <Button
                variant={
                  discard && resume.changes.length > 0 ? "danger" : "primary"
                }
                disabled={
                  busy || Boolean(reviseError) || Boolean(resume.refusal)
                }
                title={resume.refusal}
                onClick={() => void rerunAs(false)}
              >
                {starting
                  ? "Starting…"
                  : discard && resume.changes.length > 0
                    ? "Discard and resume"
                    : "Resume"}
              </Button>
            </div>
          ) : (
            <Button
              variant="primary"
              disabled={busy || Boolean(reviseError)}
              onClick={approveAndRun}
            >
              {starting ? "Starting…" : "Approve & run"}
            </Button>
          )
        }
      />
      {rerun && resume ? (
        <RerunPanel
          resume={resume}
          discard={discard}
          onDiscard={setDiscard}
          onSaveToRule={rerun.saveToRule ? () => void saveToRule() : undefined}
          disabled={busy}
        />
      ) : (
        <PlanTitle
          workflowID={plan.workflow.id}
          task={task}
          disabled={busy}
          onChanged={(next, nextBranching) => {
            const branching = nextBranching ?? plan.workflow.branching;
            setRetitled({
              workflowID: plan.workflow.id,
              task: next,
              branching,
            });
            keep({ retitled: { task: next, branching } });
          }}
        />
      )}
      {runError && <Alert>{runError}</Alert>}
      {replanError && <Alert>{replanError}</Alert>}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        {onReplan ? (
          <label className="flex items-center gap-2 text-[11px] text-muted">
            Workflow rule
            <select
              className={`${compactInputClass} w-auto`}
              aria-label="Workflow rule"
              value={ruleName}
              disabled={busy}
              onChange={(event) => void replan(event.target.value)}
            >
              {ruleOptions.map((name) => (
                <option value={name} key={name}>
                  {name}
                </option>
              ))}
            </select>
            {replanning && <span>Planning…</span>}
          </label>
        ) : (
          <Chip tone="accent">rule · {ruleName}</Chip>
        )}
        {onReplan && staffed && (
          <Button
            className="px-2.5 py-1 text-[11px]"
            disabled={busy}
            title="Ask the orchestrator to staff the auto stages again"
            onClick={() => void replan(ruleName, true)}
          >
            Re-staff
          </Button>
        )}
        <Chip tone={orchestrated ? "green" : "neutral"}>
          {orchestrated ? "orchestrated" : "deterministic staffing"}
        </Chip>
        {plan.notes?.map((note) => (
          <span
            className="min-w-0 text-[11px] wrap-anywhere text-muted"
            key={note}
          >
            {note}
          </span>
        ))}
      </div>
      {(plan.staffingReused || (plan.usage && plan.usage.length > 0)) && (
        <ul
          aria-label="Orchestrator usage"
          className="-mt-2 mb-4 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[11px] text-muted"
        >
          {plan.staffingReused && (
            <li className="min-w-0 wrap-anywhere">
              Orchestrator staffing · reused the decision made{" "}
              {formatTime(plan.staffingReused)} for the same inputs · no call
            </li>
          )}
          {(plan.usage ?? []).map((call) => (
            <li className="min-w-0 wrap-anywhere" key={call.id}>
              Orchestrator {call.stepId} ·{" "}
              {[call.runtime, call.model].filter(Boolean).join(" ")} ·{" "}
              {usageLine(call.usage)} · {exact(call.promptBytes)} prompt bytes
              {call.outcome === "completed" ? "" : ` · ${call.outcome}`}
            </li>
          ))}
        </ul>
      )}
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(260px,320px)] gap-4 max-[900px]:grid-cols-1">
        <div
          className="h-[460px] min-w-0 self-start border border-line bg-field min-[901px]:sticky min-[901px]:top-4 max-[900px]:h-[400px]"
          aria-label="Workflow graph"
        >
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={measure}
            colorMode="dark"
            fitView
          >
            <Background color="#263238" />
            <Controls />
          </ReactFlow>
        </div>
        <aside className="min-w-0 self-start border border-line bg-panel p-5">
          <h2 className="mb-1 text-[15px] font-semibold">Plan checks</h2>
          <Check label="Acyclic graph" />
          <Check label="Complexity scored" />
          <Check label="Models routed" />
          <Check label="Agents staffed" />
          <Check label="Budgets bounded" />
          <Check label="Permissions scoped" />
          <Check label="Skills available" />
          {branchPanelVisible(branching) && (
            <>
              <hr className={inspectorRule} />
              <BranchPanel plan={branching} />
            </>
          )}
          <hr className={inspectorRule} />
          <h3 className={inspectorHeading}>Staffing, access, and routing</h3>
          <p className={inspectorText}>
            Each agent step can be given another agent, runtime, and score
            before approval. Policy v{defaults.routingPolicy.version || "—"}{" "}
            picks the model from the score inside the runtime; an agent that
            pins a runtime or model keeps it. A step&apos;s permission,
            commands, and allowed paths can change too, for this run only: they
            are checked as its rule&apos;s would be, and a change that gives a
            step more than its rule did is marked.
          </p>
          {reviseError && (
            <div className="mt-3">
              <Alert>{reviseError}</Alert>
            </div>
          )}
          <div className="mt-3 grid gap-4">
            {agentSteps.map((step) => (
              <StepEditor
                key={step.id}
                step={step}
                candidates={candidates}
                runtimes={runtimes}
                servers={plan.mcpServers}
                disabled={starting || replanning || saving}
                onEdit={(patch) => void edit(step.id, patch)}
              />
            ))}
            {checkSteps.map((step) => (
              <StepChecks
                key={step.id}
                step={step}
                disabled={starting || replanning || saving}
                onEdit={(patch) => void edit(step.id, patch)}
              />
            ))}
          </div>
          <hr className={inspectorRule} />
          <h3 className={inspectorHeading}>Approved request</h3>
          <p className={`${inspectorText} mb-3 wrap-anywhere`}>
            {plan.project.name}
          </p>
          <Preview
            title={plan.task.kind}
            foot={
              <>
                {plan.task.source.kind} · {steps.length} workflow steps
                {plan.task.labels?.length
                  ? ` · ${plan.task.labels.join(", ")}`
                  : ""}
              </>
            }
          >
            {task.title ?? task.objective}
          </Preview>
        </aside>
      </div>
      <TableScroll>
        <table className={tableClass}>
          <caption className="pb-2 text-left text-muted">
            Accessible workflow list
          </caption>
          <thead>
            <tr>
              <th className={thClass}>Step</th>
              <th className={thClass}>Kind</th>
              <th className={thClass}>Agent</th>
              <th className={thClass}>Complexity</th>
              <th className={thClass}>Runtime</th>
              <th className={thClass}>Model</th>
              <th className={thClass}>Permission</th>
              <th className={thClass}>Skills</th>
              <th className={thClass}>MCP servers</th>
              <th className={thClass}>Assignment</th>
            </tr>
          </thead>
          <tbody>
            {steps.map((step) => (
              <tr key={step.id}>
                <td className={wideCell}>
                  {step.title}
                  {step.from && (
                    <>
                      {" "}
                      <StepOriginBadge origin={step.from} step={step.title} />
                    </>
                  )}
                </td>
                <td className={shortCell}>
                  {step.kind}
                  {feedbackSummary(step) && (
                    <>
                      {" "}
                      <Chip tone="warn">{feedbackSummary(step)}</Chip>
                    </>
                  )}
                </td>
                <td className={shortCell}>{step.agent ?? "—"}</td>
                <td className={shortCell}>
                  {step.complexity ? `${step.complexity.score}/10` : "—"}
                </td>
                <td className={shortCell}>{step.runtime ?? "—"}</td>
                <td className={shortCell}>{modelLabel(step) ?? "—"}</td>
                <td className={shortCell}>{step.permission}</td>
                <td className={wideCell}>
                  {step.selectedSkills?.join(", ") || "—"}
                </td>
                <td className={wideCell}>
                  {[
                    ...(step.mcpServers ?? []),
                    ...(step.userMcp ?? []).map((name) => `${name} (yours)`),
                  ].join(", ") || "—"}
                </td>
                <td className={`${wideCell} text-muted`}>
                  {step.metadata?.assignment ?? "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>
    </section>
  );
}
