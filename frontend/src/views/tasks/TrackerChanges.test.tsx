import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { TrackerChange } from "../../types";
import { TrackerChanges } from "./TrackerChanges";

const bridge = vi.hoisted(() => ({
  retryTrackerChange: vi.fn(),
  openExternal: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const body = "The migration runs in about 4 s.\n\nMeasured on the seed data.";

function change(overrides: Partial<TrackerChange>): TrackerChange {
  return {
    id: "c1",
    round: 1,
    stepId: "implement",
    tracker: "github",
    kind: "comment",
    issue: "12",
    summary: "comment: The migration runs in about 4 s. …",
    change: { kind: "comment", issue: "story", body },
    state: "pending",
    updatedAt: "2026-11-03T09:00:00Z",
    ...overrides,
  };
}

describe("TrackerChanges", () => {
  beforeEach(() => vi.resetAllMocks());

  it("shows each change with how it stands and all it would say", () => {
    render(
      <TrackerChanges
        runId="run-1"
        label="Tracker changes"
        changes={[
          change({}),
          change({
            id: "c2",
            kind: "create",
            issue: "",
            summary: "new issue: Index events",
            change: {
              kind: "create",
              title: "Index events",
              description: "Found while profiling.",
              addLabels: ["backend", "performance"],
            },
            state: "applied",
            url: "https://github.com/acme/app/issues/41",
          }),
          change({
            id: "c3",
            kind: "edit",
            summary: "edit: labels +backend",
            change: { kind: "edit", addLabels: ["backend"] },
            state: "refused",
            reason: "this step may not ask for edit changes",
          }),
        ]}
      />,
    );
    const [comment, created, edit] = screen.getAllByRole("listitem");
    expect(comment).toHaveTextContent("pending");
    expect(comment).toHaveTextContent("#12 comment: The migration");
    expect(comment).toHaveTextContent("Measured on the seed data.");
    expect(created).toHaveTextContent(
      "appliedimplementnew issue: Index events",
    );
    // A new issue's description is never in its summary, so it is shown
    // however short it is.
    expect(created).toHaveTextContent("Found while profiling.");
    expect(created).toHaveTextContent("Labels: backend, performance");
    // A change that is not a create shows no labels line of its own.
    expect(edit).not.toHaveTextContent("Labels:");
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(bridge.openExternal).toHaveBeenCalledWith(
      "https://github.com/acme/app/issues/41",
    );
    expect(edit).toHaveTextContent("this step may not ask for edit changes");
    // Nothing offers a retry unless asked to and Go says so.
    expect(
      screen.queryByRole("button", { name: "Retry" }),
    ).not.toBeInTheDocument();
  });

  it("retries a failed change and shows Go's answer", async () => {
    const failed = change({
      kind: "status",
      summary: "status → In progress",
      change: { kind: "status", status: "In progress" },
      state: "failed",
      reason: "GraphQL: the item is locked",
      retry: true,
    });
    bridge.retryTrackerChange.mockResolvedValueOnce({
      ...failed,
      state: "applied",
      reason: "",
      retry: false,
      updatedAt: "2026-11-03T09:05:00Z",
    });
    render(
      <TrackerChanges
        runId="run-1"
        label="Tracker changes"
        changes={[failed]}
        retry
      />,
    );
    expect(screen.getByRole("listitem")).toHaveTextContent(
      "the item is locked",
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(screen.getByRole("listitem")).toHaveTextContent("applied"),
    );
    expect(bridge.retryTrackerChange).toHaveBeenCalledWith("run-1", "c1");
    expect(
      screen.queryByRole("button", { name: "Retry" }),
    ).not.toBeInTheDocument();
  });

  it("shows Go's reason when a retry is refused", async () => {
    bridge.retryTrackerChange.mockRejectedValueOnce(
      new Error(
        "this change is applied; only a change that failed can be retried",
      ),
    );
    render(
      <TrackerChanges
        runId="run-1"
        label="Tracker changes"
        changes={[change({ state: "failed", retry: true })]}
        retry
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "only a change that failed can be retried",
    );
  });

  it("shows nothing when no change was asked for", () => {
    const { container } = render(
      <TrackerChanges runId="run-1" label="Tracker changes" changes={[]} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
