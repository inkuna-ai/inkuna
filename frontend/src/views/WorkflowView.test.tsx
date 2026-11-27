import {
  act,
  cleanup,
  fireEvent,
  getByLabelText,
  getByRole,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useLayoutEffect } from "react";
import { createRoot } from "react-dom/client";
import type { AppDefaults, Project, RunResume, WorkflowPlan } from "../types";
import type { RerunChoice } from "./RerunPanel";
import { WorkflowView, type WorkflowDraft } from "./WorkflowView";
import { withSuggestions } from "./tasks/suggestions";

/**
 * A ResizeObserver the test reports sizes through, as a browser does after
 * layout. The shared setup's observer never reports, so React Flow never
 * measures a node.
 */
class ReportingObserver {
  static all: ReportingObserver[] = [];
  readonly targets = new Set<Element>();
  readonly callback: ResizeObserverCallback;
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
    ReportingObserver.all.push(this);
  }
  observe(target: Element) {
    this.targets.add(target);
  }
  unobserve(target: Element) {
    this.targets.delete(target);
  }
  disconnect() {
    this.targets.clear();
  }
  report() {
    const entries = [...this.targets].map(
      (target) => ({ target }) as ResizeObserverEntry,
    );
    this.callback(entries, this as unknown as ResizeObserver);
  }
}

/** Reports every observed size from a layout effect once `pass` is set. */
function ReportAfterLayout({ pass }: { pass: boolean }) {
  useLayoutEffect(() => {
    if (pass) for (const observer of ReportingObserver.all) observer.report();
  }, [pass]);
  return null;
}

/**
 * Runs a test with the sizes a browser lays out: every element is 200×80,
 * and React Flow measures its nodes through ReportingObserver.
 */
async function withLayout(run: () => void | Promise<void>) {
  const observer = globalThis.ResizeObserver;
  const width = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "offsetWidth",
  );
  const height = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "offsetHeight",
  );
  const matrix = window.DOMMatrixReadOnly;
  ReportingObserver.all = [];
  globalThis.ResizeObserver = ReportingObserver;
  // React Flow reads the zoom from the viewport's transform; jsdom has no
  // DOMMatrixReadOnly.
  window.DOMMatrixReadOnly = class {
    m22 = 1;
  } as unknown as typeof DOMMatrixReadOnly;
  Object.defineProperty(HTMLElement.prototype, "offsetWidth", {
    configurable: true,
    get: () => 200,
  });
  Object.defineProperty(HTMLElement.prototype, "offsetHeight", {
    configurable: true,
    get: () => 80,
  });
  try {
    await run();
  } finally {
    globalThis.ResizeObserver = observer;
    window.DOMMatrixReadOnly = matrix;
    if (width)
      Object.defineProperty(HTMLElement.prototype, "offsetWidth", width);
    if (height)
      Object.defineProperty(HTMLElement.prototype, "offsetHeight", height);
  }
}

const project: Project = {
  id: "project-test",
  name: "Inkuna",
  repository: "/tmp/inkuna",
  defaultRuntime: "copilot",
  createdAt: "2026-01-01T00:00:00Z",
};

const defaults: AppDefaults = {
  maxConcurrency: 2,
  planningSeconds: 300,
  workerSeconds: 1800,
  verifierSeconds: 900,
  approvalSeconds: 900,
  routingPolicy: { version: 1, bands: {} },
};

const plan = (): WorkflowPlan => ({
  project,
  task: {
    version: 1,
    id: "task-test",
    kind: "implementation",
    objective: "Fix cancellation",
    source: { kind: "request", repository: project.repository },
    acceptanceCriteria: [],
    repository: project.repository,
    priority: 1,
    budget: { wallTimeSeconds: 1800 },
  },
  workflow: {
    version: 1,
    id: "workflow-test",
    taskId: "task-test",
    defaultRuntime: "copilot",
    routingPolicyVersion: 1,
    maxConcurrency: 2,
    createdAt: "2026-01-01T00:00:00Z",
    steps: [
      {
        id: "worker",
        title: "Implement request",
        kind: "agent",
        agent: "implementer",
        runtime: "copilot",
        complexity: { score: 8, rationale: "cross-cutting change" },
        permission: "worktree-write",
        selectedSkills: [],
        dependsOn: [],
        allowedPaths: ["."],
        budget: { wallTimeSeconds: 1800, maxOutputBytes: 4194304 },
      },
    ],
  },
  candidates: [
    {
      name: "documenter",
      description: "Writes docs",
      permission: "worktree-write",
      source: "builtin",
    },
    {
      name: "implementer",
      description: "Implements",
      permission: "worktree-write",
      source: "builtin",
    },
  ],
});

const agent = "Agent for Implement request";

describe("WorkflowView", () => {
  beforeEach(() => {
    // Go accepts the edit as sent.
    window.go = {
      app: {
        Service: { ReviseWorkflow: async (json: string) => JSON.parse(json) },
      },
    } as unknown as Window["go"];
  });
  afterEach(() => {
    // Unmount before the bindings go, so an effect still pending when a test
    // ends never calls a binding that is gone.
    cleanup();
    delete window.go;
  });

  it("keeps an edit made before the view's first effects run", async () => {
    // Rendered as a scheduled update, not inside act, the view can be on
    // screen before React runs its effects; the edit lands in that gap, as
    // it did when findBy* found the select in CI.
    const environment = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
    const actEnvironment = environment.IS_REACT_ACT_ENVIRONMENT;
    environment.IS_REACT_ACT_ENVIRONMENT = false;
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    let started = "";
    try {
      const edited = new Promise<void>((resolve) => {
        const observer = new MutationObserver(() => {
          const select = container.querySelector(`[aria-label="${agent}"]`);
          if (!select) return;
          observer.disconnect();
          fireEvent.change(select, { target: { value: "documenter" } });
          resolve();
        });
        observer.observe(container, { childList: true, subtree: true });
      });
      root.render(
        <WorkflowView
          defaults={defaults}
          plan={plan()}
          onRun={async (workflow) => {
            started = JSON.stringify(workflow);
          }}
        />,
      );
      await edited;
      const approve = await waitFor(() => {
        const button = getByRole(container, "button", {
          name: "Approve & run",
        });
        expect(button).toBeEnabled();
        return button;
      });
      expect(getByLabelText(container, agent)).toHaveValue("documenter");
      fireEvent.click(approve);
      await waitFor(() => expect(started).not.toBe(""));
      expect(
        (JSON.parse(started) as WorkflowPlan["workflow"]).steps[0].agent,
      ).toBe("documenter");
    } finally {
      root.unmount();
      container.remove();
      environment.IS_REACT_ACT_ENVIRONMENT = actEnvironment;
    }
  });

  it("shows what each orchestrator call used while planning", () => {
    const shown = plan();
    const call = {
      taskId: "task-test",
      turn: 1,
      kind: "orchestrator",
      agent: "orchestrator",
      runtime: "claude" as const,
      model: "haiku",
      startedAt: "2026-01-01T00:00:00Z",
      finishedAt: "2026-01-01T00:00:10Z",
    };
    shown.usage = [
      {
        ...call,
        id: 1,
        stepId: "staffing",
        callId: "orchestrator-task-test",
        usage: { inputTokens: 6, cacheWriteTokens: 12000, outputTokens: 400 },
        promptBytes: 18000,
        outcome: "completed",
      },
      {
        ...call,
        id: 2,
        stepId: "branching",
        callId: "branching-task-test",
        usage: { inputTokens: 3, cacheReadTokens: 12000, outputTokens: 50 },
        promptBytes: 4000,
        outcome: "timed-out",
      },
    ];
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const items = within(
      screen.getByRole("list", { name: "Orchestrator usage" }),
    ).getAllByRole("listitem");
    expect(items[0]).toHaveTextContent(
      "Orchestrator staffing · claude haiku · 12k in · 0% cached · 400 out · 18,000 prompt bytes",
    );
    expect(items[1]).toHaveTextContent(
      "Orchestrator branching · claude haiku · 12k in · 100% cached · 50 out · 4,000 prompt bytes · timed-out",
    );
  });

  it("shows each review gate and check loop as Go planned it", () => {
    const shown = plan();
    const base = {
      permission: "read-only",
      budget: { wallTimeSeconds: 900 },
      dependsOn: ["worker"],
    };
    shown.workflow.steps.push(
      {
        ...base,
        id: "verify",
        title: "Verify",
        kind: "evaluation",
        onFailure: "feedback",
        feedbackTarget: "worker",
        maxRounds: 1,
        systemExitCodes: [3],
      },
      {
        ...base,
        id: "review",
        title: "Review",
        kind: "approval",
        review: true,
        feedbackTarget: "worker",
      },
      { ...base, id: "gate", title: "Gate", kind: "approval", review: true },
      { ...base, id: "done", title: "Done", kind: "approval" },
    );
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const rows = within(
      screen.getByRole("table", { name: "Accessible workflow list" }),
    ).getAllByRole("row");
    expect(rows[2]).toHaveTextContent(
      "evaluation failures → worker, up to 1 round; exit 3 pauses for you",
    );
    expect(rows[3]).toHaveTextContent("approval pauses for review → worker");
    expect(rows[4]).toHaveTextContent(
      "approval pauses for review · approve only",
    );
    expect(rows[5]).toHaveTextContent(/^Doneapproval—/);
  });

  it("marks each step from a shared definition and says what the flow overrode", () => {
    const shown = plan();
    const base = {
      permission: "read-only",
      budget: { wallTimeSeconds: 900 },
      dependsOn: ["worker"],
    };
    shown.workflow.steps.push(
      {
        ...base,
        id: "verify",
        title: "Verify",
        kind: "evaluation",
        from: {
          stage: "verify",
          groups: ["verify-and-approve"],
          scope: "global",
          file: "/stages/checks.yaml",
          overrides: ["commands", "continueOnFailure"],
        },
      },
      {
        ...base,
        id: "approval",
        title: "Review changes",
        kind: "approval",
        from: { stage: "approval", scope: "builtin" },
      },
    );
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const rows = within(
      screen.getByRole("table", { name: "Accessible workflow list" }),
    ).getAllByRole("row");
    // A stage written inline in the flow has no badge.
    expect(within(rows[1]).queryByText(/^shared:/)).toBeNull();
    const verify = within(rows[2]).getByText(
      "shared: verify-and-approve (global)",
    );
    const popover = within(rows[2]).getByRole("group", {
      name: "Where Verify came from",
    });
    expect(popover).not.toBeVisible();
    fireEvent.click(verify);
    expect(popover).toBeVisible();
    expect(popover).toHaveTextContent(
      "From shared stage verify, in group verify-and-approve.",
    );
    expect(popover).toHaveTextContent(
      "Defined in /stages/checks.yaml (global).",
    );
    expect(popover).toHaveTextContent(
      "Overridden by this flow: commands, continueOnFailure.",
    );
    fireEvent.click(within(rows[3]).getByText("shared: approval (built-in)"));
    expect(
      within(rows[3]).getByRole("group", {
        name: "Where Review changes came from",
      }),
    ).toHaveTextContent(
      "Built into Inkuna.This flow overrides none of its fields.",
    );
  });

  it("shows the reasoning effort routing gave each step beside its model", () => {
    const shown = plan();
    shown.workflow.steps[0].modelSelection = {
      model: "opus",
      reasoningEffort: "medium",
      source: "policy",
      rationale: "complexity 8 routes claude to opus at medium effort",
    };
    shown.workflow.steps.push({
      id: "triage",
      title: "Triage",
      kind: "agent",
      agent: "implementer",
      runtime: "copilot",
      complexity: { score: 3, rationale: "reads the tracker" },
      permission: "read-only",
      budget: { wallTimeSeconds: 900 },
      modelSelection: {
        model: "claude-haiku-4.5",
        source: "policy",
        rationale: "complexity 3 falls in the 1-3 copilot band",
      },
    });
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const rows = within(
      screen.getByRole("table", { name: "Accessible workflow list" }),
    ).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("copilotopus · mediumworktree-write");
    expect(rows[2]).toHaveTextContent("copilotclaude-haiku-4.5read-only");
    expect(
      screen.getAllByText("implementer · copilot · opus · medium").length,
    ).toBeGreaterThan(0);
  });

  it("lists each step's MCP servers and what they can reach", () => {
    const shown = plan();
    shown.mcpServers = [
      {
        name: "docs-search",
        description: "Searches the design archive",
        command: ["docs-mcp", "--stdio"],
        tools: ["search"],
      },
      {
        name: "linear",
        url: "https://mcp.linear.app/mcp",
        tools: ["get_issue"],
        tracker: "linear",
      },
    ];
    shown.workflow.steps[0].mcpServers = ["docs-search"];
    shown.workflow.steps.push({
      id: "triage",
      title: "Triage",
      kind: "agent",
      agent: "implementer",
      runtime: "copilot",
      complexity: { score: 3, rationale: "reads the tracker" },
      permission: "read-only",
      budget: { wallTimeSeconds: 900 },
      mcpServers: ["docs-search", "linear"],
    });
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const [writer, reader] = screen.getAllByRole("region", {
      name: "MCP servers",
    });
    expect(writer).toHaveTextContent(
      "docs-search · Searches the design archiveTools: search · docs-mcp --stdio",
    );
    // The worktree permission covers files, so only a read-only step is
    // told its servers reach further.
    expect(writer).not.toHaveTextContent("Read-only covers the worktree");
    expect(reader).toHaveTextContent(
      "Read-only covers the worktree only: through these servers the step can change systems outside it.",
    );
    expect(reader).toHaveTextContent(
      "linear linear · declared read toolsTools: get_issue · https://mcp.linear.app/mcp",
    );
    const rows = within(
      screen.getByRole("table", { name: "Accessible workflow list" }),
    ).getAllByRole("row");
    expect(rows[2]).toHaveTextContent("docs-search, linear");
  });

  it("says which tracker changes a step may ask for", () => {
    const shown = plan();
    shown.workflow.steps[0].trackerKinds = ["comment", "create"];
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    expect(
      screen.getByText(/May ask for tracker changes: comment, create\./),
    ).toHaveTextContent("made only when you approve the run there");
  });

  it("lists the user's own servers a step's runtime gives it", () => {
    const shown = plan();
    shown.workflow.steps[0].userMcp = ["linear-server", "claude.ai Gmail"];
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const own = screen.getByRole("region", { name: "Your MCP servers" });
    expect(own).toHaveTextContent(
      "nothing reviews what the step changes through them, a tracker included",
    );
    expect(own).toHaveTextContent("linear-server yours · all tools");
    expect(own).toHaveTextContent("claude.ai Gmail yours · all tools");
    const rows = within(
      screen.getByRole("table", { name: "Accessible workflow list" }),
    ).getAllByRole("row");
    expect(rows[1]).toHaveTextContent(
      "linear-server (yours), claude.ai Gmail (yours)",
    );
  });

  it("says when staffing reused an identical plan's decision", () => {
    const shown = plan();
    shown.staffingReused = "2026-10-18T14:02:00Z";
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const items = within(
      screen.getByRole("list", { name: "Orchestrator usage" }),
    ).getAllByRole("listitem");
    expect(items).toHaveLength(1);
    expect(items[0]).toHaveTextContent(
      /^Orchestrator staffing · reused the decision made .+ for the same inputs · no call$/,
    );
  });

  it("re-staffs a plan whose auto stages were staffed", async () => {
    const replans: Array<[string, boolean | undefined]> = [];
    const onReplan = async (ruleName: string, restaff?: boolean) => {
      replans.push([ruleName, restaff]);
    };
    const view = render(
      <WorkflowView
        defaults={defaults}
        plan={plan()}
        onRun={async () => {}}
        onReplan={onReplan}
      />,
    );
    // Every stage was named by a rule, so there is no staffing to redo.
    expect(screen.queryByRole("button", { name: "Re-staff" })).toBeNull();
    const staffed = plan();
    staffed.ruleName = "bugfix";
    staffed.decision = {
      assignments: [
        {
          stageId: "worker",
          agent: "implementer",
          skills: [],
          complexity: { score: 8, rationale: "cross-cutting change" },
          rationale: "writes code",
        },
      ],
    };
    view.rerender(
      <WorkflowView
        defaults={defaults}
        plan={staffed}
        onRun={async () => {}}
        onReplan={onReplan}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Re-staff" }));
    await waitFor(() => expect(replans).toEqual([["bugfix", true]]));
  });

  it("starts again from a new plan, dropping edits to the old one", async () => {
    const view = render(
      <WorkflowView defaults={defaults} plan={plan()} onRun={async () => {}} />,
    );
    fireEvent.change(screen.getByLabelText(agent), {
      target: { value: "documenter" },
    });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Approve & run" }),
      ).toBeEnabled(),
    );
    expect(screen.getByLabelText(agent)).toHaveValue("documenter");
    view.rerender(
      <WorkflowView defaults={defaults} plan={plan()} onRun={async () => {}} />,
    );
    expect(screen.getByLabelText(agent)).toHaveValue("implementer");
  });

  it("draws the graph when a render lands between measuring and drawing it", () =>
    withLayout(() => {
      // A browser reports sizes after a commit's layout and before its
      // passive effects run. A render that handed React Flow new node objects
      // then replaced the measured nodes before they were drawn, and the graph
      // stayed hidden.
      const shown = plan();
      const tree = (pass: boolean) => (
        <>
          <WorkflowView
            defaults={defaults}
            plan={shown}
            onRun={async () => {}}
          />
          <ReportAfterLayout pass={pass} />
        </>
      );
      const view = render(tree(false));
      const node = () =>
        view.container.querySelector<HTMLElement>(".react-flow__node");
      expect(node()).toHaveStyle({ visibility: "hidden" });
      // Nothing about the steps changes, as when the rules finish loading.
      view.rerender(tree(true));
      expect(node()).toHaveStyle({ visibility: "visible" });
    }));

  it("keeps the graph drawn when Go's revision of an edit lands as it is measured", () =>
    withLayout(async () => {
      // An edit hands React Flow new nodes twice: the edit, then Go's
      // revision with the model it routed. A revision landing in the commit
      // that measured the first threw the measurement away, nothing measured
      // the nodes again, and the whole graph stayed hidden.
      let revise = () => {};
      window.go = {
        app: {
          Service: {
            ReviseWorkflow: (json: string) =>
              new Promise((resolve) => {
                const workflow = JSON.parse(json) as WorkflowPlan["workflow"];
                revise = () =>
                  resolve({
                    ...workflow,
                    steps: workflow.steps.map((step) => ({
                      ...step,
                      modelSelection: {
                        model: "opus",
                        source: "policy",
                        rationale: "complexity 3",
                      },
                    })),
                  });
              }),
          },
        },
      } as unknown as Window["go"];
      // Rendered as scheduled updates, not inside act, so that the revision
      // and the browser's report of sizes can land in one commit.
      const environment = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
      const actEnvironment = environment.IS_REACT_ACT_ENVIRONMENT;
      environment.IS_REACT_ACT_ENVIRONMENT = false;
      const container = document.createElement("div");
      document.body.append(container);
      const root = createRoot(container);
      try {
        const shown = plan();
        const tree = (pass: boolean) => (
          <>
            <WorkflowView
              defaults={defaults}
              plan={shown}
              onRun={async () => {}}
            />
            <ReportAfterLayout pass={pass} />
          </>
        );
        const node = () =>
          container.querySelector<HTMLElement>(".react-flow__node");
        root.render(tree(false));
        // React Flow observes its nodes in passive effects, which can run
        // after the node is on screen, so a single report could find nothing
        // to measure and leave the graph hidden; report until it is drawn.
        await waitFor(() => {
          for (const observer of ReportingObserver.all) observer.report();
          expect(node()).toHaveStyle({ visibility: "visible" });
        });
        fireEvent.change(
          getByLabelText(container, "Complexity score for Implement request"),
          { target: { value: "3" } },
        );
        revise();
        // Microtasks only: the revision reaches the view without rendering,
        // then renders with the browser's report of the edited nodes' sizes.
        for (let tick = 0; tick < 10; tick += 1) await Promise.resolve();
        root.render(tree(true));
        await waitFor(() =>
          expect(node()).toHaveTextContent("implementer · copilot · opus"),
        );
        // React Flow takes the revised nodes in the commit's passive effects,
        // after the text above is on screen.
        await new Promise((resolve) => setTimeout(resolve, 20));
        expect(node()).toHaveStyle({ visibility: "visible" });
      } finally {
        root.unmount();
        container.remove();
        environment.IS_REACT_ACT_ENVIRONMENT = actEnvironment;
      }
    }));

  it("renames the task and its branch before approval, and folds the details", async () => {
    const shown = plan();
    shown.task = {
      ...shown.task,
      title: "Fix cancellation",
      objective: "Cancelling a run leaves its worker running.\nStop it.",
      acceptanceCriteria: ["The worker stops within a second"],
    };
    shown.workflow = {
      ...shown.workflow,
      branching: {
        worktree: true,
        branch: "feature/fix-cancellation",
        base: "main",
        cleanup: "manual",
      },
    };
    const renamed: Array<[string, string]> = [];
    window.go = {
      app: {
        Service: {
          ReviseWorkflow: async (json: string) => JSON.parse(json),
          SetPlanTitle: async (workflowID: string, title: string) => {
            renamed.push([workflowID, title]);
            if (title.length > 20) throw new Error("the title is too long");
            return {
              task: { ...shown.task, title: title.trim() },
              branching: {
                ...shown.workflow.branching,
                branch: "feature/stop-the-worker",
              },
            };
          },
        },
      },
    } as unknown as Window["go"];
    let started = "";
    render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        onRun={async (workflow) => {
          started = JSON.stringify(workflow);
        }}
      />,
    );
    const title = screen.getByRole("textbox", { name: "Task title" });
    expect(title).toHaveValue("Fix cancellation");
    expect(screen.getByLabelText("Branch plan")).toHaveTextContent(
      "feature/fix-cancellation",
    );
    // The details stay folded until opened.
    expect(screen.queryByText(/leaves its worker running/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "▸ Details" }));
    expect(screen.getByText(/leaves its worker running/)).toBeVisible();
    expect(
      screen.getByText("The worker stops within a second"),
    ).toBeInTheDocument();

    // Go's reason shows when it refuses a title.
    fireEvent.change(title, {
      target: { value: "A title far too long to keep" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save title" }));
    expect(await screen.findByText(/the title is too long/)).toBeVisible();

    fireEvent.change(title, { target: { value: "Stop the worker " } });
    fireEvent.click(screen.getByRole("button", { name: "Save title" }));
    await waitFor(() =>
      expect(screen.getByLabelText("Branch plan")).toHaveTextContent(
        "feature/stop-the-worker",
      ),
    );
    expect(title).toHaveValue("Stop the worker");
    expect(screen.queryByRole("button", { name: "Save title" })).toBeNull();
    expect(renamed.at(-1)).toEqual(["workflow-test", "Stop the worker "]);

    fireEvent.click(screen.getByRole("button", { name: "Approve & run" }));
    await waitFor(() => expect(started).not.toBe(""));
    expect(JSON.parse(started).branching.branch).toBe(
      "feature/stop-the-worker",
    );
  });

  // Go holds the score to 1-10 and a title to 100 characters; the view
  // sends what was typed and shows Go's reason.
  it("sends a score and a title as typed, and shows why Go refuses them", async () => {
    const shown = plan();
    const sent: number[] = [];
    window.go = {
      app: {
        Service: {
          ReviseWorkflow: async (json: string) => {
            const workflow = JSON.parse(json) as WorkflowPlan["workflow"];
            const score = workflow.steps[0].complexity?.score ?? 0;
            sent.push(score);
            if (score > 10)
              throw new Error(
                'step "implement" needs a complexity score from 1 to 10',
              );
            return workflow;
          },
        },
      },
    } as unknown as Window["go"];
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    fireEvent.change(
      screen.getByLabelText("Complexity score for Implement request"),
      { target: { value: "12" } },
    );
    expect(
      await screen.findByText(/needs a complexity score from 1 to 10/),
    ).toBeInTheDocument();
    expect(sent).toEqual([12]);
    expect(
      screen.getByRole("textbox", { name: "Task title" }),
    ).not.toHaveAttribute("maxlength");
  });

  it("comes back with the steps and title Go accepted, and not a refused edit", async () => {
    const shown = plan();
    shown.task = { ...shown.task, title: "Fix cancellation" };
    shown.workflow = {
      ...shown.workflow,
      branching: {
        worktree: true,
        branch: "feature/fix-cancellation",
        base: "main",
        cleanup: "manual",
      },
    };
    let refuse = false;
    window.go = {
      app: {
        Service: {
          ReviseWorkflow: async (json: string) => {
            if (refuse) throw new Error("agent reviewer is read-only");
            return JSON.parse(json);
          },
          SetPlanTitle: async (_workflowID: string, title: string) => ({
            task: { ...shown.task, title },
            branching: {
              ...shown.workflow.branching,
              branch: "feature/stop-the-worker",
            },
          }),
        },
      },
    } as unknown as Window["go"];
    let draft: WorkflowDraft | null = null;
    const view = render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        onRun={async () => {}}
        onDraft={(update) => {
          draft = update(draft);
        }}
      />,
    );
    const score = screen.getByLabelText(
      "Complexity score for Implement request",
    );
    fireEvent.change(score, { target: { value: "3" } });
    await waitFor(() => expect(draft?.steps[0].complexity?.score).toBe(3));
    fireEvent.change(screen.getByRole("textbox", { name: "Task title" }), {
      target: { value: "Stop the worker" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save title" }));
    await waitFor(() =>
      expect(draft?.retitled?.task.title).toBe("Stop the worker"),
    );
    // Go refuses the next edit, so it is not kept.
    refuse = true;
    fireEvent.change(score, { target: { value: "5" } });
    expect(
      await screen.findByText(/agent reviewer is read-only/),
    ).toBeInTheDocument();
    view.unmount();

    let started = "";
    render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        draft={draft}
        onRun={async (workflow) => {
          started = JSON.stringify(workflow);
        }}
      />,
    );
    expect(
      screen.getByLabelText("Complexity score for Implement request"),
    ).toHaveValue(3);
    expect(screen.getByRole("textbox", { name: "Task title" })).toHaveValue(
      "Stop the worker",
    );
    fireEvent.click(screen.getByRole("button", { name: "Approve & run" }));
    await waitFor(() => expect(started).not.toBe(""));
    const workflow = JSON.parse(started) as WorkflowPlan["workflow"];
    expect(workflow.steps[0].complexity?.score).toBe(3);
    expect(workflow.branching?.branch).toBe("feature/stop-the-worker");
  });

  it("drops a revision that comes back after the screen closed, and keeps a title Go saved", async () => {
    const shown = plan();
    shown.task = { ...shown.task, title: "Fix cancellation" };
    // The first screen's revision and title save answer only once it closed.
    let answerRevision = () => {};
    let answerTitle = () => {};
    let late = true;
    window.go = {
      app: {
        Service: {
          ReviseWorkflow: (json: string) =>
            late
              ? new Promise((resolve) => {
                  answerRevision = () => resolve(JSON.parse(json));
                })
              : Promise.resolve(JSON.parse(json)),
          SetPlanTitle: (_workflowID: string, title: string) =>
            new Promise((resolve) => {
              answerTitle = () =>
                resolve({
                  task: { ...shown.task, title },
                  branching: shown.workflow.branching,
                });
            }),
        },
      },
    } as unknown as Window["go"];
    let draft: WorkflowDraft | null = null;
    const keep = (update: (current: WorkflowDraft | null) => WorkflowDraft) => {
      draft = update(draft);
    };
    const first = render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        onRun={async () => {}}
        onDraft={keep}
      />,
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Task title" }), {
      target: { value: "Stop the worker" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save title" }));
    fireEvent.change(
      screen.getByLabelText("Complexity score for Implement request"),
      { target: { value: "7" } },
    );
    first.unmount();

    // The screen opens again and an edit is accepted there.
    late = false;
    render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        draft={draft}
        onRun={async () => {}}
        onDraft={keep}
      />,
    );
    fireEvent.change(
      screen.getByLabelText("Complexity score for Implement request"),
      { target: { value: "3" } },
    );
    await waitFor(() => expect(draft?.steps[0].complexity?.score).toBe(3));

    await act(async () => {
      answerRevision();
      answerTitle();
    });
    // Set only inside callbacks, which TypeScript does not follow.
    const kept = draft as WorkflowDraft | null;
    expect(kept?.steps[0].complexity?.score).toBe(3);
    expect(kept?.retitled?.task.title).toBe("Stop the worker");
  });

  it("ignores a draft kept for another plan", () => {
    const shown = plan();
    render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        draft={{
          workflowID: "another-workflow",
          steps: shown.workflow.steps.map((step) => ({
            ...step,
            complexity: step.complexity && { ...step.complexity, score: 1 },
          })),
        }}
        onRun={async () => {}}
      />,
    );
    expect(
      screen.getByLabelText("Complexity score for Implement request"),
    ).toHaveValue(shown.workflow.steps[0].complexity?.score);
  });

  it("warns when Go says an access edit gives a step more than its rule did", async () => {
    const sent: WorkflowPlan["workflow"][] = [];
    window.go = {
      app: {
        Service: {
          ReviseWorkflow: async (json: string) => {
            const workflow = JSON.parse(json) as WorkflowPlan["workflow"];
            sent.push(workflow);
            const step = workflow.steps[0];
            step.accessEdits = [
              {
                field: "commands",
                note: "Commands changed at approval; the plan had none.",
                widens: true,
              },
              {
                field: "allowedPaths",
                note: 'Allowed paths changed at approval; the plan had ".".',
              },
            ];
            return workflow;
          },
        },
      },
    } as unknown as Window["go"];
    render(
      <WorkflowView defaults={defaults} plan={plan()} onRun={async () => {}} />,
    );
    expect(screen.queryByRole("region", { name: "Widened access" })).toBeNull();
    const commands = screen.getByLabelText("Commands for Implement request");
    // Typing alone sends nothing; leaving the field does.
    fireEvent.change(commands, {
      target: { value: 'go test ./...\ngo vet "./..."' },
    });
    expect(sent).toHaveLength(0);
    fireEvent.blur(commands);
    const warning = await screen.findByRole("region", {
      name: "Widened access",
    });
    expect(warning).toHaveTextContent(
      "Widens what this step may do beyond its rule",
    );
    expect(warning).toHaveTextContent(
      "Commands changed at approval; the plan had none.",
    );
    expect(warning).not.toHaveTextContent("Allowed paths");
    expect(
      screen.getByText('Allowed paths changed at approval; the plan had ".".'),
    ).toBeVisible();
    expect(sent[0].steps[0].commands).toEqual([
      ["go", "test", "./..."],
      ["go", "vet", "./..."],
    ]);
  });

  it("keeps typed commands Go refuses beside its reason, and holds approval", async () => {
    window.go = {
      app: {
        Service: {
          ReviseWorkflow: async () => {
            throw new Error('step "worker": "sh -c make" runs a shell');
          },
        },
      },
    } as unknown as Window["go"];
    render(
      <WorkflowView defaults={defaults} plan={plan()} onRun={async () => {}} />,
    );
    const commands = screen.getByLabelText("Commands for Implement request");
    fireEvent.change(commands, { target: { value: "sh -c make" } });
    fireEvent.blur(commands);
    expect(await screen.findByText(/runs a shell/)).toBeVisible();
    expect(commands).toHaveValue("sh -c make");
    expect(
      screen.getByRole("button", { name: "Approve & run" }),
    ).toBeDisabled();
  });

  it("edits an agent step's permission and paths, and an evaluation step's commands", async () => {
    const sent: WorkflowPlan["workflow"][] = [];
    window.go = {
      app: {
        Service: {
          ReviseWorkflow: async (json: string) => {
            const workflow = JSON.parse(json) as WorkflowPlan["workflow"];
            sent.push(workflow);
            return workflow;
          },
        },
      },
    } as unknown as Window["go"];
    const shown = plan();
    shown.workflow.steps.push({
      id: "verify",
      title: "Verify",
      kind: "evaluation",
      permission: "read-only",
      commands: [["task", "ci"]],
      budget: { wallTimeSeconds: 900 },
    });
    render(
      <WorkflowView defaults={defaults} plan={shown} onRun={async () => {}} />,
    );
    const paths = screen.getByLabelText("Allowed paths for Implement request");
    expect(paths).toHaveValue(".");
    fireEvent.change(paths, { target: { value: "internal/, docs/" } });
    fireEvent.blur(paths);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0].steps[0].allowedPaths).toEqual(["internal/", "docs/"]);

    const checks = screen.getByLabelText("Commands for Verify");
    expect(checks).toHaveValue("task ci");
    fireEvent.change(checks, { target: { value: "task deps\ntask ci" } });
    fireEvent.blur(checks);
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[1].steps[1].commands).toEqual([
      ["task", "deps"],
      ["task", "ci"],
    ]);

    fireEvent.change(
      screen.getByLabelText("Permission for Implement request"),
      {
        target: { value: "read-only" },
      },
    );
    await waitFor(() => expect(sent).toHaveLength(3));
    expect(sent[2].steps[0].permission).toBe("read-only");
    // A read-only step keeps no paths to edit.
    await waitFor(() =>
      expect(
        screen.queryByLabelText("Allowed paths for Implement request"),
      ).toBeNull(),
    );
  });

  it("reruns a stopped run from its plan, with a ticked fix already made", async () => {
    const shown = plan();
    // The fix the person ticked on the failed run, made as App makes it.
    shown.workflow.steps = withSuggestions(shown.workflow.steps, [
      {
        id: "command:worker:go test ./...",
        stepId: "worker",
        text: "Allow go test ./... on step worker",
        command: ["go", "test", "./..."],
      },
    ]);
    const resume: RunResume = {
      round: 1,
      stopped: true,
      rerun: [{ id: "worker", title: "Implement request", state: "failed" }],
      kept: [],
      workspace: "/worktrees/fix",
      changes: [{ status: "??", path: "HALF.md" }],
    };
    const revised: string[] = [];
    const reruns: RerunChoice[] = [];
    render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        onRun={async () => {
          throw new Error("a rerun never starts a first run");
        }}
        rerun={{
          resume,
          revise: async (json) => {
            revised.push(json);
            const workflow = JSON.parse(json) as WorkflowPlan["workflow"];
            return {
              workflow,
              edited: ["worker"],
              resume: { ...resume, refusal: "the run's worktree is gone" },
            };
          },
          onRerun: async (_workflow, choice) => {
            reruns.push(choice);
          },
        }}
      />,
    );
    expect(screen.getByLabelText("Commands for Implement request")).toHaveValue(
      "go test ./...",
    );
    const panel = screen.getByRole("region", { name: "Rerun" });
    expect(panel).toHaveTextContent("Resume runs worker");
    expect(screen.queryByRole("textbox", { name: "Task title" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Approve & run" })).toBeNull();

    fireEvent.click(within(panel).getByLabelText(/Discard them for good/));
    fireEvent.click(screen.getByRole("button", { name: "Discard and resume" }));
    await waitFor(() => expect(reruns).toHaveLength(1));
    expect(reruns[0]).toEqual({
      round: 1,
      startOver: false,
      discard: true,
      changes: ["HALF.md"],
    });

    // An edit is rebuilt against the run's plan, and Go's new outlook shows.
    fireEvent.change(screen.getByLabelText(agent), {
      target: { value: "documenter" },
    });
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "Rerun" })).toHaveTextContent(
        "It cannot be resumed: the run's worktree is gone",
      ),
    );
    expect(revised).toHaveLength(1);
    expect(screen.getByRole("button", { name: /resume/i })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Start over" }));
    await waitFor(() => expect(reruns).toHaveLength(2));
    expect(reruns[1].startOver).toBe(true);
    expect(reruns[1].discard).toBe(false);
  });

  it("hands a rerun's plan to the Rules editor without rerunning it", async () => {
    const shown = plan();
    shown.workflow.steps = withSuggestions(shown.workflow.steps, [
      {
        id: "command:worker:go test ./...",
        stepId: "worker",
        text: "Allow go test ./... on step worker",
        command: ["go", "test", "./..."],
      },
    ]);
    const resume: RunResume = {
      round: 1,
      stopped: true,
      refusal: "the run's worktree is gone",
      rerun: [],
      kept: [],
      workspace: "/worktrees/fix",
      changes: [],
    };
    const saved: Array<{
      workflow: WorkflowPlan["workflow"];
      resume: RunResume;
    }> = [];
    const onRerun = vi.fn();
    render(
      <WorkflowView
        defaults={defaults}
        plan={shown}
        onRun={async () => {}}
        rerun={{
          resume,
          revise: async () => {
            throw new Error("no edit is made here");
          },
          onRerun,
          saveToRule: async (workflow, current) => {
            saved.push({ workflow, resume: current });
          },
        }}
      />,
    );
    // A run that cannot be resumed can still carry its fix into the rule.
    fireEvent.click(
      within(screen.getByRole("region", { name: "Rerun" })).getByRole(
        "button",
        { name: "Save these changes to the rule…" },
      ),
    );
    await waitFor(() => expect(saved).toHaveLength(1));
    const worker = saved[0].workflow.steps.find((step) => step.id === "worker");
    expect(worker?.commands).toContainEqual(["go", "test", "./..."]);
    expect(saved[0].resume).toBe(resume);
    expect(onRerun).not.toHaveBeenCalled();
  });

  it("hands the plan over once, and holds it still until Go answers", async () => {
    const resume: RunResume = {
      round: 1,
      stopped: true,
      rerun: [{ id: "worker", title: "Implement request", state: "failed" }],
      kept: [],
      workspace: "/worktrees/fix",
      changes: [],
    };
    let answer = () => {};
    const saveToRule = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          answer = resolve;
        }),
    );
    render(
      <WorkflowView
        defaults={defaults}
        plan={plan()}
        onRun={async () => {}}
        rerun={{
          resume,
          revise: async () => {
            throw new Error("no edit is made while saving");
          },
          onRerun: async () => {},
          saveToRule,
        }}
      />,
    );
    const save = screen.getByRole("button", {
      name: "Save these changes to the rule…",
    });
    fireEvent.click(save);
    fireEvent.click(save);
    expect(saveToRule).toHaveBeenCalledTimes(1);
    expect(save).toBeDisabled();
    expect(screen.getByRole("button", { name: "Resume" })).toBeDisabled();
    expect(screen.getByLabelText(agent)).toBeDisabled();
    answer();
    await waitFor(() => expect(save).toBeEnabled());
    expect(screen.getByLabelText(agent)).toBeEnabled();
  });
});
