import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import type { StorageUsage } from "../../types";
import { DiskUsagePanel } from "./DiskUsagePanel";

const getStorageUsage = vi.hoisted(() => vi.fn());
const removeKeptWorktree = vi.hoisted(() => vi.fn());
vi.mock("../../bridge", () => ({ getStorageUsage, removeKeptWorktree }));

const kept = {
  path: "/src/inkuna-worktrees/bug/42-login",
  projectId: "inkuna",
  projectName: "Inkuna",
  branch: "bug/42-login",
  taskId: "task-1",
  runId: "run-1",
  title: "Fix the login crash",
  reason: "It has uncommitted changes, so it was kept.",
  keptAt: "2026-11-04T10:00:00Z",
  bytes: 5 * 1024 * 1024,
};

const usage: StorageUsage = {
  artifacts: 120,
  artifactBytes: 3 * 1024 * 1024,
  worktrees: 3,
  worktreeBytes: 2 * 1024 * 1024 * 1024,
  kept: [kept],
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe("DiskUsagePanel", () => {
  it("shows what history takes and lists the worktrees deletes kept", async () => {
    getStorageUsage.mockResolvedValue(usage);
    render(<DiskUsagePanel />);
    expect(await screen.findByText("2.0 GB")).toBeInTheDocument();
    expect(screen.getByText("Worktrees · 3 worktrees")).toBeInTheDocument();
    expect(screen.getByText("3.0 MB")).toBeInTheDocument();
    expect(screen.getByText("Artifacts · 120 blobs")).toBeInTheDocument();
    const list = screen.getByRole("list", { name: "Kept worktrees" });
    expect(list).toHaveTextContent(kept.path);
    expect(list).toHaveTextContent("5.0 MB · Inkuna · bug/42-login");
    expect(list).toHaveTextContent(
      "Fix the login crash: It has uncommitted changes, so it was kept.",
    );
  });

  it("removes a kept worktree only after confirmation, then measures again", async () => {
    getStorageUsage.mockResolvedValueOnce(usage).mockResolvedValueOnce({
      ...usage,
      worktrees: 2,
      kept: [],
    });
    removeKeptWorktree.mockResolvedValue(undefined);
    render(<DiskUsagePanel />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: `Remove worktree ${kept.path}`,
      }),
    );
    expect(removeKeptWorktree).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", {
      name: "Remove this worktree?",
    });
    expect(dialog).toHaveTextContent(
      `This deletes ${kept.path} (5.0 MB). The branch bug/42-login and its commits stay.`,
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Remove worktree" }),
    );
    await waitFor(() =>
      expect(removeKeptWorktree).toHaveBeenCalledWith(kept.path),
    );
    expect(await screen.findByText("None.")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps the latest measurement when an earlier one finishes last", async () => {
    let finishFirst: (value: StorageUsage) => void = () => {};
    getStorageUsage
      .mockReturnValueOnce(
        new Promise<StorageUsage>((resolve) => {
          finishFirst = resolve;
        }),
      )
      .mockResolvedValueOnce({ ...usage, worktrees: 2, kept: [] });
    // Strict mode runs the opening measurement twice, so the two overlap.
    render(
      <StrictMode>
        <DiskUsagePanel />
      </StrictMode>,
    );
    expect(await screen.findByText("None.")).toBeInTheDocument();
    expect(getStorageUsage).toHaveBeenCalledTimes(2);
    await act(async () => finishFirst(usage));
    expect(screen.getByText("None.")).toBeInTheDocument();
    expect(screen.getByText("Worktrees · 2 worktrees")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Measure again" })).toBeEnabled();
  });

  it("offers no removal while it measures again", async () => {
    let finish: (value: StorageUsage) => void = () => {};
    getStorageUsage.mockResolvedValueOnce(usage).mockReturnValueOnce(
      new Promise<StorageUsage>((resolve) => {
        finish = resolve;
      }),
    );
    render(<DiskUsagePanel />);
    const remove = await screen.findByRole("button", {
      name: `Remove worktree ${kept.path}`,
    });
    expect(remove).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Measure again" }));
    expect(
      await screen.findByRole("button", { name: "Measuring…" }),
    ).toBeDisabled();
    expect(remove).toBeDisabled();
    await act(async () => finish(usage));
    expect(remove).toBeEnabled();
  });

  it("shows Go's reason when a worktree is kept", async () => {
    getStorageUsage.mockResolvedValue(usage);
    removeKeptWorktree.mockRejectedValue(
      new Error(
        `${kept.path} has uncommitted changes; commit or discard them first`,
      ),
    );
    render(<DiskUsagePanel />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: `Remove worktree ${kept.path}`,
      }),
    );
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Remove worktree",
      }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "has uncommitted changes; commit or discard them first",
    );
  });
});
