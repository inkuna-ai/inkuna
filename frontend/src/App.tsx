import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  getCapabilities,
  getDashboard,
  planRerun,
  planWorkflow,
  rerunRuleEdits,
  rerunWorkflowRun,
  reviseRerun,
  startWorkflow,
} from "./bridge";
import { useInkunaEvent } from "./events";
import { Logo } from "./Logo";
import { navigation, shownViews } from "./navigation";
import type {
  Capability,
  DashboardData,
  ExtensionView,
  PlanSuggestion,
  RuleEdits,
  Project,
  RunResume,
  View,
  WorkflowPlan,
  WorkflowRequest,
  WorkflowRun,
} from "./types";
import { emptyDefaults } from "./types";
import { AgentsView } from "./views/AgentsView";
import { BranchesView } from "./views/BranchesView";
import { RuntimesView } from "./views/RuntimesView";
import { Alert, Button, Modal } from "./views/common";
import { ConnectionsView } from "./views/ConnectionsView";
import { CostsView } from "./views/CostsView";
import { DocsView } from "./views/DocsView";
import { MessagesView } from "./views/MessagesView";
import { Projects } from "./views/Projects";
import { RequestComposer } from "./views/RequestComposer";
import { RulesView } from "./views/RulesView";
import { SetupView } from "./views/SetupView";
import { TasksView } from "./views/TasksView";
import { WorkflowView, type WorkflowDraft } from "./views/WorkflowView";
import { withSuggestions } from "./views/tasks/suggestions";

// A stable empty list, so Inkuna's own bundle never recomputes navigation.
const noViews: ExtensionView[] = [];

export default function App({
  views = noViews,
}: {
  /** Screens an embedding frontend adds; see renderInkuna. */
  views?: ExtensionView[];
}) {
  const [view, setView] = useState<View>("projects");
  // What Go reports the build has. Fixed when the app starts, so it is read
  // once, and not at all when no screen depends on it.
  const [capabilities, setCapabilities] = useState<Capability[]>([]);
  const [data, setData] = useState<DashboardData>({
    projects: [],
    health: [],
    defaults: emptyDefaults,
  });
  const [error, setError] = useState("");
  // The selection is the project's ID, and the project is looked up in the
  // dashboard each render, so a removed project is never still selected and
  // a renamed one shows its new fields. Before any choice (undefined), and
  // when the chosen project is gone, it falls back to the first project;
  // null is a choice of none, which Agents offers for the global agents.
  const [selectedProjectID, setSelectedProjectID] = useState<
    string | null | undefined
  >(undefined);
  const selectedProject = useMemo(
    () =>
      selectedProjectID === null
        ? null
        : (data.projects.find((project) => project.id === selectedProjectID) ??
          data.projects[0] ??
          null),
    [data.projects, selectedProjectID],
  );
  const setSelectedProject = useCallback(
    (project: Project | null) => setSelectedProjectID(project?.id ?? null),
    [],
  );
  const [workflowPlan, setWorkflowPlan] = useState<WorkflowPlan | null>(null);
  // The Workflow screen's accepted edits, kept while another screen is
  // open. A new plan or rerun drops them.
  const [workflowDraft, setWorkflowDraft] = useState<WorkflowDraft | null>(
    null,
  );
  // The request behind the plan, so the workflow view can plan it again
  // under another rule.
  const [workflowRequest, setWorkflowRequest] =
    useState<WorkflowRequest | null>(null);
  const [selectedRunID, setSelectedRunID] = useState<string | null>(null);
  // A rerun of a stopped run shows on the Workflow screen in place of a new
  // plan, from the run's plan with the fixes the person ticked.
  const [rerun, setRerun] = useState<{
    runID: string;
    plan: WorkflowPlan;
    resume: RunResume;
    refused?: string;
  } | null>(null);
  const [selectedThreadID, setSelectedThreadID] = useState<string | null>(null);
  // The screens with edits not yet saved: leaving one asks first, as its
  // edits live only in it. Each screen says so through onDirtyChange.
  const [dirtyViews, setDirtyViews] = useState<ReadonlySet<View>>(new Set());
  const dirtyChange = useMemo(() => {
    const reporters = new Map<View, (dirty: boolean) => void>();
    return (id: View) => {
      let report = reporters.get(id);
      if (!report) {
        report = (dirty: boolean) =>
          setDirtyViews((current) => {
            if (current.has(id) === dirty) return current;
            const next = new Set(current);
            if (dirty) next.add(id);
            else next.delete(id);
            return next;
          });
        reporters.set(id, report);
      }
      return report;
    };
  }, []);
  // A rerun's changes Go made to its rule, which the Rules view shows
  // unsaved; leaving the view drops them.
  const [rulesProposal, setRulesProposal] = useState<RuleEdits | null>(null);
  const [blockedView, setBlockedView] = useState<View | null>(null);
  // What is shown now, for work that finishes after an await, when the
  // values its closure captured may be stale.
  const latest = useRef({ plan: workflowPlan, rerun, view });
  useEffect(() => {
    latest.current = { plan: workflowPlan, rerun, view };
  }, [workflowPlan, rerun, view]);

  const refreshDashboard = useCallback(async () => {
    try {
      const dashboard = await getDashboard();
      setData(dashboard);
      setError("");
    } catch (reason) {
      setError(String(reason));
    }
  }, []);

  useEffect(() => {
    void refreshDashboard();
  }, [refreshDashboard]);

  useEffect(() => {
    if (views.length === 0) return;
    getCapabilities()
      .then((info) => setCapabilities(info.capabilities ?? []))
      .catch((reason: unknown) => setError(String(reason)));
  }, [views]);
  const shown = useMemo(
    () => shownViews(views, capabilities),
    [views, capabilities],
  );
  const navGroups = useMemo(() => navigation(shown), [shown]);
  // Opens a screen from the navigation, once leaving the current one is
  // settled. Everything it changes waits for that, since a change of
  // project resets what a screen holds, as Agents' form.
  function navigate(target: View) {
    // A request needs a project: none, as Agents can choose, goes back to
    // the first.
    if (target === "request" && selectedProjectID === null) {
      setSelectedProjectID(undefined);
    }
    if (target !== "rules") setRulesProposal(null);
    setView(target);
  }
  const viewLabel =
    navGroups.flatMap((group) => group.items).find((item) => item.id === view)
      ?.label ?? "this screen";
  const extensionView = shown.find((candidate) => candidate.id === view);

  // Opens the Workflow screen on a rerun of a stopped run, with the fixes
  // the person ticked made and Go's rebuilding of them shown. A fix Go
  // refuses leaves the run's plan as it was, with Go's reason.
  async function openRerun(run: WorkflowRun, suggestions: PlanSuggestion[]) {
    try {
      const started = await planRerun(run.id);
      let plan = started.plan;
      let resume = started.resume;
      let refused: string | undefined;
      if (suggestions.length > 0) {
        try {
          const revised = await reviseRerun(
            run.id,
            JSON.stringify({
              ...plan.workflow,
              steps: withSuggestions(plan.workflow.steps, suggestions),
            }),
          );
          plan = { ...plan, workflow: revised.workflow };
          resume = revised.resume;
        } catch (reason) {
          refused = String(reason);
        }
      }
      setRerun({ runID: run.id, plan, resume, refused });
      setWorkflowDraft(null);
      setView("workflow");
      setError("");
    } catch (reason) {
      setError(String(reason));
    }
  }
  useInkunaEvent("inkuna:inbox", refreshDashboard);
  useInkunaEvent("inkuna:runs", refreshDashboard);

  const pending = data.pendingInputs ?? 0;
  // Tasks and Costs list history, which a removed project keeps, so they
  // name removed projects too.
  const historyProjects = [
    ...data.projects,
    ...(data.removedProjects ?? []).map((project) => ({
      ...project,
      name: `${project.name} (removed)`,
    })),
  ];

  return (
    <div className="grid min-h-screen grid-cols-[236px_minmax(0,1fr)] max-[900px]:grid-cols-[72px_minmax(0,1fr)]">
      <aside className="sticky top-0 flex h-screen flex-col overflow-y-auto border-r border-line bg-sidebar px-4 py-7 max-[900px]:px-2.5">
        <div className="flex items-center gap-3 px-2 pb-7 text-lg font-bold max-[900px]:justify-center max-[900px]:px-0">
          <span className="grid size-9 flex-none place-items-center rounded-lg border border-line bg-raised">
            <Logo size={30} />
          </span>
          <span className="max-[900px]:sr-only">
            Inkuna
            <small className="mt-0.5 block font-mono text-[9px] font-normal tracking-[0.12em] text-faint">
              LOCAL AGENT HARNESS
            </small>
          </span>
        </div>
        <nav aria-label="Primary" className="flex flex-col gap-5">
          {navGroups.map((group) => (
            <div
              key={group.label ?? group.items[0].id}
              role="group"
              aria-label={group.label}
              className="flex flex-col gap-1 max-[900px]:border-t max-[900px]:border-line max-[900px]:pt-3 max-[900px]:first:border-t-0 max-[900px]:first:pt-0"
            >
              {group.label && (
                <h2 className="m-0 px-3 pb-1 font-mono text-[10px] font-normal tracking-[0.16em] text-faint uppercase max-[900px]:sr-only">
                  {group.label}
                </h2>
              )}
              {group.items.map((item) => {
                const active = view === item.id;
                return (
                  <button
                    key={item.id}
                    aria-current={active ? "page" : undefined}
                    title={item.label}
                    className={`relative flex w-full items-center gap-3 rounded-md border-0 px-3 py-2 text-left text-sm max-[900px]:justify-center max-[900px]:px-0 ${
                      active
                        ? "bg-raised text-ink-strong"
                        : "bg-transparent text-muted hover:text-ink-strong"
                    }`}
                    onClick={() => {
                      // Unsaved edits live in their screen; ask before
                      // navigation throws them away, and change nothing
                      // until it is confirmed.
                      if (dirtyViews.has(view) && item.id !== view) {
                        setBlockedView(item.id);
                        return;
                      }
                      navigate(item.id);
                    }}
                  >
                    <span
                      aria-hidden="true"
                      className="w-5 flex-none text-center font-mono text-base"
                    >
                      {item.glyph}
                    </span>
                    <span className="min-w-0 truncate max-[900px]:sr-only">
                      {item.label}
                    </span>
                    {item.id === "messages" && pending > 0 && (
                      <span
                        aria-label={`${pending} awaiting reply`}
                        className="ml-auto flex-none rounded-full bg-accent px-1.5 py-0.5 font-mono text-[10px] leading-none font-bold text-accent-ink max-[900px]:absolute max-[900px]:top-0.5 max-[900px]:right-1.5 max-[900px]:ml-0"
                      >
                        {pending}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          ))}
        </nav>
        <div className="mt-auto border-t border-line px-2.5 pt-4 text-xs max-[900px]:hidden">
          <span className="mr-2 inline-block size-[7px] rounded-full bg-ok shadow-[0_0_8px_#6bcf9b88]" />
          Review gates active
          <small className="mt-1 ml-[15px] block text-faint">
            No writes without approval
          </small>
        </div>
      </aside>
      <main className="min-w-0 px-[clamp(28px,5vw,72px)] pt-10 pb-20 max-[600px]:px-[18px] max-[600px]:py-6">
        {error && <Alert>{error}</Alert>}
        {view === "projects" && (
          <Projects
            data={data}
            onChange={setData}
            onCompose={(project) => {
              setSelectedProject(project);
              setView("request");
            }}
            onBranches={(project) => {
              setSelectedProject(project);
              setView("branches");
            }}
            onConnections={() => setView("connections")}
            onSetup={() => setView("setup")}
          />
        )}
        {view === "request" && (
          <RequestComposer
            projects={data.projects}
            selectedProject={selectedProject}
            onProjectChange={setSelectedProject}
            onDirtyChange={dirtyChange("request")}
            onContinue={(plan, request) => {
              setWorkflowPlan(plan);
              setWorkflowRequest(request);
              setWorkflowDraft(null);
              setRerun(null);
              setView("workflow");
            }}
          />
        )}
        {view === "workflow" && rerun && (
          <>
            {rerun.refused && (
              <Alert>Go refused the ticked fixes: {rerun.refused}</Alert>
            )}
            <WorkflowView
              key={rerun.runID}
              defaults={data.defaults}
              plan={rerun.plan}
              draft={workflowDraft}
              onDraft={setWorkflowDraft}
              onRun={async () => {}}
              rerun={{
                resume: rerun.resume,
                revise: (workflowJSON) =>
                  reviseRerun(rerun.runID, workflowJSON),
                onRerun: async (workflow, choice) => {
                  const run = await rerunWorkflowRun(rerun.runID, {
                    ...choice,
                    workflow: JSON.stringify(workflow),
                  });
                  // As a start does: what was planned or opened meanwhile
                  // is left as it is.
                  const shown = latest.current;
                  const stillShown = shown.rerun?.runID === rerun.runID;
                  if (stillShown) setRerun(null);
                  setWorkflowDraft((current) =>
                    current?.workflowID === workflow.id ? null : current,
                  );
                  setSelectedRunID(run.id);
                  if (stillShown && shown.view === "workflow") {
                    setView("tasks");
                  }
                  void refreshDashboard();
                },
                saveToRule: async (workflow, resume) => {
                  const edits = await rerunRuleEdits(
                    rerun.runID,
                    JSON.stringify(workflow),
                  );
                  // The rerun keeps its edits on the Workflow screen, to
                  // come back to once the rule is saved or not.
                  setRerun({
                    ...rerun,
                    plan: { ...rerun.plan, workflow },
                    resume,
                    refused: undefined,
                  });
                  if (
                    data.projects.some(
                      (project) => project.id === edits.projectId,
                    )
                  ) {
                    setSelectedProjectID(edits.projectId);
                  }
                  setRulesProposal(edits);
                  setView("rules");
                },
              }}
            />
          </>
        )}
        {view === "workflow" && !rerun && (
          <WorkflowView
            defaults={data.defaults}
            plan={workflowPlan}
            draft={workflowDraft}
            onDraft={setWorkflowDraft}
            onReplan={
              workflowRequest
                ? async (ruleName, restaff) => {
                    const request = { ...workflowRequest, ruleName };
                    // Re-staffing is this plan's alone; a later replan may
                    // reuse what it decided.
                    setWorkflowPlan(
                      await planWorkflow(
                        restaff ? { ...request, restaff } : request,
                      ),
                    );
                    setWorkflowDraft(null);
                    setWorkflowRequest(request);
                  }
                : undefined
            }
            onRun={async (workflow) => {
              const run = await startWorkflow(JSON.stringify(workflow));
              // The plan has run: the Workflow screen no longer offers it.
              // The start can take a while, so the person may have planned
              // another request or opened another screen meanwhile; that is
              // left as it is.
              const shown = latest.current;
              const stillShown = shown.plan?.workflow.id === workflow.id;
              if (stillShown) {
                setWorkflowPlan(null);
                setWorkflowDraft((current) =>
                  current?.workflowID === workflow.id ? null : current,
                );
                setWorkflowRequest(null);
              }
              setSelectedRunID(run.id);
              if (stillShown && shown.view === "workflow") setView("tasks");
              void refreshDashboard();
            }}
          />
        )}
        {view === "tasks" && (
          <TasksView
            selectedRunID={selectedRunID}
            onSelectRun={setSelectedRunID}
            projects={historyProjects}
            onOpenThread={(taskID) => {
              setSelectedThreadID(taskID);
              setView("messages");
            }}
            onRerun={(run, suggestions) => void openRerun(run, suggestions)}
          />
        )}
        {view === "costs" && (
          <CostsView
            projects={historyProjects}
            onOpenRun={(runID) => {
              setSelectedRunID(runID);
              setView("tasks");
            }}
          />
        )}
        {view === "docs" && (
          <DocsView
            projects={data.projects}
            selectedProject={selectedProject}
            onProjectChange={setSelectedProject}
            onOpenRun={(runID) => {
              setSelectedRunID(runID);
              setView("tasks");
            }}
          />
        )}
        {view === "branches" && (
          <BranchesView
            projects={data.projects}
            selectedProject={selectedProject}
            onProjectChange={setSelectedProject}
          />
        )}
        {view === "messages" && (
          <MessagesView
            selectedThreadID={selectedThreadID}
            onSelectThread={setSelectedThreadID}
            onChanged={refreshDashboard}
          />
        )}
        {view === "agents" && (
          <AgentsView
            projects={data.projects}
            selectedProject={selectedProject}
            onProjectChange={setSelectedProject}
            onDirtyChange={dirtyChange("agents")}
          />
        )}
        {view === "rules" && (
          <RulesView
            projects={data.projects}
            selectedProject={selectedProject}
            onProjectChange={setSelectedProject}
            onDirtyChange={dirtyChange("rules")}
            proposal={rulesProposal}
          />
        )}
        {blockedView && (
          <Modal
            title="Discard unsaved changes?"
            onClose={() => setBlockedView(null)}
          >
            <p className="m-0 text-sm text-muted">
              Leaving {viewLabel} drops the edits you have not saved.
            </p>
            <div className="mt-6 flex justify-end gap-2">
              <Button onClick={() => setBlockedView(null)}>Keep editing</Button>
              <Button
                variant="danger"
                onClick={() => {
                  dirtyChange(view)(false);
                  navigate(blockedView);
                  setBlockedView(null);
                }}
              >
                Discard and leave
              </Button>
            </div>
          </Modal>
        )}
        {view === "runtimes" && <RuntimesView />}
        {view === "setup" && (
          <SetupView
            onSaved={refreshDashboard}
            onDirtyChange={dirtyChange("setup")}
          />
        )}
        {view === "connections" && (
          <ConnectionsView onAuthenticationChange={refreshDashboard} />
        )}
        {/* Keyed so moving between two extension screens remounts it. */}
        {extensionView && (
          <Fragment key={extensionView.id}>{extensionView.render()}</Fragment>
        )}
      </main>
    </div>
  );
}
