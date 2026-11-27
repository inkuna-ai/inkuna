import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import type { PlanSuggestion, RunFailure, WorkflowRun } from "../../types";
import { RunFailurePanel } from "./RunFailurePanel";
import { withSuggestions } from "./suggestions";

const bridge = vi.hoisted(() => ({ getRunFailure: vi.fn() }));
vi.mock("../../bridge", () => bridge);

const run: WorkflowRun = {
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "failed",
  round: 1,
  title: "Fix the login crash",
  startedAt: "2026-11-06T09:00:00Z",
  events: [],
};

const allow: PlanSuggestion = {
  id: "command:fix:go test ./...",
  stepId: "fix",
  text: "Allow go test ./... on step fix",
  command: ["go", "test", "./..."],
};

const failure: RunFailure = {
  round: 1,
  stopped: true,
  steps: [
    {
      stepId: "fix",
      title: "Implement the fix",
      state: "failed",
      error: "Could not run the tests",
      denials: [
        {
          tool: "Bash",
          command: "go test ./...",
          detail: "go test ./... # run the tests",
          grantable: true,
        },
        { tool: "Edit", detail: "main.go", write: true },
      ],
      checks: [],
    },
    {
      stepId: "verify",
      title: "verify",
      state: "failed",
      denials: [],
      checks: [{ checkId: "c1", argv: ["task", "ci"], exitCode: 2 }],
    },
  ],
  suggestions: [allow],
};

describe("RunFailurePanel", () => {
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    bridge.getRunFailure.mockReset();
  });

  it("says why the run stopped beside each step, and reruns with the fixes ticked", async () => {
    bridge.getRunFailure.mockResolvedValue(failure);
    const rerun = vi.fn();
    render(<RunFailurePanel run={run} onRerun={rerun} />);
    const panel = await screen.findByRole("region", { name: "Why it stopped" });
    expect(panel).toHaveTextContent("fix · Implement the fix");
    expect(panel).toHaveTextContent("Could not run the tests");
    const refused = within(
      screen.getByRole("list", { name: "What fix was refused" }),
    ).getAllByRole("listitem");
    expect(refused[0]).toHaveTextContent(
      "Refused Bash go test ./... (go test ./... # run the tests) · asked in the inbox; not allowed",
    );
    expect(refused[1]).toHaveTextContent(
      "Refused Edit main.go · a write; only the step's permission allows it",
    );
    expect(
      screen.getByRole("list", { name: "Checks of verify that failed" }),
    ).toHaveTextContent("task ci · exited 2");

    // Nothing is suggested into the plan until it is ticked.
    fireEvent.click(screen.getByRole("button", { name: "Rerun…" }));
    expect(rerun).toHaveBeenLastCalledWith([]);
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Allow go test ./... on step fix" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Rerun…" }));
    expect(rerun).toHaveBeenLastCalledWith([allow]);
  });

  it("offers a rerun for a run that stopped though no step says why", async () => {
    bridge.getRunFailure.mockResolvedValue({
      round: 1,
      stopped: true,
      steps: [],
      suggestions: [],
    });
    const rerun = vi.fn();
    render(<RunFailurePanel run={run} onRerun={rerun} />);
    const panel = await screen.findByRole("region", { name: "Why it stopped" });
    expect(panel).toHaveTextContent("No step recorded why it stopped.");
    fireEvent.click(screen.getByRole("button", { name: "Rerun…" }));
    expect(rerun).toHaveBeenLastCalledWith([]);
  });

  it("shows nothing for a run Go says did not stop", async () => {
    bridge.getRunFailure.mockResolvedValue({
      round: 1,
      stopped: false,
      steps: [],
      suggestions: [],
    });
    const { container } = render(<RunFailurePanel run={run} />);
    await vi.waitFor(() =>
      expect(bridge.getRunFailure).toHaveBeenCalledWith("run-1"),
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe("withSuggestions", () => {
  const steps = [
    {
      id: "fix",
      permission: "read-only",
      commands: [["go", "vet", "./..."]],
    },
    { id: "verify", permission: "read-only" },
  ];

  it("makes a ticked suggestion's edit on its step only", () => {
    const write: PlanSuggestion = {
      id: "permission:fix",
      stepId: "fix",
      text: "Make step fix worktree-write",
      permission: "worktree-write",
    };
    const next = withSuggestions(steps, [allow, write, allow]);
    expect(next[0]).toEqual({
      id: "fix",
      permission: "worktree-write",
      commands: [
        ["go", "vet", "./..."],
        ["go", "test", "./..."],
      ],
    });
    expect(next[1]).toBe(steps[1]);
  });
});
