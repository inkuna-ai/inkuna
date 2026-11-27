import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import App from "./App";
import type {
  AppDefaults,
  CapabilitiesInfo,
  ExtensionView,
  AuthenticationStatus,
  ConfigurationInfo,
  Project,
  RulesDocument,
  RunResume,
  RuntimeInfo,
  WorkflowPlan,
  WorkflowRun,
} from "./types";

const defaults: AppDefaults = {
  maxConcurrency: 2,
  planningSeconds: 300,
  workerSeconds: 1800,
  verifierSeconds: 900,
  approvalSeconds: 900,
  routingPolicy: {
    version: 1,
    bands: {
      codex: [
        { minimum: 1, maximum: 3, model: "gpt-5.6-luna" },
        { minimum: 4, maximum: 7, model: "gpt-5.6-terra" },
        { minimum: 8, maximum: 10, model: "gpt-5.6-sol" },
      ],
      claude: [
        { minimum: 1, maximum: 3, model: "fable" },
        { minimum: 4, maximum: 7, model: "sonnet" },
        { minimum: 8, maximum: 10, model: "opus" },
      ],
      copilot: [
        { minimum: 1, maximum: 3, model: "claude-haiku-4.5" },
        { minimum: 4, maximum: 7, model: "claude-sonnet-4.6" },
        { minimum: 8, maximum: 10, model: "gpt-5.3-codex" },
      ],
    },
  },
};

const authentication: AuthenticationStatus = {
  github: {
    provider: "github",
    authenticated: true,
    account: "octocat",
    detail: "Authenticated through gh for github.com",
  },
  claude: {
    provider: "claude",
    authenticated: false,
    detail: "Not authenticated through Claude Code",
  },
  linear: {
    provider: "linear",
    authenticated: false,
    detail: "Not authenticated",
  },
};

const project: Project = {
  id: "project-test",
  name: "Inkuna",
  repository: "/tmp/inkuna",
  defaultRuntime: "copilot",
  createdAt: "2026-01-01T00:00:00Z",
};

const workflowPlan: WorkflowPlan = {
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
        runtime: "copilot",
        complexity: { score: 8, rationale: "cross-cutting change" },
        modelSelection: {
          model: "gpt-5.3-codex",
          policyVersion: 1,
          source: "policy",
          rationale: "complexity 8",
        },
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
    {
      name: "reviewer",
      description: "Reviews",
      permission: "read-only",
      source: "builtin",
    },
  ],
};

/** Routes a copilot step the way the default policy does, from its score. */
function routeCopilot(score: number) {
  return score <= 3
    ? "claude-haiku-4.5"
    : score <= 7
      ? "claude-sonnet-4.6"
      : "gpt-5.3-codex";
}

/** Stands in for Go's ReviseWorkflow: re-routes every agent step. */
async function reviseWorkflowStub(workflowJSON: string) {
  const spec = JSON.parse(workflowJSON) as WorkflowPlan["workflow"];
  return {
    ...spec,
    steps: spec.steps.map((step) =>
      step.kind !== "agent" || !step.complexity
        ? step
        : {
            ...step,
            modelSelection: {
              model: routeCopilot(step.complexity.score),
              policyVersion: 1,
              source: "policy",
              rationale: `complexity ${step.complexity.score}`,
            },
          },
    ),
  };
}

/**
 * Stands in for Go's stage library listing: the built-in definitions, and
 * the edited rules' own, which override a built-in one with their name.
 */
function stageLibrary(documentJSON: string) {
  const document = JSON.parse(documentJSON) as RulesDocument;
  const own = {
    ...(document.stages ?? {}),
    ...(document.groups ?? {}),
  };
  const builtin = [
    {
      name: "approval",
      scope: "builtin" as const,
      kind: "stage" as const,
      stage: { id: "", kind: "approval" },
    },
    {
      name: "verify",
      scope: "builtin" as const,
      kind: "stage" as const,
      stage: { id: "", kind: "evaluation", commands: [["task", "ci"]] },
    },
    {
      name: "verify-and-approve",
      scope: "builtin" as const,
      kind: "group" as const,
      group: {
        stages: [
          { use: "verify", id: "", kind: "" },
          { use: "approval", id: "", kind: "", dependsOn: ["verify"] },
        ],
      },
    },
  ].map((definition) => ({
    ...definition,
    overridden: definition.name in own,
    readOnly:
      "Built-in definitions are part of Inkuna and cannot be edited. Copy one to the repository to change it for this repository's flows.",
  }));
  const repository = Object.entries(document.stages ?? {}).map(
    ([name, stage]) => ({
      name,
      scope: "repository" as const,
      file: "/tmp/inkuna/.inkuna/rules.yaml",
      kind: "stage" as const,
      stage,
    }),
  );
  return {
    definitions: [...builtin, ...repository],
    problems: [],
    overridable: [
      { name: "title" },
      { name: "commands", kinds: ["agent", "evaluation"] },
      { name: "continueOnFailure", kinds: ["evaluation"] },
      { name: "review", kinds: ["approval"] },
      { name: "feedback", kinds: ["approval", "evaluation"] },
    ],
  };
}

/** Rules whose fallback flow uses the built-in verify stage. */
function sharedRules() {
  return {
    document: {
      version: 2,
      source: "",
      rules: [],
      default: {
        stages: [
          { id: "implement", kind: "agent", agent: "auto" },
          { use: "verify", id: "", kind: "", dependsOn: ["implement"] },
          { id: "approve", kind: "approval", dependsOn: ["verify"] },
        ],
      },
    },
    exists: true,
    path: "/tmp/inkuna/.inkuna/rules.yaml",
  };
}

const rulesInfo = {
  document: {
    version: 1,
    rules: [
      {
        name: "bugfix",
        description: "Reproduce before fixing.",
        when: { kind: ["bugfix"] },
        then: { stages: [] },
      },
      {
        name: "read-only",
        when: { kind: ["research"] },
        then: { stages: [] },
      },
    ],
    default: { stages: [] },
    source: "builtin",
  },
  exists: false,
  path: "",
};

const queuedRun: WorkflowRun = {
  id: "run-test",
  workflowId: workflowPlan.workflow.id,
  taskId: workflowPlan.task.id,
  projectId: project.id,
  state: "running",
  title: workflowPlan.task.objective,
  runtime: "copilot",
  model: "gpt-5.3-codex",
  agent: "implementer",
  ruleName: "default",
  orchestrated: true,
  live: true,
  worktree: "/tmp/inkuna-worktree",
  startedAt: "2026-01-01T00:00:00Z",
  steps: [
    {
      id: "worker",
      title: "Implement request",
      kind: "agent",
      agent: "implementer",
      model: "gpt-5.3-codex",
      state: "running",
    },
  ],
  events: [],
};

describe("Inkuna desktop flows", () => {
  beforeEach(() => {
    window.go = {
      app: {
        Service: {
          Dashboard: async () => ({
            projects: [project],
            health: [],
            defaults,
          }),
          AddProject: async (name, repository, runtime) => ({
            id: "project-test",
            name,
            repository,
            defaultRuntime: runtime,
            createdAt: "2026-01-01T00:00:00Z",
          }),
          ImportLinearIssue: async () => {
            throw new Error("not configured for this test");
          },
          AuthenticationStatus: async () => authentication,
          AuthenticateClaude: async () => ({
            provider: "claude",
            authenticated: true,
            detail: "Authenticated through Claude Code via claude.ai",
          }),
          AuthenticateGitHub: async () => authentication.github,
          AuthenticateLinear: async () => ({
            provider: "linear",
            authenticated: true,
            account: "Ada",
            detail: "Authenticated for this Inkuna session",
          }),
          ForgetLinearAuthentication: async () => ({
            provider: "linear",
            authenticated: false,
            detail: "Not authenticated",
          }),
          PlanWorkflow: async () => workflowPlan,
          StartWorkflow: async () => queuedRun,
          ReviseWorkflow: reviseWorkflowStub,
          LatestWorkflowRun: async () => queuedRun,
          WorkflowRun: async () => queuedRun,
          WorkflowRuns: async () => [queuedRun],
          CancelWorkflowRun: async () => ({
            ...queuedRun,
            state: "cancelled",
            finishedAt: "2026-01-01T00:01:00Z",
            events: [
              {
                version: 1,
                runId: "run-test-worker",
                sequence: 0,
                timestamp: "2026-01-01T00:00:30Z",
                kind: "lifecycle",
                summary: "Worktree preserved for inspection",
              },
            ],
          }),
        },
      },
    };
  });

  afterEach(() => {
    // Unmount before the bindings go, so an effect still pending when a test
    // ends never calls a binding that is gone.
    cleanup();
    delete window.go;
    // Tests that stub the Wails runtime must not leak it into the next one.
    delete window.runtime;
  });

  it("groups navigation into work, messages, and configure", async () => {
    render(<App />);
    const primary = await screen.findByRole("navigation", { name: "Primary" });
    const labels = (name: string) =>
      within(within(primary).getByRole("group", { name }))
        .getAllByRole("button")
        .map((button) => button.textContent);
    expect(labels("Work")).toEqual([
      "◇Projects",
      "+New request",
      "⌘Workflow",
      "◉Tasks",
      "$Costs",
      "¶Docs",
      "⎇Branches",
    ]);
    expect(labels("Configure")).toEqual([
      "⚇Agents",
      "⇶Rules",
      "▣Runtimes",
      "⚙Settings",
      "↗Connections",
    ]);
    expect(
      within(primary).queryByRole("button", { name: /Library/ }),
    ).not.toBeInTheDocument();

    const messages = within(primary).getByRole("button", { name: /Messages/ });
    expect(messages).not.toHaveAttribute("aria-current");
    fireEvent.click(messages);
    expect(messages).toHaveAttribute("aria-current", "page");
  });

  describe("extension screens", () => {
    const auditView: ExtensionView = {
      id: "audit.records",
      label: "Audit records",
      group: "work",
      glyph: "§",
      render: () => <p>Audit screen</p>,
    };
    const capabilities = (ids: string[]): CapabilitiesInfo => ({
      product: { name: "Acme Desk", version: "2.1.0", inkuna: false },
      capabilities: ids.map((id) => ({ id, label: id, extension: "audit" })),
    });
    const stubCapabilities = (info: CapabilitiesInfo) => {
      const Capabilities = vi.fn(async () => info);
      window.go!.app!.Service!.Capabilities = Capabilities;
      return Capabilities;
    };

    it("lists and opens a screen whose capability Go reports", async () => {
      stubCapabilities(capabilities(["audit.records"]));
      render(<App views={[auditView]} />);
      const primary = await screen.findByRole("navigation", {
        name: "Primary",
      });
      const work = within(primary).getByRole("group", { name: "Work" });
      const audit = await within(work).findByRole("button", {
        name: /Audit records/,
      });
      expect(audit).toHaveTextContent("§Audit records");
      expect(screen.queryByText("Audit screen")).not.toBeInTheDocument();
      fireEvent.click(audit);
      expect(audit).toHaveAttribute("aria-current", "page");
      expect(screen.getByText("Audit screen")).toBeInTheDocument();
      fireEvent.click(within(primary).getByRole("button", { name: /Tasks/ }));
      expect(screen.queryByText("Audit screen")).not.toBeInTheDocument();
    });

    it("hides a screen whose capability Go does not report", async () => {
      const Capabilities = stubCapabilities(capabilities(["audit.other"]));
      render(<App views={[auditView]} />);
      const primary = await screen.findByRole("navigation", {
        name: "Primary",
      });
      await waitFor(() => expect(Capabilities).toHaveBeenCalled());
      await act(async () => {});
      expect(
        within(primary).queryByRole("button", { name: /Audit records/ }),
      ).not.toBeInTheDocument();
      expect(screen.queryByText("Audit screen")).not.toBeInTheDocument();
    });

    it("asks Go nothing when the bundle adds no screen", async () => {
      const Capabilities = stubCapabilities(capabilities(["audit.records"]));
      render(<App />);
      await screen.findByRole("navigation", { name: "Primary" });
      await act(async () => {});
      expect(Capabilities).not.toHaveBeenCalled();
    });
  });

  it("opens the add-project dialog outside the layout and manages focus", async () => {
    render(<App />);
    const trigger = (
      await screen.findAllByRole("button", { name: /Add project/ })
    )[0];
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "Add project" });
    expect(dialog.closest("main")).toBeNull();

    // Focus moves into the dialog, on the repository, which the name is
    // filled from, and Tab wraps at both ends.
    const first = within(dialog).getByLabelText("Repository folder");
    const last = within(dialog).getByRole("button", { name: "Add project" });
    expect(first).toHaveFocus();
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(last).toHaveFocus();
    fireEvent.keyDown(document, { key: "Tab" });
    expect(first).toHaveFocus();

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("does not keep a removed project selected", async () => {
    const other: Project = {
      ...project,
      id: "project-other",
      name: "Bakery",
      repository: "/tmp/bakery",
    };
    const service = window.go!.app!.Service!;
    service.Dashboard = async () => ({
      projects: [project, other],
      health: [],
      defaults,
    });
    service.PreviewRemoveProject = async () => ({
      projectId: other.id,
      name: other.name,
      repository: other.repository,
      tasks: 0,
      runs: 0,
      worktrees: [],
    });
    service.RemoveProject = async () => ({
      project: other,
      deletedTasks: 0,
    });
    render(<App />);
    const bakery = (
      await screen.findByRole("heading", { name: "Bakery" })
    ).closest("article")!;
    fireEvent.click(
      within(bakery as HTMLElement).getByRole("button", {
        name: "New request →",
      }),
    );
    expect(await screen.findByLabelText("Project")).toHaveValue(other.id);

    fireEvent.click(screen.getByRole("button", { name: /Projects/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Remove Bakery" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Remove project" }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("heading", { name: "Bakery" }),
      ).not.toBeInTheDocument(),
    );

    // The navigation's New request, not the card's.
    const compose = screen
      .getAllByRole("button", { name: /New request/ })
      .find((button) => !button.textContent?.includes("→"))!;
    fireEvent.click(compose);
    expect(await screen.findByLabelText("Project")).toHaveValue(project.id);
  });

  it("asks before leaving a request not yet planned, and clears a plan once it starts", async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Fix cancellation" } },
    );
    fireEvent.click(screen.getByRole("button", { name: /Tasks/ }));
    const guard = screen.getByRole("dialog", {
      name: "Discard unsaved changes?",
    });
    expect(guard).toHaveTextContent("Leaving New request drops the edits");
    fireEvent.click(
      within(guard).getByRole("button", { name: "Keep editing" }),
    );
    expect(
      screen.getByPlaceholderText("Describe the outcome you want…"),
    ).toHaveValue("Fix cancellation");

    // Planned, the request is the plan's: nothing is left to lose.
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Approve & run" }),
    );
    // The run started: the Workflow screen no longer offers the plan.
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Approve & run" }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Workflow/ }));
    expect(
      screen.queryByRole("button", { name: "Approve & run" }),
    ).not.toBeInTheDocument();
  });

  // A change of project resets the Agents form, so New request, which
  // brings back a project when none is chosen, waits for the answer.
  it("keeps an agent edited with no project when leaving is declined", async () => {
    const service = window.go!.app!.Service!;
    service.Agents = async () => [] as never;
    service.AgentLocations = async () => ({ global: "/tmp/data/agents" });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Agents/ }));
    fireEvent.change(await screen.findByLabelText("Agents project"), {
      target: { value: "" },
    });
    fireEvent.click(await screen.findByRole("button", { name: "+ New agent" }));
    const form = await screen.findByRole("form", { name: "New agent" });
    fireEvent.change(within(form).getByLabelText(/^Name/), {
      target: { value: "security-reviewer" },
    });
    const compose = screen
      .getAllByRole("button", { name: /New request/ })
      .find((button) => !button.textContent?.includes("→"))!;
    fireEvent.click(compose);
    const guard = screen.getByRole("dialog", {
      name: "Discard unsaved changes?",
    });
    expect(guard).toHaveTextContent("Leaving Agents drops the edits");
    fireEvent.click(
      within(guard).getByRole("button", { name: "Keep editing" }),
    );
    expect(
      within(screen.getByRole("form", { name: "New agent" })).getByLabelText(
        /^Name/,
      ),
    ).toHaveValue("security-reviewer");
    expect(screen.getByLabelText("Agents project")).toHaveValue("");
  });

  // A start can take a while; a plan made meanwhile is not the one that
  // started, so the start leaves it be.
  it("keeps a plan made while another was starting", async () => {
    const planB: WorkflowPlan = {
      ...workflowPlan,
      task: { ...workflowPlan.task, id: "task-b", title: "Plan B" },
      workflow: {
        ...workflowPlan.workflow,
        id: "workflow-b",
        taskId: "task-b",
      },
    };
    const service = window.go!.app!.Service!;
    let planned = 0;
    service.PlanWorkflow = async () => (planned++ === 0 ? workflowPlan : planB);
    let finishStart: (run: WorkflowRun) => void = () => {};
    service.StartWorkflow = () =>
      new Promise<WorkflowRun>((resolve) => (finishStart = resolve));
    render(<App />);
    const plan = async (objective: string) => {
      const compose = screen
        .getAllByRole("button", { name: /New request/ })
        .find((button) => !button.textContent?.includes("→"))!;
      fireEvent.click(compose);
      fireEvent.change(
        screen.getByPlaceholderText("Describe the outcome you want…"),
        { target: { value: objective } },
      );
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "Generate workflow →" }),
        ).toBeEnabled(),
      );
      fireEvent.click(
        screen.getByRole("button", { name: "Generate workflow →" }),
      );
      return screen.findByRole("button", { name: "Approve & run" });
    };
    await screen.findByRole("button", { name: /Projects/ });
    fireEvent.click(await plan("Fix cancellation"));
    // While A starts, B is planned.
    await plan("Plan B");
    expect(screen.getByRole("textbox", { name: "Task title" })).toHaveValue(
      "Plan B",
    );
    await act(async () => finishStart(queuedRun));
    expect(screen.getByRole("textbox", { name: "Task title" })).toHaveValue(
      "Plan B",
    );
    expect(
      screen.getByRole("button", { name: "Approve & run" }),
    ).toBeInTheDocument();
  });

  // A rerun can take a while to start too: the plan made meanwhile keeps
  // its accepted edits when the rerun finishes starting.
  it("keeps a plan's edits made while a rerun was starting", async () => {
    const failed: WorkflowRun = {
      ...queuedRun,
      state: "failed",
      finishedAt: "2026-01-01T00:05:00Z",
    };
    const planB: WorkflowPlan = {
      ...workflowPlan,
      task: { ...workflowPlan.task, id: "task-b", title: "Plan B" },
      workflow: {
        ...workflowPlan.workflow,
        id: "workflow-b",
        taskId: "task-b",
      },
    };
    const service = window.go!.app!.Service!;
    service.WorkflowRuns = async () => [failed];
    service.WorkflowRun = async () => failed;
    service.LatestWorkflowRun = async () => failed;
    service.RunFailure = async () => ({
      round: 1,
      stopped: true,
      steps: [],
      suggestions: [],
    });
    const resume: RunResume = {
      round: 1,
      stopped: true,
      rerun: [{ id: "worker", title: "Implement request", state: "failed" }],
      kept: [],
      changes: [],
    };
    service.RunResume = async () => resume;
    service.PlanRerun = async () => ({
      runId: failed.id,
      plan: workflowPlan,
      resume,
    });
    let finishRerun: (run: WorkflowRun) => void = () => {};
    service.RerunWorkflowRun = () =>
      new Promise<WorkflowRun>((resolve) => (finishRerun = resolve));
    service.PlanWorkflow = async () => planB;
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Tasks/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: /Fix cancellation/ }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Rerun…" }));
    fireEvent.click(await screen.findByRole("button", { name: "Start over" }));

    // While the rerun starts, plan B is made and edited.
    const compose = screen
      .getAllByRole("button", { name: /New request/ })
      .find((button) => !button.textContent?.includes("→"))!;
    fireEvent.click(compose);
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Plan B" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    const score = await screen.findByLabelText(
      "Complexity score for Implement request",
    );
    fireEvent.change(score, { target: { value: "3" } });
    await waitFor(() =>
      expect(
        screen.getByLabelText("Complexity score for Implement request"),
      ).toHaveValue(3),
    );

    await act(async () => finishRerun({ ...queuedRun, id: "run-rerun" }));
    expect(screen.getByRole("textbox", { name: "Task title" })).toHaveValue(
      "Plan B",
    );
    expect(
      screen.getByLabelText("Complexity score for Implement request"),
    ).toHaveValue(3);
  });

  it("moves from request composition to workflow approval", async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    expect(
      screen.getByRole("heading", { name: "Turn intent into a workflow" }),
    ).toBeInTheDocument();
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Fix cancellation" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    expect(
      await screen.findByRole("heading", { name: "Workflow" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Acyclic graph")).toBeInTheDocument();
    expect(screen.getByText("Complexity scored")).toBeInTheDocument();
    expect(
      (await screen.findAllByText("gpt-5.3-codex")).length,
    ).toBeGreaterThan(0);
    fireEvent.change(
      screen.getByLabelText("Complexity score for Implement request"),
      { target: { value: "2" } },
    );
    expect(
      (await screen.findAllByText("claude-haiku-4.5")).length,
    ).toBeGreaterThan(0);
  });

  it("re-staffs a step with another agent and approves it", async () => {
    let startedWorkflow = "";
    const revised: string[] = [];
    window.go!.app!.Service!.ReviseWorkflow = async (workflowJSON) => {
      revised.push(workflowJSON);
      return reviseWorkflowStub(workflowJSON);
    };
    window.go!.app!.Service!.StartWorkflow = async (workflowJSON) => {
      startedWorkflow = workflowJSON;
      return queuedRun;
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Fix cancellation" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    const agent = await screen.findByLabelText("Agent for Implement request");
    // A writable step only lists writable agents.
    expect(
      within(agent)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["documenter", "implementer"]);
    fireEvent.change(agent, { target: { value: "documenter" } });
    await waitFor(() => expect(revised).toHaveLength(1));
    expect(
      (JSON.parse(revised[0]) as WorkflowPlan["workflow"]).steps[0].agent,
    ).toBe("documenter");
    const approve = screen.getByRole("button", { name: "Approve & run" });
    await waitFor(() => expect(approve).toBeEnabled());
    fireEvent.click(approve);
    await waitFor(() => expect(startedWorkflow).not.toBe(""));
    expect(
      (JSON.parse(startedWorkflow) as WorkflowPlan["workflow"]).steps[0].agent,
    ).toBe("documenter");
  });

  it("keeps a plan's edits while another screen is open and approves them", async () => {
    let startedWorkflow = "";
    window.go!.app!.Service!.ReviseWorkflow = async (workflowJSON) =>
      reviseWorkflowStub(workflowJSON);
    window.go!.app!.Service!.StartWorkflow = async (workflowJSON) => {
      startedWorkflow = workflowJSON;
      return queuedRun;
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Fix cancellation" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    fireEvent.change(
      await screen.findByLabelText("Agent for Implement request"),
      {
        target: { value: "documenter" },
      },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Approve & run" }),
      ).toBeEnabled(),
    );
    const primary = screen.getByRole("navigation", { name: "Primary" });
    fireEvent.click(within(primary).getByRole("button", { name: /Agents/ }));
    expect(
      screen.queryByLabelText("Agent for Implement request"),
    ).not.toBeInTheDocument();
    fireEvent.click(within(primary).getByRole("button", { name: /Workflow/ }));
    expect(
      await screen.findByLabelText("Agent for Implement request"),
    ).toHaveValue("documenter");
    fireEvent.click(screen.getByRole("button", { name: "Approve & run" }));
    await waitFor(() => expect(startedWorkflow).not.toBe(""));
    expect(
      (JSON.parse(startedWorkflow) as WorkflowPlan["workflow"]).steps[0].agent,
    ).toBe("documenter");
  });

  it("keeps the approval blocked when Go refuses an edit", async () => {
    window.go!.app!.Service!.ReviseWorkflow = async () => {
      throw new Error("agent reviewer is read-only");
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Fix cancellation" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    fireEvent.change(
      await screen.findByLabelText("Complexity score for Implement request"),
      { target: { value: "3" } },
    );
    expect(
      await screen.findByText(/agent reviewer is read-only/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Approve & run" }),
    ).toBeDisabled();
    // The rejected edit is rolled back to the plan's value.
    expect(
      screen.getByLabelText("Complexity score for Implement request"),
    ).toHaveValue(8);
  });

  it("lets the user choose the workflow rule when composing and re-plan it later", async () => {
    const requests: Array<{
      ruleName?: string;
      labels?: string[];
      restaff?: boolean;
    }> = [];
    window.go!.app!.Service!.Rules = async () => rulesInfo as never;
    window.go!.app!.Service!.PlanWorkflow = async (request) => {
      requests.push(request);
      return {
        ...workflowPlan,
        decision: {
          assignments: [
            {
              stageId: "worker",
              agent: "implementer",
              skills: [],
              complexity: { score: 8, rationale: "cross-cutting change" },
              rationale: "writes code",
            },
          ],
        },
        ruleName: request.ruleName || "default",
        notes: request.ruleName
          ? [`workflow "${request.ruleName}" chosen by the user`]
          : [],
      };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Fix cancellation" } },
    );
    fireEvent.change(screen.getByLabelText("Labels"), {
      target: { value: "bugfix, ui" },
    });
    const picker = await screen.findByLabelText("Workflow rule");
    expect(
      within(picker)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual([
      "Let the rules decide",
      "bugfix · Reproduce before fixing.",
      "read-only",
      "default · the fallback flow",
    ]);
    fireEvent.change(picker, { target: { value: "read-only" } });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    expect(
      await screen.findByRole("heading", { name: "Workflow" }),
    ).toBeInTheDocument();
    expect(requests).toHaveLength(1);
    expect(requests[0].ruleName).toBe("read-only");
    expect(requests[0].labels).toEqual(["bugfix", "ui"]);
    expect(
      screen.getByText('workflow "read-only" chosen by the user'),
    ).toBeInTheDocument();

    // The workflow view plans the same request again under another rule.
    const replan = await screen.findByLabelText("Workflow rule");
    expect(replan).toHaveValue("read-only");
    // The view lists the other rules once its own load of them returns;
    // until then a select has no "bugfix" to choose.
    await within(replan).findByRole("option", { name: "bugfix" });
    fireEvent.change(replan, { target: { value: "bugfix" } });
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[1].ruleName).toBe("bugfix");
    expect(requests[1].labels).toEqual(["bugfix", "ui"]);
    expect(
      await screen.findByText('workflow "bugfix" chosen by the user'),
    ).toBeInTheDocument();

    // Re-staff plans it again under the same rule without the kept
    // decision; the next replan may reuse what that decided.
    fireEvent.click(screen.getByRole("button", { name: "Re-staff" }));
    await waitFor(() => expect(requests).toHaveLength(3));
    expect(requests[2]).toMatchObject({ ruleName: "bugfix", restaff: true });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Re-staff" })).toBeEnabled(),
    );
    fireEvent.change(screen.getByLabelText("Workflow rule"), {
      target: { value: "read-only" },
    });
    await waitFor(() => expect(requests).toHaveLength(4));
    expect(requests[3].restaff).toBeUndefined();
  });

  it("starts the approved workflow before showing and cancelling the live run", async () => {
    let startedWorkflow = "";
    window.go!.app!.Service!.StartWorkflow = async (workflowJSON) => {
      startedWorkflow = workflowJSON;
      return {
        ...queuedRun,
        events: undefined as unknown as WorkflowRun["events"],
      };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Fix cancellation" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Approve & run" }),
    );
    expect(
      await screen.findByRole("heading", { name: "Fix cancellation" }),
    ).toBeInTheDocument();
    expect(JSON.parse(startedWorkflow).id).toBe("workflow-test");
    const cancel = await screen.findByRole("button", { name: "Cancel run" });
    await waitFor(() => expect(cancel).toBeEnabled());
    fireEvent.click(cancel);
    expect(
      await screen.findByText("Worktree preserved for inspection"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("cancelled").length).toBeGreaterThan(0);
  });

  it("lists backend runs and opens context and commit details", async () => {
    const commit = {
      hash: "abcdef1234567890",
      shortHash: "abcdef123456",
      author: "Inkuna <inkuna@localhost>",
      date: "2026-01-01T00:00:40Z",
      subject: "inkuna(worker): Added feature package",
      stat: " feature.go | 1 +",
      stepId: "worker",
    };
    const runWithCommit = {
      ...queuedRun,
      branch: "feature/fix-cancellation",
      lastCommit: commit,
    };
    window.go!.app!.Service!.WorkflowRuns = async () => [runWithCommit];
    window.go!.app!.Service!.WorkflowRun = async () => runWithCommit;
    window.go!.app!.Service!.RunContext = async (runID, stepID) => ({
      stepId: stepID,
      agent: "implementer",
      envelope: {
        version: 1,
        taskId: "task-test",
        stepId: stepID,
        runtime: "copilot",
        agent: "implementer",
        objective: "Fix cancellation",
        acceptanceCriteria: ["The run starts"],
      },
      handoff: {
        version: 1,
        status: "completed",
        summary: "Cancellation fixed",
        decisions: ["Used context cancellation"],
      },
      prompt: `# Agent: implementer\nrun ${runID}`,
    });
    const file = {
      path: "feature.go",
      status: "added" as const,
      newMode: "100644",
      additions: 1,
      deletions: 0,
      blob: "1".repeat(40),
    };
    window.go!.app!.Service!.RunCommits = async () => [
      { ...commit, agent: "implementer", files: 1, additions: 1, deletions: 0 },
    ];
    window.go!.app!.Service!.RunViewed = async () => [];
    window.go!.app!.Service!.RunChanges = async () => ({
      from: "0".repeat(40),
      to: commit.hash,
      files: [file],
      additions: 1,
      deletions: 0,
    });
    window.go!.app!.Service!.RunFilePatch = async () => ({
      file,
      from: "0".repeat(40),
      to: commit.hash,
      hunks: [
        {
          header: "@@ -0,0 +1 @@",
          oldStart: 0,
          oldLines: 0,
          newStart: 1,
          newLines: 1,
          lines: [{ kind: "added" as const, new: 1, text: "package feature" }],
        },
      ],
      bytes: 40,
      lines: 4,
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Tasks/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: /Fix cancellation/ }),
    );
    expect(
      await screen.findByRole("heading", { name: "Fix cancellation" }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByText(/implementer · copilot · gpt-5.3-codex/).length,
    ).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("tab", { name: "Context" }));
    expect(await screen.findByText("Cancellation fixed")).toBeInTheDocument();
    expect(screen.getByText("Used context cancellation")).toBeInTheDocument();
    expect(screen.getByText(/# Agent: implementer/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Changes" }));
    expect(
      await screen.findByRole("option", {
        name: /inkuna\(worker\): Added feature package · worker/,
      }),
    ).toBeInTheDocument();
    expect(await screen.findByText("package feature")).toBeInTheDocument();
  });

  it("answers an agent in its thread and keeps the question on record", async () => {
    const answers: string[] = [];
    const question = {
      id: "input-1",
      executionId: "run-test",
      stepId: "worker",
      a2aTaskId: "a2a-1",
      agent: "implementer",
      askedBy: "implementer",
      question: "Which database?",
      options: ["sqlite", "postgres"],
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      runTitle: "Fix cancellation",
    };
    const asked = {
      messageId: "m1",
      role: "agent",
      parts: [{ kind: "text" as const, text: "Need a decision" }],
      timestamp: "2026-01-01T00:00:09Z",
    };
    const thread = () => ({
      taskId: "a2a-1",
      contextId: "task-test",
      agent: "implementer",
      state: answers.length ? "completed" : "input-required",
      messageCount: answers.length ? 2 : 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Fix cancellation",
      preview: "Need a decision",
      pendingInput: answers.length ? undefined : question,
    });
    window.go!.app!.Service!.Dashboard = async () => ({
      projects: [project],
      health: [],
      defaults,
      pendingInputs: answers.length ? 0 : 1,
      activeRuns: 1,
    });
    window.go!.app!.Service!.Threads = async () => [
      thread(),
      {
        taskId: "a2a-2",
        contextId: "task-test",
        agent: "reviewer",
        state: "completed",
        messageCount: 2,
        updatedAt: "2026-01-01T00:00:05Z",
        runId: "run-test",
        runTitle: "Fix cancellation",
        preview: "Looks good",
      },
    ];
    window.go!.app!.Service!.Thread = async (taskID) => ({
      ...thread(),
      taskId: taskID,
      stepId: "worker",
      messages: answers.length
        ? [
            asked,
            {
              messageId: "m2",
              role: "user",
              parts: [{ kind: "text" as const, text: "sqlite" }],
              timestamp: "2026-01-01T00:00:20Z",
            },
          ]
        : [asked],
      artifacts: [],
      inputs: [
        answers.length
          ? {
              ...question,
              state: "answered",
              answer: "sqlite",
              resolvedAt: "2026-01-01T00:00:20Z",
            }
          : question,
      ],
    });
    window.go!.app!.Service!.AnswerInput = async (inputID, answer) => {
      answers.push(`${inputID}=${answer}`);
      return { ...question, state: "answered", answer };
    };
    render(<App />);
    expect(
      await screen.findByLabelText("1 awaiting reply"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Messages/ }));

    // Threads are grouped under their run, and the one that needs a reply opens.
    const threads = await screen.findByRole("navigation", { name: "Threads" });
    const group = within(threads).getByRole("group", {
      name: "Fix cancellation",
    });
    expect(within(group).getAllByRole("button")).toHaveLength(2);
    expect(within(group).getByText("needs reply")).toBeInTheDocument();
    const pane = await screen.findByRole("article", {
      name: "Thread with implementer",
    });
    expect(within(pane).getByText("Which database?")).toBeInTheDocument();
    expect(within(pane).getByText("Need a decision")).toBeInTheDocument();

    fireEvent.click(within(pane).getByRole("button", { name: "sqlite" }));
    await waitFor(() => expect(answers).toEqual(["input-1=sqlite"]));

    // The thread does not disappear: question, answer, and record remain.
    expect(await within(pane).findByText(/Answered/)).toHaveTextContent(
      "sqlite",
    );
    expect(within(pane).getByText("Which database?")).toBeInTheDocument();
    expect(
      within(pane).queryByRole("button", { name: "Send reply" }),
    ).not.toBeInTheDocument();
    expect(within(threads).queryByText("needs reply")).not.toBeInTheDocument();
  });

  it("shows a round's feedback and replies in its thread, and offers the previous answer", async () => {
    const answers: string[] = [];
    const question = {
      id: "input-2",
      executionId: "run-test",
      stepId: "worker",
      a2aTaskId: "a2a-1",
      agent: "human",
      askedBy: "rule:default",
      question: "Which format?",
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      runTitle: "Fix cancellation",
      previousAnswer: "JSON",
    };
    const summary = {
      taskId: "a2a-1",
      contextId: "task-test",
      agent: "implementer",
      state: "input-required",
      messageCount: 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Fix cancellation",
      pendingInput: question,
    };
    window.go!.app!.Service!.Threads = async () => [summary];
    window.go!.app!.Service!.Thread = async () => ({
      ...summary,
      stepId: "worker",
      messages: [
        {
          messageId: "m1",
          role: "user",
          parts: [{ kind: "text" as const, text: "# Continue" }],
          metadata: { round: 2, from: ["human"] },
          timestamp: "2026-01-01T00:00:05Z",
        },
      ],
      artifacts: [],
      inputs: [question],
      feedback: [
        {
          executionId: "run-test",
          round: 2,
          id: "f1",
          target: "worker",
          from: "human",
          text: "Add a header row.",
          status: "fixed",
          note: "Added it.",
          createdAt: "2026-01-01T00:00:04Z",
        },
        {
          executionId: "run-test",
          round: 2,
          id: "f2",
          target: "worker",
          from: "human",
          text: "Name the file.",
          status: "no-reply",
          createdAt: "2026-01-01T00:00:04Z",
        },
      ],
    });
    window.go!.app!.Service!.AnswerInput = async (inputID, answer) => {
      answers.push(`${inputID}=${answer}`);
      return { ...question, state: "answered", answer };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const pane = await screen.findByRole("article", {
      name: "Thread with implementer",
    });
    const sent = within(pane).getByLabelText("Feedback for round 2");
    expect(sent).toHaveTextContent("human · feedback for round 2");
    expect(sent).toHaveTextContent("f1Add a header row.");
    const replies = within(pane).getByLabelText("Replies for round 2");
    expect(replies).toHaveTextContent("implementer · replies");
    expect(replies).toHaveTextContent("f1fixed — Added it.");
    expect(replies).toHaveTextContent("f2no reply");

    fireEvent.click(
      within(pane).getByRole("button", { name: "Answer as before: JSON" }),
    );
    await waitFor(() => expect(answers).toEqual(["input-2=JSON"]));
  });

  it("marks a run awaiting review and its round in the run list", async () => {
    const reviewing = { ...queuedRun, state: "reviewing", round: 3 };
    window.go!.app!.Service!.WorkflowRuns = async () => [reviewing];
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Tasks/ }));
    const card = await screen.findByRole("button", {
      name: /Fix cancellation/,
    });
    expect(within(card).getByText("awaiting review")).toBeInTheDocument();
    expect(within(card).getByText("round 3")).toBeInTheDocument();
  });

  it("approves a review gate in Messages, and a reply there requests changes", async () => {
    const calls: string[] = [];
    const gate = (target: string) => ({
      id: "input-gate",
      executionId: "run-test",
      stepId: "approval",
      a2aTaskId: "a2a-gate",
      agent: "human",
      askedBy: "rule:gated",
      question: target
        ? `Approval, round 1: approve the run, or reply with what ${target} should change.`
        : "Approval, round 1: approve the run. No stage before this gate writes, so it can only be approved.",
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      review: true,
      reviewTarget: target,
      runTitle: "Fix cancellation",
    });
    let target = "implement";
    const thread = () => ({
      taskId: "a2a-gate",
      contextId: "task-test",
      agent: "human",
      state: calls.length ? "completed" : "input-required",
      messageCount: 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Fix cancellation",
      preview: "Approval, round 1",
      pendingInput: calls.length ? undefined : gate(target),
    });
    window.go!.app!.Service!.Threads = async () => [thread()];
    window.go!.app!.Service!.Thread = async () => ({
      ...thread(),
      stepId: "approval",
      messages: [],
      artifacts: [],
      inputs: [gate(target)],
    });
    window.go!.app!.Service!.ApproveReview = async (runID) => {
      calls.push(`approve ${runID}`);
      return queuedRun;
    };
    window.go!.app!.Service!.AnswerInput = async (inputID, answer) => {
      calls.push(`${inputID}=${answer}`);
      return { ...gate(target), state: "answered", answer };
    };
    const { unmount } = render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const pane = await screen.findByRole("article", {
      name: "Thread with human",
    });
    fireEvent.change(
      within(pane).getByLabelText("Request changes from implement"),
      { target: { value: "Keep the title." } },
    );
    fireEvent.click(
      within(pane).getByRole("button", { name: "Request changes" }),
    );
    await waitFor(() => expect(calls).toEqual(["input-gate=Keep the title."]));
    unmount();

    // Approve is its own action, not an answer.
    calls.length = 0;
    target = "";
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const approveOnly = await screen.findByRole("article", {
      name: "Thread with human",
    });
    expect(
      within(approveOnly).getByText(/can only be approved/),
    ).toBeInTheDocument();
    expect(
      within(approveOnly).queryByRole("button", { name: "Request changes" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      within(approveOnly).getByRole("button", { name: "Approve" }),
    );
    await waitFor(() => expect(calls).toEqual(["approve run-test"]));
  });

  it("answers a check pause in Messages with one of its choices only", async () => {
    const calls: string[] = [];
    const pause = {
      id: "input-pause",
      executionId: "run-test",
      stepId: "verify",
      a2aTaskId: "a2a-pause",
      agent: "human",
      askedBy: "rule:feature",
      question:
        "Round 1: a check of verify failed in a way implement cannot fix. Fix what broke, then re-run the checks; or send the failures to implement anyway, or fail the run.",
      options: ["Re-run checks", "Send to the writer", "Fail the run"],
      context:
        "verify: task db:test exited with code 3\nSYSTEM: Postgres is down",
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      checkPause: true,
      runTitle: "Fix cancellation",
    };
    const thread = () => ({
      taskId: "a2a-pause",
      contextId: "task-test",
      agent: "human",
      state: calls.length ? "completed" : "input-required",
      messageCount: 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Fix cancellation",
      preview: "Round 1: a check of verify failed",
      pendingInput: calls.length ? undefined : pause,
    });
    window.go!.app!.Service!.Threads = async () => [thread()];
    window.go!.app!.Service!.Thread = async () => ({
      ...thread(),
      stepId: "verify",
      messages: [],
      artifacts: [],
      inputs: [pause],
    });
    window.go!.app!.Service!.AnswerInput = async (inputID, answer) => {
      calls.push(`${inputID}=${answer}`);
      return { ...pause, state: "answered", answer };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const pane = await screen.findByRole("article", {
      name: "Thread with human",
    });
    expect(
      within(pane).getByText(/SYSTEM: Postgres is down/),
    ).toBeInTheDocument();
    expect(within(pane).queryByRole("textbox")).not.toBeInTheDocument();
    const choices = within(pane).getByRole("group", { name: "Choices" });
    expect(
      within(choices)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["Re-run checks", "Send to the writer", "Fail the run"]);
    fireEvent.click(
      within(choices).getByRole("button", { name: "Re-run checks" }),
    );
    await waitFor(() => expect(calls).toEqual(["input-pause=Re-run checks"]));
  });

  it("links the plan a review gate follows before Approve, and opens it rendered", async () => {
    const gate = {
      id: "input-gate",
      executionId: "run-test",
      stepId: "approval",
      a2aTaskId: "a2a-gate",
      agent: "human",
      askedBy: "rule:planned",
      question:
        "Approval, round 1: approve the run, or reply with what implement should change.",
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      review: true,
      reviewTarget: "implement",
      runTitle: "Fix cancellation",
    };
    const thread = {
      taskId: "a2a-gate",
      contextId: "task-test",
      agent: "human",
      state: "input-required",
      messageCount: 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Fix cancellation",
      preview: "Approval, round 1",
      pendingInput: gate,
    };
    const asked: string[] = [];
    window.go!.app!.Service!.Threads = async () => [thread];
    window.go!.app!.Service!.Thread = async () => ({
      ...thread,
      stepId: "approval",
      messages: [],
      artifacts: [],
      inputs: [gate],
    });
    window.go!.app!.Service!.GateArtifacts = async (runID, gateID) => {
      asked.push(`${runID}/${gateID}`);
      return [
        {
          stepId: "plan",
          title: "Plan the approach",
          agent: "researcher",
          round: 1,
          artifacts: [
            {
              digest: "d1",
              name: "plan.md",
              kind: "document",
              mediaType: "text/markdown",
              size: 40,
            },
          ],
        },
      ];
    };
    window.go!.app!.Service!.ReadArtifact = async (digest) => ({
      digest,
      taskId: "task-test",
      stepId: "plan",
      kind: "document",
      name: "plan.md",
      mediaType: "text/markdown",
      size: 40,
      createdAt: "2026-01-01T00:00:09Z",
      content: "# Approach\n\n1. Change the parser.\n",
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const pane = await screen.findByRole("article", {
      name: "Thread with human",
    });
    const read = await within(pane).findByRole("region", {
      name: "Read before approving",
    });
    expect(asked).toEqual(["run-test/approval"]);
    const link = within(read).getByRole("button", { name: /plan\.md/ });
    const approve = within(pane).getByRole("button", { name: "Approve" });
    // The plan comes before the button that approves it.
    expect(
      link.compareDocumentPosition(approve) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    fireEvent.click(link);
    const drawer = await screen.findByRole("dialog", { name: "plan.md" });
    expect(
      await within(drawer).findByRole("heading", { name: "Approach" }),
    ).toBeInTheDocument();
    // The drawer is beside the thread, not over it: Approve stays usable.
    expect(approve).toBeEnabled();
    expect(drawer).not.toHaveAttribute("aria-modal");
  });

  it("lists each command a permission request asks for on its own, one chained command per line", async () => {
    const permission = {
      id: "input-permission",
      executionId: "run-test",
      stepId: "plan",
      a2aTaskId: "a2a-plan",
      agent: "researcher",
      askedBy: "researcher",
      question:
        'researcher was denied 2 commands and a tool while working on "Plan the work". Allow them for this run?',
      options: ["Allow for this run", "Deny"],
      context:
        "The step runs read-only in /repo. Approval lasts for this run only.",
      permissions: [
        { command: "ls lib | head -5; grep -n Watch lib/care.dart || true" },
        { command: "ls .inkuna" },
        { tool: "WebFetch", detail: "https://example.com/care" },
      ],
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      runTitle: "Watch care",
    };
    const thread = {
      taskId: "a2a-plan",
      contextId: "task-test",
      agent: "researcher",
      state: "input-required",
      messageCount: 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Watch care",
      preview: permission.question,
      pendingInput: permission,
    };
    window.go!.app!.Service!.Threads = async () => [thread];
    window.go!.app!.Service!.Thread = async () => ({
      ...thread,
      stepId: "plan",
      messages: [],
      artifacts: [],
      inputs: [permission],
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const pane = await screen.findByRole("article", {
      name: "Thread with researcher",
    });
    const asks = within(pane).getByRole("list", {
      name: "Asks to be allowed",
    });
    const items = within(asks).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveTextContent("1. Run");
    expect(items[0].querySelector("pre")?.textContent).toBe(
      "ls lib | head -5;\ngrep -n Watch lib/care.dart ||\ntrue",
    );
    expect(items[1].querySelector("pre")?.textContent).toBe("ls .inkuna");
    expect(items[2]).toHaveTextContent("3. Use WebFetch");
    expect(items[2]).toHaveTextContent("https://example.com/care");
    // What approval covers reads as prose, after the list.
    const context = within(pane).getByText(/Approval lasts for this run only/);
    expect(context.tagName).toBe("P");
    expect(
      within(pane).getByRole("button", { name: "Allow for this run" }),
    ).toBeInTheDocument();
  });

  it("links the plan a human-input stage asks about before the reply", async () => {
    const stage = {
      id: "input-confirm",
      executionId: "run-test",
      stepId: "confirm-plan",
      a2aTaskId: "a2a-confirm",
      agent: "human",
      askedBy: "rule:story",
      question: 'Review the plan. Reply "approved" to start implementation.',
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      stage: true,
      runTitle: "Watch care",
    };
    const thread = {
      taskId: "a2a-confirm",
      contextId: "task-test",
      agent: "human",
      state: "input-required",
      messageCount: 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Watch care",
      preview: stage.question,
      pendingInput: stage,
    };
    const asked: string[] = [];
    window.go!.app!.Service!.Threads = async () => [thread];
    window.go!.app!.Service!.Thread = async () => ({
      ...thread,
      stepId: "confirm-plan",
      messages: [],
      artifacts: [],
      inputs: [stage],
    });
    window.go!.app!.Service!.GateArtifacts = async (runID, gateID) => {
      asked.push(`${runID}/${gateID}`);
      return [
        {
          stepId: "plan",
          title: "Plan the work",
          agent: "researcher",
          round: 1,
          artifacts: [
            {
              digest: "d1",
              name: "plan.md",
              kind: "document",
              mediaType: "text/markdown",
              size: 40,
            },
          ],
        },
      ];
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const pane = await screen.findByRole("article", {
      name: "Thread with human",
    });
    const read = await within(pane).findByRole("region", {
      name: "Read before answering",
    });
    expect(asked).toEqual(["run-test/confirm-plan"]);
    const link = within(read).getByRole("button", { name: /plan\.md/ });
    const reply = within(pane).getByRole("textbox");
    expect(
      link.compareDocumentPosition(reply) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      within(pane).queryByRole("button", { name: "Approve" }),
    ).not.toBeInTheDocument();
  });

  it("opens a run's conversation as a thread from the Tasks view", async () => {
    const message = {
      messageId: "m1",
      role: "agent",
      parts: [{ kind: "text" as const, text: "Implemented the change" }],
      timestamp: "2026-01-01T00:00:09Z",
    };
    const summary = {
      taskId: "a2a-1",
      contextId: "task-test",
      agent: "implementer",
      state: "completed",
      messageCount: 1,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Fix cancellation",
    };
    window.go!.app!.Service!.RunMessages = async () => [
      {
        task: {
          id: "a2a-1",
          contextId: "task-test",
          agent: "implementer",
          status: { state: "completed", timestamp: "2026-01-01T00:00:10Z" },
          history: [message],
        },
      },
    ];
    window.go!.app!.Service!.Threads = async () => [summary];
    window.go!.app!.Service!.Thread = async () => ({
      ...summary,
      messages: [message],
      artifacts: [],
      inputs: [],
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Tasks/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: /Fix cancellation/ }),
    );
    fireEvent.click(await screen.findByRole("tab", { name: "Messages" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Open thread →" }),
    );
    const pane = await screen.findByRole("article", {
      name: "Thread with implementer",
    });
    expect(
      within(pane).getByText("Implemented the change"),
    ).toBeInTheDocument();
    expect(within(pane).getByText(/kept\s+as a record/)).toBeInTheDocument();
  });

  it("explains why an expired question cannot be answered", async () => {
    const expired = {
      id: "input-9",
      executionId: "run-gone",
      stepId: "worker",
      a2aTaskId: "a2a-9",
      agent: "implementer",
      askedBy: "implementer",
      question: "Rename the API?",
      state: "pending",
      createdAt: "2026-01-01T00:00:10Z",
      expired: true,
    };
    const summary = {
      taskId: "a2a-9",
      contextId: "task-old",
      agent: "implementer",
      state: "input-required",
      messageCount: 0,
      updatedAt: "2026-01-01T00:00:10Z",
      pendingInput: expired,
    };
    window.go!.app!.Service!.Threads = async () => [summary];
    window.go!.app!.Service!.Thread = async () => ({
      ...summary,
      messages: [],
      artifacts: [],
      inputs: [expired],
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    expect(
      await screen.findByText("This question can no longer be answered"),
    ).toBeInTheDocument();
    expect(screen.getByText("Rename the API?")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Send reply" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("needs reply")).not.toBeInTheDocument();
  });

  it("edits rules in the form, round-trips YAML, and saves through Go validation", async () => {
    const stage = (id: string, kind: string, extra = {}) => ({
      id,
      kind,
      ...extra,
    });
    const builtIn = {
      version: 1,
      source: "",
      rules: [
        {
          name: "bugfix",
          when: { kind: ["bugfix" as const] },
          then: {
            stages: [
              stage("fix", "agent", { agent: "implementer" }),
              stage("approve", "approval", { dependsOn: ["fix"] }),
            ],
          },
        },
      ],
      default: {
        stages: [
          stage("implement", "agent", { agent: "auto" }),
          stage("approve", "approval", { dependsOn: ["implement"] }),
        ],
      },
    };
    const saved: string[] = [];
    let reject = "";
    window.go!.app!.Service!.Rules = async () => ({
      document: builtIn,
      exists: false,
      path: "/tmp/inkuna/.inkuna/rules.yaml",
    });
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.RulesToYAML = async (_project, documentJSON) =>
      `# yaml\n${documentJSON}`;
    window.go!.app!.Service!.RulesFromYAML = async (_project, content) =>
      JSON.parse(content.replace("# yaml\n", ""));
    window.go!.app!.Service!.SaveRules = async (_project, documentJSON) => {
      if (reject) throw new Error(reject);
      saved.push(documentJSON);
      return {
        document: JSON.parse(documentJSON),
        exists: true,
        path: "/tmp/inkuna/.inkuna/rules.yaml",
      };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    fireEvent.click(await screen.findByRole("button", { name: "+ Add rule" }));
    fireEvent.change(screen.getByLabelText("Rule name"), {
      target: { value: "docs-only" },
    });
    fireEvent.click(screen.getByLabelText("documentation"));
    fireEvent.change(screen.getByLabelText("Keywords"), {
      target: { value: "readme, changelog" },
    });
    expect(screen.getByText("unsaved changes")).toBeInTheDocument();

    // Leaving with unsaved edits asks first.
    fireEvent.click(screen.getByRole("button", { name: /Agents/ }));
    const guard = screen.getByRole("dialog", {
      name: "Discard unsaved changes?",
    });
    expect(guard).toHaveTextContent("Leaving Rules drops the edits");
    fireEvent.click(
      within(guard).getByRole("button", { name: "Keep editing" }),
    );
    expect(screen.getByLabelText("Rule name")).toHaveValue("docs-only");

    // The YAML tab shows what Go rendered, and switching back parses it.
    fireEvent.click(screen.getByRole("tab", { name: "YAML" }));
    const yaml = await screen.findByLabelText("Rules YAML");
    expect((yaml as HTMLTextAreaElement).value).toContain("docs-only");
    fireEvent.click(screen.getByRole("tab", { name: "Form" }));
    expect(
      await screen.findByRole("heading", { name: "docs-only" }),
    ).toBeInTheDocument();

    // A rejected save keeps the edits and shows Go's reason.
    reject = 'rule "docs-only": the final stage must be an approval';
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    // The stage library, which this test does not stand in for, has an
    // alert of its own that can show first, so the refusal is found by its
    // text.
    const refusal = await screen.findByText(
      /the final stage must be an approval/,
    );
    expect(refusal.closest('[role="alert"]')).not.toBeNull();
    expect(screen.getByText("unsaved changes")).toBeInTheDocument();

    reject = "";
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Saved to /tmp/inkuna/.inkuna/rules.yaml",
    );
    const document = JSON.parse(saved[0]);
    expect(document.rules.map((rule: { name: string }) => rule.name)).toEqual([
      "bugfix",
      "docs-only",
    ]);
    expect(document.rules[1].when).toEqual({
      kind: ["documentation"],
      keywords: ["readme", "changelog"],
    });
    expect(document.rules[1].then.stages.at(-1).kind).toBe("approval");
    expect(screen.queryByText("unsaved changes")).not.toBeInTheDocument();
  });

  it("sets a review gate and a check loop in the form and keeps them through YAML", async () => {
    const saved: string[] = [];
    window.go!.app!.Service!.Rules = async () => ({
      document: {
        version: 1,
        source: "",
        rules: [],
        default: {
          stages: [
            { id: "implement", kind: "agent", agent: "auto" },
            {
              id: "verify",
              kind: "evaluation",
              dependsOn: ["implement"],
            },
            { id: "approve", kind: "approval", dependsOn: ["verify"] },
          ],
        },
      },
      exists: true,
      path: "/tmp/inkuna/.inkuna/rules.yaml",
    });
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.RulesToYAML = async (_project, documentJSON) =>
      `# yaml\n${documentJSON}`;
    window.go!.app!.Service!.RulesFromYAML = async (_project, content) =>
      JSON.parse(content.replace("# yaml\n", ""));
    window.go!.app!.Service!.SaveRules = async (_project, documentJSON) => {
      saved.push(documentJSON);
      return {
        document: JSON.parse(documentJSON),
        exists: true,
        path: "/tmp/inkuna/.inkuna/rules.yaml",
      };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit fallback flow" }),
    );
    const flow = screen.getByRole("group", {
      name: "Stages of the fallback flow",
    });
    const verify = within(flow).getByRole("listitem", { name: "Stage verify" });
    fireEvent.change(within(verify).getByLabelText("When a command fails"), {
      target: { value: "feedback" },
    });
    // Only earlier agent stages are offered; Go decides the rest.
    expect(
      within(within(verify).getByLabelText("Send failures to"))
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["nearest writable agent stage upstream", "implement"]);
    fireEvent.change(within(verify).getByLabelText("Send failures to"), {
      target: { value: "implement" },
    });
    fireEvent.change(
      within(verify).getByLabelText("Automatic rounds before the run fails"),
      { target: { value: "3" } },
    );
    const approve = within(flow).getByRole("listitem", {
      name: "Stage approve",
    });
    fireEvent.click(
      within(approve).getByLabelText(
        "Pause for review: approve or request changes",
      ),
    );

    fireEvent.click(screen.getByRole("tab", { name: "YAML" }));
    expect(
      ((await screen.findByLabelText("Rules YAML")) as HTMLTextAreaElement)
        .value,
    ).toContain('"onFailure":"feedback"');
    // Back in the form, the flow is still open and the gate still set.
    fireEvent.click(screen.getByRole("tab", { name: "Form" }));
    const reopened = await screen.findByRole("group", {
      name: "Stages of the fallback flow",
    });
    expect(
      within(
        within(reopened).getByRole("listitem", { name: "Stage approve" }),
      ).getByLabelText("Pause for review: approve or request changes"),
    ).toBeChecked();

    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await screen.findByRole("status");
    expect(JSON.parse(saved[0]).default.stages.slice(1)).toEqual([
      {
        id: "verify",
        kind: "evaluation",
        dependsOn: ["implement"],
        onFailure: "feedback",
        feedback: { target: "implement", maxRounds: 3 },
      },
      {
        id: "approve",
        kind: "approval",
        dependsOn: ["verify"],
        review: true,
      },
    ]);
  });

  it("inserts a shared group into a flow and saves the use, not its stages", async () => {
    const saved: string[] = [];
    window.go!.app!.Service!.Rules = async () => sharedRules();
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.StageLibrary = async (_project, documentJSON) =>
      stageLibrary(documentJSON) as never;
    window.go!.app!.Service!.SaveRules = async (_project, documentJSON) => {
      saved.push(documentJSON);
      return { ...sharedRules(), document: JSON.parse(documentJSON) };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit fallback flow" }),
    );
    const flow = screen.getByRole("group", {
      name: "Stages of the fallback flow",
    });
    // Remove the inline approval and the verify use; the group brings both.
    fireEvent.click(
      within(flow).getByRole("button", { name: "Remove stage approve" }),
    );
    fireEvent.click(
      within(flow).getByRole("button", { name: "Remove stage verify" }),
    );
    fireEvent.change(
      await within(flow).findByLabelText(
        "Shared stage or group to insert into Stages of the fallback flow",
      ),
      { target: { value: "verify-and-approve" } },
    );
    fireEvent.click(within(flow).getByRole("button", { name: "Insert" }));
    const group = within(flow).getByRole("listitem", {
      name: "Stage verify-and-approve",
    });
    expect(group).toHaveTextContent(
      "Uses the built-in group verify-and-approve.",
    );
    expect(group).toHaveTextContent("A use of a group overrides nothing.");
    fireEvent.change(
      within(group).getByLabelText("Dependencies of verify-and-approve"),
      { target: { value: "implement" } },
    );

    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await screen.findByRole("status");
    expect(JSON.parse(saved[0]).default.stages).toEqual([
      { id: "implement", kind: "agent", agent: "auto" },
      {
        use: "verify-and-approve",
        id: "",
        kind: "",
        dependsOn: ["implement"],
      },
    ]);
  });

  it("overrides a shared stage's field for one use and resets another to inherited", async () => {
    const saved: string[] = [];
    window.go!.app!.Service!.Rules = async () => sharedRules();
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.StageLibrary = async (_project, documentJSON) =>
      stageLibrary(documentJSON) as never;
    window.go!.app!.Service!.SaveRules = async (_project, documentJSON) => {
      saved.push(documentJSON);
      return { ...sharedRules(), document: JSON.parse(documentJSON) };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit fallback flow" }),
    );
    const verify = within(
      screen.getByRole("group", { name: "Stages of the fallback flow" }),
    ).getByRole("listitem", { name: "Stage verify" });
    // Inherited from the built-in stage until the use sets it.
    const commands = await within(verify).findByLabelText(
      "Commands for verify",
    );
    expect(commands).toHaveValue("task ci");
    expect(within(verify).getByText("commands inherited")).toBeInTheDocument();
    fireEvent.change(commands, { target: { value: "go test ./..." } });
    expect(within(verify).getByText("commands overridden")).toBeInTheDocument();
    fireEvent.click(
      within(verify).getByLabelText(
        "Run every command, even after one fails (the stage still fails)",
      ),
    );
    fireEvent.click(
      within(verify).getByRole("button", {
        name: "Reset continueOnFailure to inherited",
      }),
    );
    expect(
      within(verify).getByText("continueOnFailure inherited"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await screen.findByRole("status");
    expect(JSON.parse(saved[0]).default.stages[1]).toEqual({
      use: "verify",
      id: "",
      kind: "",
      dependsOn: ["implement"],
      commands: [["go", "test", "./..."]],
      overrides: ["commands"],
    });
  });

  it("points at the flow entry Go refused a save for", async () => {
    window.go!.app!.Service!.Rules = async () => sharedRules();
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.StageLibrary = async (_project, documentJSON) =>
      stageLibrary(documentJSON) as never;
    const reason =
      'default flow: entry 2 (use "verify"): overrides review, which an evaluation stage does not have';
    window.go!.app!.Service!.SaveRules = async () => {
      throw new Error(reason);
    };
    window.go!.app!.Service!.CheckRules = async () => ({
      message: reason,
      flow: { rule: -1, entry: 1 },
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit fallback flow" }),
    );
    fireEvent.change(screen.getAllByLabelText("Title (optional)")[0], {
      target: { value: "Implement" },
    });
    // Saving with the fallback flow closed still opens it at the entry.
    fireEvent.click(
      screen.getByRole("button", { name: "Close fallback flow" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(reason);
    const flow = await screen.findByRole("group", {
      name: "Stages of the fallback flow",
    });
    const verify = within(flow).getByRole("listitem", { name: "Stage verify" });
    expect(verify).toHaveAttribute("aria-invalid", "true");
    expect(verify).toHaveAccessibleDescription(
      `Go refused this entry: ${reason}`,
    );
    expect(
      within(flow).getByRole("listitem", { name: "Stage implement" }),
    ).not.toHaveAttribute("aria-invalid");
  });

  it("keeps a built-in definition read-only and copies it to the repository to change it", async () => {
    const saved: string[] = [];
    let globalSaves = 0;
    window.go!.app!.Service!.Rules = async () => sharedRules();
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.StageLibrary = async (_project, documentJSON) =>
      stageLibrary(documentJSON) as never;
    window.go!.app!.Service!.SaveGlobalDefinition = async () => {
      globalSaves += 1;
    };
    window.go!.app!.Service!.SaveRules = async (_project, documentJSON) => {
      saved.push(documentJSON);
      return { ...sharedRules(), document: JSON.parse(documentJSON) };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    const builtin = await screen.findByRole("listitem", {
      name: "built-in stage verify",
    });
    fireEvent.click(
      within(builtin).getByRole("button", {
        name: "View built-in stage verify",
      }),
    );
    // Go's reason, and nothing that would send an edit.
    expect(builtin).toHaveTextContent(
      "Built-in definitions are part of Inkuna and cannot be edited.",
    );
    expect(
      within(builtin).getByLabelText("Commands for verify"),
    ).toBeDisabled();
    expect(within(builtin).getByLabelText("Kind of verify")).toBeDisabled();
    expect(within(builtin).queryByRole("button", { name: /^Save/ })).toBeNull();

    fireEvent.click(
      within(builtin).getByRole("button", {
        name: "Copy built-in stage verify to the repository",
      }),
    );
    const copy = await screen.findByRole("listitem", {
      name: "repository stage verify",
    });
    expect(
      screen.getByRole("listitem", { name: "built-in stage verify" }),
    ).toHaveTextContent("overridden");
    fireEvent.click(
      within(copy).getByRole("button", {
        name: "Edit repository stage verify",
      }),
    );
    fireEvent.change(within(copy).getByLabelText("Commands for verify"), {
      target: { value: "task test" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await screen.findByRole("status");
    expect(JSON.parse(saved[0]).stages).toEqual({
      verify: { id: "", kind: "evaluation", commands: [["task", "test"]] },
    });
    expect(globalSaves).toBe(0);
  });

  it("creates, customizes, imports, and deletes agents", async () => {
    const card = (name: string) => ({
      name,
      description: "",
      version: "1",
      protocolVersion: "0.3",
      skills: [],
    });
    const builtIn = {
      definition: {
        name: "implementer",
        description: "Writes code",
        permission: "worktree-write",
        instructions: "Implement the request.",
        source: "builtin",
        extra: "color: green\n",
      },
      card: card("implementer"),
    };
    let agents: Array<Record<string, unknown>> = [builtIn];
    const saves: Array<[string, string, Record<string, unknown>]> = [];
    const deletes: string[] = [];
    window.go!.app!.Service!.Agents = async () => agents as never;
    window.go!.app!.Service!.AgentLocations = async () => ({
      repository: "/tmp/inkuna/.inkuna/agents",
      global: "/tmp/data/agents",
    });
    window.go!.app!.Service!.Configuration = async () =>
      ({
        path: "",
        exists: true,
        dataDir: "",
        file: { secrets: [{ name: "SNYK_TOKEN" }] },
        secrets: [],
      }) as never;
    window.go!.app!.Service!.ParseAgent = async (content) => {
      if (!content.startsWith("---")) throw new Error("missing frontmatter");
      return {
        name: "migration-writer",
        description: "Writes migrations",
        permission: "worktree-write",
        instructions: "Write reversible migrations.",
        secrets: ["OLD_TOKEN"],
        source: "",
      };
    };
    window.go!.app!.Service!.SaveAgent = async (
      _project,
      scope,
      originalName,
      definitionJSON,
    ) => {
      const definition = JSON.parse(definitionJSON);
      saves.push([scope, originalName, definition]);
      const view = {
        definition: {
          ...definition,
          source: scope,
          path: `/tmp/inkuna/.inkuna/agents/${definition.name}.md`,
        },
        card: card(definition.name),
        overrides: definition.name === "implementer" ? "builtin" : undefined,
      };
      agents = [
        ...agents.filter(
          (agent) =>
            (agent.definition as { name: string }).name !== definition.name,
        ),
        view,
      ];
      return view as never;
    };
    let finishDelete = () => {};
    window.go!.app!.Service!.DeleteAgent = (_project, scope, name) => {
      deletes.push(`${scope}/${name}`);
      agents = [builtIn];
      return new Promise((resolve) => (finishDelete = resolve));
    };

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Agents/ }));

    // New agent: declared secrets are offered, and the save carries the form.
    fireEvent.click(await screen.findByRole("button", { name: "+ New agent" }));
    const form = screen.getByRole("form", { name: "New agent" });
    fireEvent.change(within(form).getByLabelText(/^Name/), {
      target: { value: "security-reviewer" },
    });
    fireEvent.change(within(form).getByLabelText(/^Description/), {
      target: { value: "Reviews for security issues" },
    });
    fireEvent.click(within(form).getByLabelText("review"));
    fireEvent.click(within(form).getByLabelText("SNYK_TOKEN"));
    fireEvent.change(within(form).getByLabelText(/^Instructions/), {
      target: { value: "Check for injection." },
    });
    expect(
      within(form).getByText(
        "Saved as /tmp/inkuna/.inkuna/agents/security-reviewer.md",
      ),
    ).toBeInTheDocument();
    fireEvent.click(within(form).getByRole("button", { name: "Save agent" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "security-reviewer.md",
    );
    expect(saves[0][0]).toBe("repository");
    expect(saves[0][1]).toBe("");
    expect(saves[0][2]).toMatchObject({
      name: "security-reviewer",
      permission: "read-only",
      taskKinds: ["review"],
      secrets: ["SNYK_TOKEN"],
    });

    // Customizing a built-in saves an override under the same name.
    fireEvent.click(
      screen.getByRole("button", { name: "Customize implementer" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save agent" }));
    const overridden = await screen.findByRole("article", {
      name: "implementer",
    });
    expect(
      within(overridden).getByText("overrides builtin"),
    ).toBeInTheDocument();
    // The keys Go kept for another tool go back with the save untouched.
    expect(saves[1][2]).toMatchObject({
      name: "implementer",
      extra: "color: green\n",
    });

    // Import validates in Go and opens the form for review instead of saving.
    const picker = screen.getByLabelText("Agent file to import");
    const upload = (text: string) => {
      const file = new File([text], "agent.md", { type: "text/markdown" });
      file.text = async () => text;
      fireEvent.change(picker, { target: { files: [file] } });
    };
    upload("not an agent");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "agent.md: Error: missing frontmatter",
    );
    upload("---\nname: migration-writer\n---\nbody");
    const imported = await screen.findByRole("form", { name: "New agent" });
    expect(within(imported).getByLabelText(/^Name/)).toHaveValue(
      "migration-writer",
    );
    expect(saves).toHaveLength(2);
    // A secret Settings no longer declares stays listed so it can be removed.
    const stale = within(imported).getByLabelText(/OLD_TOKEN/);
    expect(stale).toBeChecked();
    fireEvent.click(stale);
    expect(within(imported).getByLabelText(/OLD_TOKEN/)).not.toBeChecked();
    fireEvent.click(within(imported).getByRole("button", { name: "Cancel" }));

    // A pending delete locks the project, and one opened for another project
    // is dropped instead of being confirmed against this one.
    fireEvent.click(screen.getByRole("button", { name: "Delete implementer" }));
    const selector = screen.getByLabelText("Agents project");
    expect(selector).toBeDisabled();
    fireEvent.change(selector, { target: { value: "" } });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(deletes).toEqual([]);
    fireEvent.change(selector, { target: { value: project.id } });

    // Deleting asks first and explains what comes back.
    fireEvent.click(
      await screen.findByRole("button", { name: "Delete implementer" }),
    );
    const confirm = screen.getByRole("dialog", { name: "Delete implementer?" });
    expect(confirm).toHaveTextContent(
      "The builtin agent with the same name applies again.",
    );
    // A second confirmation is ignored, and the dialog stays until the delete
    // settles.
    const confirmDelete = within(confirm).getByRole("button", {
      name: "Delete agent",
    });
    fireEvent.click(confirmDelete);
    fireEvent.click(confirmDelete);
    await waitFor(() => expect(confirmDelete).toBeDisabled());
    expect(
      within(confirm).getByRole("button", { name: "Cancel" }),
    ).toBeDisabled();
    fireEvent.keyDown(confirm, { key: "Escape" });
    expect(confirm).toBeInTheDocument();
    await act(async () => finishDelete());
    expect(deletes).toEqual(["repository/implementer"]);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "the builtin implementer applies again",
    );

    // Another project's locations never stand in while this one's load.
    window.go!.app!.Service!.AgentLocations = () => new Promise(() => {});
    fireEvent.change(screen.getByLabelText("Agents project"), {
      target: { value: "" },
    });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "+ New agent" }),
      ).toBeDisabled(),
    );
  });

  it("connects a remote advisor after reviewing its card, then disables, re-pins, and removes it", async () => {
    const pin = {
      digest: "sha256:one",
      url: "https://advisor.example/a2a",
      protocolVersion: "0.3.0",
      auth: "bearer",
      fetchedAt: "2026-09-19T12:00:00Z",
    };
    const preview = (changed: boolean) => ({
      card: {
        name: "Security Advisor",
        description: changed
          ? "Reviews plans. Now also stores them."
          : "Reviews plans for security issues",
        url: pin.url,
        version: "1.0.0",
        protocolVersion: "0.3.0",
        provider: { organization: "Example Corp" },
        capabilities: { streaming: false },
        defaultInputModes: [],
        defaultOutputModes: [],
        skills: [
          {
            id: "threat-review",
            name: "Threat review",
            description: "Finds security risks in a plan",
          },
        ],
        digest: changed ? "sha256:two" : "sha256:one",
      },
      pin: changed ? { ...pin, digest: "sha256:two" } : pin,
      suggestedName: "security-advisor",
      samplePayload: '{"objective": "Add rate limiting"}',
      samplePayloadWithArtifacts:
        '{"objective": "Add rate limiting", "artifactBodies": []}',
      changed,
      changes: changed
        ? [
            {
              field: "Description",
              before: "Reviews plans for security issues",
              after: "Reviews plans. Now also stores them.",
            },
          ]
        : [],
    });
    let remote: Array<Record<string, unknown>> = [];
    let cardChanged = false;
    const previews: string[][] = [];
    const saves: Array<Record<string, unknown>> = [];
    const repins: string[] = [];
    const service = window.go!.app!.Service!;
    service.Agents = async () => [];
    service.AgentLocations = async () => ({ global: "/tmp/data/agents" });
    service.Configuration = async () =>
      ({
        path: "",
        exists: true,
        dataDir: "",
        file: { secrets: [{ name: "ADVISOR_TOKEN" }] },
        secrets: [],
      }) as never;
    service.RemoteAgents = async () => remote as never;
    service.PreviewRemoteAgent = async (cardURL, existingName) => {
      previews.push([cardURL, existingName]);
      if (cardURL.includes("internal"))
        throw new Error("remote agents must be on a public address");
      return preview(cardChanged) as never;
    };
    service.SaveRemoteAgent = async (requestJSON) => {
      const request = JSON.parse(requestJSON);
      saves.push(request);
      remote = [
        {
          name: request.name,
          cardUrl: request.cardUrl,
          enabled: request.enabled,
          secret: request.secret,
          auto: request.auto,
          sendArtifacts: request.sendArtifacts,
          description: "Reviews plans for security issues",
          skills: ["Threat review"],
          pinned: pin,
          secretStatus: {
            name: request.secret,
            source: "missing",
            available: false,
          },
        },
      ];
      return remote as never;
    };
    service.SetRemoteAgentEnabled = async (name, enabled) => {
      remote = remote.map((agent) =>
        agent.name === name ? { ...agent, enabled } : agent,
      );
      return remote as never;
    };
    service.RepinRemoteAgent = async (name, digest) => {
      repins.push(`${name}@${digest}`);
      return remote as never;
    };
    service.DeleteRemoteAgent = async () => {
      remote = [];
      return remote as never;
    };

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Agents/ }));
    expect(
      await screen.findByText("No remote agents are connected."),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "+ Connect agent" }));
    const form = screen.getByRole("form", { name: "Connect agent" });

    // Nothing can be saved before the card has been fetched and shown.
    expect(
      within(form).queryByRole("button", { name: "Pin card and save" }),
    ).not.toBeInTheDocument();
    fireEvent.change(within(form).getByLabelText("Agent Card URL"), {
      target: { value: "https://internal.example/card.json" },
    });
    fireEvent.click(within(form).getByRole("button", { name: "Fetch card" }));
    expect(await within(form).findByRole("alert")).toHaveTextContent(
      "public address",
    );
    fireEvent.change(within(form).getByLabelText("Agent Card URL"), {
      target: {
        value: "https://advisor.example/.well-known/agent-card.json",
      },
    });
    fireEvent.click(within(form).getByRole("button", { name: "Fetch card" }));

    // The review shows who the agent is, where data goes, and what is sent.
    expect(await within(form).findByText(/Example Corp/)).toBeInTheDocument();
    expect(within(form).getByText(/Threat review/)).toBeInTheDocument();
    expect(
      within(form).getByText("https://advisor.example/a2a"),
    ).toBeInTheDocument();
    expect(within(form).getByLabelText("Agent name")).toHaveValue(
      "security-advisor",
    );
    fireEvent.click(
      within(form).getByRole("button", {
        name: /What a stage would send/,
      }),
    );
    expect(within(form).getByText(/"objective"/)).not.toHaveTextContent(
      "artifactBodies",
    );
    fireEvent.click(
      within(form).getByLabelText(/Send the text of upstream artifacts/),
    );
    expect(within(form).getByText(/"objective"/)).toHaveTextContent(
      "artifactBodies",
    );
    fireEvent.click(
      within(form).getByLabelText(/Send the text of upstream artifacts/),
    );
    fireEvent.change(within(form).getByLabelText("Token secret"), {
      target: { value: "ADVISOR_TOKEN" },
    });
    fireEvent.click(
      within(form).getByRole("button", { name: "Pin card and save" }),
    );
    await waitFor(() =>
      expect(saves).toEqual([
        {
          name: "security-advisor",
          cardUrl: "https://advisor.example/.well-known/agent-card.json",
          secret: "ADVISOR_TOKEN",
          auto: false,
          sendArtifacts: false,
          maxArtifactBytes: 0,
          enabled: true,
          digest: "sha256:one",
        },
      ]),
    );

    const card = await screen.findByRole("article", {
      name: "security-advisor",
    });
    expect(within(card).getByText("remote")).toBeInTheDocument();
    expect(within(card).getByText("read-only")).toBeInTheDocument();
    expect(within(card).getByText("enabled")).toBeInTheDocument();
    expect(within(card).getByText("secret has no value")).toBeInTheDocument();

    fireEvent.click(
      within(card).getByRole("button", { name: "Disable security-advisor" }),
    );
    expect(await within(card).findByText("disabled")).toBeInTheDocument();

    // Settings keep the pin: no digest is sent when the URL is unchanged.
    fireEvent.click(
      within(card).getByRole("button", {
        name: "Settings for security-advisor",
      }),
    );
    const settings = screen.getByRole("form", {
      name: "Settings for security-advisor",
    });
    fireEvent.click(within(settings).getByLabelText(/Let the orchestrator/));
    fireEvent.click(within(settings).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(saves).toHaveLength(2));
    expect(saves[1]).toMatchObject({ auto: true, digest: "" });

    // A changed card is shown field by field before it can be pinned again.
    cardChanged = true;
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Check the card of security-advisor",
      }),
    );
    const review = await screen.findByRole("group", {
      name: "Card of security-advisor",
    });
    expect(
      await within(review).findByText("The card changed since you reviewed it"),
    ).toBeInTheDocument();
    const row = within(review).getByRole("row", { name: /Description/ });
    expect(row).toHaveTextContent("Reviews plans for security issues");
    expect(row).toHaveTextContent("Now also stores them.");
    fireEvent.click(
      within(review).getByRole("button", { name: "Pin this card" }),
    );
    await waitFor(() =>
      expect(repins).toEqual(["security-advisor@sha256:two"]),
    );
    expect(previews.at(-1)).toEqual(["", "security-advisor"]);

    fireEvent.click(
      await screen.findByRole("button", { name: "Remove security-advisor" }),
    );
    const dialog = screen.getByRole("dialog", {
      name: "Remove security-advisor?",
    });
    expect(dialog).toHaveTextContent("advisor.example");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Remove agent" }),
    );
    expect(
      await screen.findByText("No remote agents are connected."),
    ).toBeInTheDocument();
  });

  it("holds a stage staffed by a remote advisor to read-only with no runtime", async () => {
    const saved: string[] = [];
    const service = window.go!.app!.Service!;
    service.Rules = async () => ({
      document: {
        version: 1,
        source: "",
        rules: [],
        default: {
          stages: [
            {
              id: "review",
              kind: "agent",
              agent: "implementer",
              runtime: "claude",
              permission: "worktree-write",
            },
            { id: "approve", kind: "approval", dependsOn: ["review"] },
          ],
        },
      } as never,
      exists: true,
      path: "/tmp/inkuna/.inkuna/rules.yaml",
    });
    service.Agents = async () =>
      [
        {
          definition: {
            name: "implementer",
            description: "Writes code",
            permission: "worktree-write",
            instructions: "Implement.",
            source: "builtin",
          },
          card: { name: "implementer" },
        },
      ] as never;
    service.RemoteAgents = async () =>
      [
        {
          name: "security-advisor",
          cardUrl: "https://advisor.example/card.json",
          enabled: true,
          pinned: { digest: "sha256:one", url: "https://advisor.example/a2a" },
        },
        {
          name: "unreviewed-advisor",
          cardUrl: "https://other.example/card.json",
          enabled: false,
        },
      ] as never;
    service.SaveRules = async (_project, documentJSON) => {
      saved.push(documentJSON);
      return {
        document: JSON.parse(documentJSON),
        exists: true,
        path: "/tmp/inkuna/.inkuna/rules.yaml",
      };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit fallback flow" }),
    );
    const stage = await screen.findByRole("listitem", { name: "Stage review" });
    const agent = within(stage).getByLabelText("Agent");
    await within(stage).findByRole("option", {
      name: "security-advisor (remote, read-only)",
    });
    // Only an enabled, reviewed advisor can be named.
    expect(
      within(stage).queryByRole("option", { name: /unreviewed-advisor/ }),
    ).not.toBeInTheDocument();

    fireEvent.change(agent, { target: { value: "security-advisor" } });
    expect(within(stage).getByLabelText("Permission")).toHaveValue("read-only");
    expect(within(stage).getByLabelText("Permission")).toBeDisabled();
    expect(within(stage).getByLabelText("Runtime")).toHaveValue("");
    expect(within(stage).getByLabelText("Runtime")).toBeDisabled();
    expect(
      within(stage).getByRole("option", { name: "worktree-write" }),
    ).toBeDisabled();

    // Back on a local agent the controls are the user's again.
    fireEvent.change(agent, { target: { value: "implementer" } });
    expect(within(stage).getByLabelText("Permission")).toBeEnabled();
    expect(within(stage).getByLabelText("Runtime")).toBeEnabled();
    fireEvent.change(agent, { target: { value: "security-advisor" } });

    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(saved).toHaveLength(1));
    const review = JSON.parse(saved[0]).default.stages[0];
    expect(review).toMatchObject({
      agent: "security-advisor",
      permission: "read-only",
    });
    expect(review.runtime).toBeUndefined();
  });

  it("shows what was sent to a remote advisor before its reply", async () => {
    const sent = JSON.stringify({
      message: {
        role: "user",
        parts: [
          { kind: "text", text: "Review the login flow" },
          { kind: "data", data: { objective: "Review the login flow" } },
        ],
      },
    });
    const summary = {
      taskId: "a2a-remote",
      contextId: "task-test",
      agent: "security-advisor",
      state: "completed",
      messageCount: 2,
      updatedAt: "2026-01-01T00:00:10Z",
      runId: "run-test",
      runTitle: "Fix cancellation",
      preview: "Add rate limiting.",
    };
    window.go!.app!.Service!.Threads = async () => [summary];
    window.go!.app!.Service!.Thread = async () => ({
      ...summary,
      stepId: "advice",
      messages: [
        {
          messageId: "m1",
          role: "user",
          parts: [{ kind: "text" as const, text: "Review the login flow" }],
          timestamp: "2026-01-01T00:00:01Z",
        },
        {
          messageId: "m2",
          role: "agent",
          parts: [{ kind: "text" as const, text: "Add rate limiting." }],
          timestamp: "2026-01-01T00:00:09Z",
        },
      ],
      artifacts: [
        {
          artifactId: "artifact-1",
          name: "remote-request",
          description:
            "Exactly what was sent to security-advisor at advisor.example",
          parts: [{ kind: "text" as const, text: sent }],
          metadata: { outbound: true, bytes: sent.length },
        },
        {
          // Named like Inkuna's record, but without the marker only Inkuna sets.
          artifactId: "artifact-spoof",
          name: "remote-request",
          description: "Exactly what was sent: nothing at all",
          parts: [{ kind: "text" as const, text: "Nothing was sent." }],
        },
        {
          artifactId: "artifact-2",
          name: "report",
          parts: [{ kind: "text" as const, text: "Risk 1: no lockout." }],
        },
      ],
      inputs: [],
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Messages/ }));
    const pane = await screen.findByRole("article", {
      name: "Thread with security-advisor",
    });
    expect(
      within(pane).getByText(/Exactly what was sent to security-advisor/),
    ).toHaveTextContent(`(${sent.length} bytes)`);
    const disclosure = within(pane).getByRole("button", {
      name: /Outbound payload/,
    });
    // The payload sits between the request and the agent's answer.
    const reply = within(pane).getByText("Add rate limiting.");
    expect(
      disclosure.compareDocumentPosition(reply) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    fireEvent.click(disclosure);
    expect(within(pane).getByText(/"objective"/)).toBeInTheDocument();
    // It is not repeated among the agent's own artifacts, and an artifact the
    // agent named like it stays one of the agent's.
    expect(within(pane).getByText("Risk 1: no lockout.")).toBeInTheDocument();
    expect(
      within(pane).getAllByRole("button", { name: /Outbound payload/ }),
    ).toHaveLength(1);
    expect(within(pane).getByText("remote-request")).toBeInTheDocument();
    expect(within(pane).getByText("Nothing was sent.")).toBeInTheDocument();
    expect(within(pane).queryByText(/nothing at all/)).not.toBeInTheDocument();
  });

  it("ignores an agent list that arrives after a newer request", async () => {
    const listed = (name: string) => ({
      definition: {
        name,
        description: "",
        permission: "read-only",
        instructions: "x",
        source: "builtin",
      },
      card: {
        name,
        description: "",
        version: "1",
        protocolVersion: "0.3",
        skills: [],
      },
    });
    let finishSlow: (value: never) => void = () => {};
    window.go!.app!.Service!.Agents = (projectID) =>
      projectID
        ? new Promise((resolve) => (finishSlow = resolve))
        : Promise.resolve([listed("global-agent")] as never);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Agents/ }));
    fireEvent.change(await screen.findByLabelText("Agents project"), {
      target: { value: "" },
    });
    expect(
      await screen.findByRole("article", { name: "global-agent" }),
    ).toBeInTheDocument();
    await act(async () => finishSlow([listed("project-agent")] as never));
    expect(
      screen.queryByRole("article", { name: "project-agent" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("article", { name: "global-agent" }),
    ).toBeInTheDocument();
  });

  it("moves between run tabs with the keyboard and labels the panel", async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Tasks/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: /Fix cancellation/ }),
    );
    const timeline = await screen.findByRole("tab", { name: "Timeline" });
    const cost = screen.getByRole("tab", { name: "Cost" });
    expect(timeline).toHaveAttribute("tabindex", "0");
    expect(cost).toHaveAttribute("tabindex", "-1");
    expect(screen.getByRole("tabpanel")).toHaveAttribute(
      "aria-labelledby",
      timeline.id,
    );

    // ArrowLeft from the first tab wraps to the last and moves focus there.
    fireEvent.keyDown(timeline, { key: "ArrowLeft" });
    expect(cost).toHaveAttribute("aria-selected", "true");
    expect(cost).toHaveFocus();
    expect(screen.getByRole("tabpanel").id).toBe(
      cost.getAttribute("aria-controls"),
    );
    fireEvent.keyDown(cost, { key: "Home" });
    expect(timeline).toHaveAttribute("aria-selected", "true");
    expect(timeline).toHaveFocus();
  });

  it("keeps each stage's commands with it when stages move, and reports a save whose YAML refresh fails", async () => {
    const document = {
      version: 1,
      source: "",
      rules: [],
      default: {
        stages: [
          { id: "lint", kind: "script", commands: [["task", "lint"]] },
          {
            id: "test",
            kind: "script",
            commands: [["go", "test", "-run", 'Test "A"', ""]],
          },
          { id: "approve", kind: "approval" },
        ],
      },
    };
    let yamlCalls = 0;
    window.go!.app!.Service!.Rules = async () => ({
      document,
      exists: true,
      path: "/tmp/inkuna/.inkuna/rules.yaml",
    });
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.RulesToYAML = async (_project, documentJSON) => {
      yamlCalls += 1;
      if (yamlCalls > 1) throw new Error("render failed");
      return documentJSON;
    };
    window.go!.app!.Service!.RulesFromYAML = async (_project, content) =>
      JSON.parse(content);
    window.go!.app!.Service!.SaveRules = async (_project, documentJSON) => ({
      document: JSON.parse(documentJSON),
      exists: true,
      path: "/tmp/inkuna/.inkuna/rules.yaml",
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Rules/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit fallback flow" }),
    );
    expect(screen.getByLabelText("Commands for lint")).toHaveValue("task lint");
    expect(screen.getByLabelText("Commands for test")).toHaveValue(
      'go test -run "Test \\"A\\"" ""',
    );

    // Moving a stage moves its command text with it.
    fireEvent.click(
      screen.getByRole("button", { name: "Move stage lint later" }),
    );
    const stages = screen
      .getAllByRole("listitem")
      .map((item) => item.getAttribute("aria-label"));
    expect(stages.slice(0, 2)).toEqual(["Stage test", "Stage lint"]);
    expect(screen.getByLabelText("Commands for lint")).toHaveValue("task lint");
    expect(screen.getByLabelText("Commands for test")).toHaveValue(
      'go test -run "Test \\"A\\"" ""',
    );

    // Save from the YAML tab: the write succeeds, the re-render fails.
    fireEvent.click(screen.getByRole("tab", { name: "YAML" }));
    await screen.findByLabelText("Rules YAML");
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Saved to /tmp/inkuna/.inkuna/rules.yaml",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Saved, but the YAML view could not be refreshed: Error: render failed",
    );
    expect(screen.queryByText("unsaved changes")).not.toBeInTheDocument();
  });

  it("saves configuration and stores a secret without echoing the value", async () => {
    let savedConfig = "";
    const storedSecrets: Array<[string, string]> = [];
    const configuration = {
      path: "/tmp/inkuna/inkuna.yaml",
      exists: false,
      dataDir: "/tmp/inkuna",
      file: {
        version: 1,
        orchestrator: { runtime: "claude" as const, maxTurns: 6 },
        runtimes: { codex: {}, claude: {}, copilot: {} },
        concurrency: { maxTasks: 3, maxWorkers: 2 },
        timeouts: {},
        notifications: { desktop: true },
        secrets: [],
        repositories: [],
        agents: {},
        stages: {},
        linear: {},
        github: {},
        routing: {},
      },
      secrets: [],
    };
    window.go!.app!.Service!.Configuration = async () => configuration;
    window.go!.app!.Service!.SaveConfiguration = async (fileJSON) => {
      savedConfig = fileJSON;
      return { ...configuration, exists: true, file: JSON.parse(fileJSON) };
    };
    window.go!.app!.Service!.SaveSecret = async (name, value) => {
      storedSecrets.push([name, value]);
      return {
        ...configuration,
        exists: true,
        secrets: [{ name, source: "keychain", available: true }],
      };
    };
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Settings/ }));
    expect(
      await screen.findByText("inkuna.yaml not created yet"),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Orchestrator runtime"), {
      target: { value: "codex" },
    });
    // The default stays selectable after another runtime was chosen.
    expect(
      within(screen.getByLabelText("Orchestrator runtime")).getByRole(
        "option",
        { name: "Default (Claude Code)" },
      ),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Max tasks"), {
      target: { value: "5" },
    });
    // Clearing a number, or going below its minimum, never stores 0: the
    // field is flagged and the configuration keeps its last valid value.
    const workers = screen.getByLabelText("Max workers");
    fireEvent.change(workers, { target: { value: "" } });
    expect(workers).toHaveAttribute("aria-invalid", "true");
    fireEvent.change(workers, { target: { value: "0" } });
    fireEvent.blur(workers);
    expect(workers).toHaveValue(2);
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    expect(await screen.findByText("Configuration saved")).toBeInTheDocument();
    const parsed = JSON.parse(savedConfig);
    expect(parsed.version).toBe(1);
    expect(parsed.orchestrator.runtime).toBe("codex");
    expect(parsed.concurrency.maxTasks).toBe(5);
    expect(parsed.concurrency.maxWorkers).toBe(2);
    fireEvent.change(screen.getByLabelText("Secret name"), {
      target: { value: "ANTHROPIC_API_KEY" },
    });
    fireEvent.change(screen.getByLabelText("Secret value"), {
      target: { value: "sk-ant-secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Store in keychain" }));
    expect(
      await screen.findByText("Stored ANTHROPIC_API_KEY in the keychain"),
    ).toBeInTheDocument();
    expect(storedSecrets).toEqual([["ANTHROPIC_API_KEY", "sk-ant-secret"]]);
    expect(screen.queryByDisplayValue("sk-ant-secret")).toBeNull();
    expect(screen.queryByText("sk-ant-secret")).toBeNull();
    expect(screen.getByText("available")).toBeInTheDocument();
  });

  it("edits every inkuna.yaml section, shows overrides and field errors, and forgets a secret", async () => {
    const band = (minimum: number, maximum: number, model: string) => ({
      minimum,
      maximum,
      model,
    });
    const defaults = {
      version: 1,
      orchestrator: { runtime: "claude" as const, maxTurns: 6 },
      runtimes: {
        codex: { binary: "codex" },
        claude: { binary: "claude" },
        copilot: { binary: "copilot" },
      },
      concurrency: { maxTasks: 3, maxWorkers: 2 },
      timeouts: { planning: "5m", worker: "30m" },
      notifications: { desktop: true },
      secrets: [],
      repositories: [],
      agents: {},
      stages: {},
      linear: { endpoint: "https://api.linear.app/graphql" },
      github: { host: "github.com", protocol: "https", binary: "gh" },
      routing: {},
    };
    let configuration: ConfigurationInfo = {
      path: "/tmp/inkuna/inkuna.yaml",
      exists: true,
      dataDir: "/tmp/inkuna",
      file: {
        ...defaults,
        runtimes: {
          codex: {},
          claude: { secrets: ["NPM_TOKEN"] },
          copilot: {},
        },
        timeouts: {},
        secrets: [{ name: "NPM_TOKEN" }],
        linear: {},
        github: {},
      },
      secrets: [{ name: "NPM_TOKEN", source: "keychain", available: true }],
      defaults,
      overrides: [
        {
          field: "timeouts.worker",
          variable: "INKUNA_WORKER_TIMEOUT",
          value: "45m",
        },
      ],
      routingDefaults: {
        version: 1,
        bands: {
          codex: [band(1, 10, "gpt")],
          claude: [band(1, 3, "fable"), band(4, 10, "opus")],
          copilot: [band(1, 10, "haiku")],
        },
      },
    };
    const saves: string[] = [];
    const forgotten: string[] = [];
    const events: Record<string, () => void> = {};
    window.runtime = {
      EventsOnMultiple: (name: string, callback: () => void) => {
        events[name] = callback;
        return () => {};
      },
    };
    window.go!.app!.Service!.Configuration = async () => configuration as never;
    window.go!.app!.Service!.ValidateConfiguration = async (fileJSON) =>
      JSON.parse(fileJSON).timeouts.planning === "soon"
        ? [
            {
              field: "timeouts.planning",
              message: 'timeout "soon" must be a positive Go duration',
            },
          ]
        : [];
    window.go!.app!.Service!.SaveConfiguration = async (fileJSON) => {
      saves.push(fileJSON);
      configuration = { ...configuration, file: JSON.parse(fileJSON) };
      return configuration as never;
    };
    window.go!.app!.Service!.RemoveSecretDeclaration = async (name) => {
      forgotten.push(name);
      configuration = {
        ...configuration,
        secrets: [],
        file: {
          ...configuration.file,
          secrets: [],
          runtimes: { codex: {}, claude: {}, copilot: {} },
        },
      };
      return configuration as never;
    };
    window.go!.app!.Service!.ChooseDirectory = async () => "/agents/team";

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Settings/ }));

    // A field the environment overrides says so and cannot be edited.
    const worker = await screen.findByLabelText(/^Worker step/);
    expect(worker).toBeDisabled();
    expect(
      screen.getByText("overridden by $INKUNA_WORKER_TIMEOUT=45m"),
    ).toBeInTheDocument();
    // Blank fields show what they resolve to.
    expect(screen.getByLabelText(/^Planning/)).toHaveAttribute(
      "placeholder",
      "5m",
    );

    fireEvent.change(screen.getByLabelText(/^Planning/), {
      target: { value: "soon" },
    });
    fireEvent.change(screen.getByLabelText("Orchestrator reasoning effort"), {
      target: { value: "high" },
    });
    fireEvent.change(screen.getByLabelText(/^Linear GraphQL endpoint/), {
      target: { value: "https://linear.example/graphql" },
    });
    fireEvent.change(screen.getByLabelText(/^GitHub host/), {
      target: { value: "github.example" },
    });
    fireEvent.change(screen.getByLabelText(/^Git protocol/), {
      target: { value: "ssh" },
    });
    fireEvent.change(screen.getByLabelText(/^GitHub CLI binary/), {
      target: { value: "/opt/gh" },
    });
    fireEvent.change(screen.getByLabelText("Description of NPM_TOKEN"), {
      target: { value: "Registry token" },
    });
    fireEvent.click(screen.getByLabelText("NPM_TOKEN is required"));

    // The native picker fills in a directory; typing works too.
    const storage = screen
      .getByRole("heading", { name: "Agent directories & storage" })
      .closest("section, div")!;
    fireEvent.click(
      within(storage as HTMLElement).getByRole("button", { name: "Browse…" }),
    );
    expect(await screen.findByText("/agents/team")).toBeInTheDocument();

    // A repository carries its runtime, rules, and docs overrides.
    fireEvent.change(screen.getByLabelText("Repository name"), {
      target: { value: "inkuna" },
    });
    fireEvent.change(screen.getByLabelText("Repository path"), {
      target: { value: "/src/inkuna" },
    });
    fireEvent.change(screen.getByLabelText("Repository runtime"), {
      target: { value: "codex" },
    });
    fireEvent.change(screen.getByLabelText("Repository rules file"), {
      target: { value: "rules/house.yaml" },
    });
    fireEvent.change(screen.getByLabelText("Repository docs directory"), {
      target: { value: "handbook" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add repository" }));

    // Built-in bands are read-only until a runtime is customized.
    const routing = screen.getByRole("group", { name: "Claude Code routing" });
    expect(within(routing).getByText("built-in")).toBeInTheDocument();
    fireEvent.click(
      within(routing).getByRole("button", {
        name: "Customize bands for Claude Code",
      }),
    );
    fireEvent.change(
      within(routing).getByLabelText("Claude Code band 2 model"),
      { target: { value: "sonnet" } },
    );

    // Go's problems land beside their field and block the save.
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    expect(
      await screen.findByText('timeout "soon" must be a positive Go duration'),
    ).toBeInTheDocument();
    expect(saves).toHaveLength(0);

    // A configuration event does not throw away unsaved edits.
    await act(async () => events["inkuna:config"]?.());
    expect(screen.getByLabelText(/^Planning/)).toHaveValue("soon");

    fireEvent.change(screen.getByLabelText(/^Planning/), {
      target: { value: "2m" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    expect(await screen.findByText("Configuration saved")).toBeInTheDocument();
    expect(
      screen.queryByText('timeout "soon" must be a positive Go duration'),
    ).not.toBeInTheDocument();
    expect(JSON.parse(saves[0])).toMatchObject({
      orchestrator: { reasoningEffort: "high" },
      // Runtimes belong to their own view; the save carries the saved ones.
      runtimes: { codex: {}, claude: { secrets: ["NPM_TOKEN"] }, copilot: {} },
      timeouts: { planning: "2m" },
      secrets: [
        { name: "NPM_TOKEN", description: "Registry token", required: true },
      ],
      repositories: [
        {
          name: "inkuna",
          path: "/src/inkuna",
          defaultRuntime: "codex",
          rules: "rules/house.yaml",
          docs: "handbook",
        },
      ],
      agents: { directories: ["/agents/team"] },
      linear: { endpoint: "https://linear.example/graphql" },
      github: { host: "github.example", protocol: "ssh", binary: "/opt/gh" },
      routing: {
        bands: { claude: [band(1, 3, "fable"), band(4, 10, "sonnet")] },
      },
    });

    // Forgetting asks first, then removes the declaration and its exports.
    fireEvent.click(screen.getByRole("button", { name: "Forget NPM_TOKEN" }));
    const confirm = screen.getByRole("dialog", { name: "Forget NPM_TOKEN?" });
    fireEvent.click(
      within(confirm).getByRole("button", { name: "Forget secret" }),
    );
    expect(
      await screen.findByText(
        "Forgot NPM_TOKEN: value, declaration, and exports",
      ),
    ).toBeInTheDocument();
    expect(forgotten).toEqual(["NPM_TOKEN"]);
    expect(
      screen.queryByLabelText("Description of NPM_TOKEN"),
    ).not.toBeInTheDocument();

    // Retyping the path of a row being edited moves it instead of leaving the
    // old entry behind, and a save that relocates data asks for a restart.
    window.go!.app!.Service!.SaveConfiguration = async (fileJSON) => {
      saves.push(fileJSON);
      return {
        ...configuration,
        file: JSON.parse(fileJSON),
        restartRequired: true,
      } as never;
    };
    fireEvent.click(
      screen.getByRole("button", { name: "Edit repository inkuna" }),
    );
    fireEvent.change(screen.getByLabelText("Repository path"), {
      target: { value: "/src/moved" },
    });
    fireEvent.change(screen.getByLabelText("Repository rules file"), {
      target: { value: "" },
    });
    fireEvent.change(screen.getByLabelText("Repository docs directory"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Update repository" }));
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    expect(
      await screen.findByText(/Restart Inkuna to use the new data directory/),
    ).toBeInTheDocument();
    expect(JSON.parse(saves[saves.length - 1]).repositories).toEqual([
      { name: "inkuna", path: "/src/moved", defaultRuntime: "codex" },
    ]);
  });

  it("adds, tests, and removes a runtime, and offers it to the pickers", async () => {
    const builtIn = (name: string, label: string): RuntimeInfo => ({
      name,
      label,
      builtIn: true,
      preset: name,
      binary: name,
      canWrite: true,
      config: {},
    });
    let runtimes: RuntimeInfo[] = [
      builtIn("claude", "Claude Code"),
      { ...builtIn("codex", "Codex"), binary: "/opt/codex", customized: true },
      builtIn("copilot", "GitHub Copilot"),
    ];
    const saved: Array<[string, Record<string, unknown>]> = [];
    const removed: string[] = [];
    window.go!.app!.Service!.Runtimes = async () => runtimes;
    window.go!.app!.Service!.Configuration = async () =>
      ({
        path: "",
        exists: true,
        dataDir: "",
        file: { secrets: [{ name: "GEMINI_API_KEY" }] },
        secrets: [],
      }) as never;
    window.go!.app!.Service!.SaveRuntime = async (name, runtimeJSON) => {
      const config = JSON.parse(runtimeJSON);
      saved.push([name, config]);
      if (config.args?.includes("--yolo"))
        throw new Error("--yolo turns the CLI's permission checks off");
      runtimes = [
        ...runtimes.filter((runtime) => runtime.name !== name),
        {
          name,
          label: config.label ?? name,
          builtIn: false,
          preset: config.preset,
          binary: config.binary,
          canWrite: Boolean(config.preset || config.writeArgs?.length),
          config,
        },
      ];
      return runtimes;
    };
    window.go!.app!.Service!.DeleteRuntime = async (name) => {
      removed.push(name);
      // A built-in only resets; an added runtime goes away.
      runtimes = runtimes.flatMap((runtime) =>
        runtime.name !== name
          ? [runtime]
          : runtime.builtIn
            ? [builtIn(runtime.name, runtime.label)]
            : [],
      );
      return runtimes;
    };
    window.go!.app!.Service!.TestRuntime = async (name) =>
      name === "gemini"
        ? { available: true, path: "/opt/gemini", version: "gemini 0.9" }
        : { available: false, detail: `${name} was not found` };

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Runtimes/ }));
    const claude = await screen.findByRole("article", { name: "Claude Code" });
    expect(within(claude).getByText("built-in")).toBeInTheDocument();
    // An untouched built-in has nothing to reset.
    expect(
      within(claude).queryByRole("button", { name: /Reset/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Reset Codex" }),
    ).toBeInTheDocument();
    fireEvent.click(
      within(claude).getByRole("button", { name: "Test Claude Code" }),
    );
    expect(
      await within(claude).findByText("claude was not found"),
    ).toBeInTheDocument();

    // A blank template declares the CLI's arguments, one per line.
    fireEvent.click(screen.getByRole("button", { name: "+ Add runtime" }));
    const form = screen.getByRole("form", { name: "Add runtime" });
    fireEvent.change(within(form).getByLabelText("Runtime name"), {
      target: { value: "claude" },
    });
    expect(within(form).getByRole("alert")).toHaveTextContent(
      "claude is already declared",
    );
    expect(
      within(form).getByRole("button", { name: "Save runtime" }),
    ).toBeDisabled();
    fireEvent.change(within(form).getByLabelText("Runtime name"), {
      target: { value: "gemini" },
    });
    fireEvent.change(within(form).getByLabelText("Runtime label"), {
      target: { value: "Gemini CLI" },
    });
    fireEvent.change(within(form).getByLabelText("Runtime binary"), {
      target: { value: "/opt/gemini" },
    });
    fireEvent.change(within(form).getByLabelText("Arguments for every run"), {
      target: { value: "--model\n{model}\n\n--yolo\n" },
    });
    fireEvent.change(
      within(form).getByLabelText(/^Added for read-only steps/),
      {
        target: { value: "--approval-mode\nplan" },
      },
    );
    fireEvent.click(within(form).getByLabelText("GEMINI_API_KEY"));

    // Go's refusal is shown in the form, which stays open for the fix.
    fireEvent.click(within(form).getByRole("button", { name: "Save runtime" }));
    expect(await within(form).findByRole("alert")).toHaveTextContent(
      "permission checks off",
    );
    fireEvent.change(within(form).getByLabelText("Arguments for every run"), {
      target: { value: "--model\n{model}" },
    });
    fireEvent.click(within(form).getByRole("button", { name: "Save runtime" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Saved gemini to inkuna.yaml.",
    );
    expect(saved[1]).toEqual([
      "gemini",
      {
        label: "Gemini CLI",
        binary: "/opt/gemini",
        args: ["--model", "{model}"],
        readOnlyArgs: ["--approval-mode", "plan"],
        stdin: "prompt",
        output: "text",
        secrets: ["GEMINI_API_KEY"],
      },
    ]);
    const gemini = screen.getByRole("article", { name: "Gemini CLI" });
    expect(within(gemini).getByText("template")).toBeInTheDocument();
    expect(within(gemini).getByText("read-only")).toBeInTheDocument();
    fireEvent.click(
      within(gemini).getByRole("button", { name: "Test Gemini CLI" }),
    );
    expect(
      await within(gemini).findByText("Found /opt/gemini: gemini 0.9"),
    ).toBeInTheDocument();

    // Choosing an adapter replaces the template with that CLI's own arguments.
    fireEvent.click(screen.getByRole("button", { name: "+ Add runtime" }));
    const copy = screen.getByRole("form", { name: "Add runtime" });
    fireEvent.change(within(copy).getByLabelText("Start from"), {
      target: { value: "claude" },
    });
    expect(
      within(copy).queryByLabelText("Arguments for every run"),
    ).not.toBeInTheDocument();
    fireEvent.change(within(copy).getByLabelText("Runtime name"), {
      target: { value: "claude-work" },
    });
    fireEvent.click(within(copy).getByRole("button", { name: "Save runtime" }));
    await waitFor(() =>
      expect(saved[2]).toEqual(["claude-work", { preset: "claude" }]),
    );

    // Pickers elsewhere offer the added runtimes; a read-only one says so and
    // is left out once the agent needs to write.
    window.go!.app!.Service!.Agents = async () => [];
    window.go!.app!.Service!.AgentLocations = async () => ({
      global: "/tmp/data/agents",
    });
    fireEvent.click(screen.getByRole("button", { name: /Agents/ }));
    const newAgent = await screen.findByRole("button", { name: "+ New agent" });
    await waitFor(() => expect(newAgent).toBeEnabled());
    fireEvent.click(newAgent);
    const agent = screen.getByRole("form", { name: "New agent" });
    const picker = within(agent).getByLabelText("Runtime");
    expect(
      await within(picker).findByRole("option", {
        name: "Gemini CLI (read-only)",
      }),
    ).toBeInTheDocument();
    fireEvent.change(within(agent).getByLabelText("Permission"), {
      target: { value: "worktree-write" },
    });
    expect(
      within(picker).queryByRole("option", { name: "Gemini CLI (read-only)" }),
    ).not.toBeInTheDocument();
    expect(
      within(picker).getByRole("option", { name: "claude-work" }),
    ).toBeInTheDocument();
    fireEvent.click(within(agent).getByRole("button", { name: "Cancel" }));

    // Removing asks first.
    fireEvent.click(screen.getByRole("button", { name: /Runtimes/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Remove Gemini CLI" }),
    );
    const confirm = screen.getByRole("dialog", { name: "Remove Gemini CLI?" });
    fireEvent.click(
      within(confirm).getByRole("button", { name: "Remove runtime" }),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Removed Gemini CLI.",
    );
    expect(removed).toEqual(["gemini"]);
    expect(
      screen.queryByRole("article", { name: "Gemini CLI" }),
    ).not.toBeInTheDocument();

    // A test result describes the declaration it ran against, so a reset
    // drops it along with the Reset button.
    const codex = screen.getByRole("article", { name: "Codex" });
    fireEvent.click(within(codex).getByRole("button", { name: "Test Codex" }));
    expect(
      await within(codex).findByText("codex was not found"),
    ).toBeInTheDocument();
    // A probe still running when the declaration changes answers for the old
    // one, so its result is dropped too.
    let finishProbe: (value: never) => void = () => {};
    window.go!.app!.Service!.TestRuntime = () =>
      new Promise((resolve) => (finishProbe = resolve));
    fireEvent.click(within(codex).getByRole("button", { name: "Test Codex" }));
    fireEvent.click(within(codex).getByRole("button", { name: "Reset Codex" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "Reset Codex?" })).getByRole(
        "button",
        { name: "Reset runtime" },
      ),
    );
    // The removal's status stays until the reset finishes, so wait for the
    // reset's own message rather than the first status on screen.
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Codex is back to its defaults.",
      ),
    );
    expect(screen.queryByText("codex was not found")).not.toBeInTheDocument();
    await act(async () =>
      finishProbe({
        available: false,
        detail: "old codex was not found",
      } as never),
    );
    expect(
      screen.queryByText("old codex was not found"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Reset Codex" }),
    ).not.toBeInTheDocument();
  });

  it("keeps the dashboard's project when New request is clicked before the dashboard renders", async () => {
    let deliver = () => {};
    const planned: string[] = [];
    window.go!.app!.Service!.PlanWorkflow = async (request) => {
      planned.push(request.projectId);
      return workflowPlan;
    };
    window.go!.app!.Service!.Dashboard = () =>
      new Promise((resolve) => {
        deliver = () => resolve({ projects: [project], health: [], defaults });
      });
    render(<App />);
    const button = screen.getByRole("button", { name: /New request/ });
    // The dashboard arrives, and the click lands before React has rendered
    // it: the handler still belongs to the render without projects.
    deliver();
    for (let tick = 0; tick < 10; tick++) await Promise.resolve();
    fireEvent.click(button);
    fireEvent.change(
      await screen.findByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Race test" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    // The plan is requested for the project the dashboard delivered.
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    await waitFor(() => expect(planned).toEqual([project.id]));
  });

  it("keeps the composer visible when the desktop returns an invalid plan", async () => {
    window.go!.app!.Service!.PlanWorkflow = async () =>
      ({ task: {}, workflow: {} }) as unknown as WorkflowPlan;
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Invalid plan test" } },
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Generate workflow →" }),
      ).toBeEnabled(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Generate workflow →" }),
    );
    expect(
      await screen.findByText(
        "Error: Inkuna returned an invalid workflow project",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Turn intent into a workflow" }),
    ).toBeInTheDocument();
  });

  it("imports a Linear story into the composer", async () => {
    window.go!.app!.Service!.ImportLinearIssue = async () => ({
      id: "uuid",
      identifier: "ENG-123",
      title: "Improve import",
      description: "Pull the initial story from Linear.",
      url: "https://linear.app/acme/issue/ENG-123/improve-import",
      priority: 2,
      team: "Engineering",
      state: "Todo",
      project: "Inkuna",
      labels: ["Feature"],
      suggestedKind: "implementation",
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.click(screen.getByRole("tab", { name: "Linear issue" }));
    fireEvent.change(
      screen.getByPlaceholderText("ENG-123 or https://linear.app/…"),
      { target: { value: "ENG-123" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    expect(
      await screen.findByText("ENG-123 · Improve import"),
    ).toBeInTheDocument();
    expect(
      screen.getByDisplayValue(/Pull the initial story from Linear/),
    ).toBeInTheDocument();
  });

  it("offers GitHub Copilot as a project runtime", async () => {
    render(<App />);
    fireEvent.click(
      await screen.findByRole("button", { name: "+ Add project" }),
    );
    expect(
      screen.getByRole("option", { name: "GitHub Copilot" }),
    ).toBeInTheDocument();
  });

  it("shows each dependency's fix and links the screen Go names", async () => {
    window.go!.app!.Service!.Dashboard = async () => ({
      projects: [project],
      defaults,
      health: [
        {
          name: "Claude Code",
          status: "missing",
          detail: "not signed in; sign in with claude auth login",
          required: false,
          action: "connections",
        },
        // Codex signs in in a terminal, so its fix is in the detail alone.
        {
          name: "Codex CLI",
          status: "missing",
          detail: "not signed in; sign in with codex login",
          required: false,
        },
        {
          name: "Secret DOCS_TOKEN",
          status: "missing",
          detail: "not set; add it in Setup",
          required: false,
          action: "setup",
        },
      ],
    });
    render(<App />);
    expect(await screen.findByText("Claude Code")).toBeInTheDocument();
    expect(
      screen.getByText("not signed in; sign in with codex login"),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Connect →" })).toHaveLength(
      1,
    );
    expect(screen.getAllByRole("button", { name: "Set up →" })).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Connect →" }));
    expect(
      screen.getByRole("heading", { name: "Connections" }),
    ).toBeInTheDocument();
    const connectClaude = await screen.findByRole("button", {
      name: "Connect Claude",
    });
    await waitFor(() => expect(connectClaude).toBeEnabled());
  });

  it("authenticates GitHub, Claude, and Linear without exposing the Linear key", async () => {
    let submittedLinearKey = "";
    window.go!.app!.Service!.AuthenticationStatus = async () => ({
      github: {
        provider: "github",
        authenticated: false,
        detail: "Not authenticated through gh",
      },
      claude: {
        provider: "claude",
        authenticated: false,
        detail: "Not authenticated through Claude Code",
      },
      linear: {
        provider: "linear",
        authenticated: false,
        detail: "Not authenticated",
      },
    });
    window.go!.app!.Service!.AuthenticateGitHub = async () =>
      authentication.github;
    window.go!.app!.Service!.AuthenticateClaude = async () => ({
      provider: "claude",
      authenticated: true,
      detail: "Authenticated through Claude Code via claude.ai",
    });
    window.go!.app!.Service!.AuthenticateLinear = async (apiKey) => {
      submittedLinearKey = apiKey;
      return {
        provider: "linear",
        authenticated: true,
        account: "Ada",
        detail: "Authenticated for this Inkuna session",
      };
    };

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Connections/ }));
    const connectGitHub = await screen.findByRole("button", {
      name: "Connect GitHub",
    });
    await waitFor(() => expect(connectGitHub).toBeEnabled());
    fireEvent.click(connectGitHub);
    expect(await screen.findByText("octocat")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Connect Claude" }));
    expect(
      await screen.findByText(
        "Authenticated through Claude Code via claude.ai",
      ),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Linear API key"), {
      target: { value: "lin_api_session_secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Connect Linear" }));
    expect(await screen.findByText("Ada")).toBeInTheDocument();
    expect(submittedLinearKey).toBe("lin_api_session_secret");
    expect(screen.queryByDisplayValue("lin_api_session_secret")).toBeNull();
  });
});
