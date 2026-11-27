import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { Check, CheckLog, WorkflowRun } from "../../../types";
import { ChangesTab } from "./ChangesTab";
import { ChecksView } from "./ChecksView";

const bridge = vi.hoisted(() => ({
  getRunChecks: vi.fn(),
  getCheckLog: vi.fn(),
  saveCheckLog: vi.fn(),
  getRunChanges: vi.fn(),
  getRunCommits: vi.fn(),
  getRunViewed: vi.fn(),
}));
vi.mock("../../../bridge", () => bridge);

const check = (position: number, fields: Partial<Check> = {}): Check => ({
  id: `run-verify-${position}`,
  executionId: "run-1",
  stepId: "verify",
  stepRunId: "run-verify",
  position,
  argv: ["task", "test"],
  status: "passed",
  exitCode: 0,
  startedAt: "2026-10-26T09:00:00Z",
  finishedAt: "2026-10-26T09:00:12Z",
  logDigest: "a".repeat(64),
  logBytes: 10,
  durationMs: 12_400,
  stepTitle: "Verify",
  ...fields,
});
const logOf = (text: string, fields: Partial<CheckLog> = {}): CheckLog => ({
  text,
  offset: 0,
  end: new TextEncoder().encode(text).length,
  reset: true,
  running: false,
  cut: false,
  bytes: text.length,
  ...fields,
});

beforeEach(() => {
  vi.resetAllMocks();
});

describe("ChecksView", () => {
  it("opens a failed check and leaves a passed one closed", async () => {
    bridge.getRunChecks.mockResolvedValue([
      check(0, { argv: ["task", "lint"] }),
      check(1, { status: "failed", exitCode: 1 }),
      check(2, {
        argv: ["task", "build"],
        status: "skipped",
        exitCode: undefined,
        startedAt: undefined,
        finishedAt: undefined,
        logDigest: undefined,
        durationMs: 0,
      }),
    ]);
    bridge.getCheckLog.mockResolvedValue(
      logOf("=== RUN TestPlan\n--- FAIL: TestPlan (0.01s)\nFAIL\n"),
    );
    render(<ChecksView runID="run-1" />);
    const step = await screen.findByRole("region", {
      name: "Checks of Verify",
    });
    const [lint, test, build] = within(step).getAllByRole("listitem");
    expect(within(lint).getByRole("button")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(
      within(test).getByRole("button", { name: /task test/ }),
    ).toHaveAttribute("aria-expanded", "true");
    expect(within(test).getByText("exit 1")).toBeInTheDocument();
    expect(
      await within(test).findByText(/--- FAIL: TestPlan/),
    ).toBeInTheDocument();
    expect(bridge.getCheckLog).toHaveBeenCalledWith("run-verify-1", -1);
    expect(within(build).getByText("did not run")).toBeInTheDocument();
    expect(within(build).getByRole("button")).toBeDisabled();

    fireEvent.click(within(lint).getByRole("button", { name: /task lint/ }));
    expect(await within(lint).findByRole("log")).toBeInTheDocument();
    expect(bridge.getCheckLog).toHaveBeenCalledWith("run-verify-0", -1);
  });

  it("searches a log and jumps to its first error", async () => {
    bridge.getRunChecks.mockResolvedValue([
      check(0, { status: "failed", exitCode: 2 }),
    ]);
    bridge.getCheckLog.mockResolvedValue(
      logOf(
        "building\n\x1b[32mok\x1b[0m pkg/a\nwarning: slow\n\x1b[31merror: x is undefined\x1b[0m\nwarning: again\n",
      ),
    );
    render(<ChecksView runID="run-1" />);
    const log = await screen.findByRole("log", { name: "Log of task test" });
    expect(within(log).getByText("ok")).toHaveClass("ansi-fg-2");
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Search the log of task test" }),
      { target: { value: "warning" } },
    );
    expect(screen.getByText("1 of 2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Next match" }));
    expect(screen.getByText("2 of 2")).toBeInTheDocument();
    expect(within(log).getAllByText("warning")).toHaveLength(2);
    fireEvent.click(screen.getByRole("button", { name: "First error" }));
    const marked = log.querySelector('[data-line="4"]');
    expect(marked).toHaveClass("bg-accent/15");
    expect(marked).toHaveTextContent("error: x is undefined");
  });

  it("grows a running check's log in place", async () => {
    bridge.getRunChecks.mockResolvedValue([
      check(0, {
        status: "running",
        exitCode: undefined,
        finishedAt: undefined,
        logDigest: undefined,
      }),
    ]);
    // Offsets are Go's bytes: "firsté\n" is 7 characters but 8 bytes.
    bridge.getCheckLog
      .mockResolvedValueOnce(logOf("firsté\n", { running: true }))
      .mockResolvedValue(
        logOf("second\n", { running: true, reset: false, offset: 8, end: 15 }),
      );
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      render(<ChecksView runID="run-1" />);
      const log = await screen.findByRole("log");
      expect(within(log).getByText("firsté")).toBeInTheDocument();
      await act(() => vi.advanceTimersByTimeAsync(1100));
      await waitFor(() =>
        expect(bridge.getCheckLog).toHaveBeenLastCalledWith("run-verify-0", 8),
      );
      expect(await within(log).findByText("second")).toBeInTheDocument();
      expect(within(log).getAllByText("firsté")).toHaveLength(1);
      expect(
        screen.getByRole("button", { name: "Download log" }),
      ).toBeDisabled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("says when output was cut and saves the whole kept log", async () => {
    bridge.getRunChecks.mockResolvedValue([
      check(0, { status: "failed", exitCode: 1, logCut: true }),
    ]);
    bridge.getCheckLog.mockResolvedValue(
      logOf("head\n[… left out …]\ntail\n", { cut: true, bytes: 3_145_728 }),
    );
    bridge.saveCheckLog.mockResolvedValue("/tmp/verify-check-1.log");
    render(<ChecksView runID="run-1" />);
    expect(await screen.findByText(/wrote 3.0 MB/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Download log" }));
    expect(
      await screen.findByText("Saved to /tmp/verify-check-1.log"),
    ).toBeInTheDocument();
    expect(bridge.saveCheckLog).toHaveBeenCalledWith("run-verify-0");
  });

  it("keeps each step run's checks together when two steps ran at once", async () => {
    const other = (position: number, fields: Partial<Check> = {}) =>
      check(position, {
        id: `run-docs-${position}`,
        stepId: "docs",
        stepRunId: "run-docs",
        stepTitle: "Check the docs",
        argv: ["task", `docs-${position}`],
        ...fields,
      });
    bridge.getRunChecks.mockResolvedValue([
      check(0, { argv: ["task", "lint"] }),
      other(0),
      check(1, { argv: ["task", "test"] }),
      other(1),
    ]);
    render(<ChecksView runID="run-1" />);
    const verify = await screen.findByRole("region", {
      name: "Checks of Verify",
    });
    const docs = screen.getByRole("region", {
      name: "Checks of Check the docs",
    });
    expect(screen.getAllByRole("region")).toHaveLength(2);
    expect(
      within(verify)
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual([
      expect.stringContaining("task lint"),
      expect.stringContaining("task test"),
    ]);
    expect(within(docs).getAllByRole("listitem")).toHaveLength(2);
  });

  it("shows a run's checks before it has written anything", async () => {
    bridge.getRunChecks.mockResolvedValue([check(0)]);
    const run: WorkflowRun = {
      id: "run-1",
      workflowId: "workflow-1",
      taskId: "task-1",
      state: "completed",
      title: "Check only",
      startedAt: "2026-10-26T09:00:00Z",
      events: [],
      checks: { passed: 1, failed: 0, running: 0, total: 1 },
    };
    render(<ChangesTab run={run} />);
    expect(screen.getByText("No changes yet")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Checks 1/1" }));
    expect(
      await screen.findByRole("region", { name: "Checks of Verify" }),
    ).toBeInTheDocument();
    expect(bridge.getRunChanges).not.toHaveBeenCalled();
  });
});
