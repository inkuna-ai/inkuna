import { fireEvent, render, screen, within } from "@testing-library/react";
import type { BranchSync, BranchSyncInfo, Project } from "../types";
import { BranchesView } from "./BranchesView";

const getProjectBranches = vi.hoisted(() => vi.fn());
const fetchProject = vi.hoisted(() => vi.fn());
const updateBranch = vi.hoisted(() => vi.fn());
const deleteBranch = vi.hoisted(() => vi.fn());
const shareSettings = vi.hoisted(() => vi.fn());
const openExternal = vi.hoisted(() => vi.fn());
vi.mock("../bridge", () => ({
  getProjectBranches,
  fetchProject,
  updateBranch,
  deleteBranch,
  shareSettings,
  openExternal,
}));

const project: Project = {
  id: "project",
  name: "Inkuna",
  repository: "/src/inkuna",
  defaultRuntime: "claude",
  createdAt: "2026-09-01T00:00:00Z",
};

const branch = (fields: Partial<BranchSync>): BranchSync => ({
  name: "main",
  upstream: "origin/main",
  state: "synced",
  ahead: 0,
  behind: 0,
  behindBase: 0,
  commit: "abc1234",
  committedAt: "2026-10-20T09:00:00Z",
  updatable: false,
  unique: 0,
  deletable: false,
  ...fields,
});

const view = (
  branches: BranchSync[],
  fields: Partial<BranchSyncInfo> = {},
): BranchSyncInfo => ({
  projectId: "project",
  sync: {
    repository: "/src/inkuna",
    hasOrigin: true,
    origin: "github.com/inkuna-ai/inkuna",
    base: "main",
    baseRef: "origin/main",
    fetchedAt: "2026-10-20T10:00:00Z",
    branches,
  },
  ...fields,
});

// What Go reports once a fetch has seen origin move.
const fetched = view([
  branch({
    name: "main",
    isBase: true,
    main: true,
    checkedOut: "/src/inkuna",
    state: "behind",
    behind: 2,
    behindBase: 2,
    updatable: true,
  }),
  branch({
    name: "feature/12-login",
    upstream: "origin/feature/12-login",
    state: "diverged",
    ahead: 1,
    behind: 1,
    behindBase: 2,
    reason:
      "Has 1 commit(s) origin/feature/12-login does not; Inkuna only fast-forwards, so merge or rebase it yourself.",
  }),
  branch({
    name: "feature/run-branch",
    upstream: undefined,
    state: "local",
    behindBase: 3,
    checkedOut: "/src/inkuna-worktrees/feature/run-branch",
  }),
]);

function renderView() {
  return render(
    <BranchesView
      projects={[project]}
      selectedProject={project}
      onProjectChange={() => {}}
    />,
  );
}

beforeEach(() => {
  getProjectBranches.mockReset();
  fetchProject.mockReset();
  updateBranch.mockReset();
  deleteBranch.mockReset();
  shareSettings.mockReset();
  openExternal.mockReset();
});

describe("BranchesView", () => {
  it("shows local state, then fetches and marks what is out of sync", async () => {
    getProjectBranches.mockResolvedValue(
      view([branch({ isBase: true, main: true, checkedOut: "/src/inkuna" })]),
    );
    fetchProject.mockResolvedValue(fetched);
    renderView();

    expect(await screen.findByText("2 behind")).toBeInTheDocument();
    expect(getProjectBranches).toHaveBeenCalledWith("project");
    expect(fetchProject).toHaveBeenCalledTimes(1);

    const main = screen.getByRole("row", { name: "main" });
    expect(within(main).getByText("Project checkout")).toBeInTheDocument();
    expect(
      within(main).getByRole("button", { name: "Update main" }),
    ).toBeEnabled();

    const diverged = screen.getByRole("row", { name: "feature/12-login" });
    expect(within(diverged).getByText("1 ahead, 1 behind")).toBeInTheDocument();
    expect(within(diverged).queryByRole("button")).toBeNull();
    expect(
      within(diverged).getByText(/Inkuna only fast-forwards/),
    ).toBeInTheDocument();

    const local = screen.getByRole("row", { name: "feature/run-branch" });
    expect(within(local).getByText("local only")).toBeInTheDocument();
    expect(within(local).getByText("3 behind origin/main")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("checkbox", { name: "Out of sync only" }));
    expect(
      screen.queryByRole("row", { name: "feature/run-branch" }),
    ).toBeNull();
    expect(screen.getByRole("row", { name: "main" })).toBeInTheDocument();
  });

  it("fast-forwards a branch and says what moved", async () => {
    getProjectBranches.mockResolvedValue(fetched);
    fetchProject.mockResolvedValue(fetched);
    updateBranch.mockResolvedValue(
      view([branch({ isBase: true, main: true, checkedOut: "/src/inkuna" })], {
        update: {
          branch: "main",
          upstream: "origin/main",
          from: "1111111aaaa",
          to: "2222222bbbb",
          checkedOut: "/src/inkuna",
        },
      }),
    );
    renderView();

    fireEvent.click(await screen.findByRole("button", { name: "Update main" }));

    expect(
      await screen.findByText(
        "Fast-forwarded main to origin/main (1111111 → 2222222), and its checkout at /src/inkuna.",
      ),
    ).toBeInTheDocument();
    expect(updateBranch).toHaveBeenCalledWith("project", "main");
    expect(screen.queryByRole("button", { name: "Update main" })).toBeNull();
  });

  it("shows Go's reason when an update is refused", async () => {
    getProjectBranches.mockResolvedValue(fetched);
    fetchProject.mockResolvedValue(fetched);
    updateBranch.mockRejectedValue(
      "update main: run run-7 is writing on main; update it once the run finishes",
    );
    renderView();

    fireEvent.click(await screen.findByRole("button", { name: "Update main" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "run run-7 is writing on main",
    );
    expect(screen.getByRole("button", { name: "Update main" })).toBeEnabled();
  });

  it("keeps the branches when a fetch fails", async () => {
    getProjectBranches.mockResolvedValue(fetched);
    fetchProject.mockResolvedValue({
      ...fetched,
      fetchError: "Git could not sign in to GitHub. Connect GitHub",
    });
    renderView();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Git could not sign in to GitHub",
    );
    expect(screen.getByRole("row", { name: "main" })).toBeInTheDocument();
  });

  it("does not fetch a repository without origin", async () => {
    getProjectBranches.mockResolvedValue({
      projectId: "project",
      sync: {
        repository: "/src/inkuna",
        hasOrigin: false,
        branches: [branch({ upstream: undefined, state: "local" })],
      },
    });
    renderView();

    expect(await screen.findByText(/has no origin remote/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Fetch origin" })).toBeDisabled();
    expect(fetchProject).not.toHaveBeenCalled();
  });

  it("deletes a branch without an upstream after confirming what it drops", async () => {
    const stale = view([
      branch({
        name: "main",
        isBase: true,
        main: true,
        checkedOut: "/src/inkuna",
      }),
      branch({
        name: "bug/42-fix-login",
        upstream: undefined,
        state: "local",
        commit: "5497aa0",
        unique: 2,
        deletable: true,
        checkedOut: "/src/inkuna-worktrees/bug/42-fix-login",
      }),
      branch({
        name: "feature/old-work",
        upstream: "origin/feature/old-work",
        state: "gone",
        deletable: true,
      }),
      branch({
        name: "feature/in-progress",
        upstream: undefined,
        state: "local",
        dirty: true,
        checkedOut: "/src/inkuna-worktrees/feature/in-progress",
        deleteReason:
          "Checked out in /src/inkuna-worktrees/feature/in-progress with uncommitted changes; commit or discard them first.",
      }),
    ]);
    getProjectBranches.mockResolvedValue(stale);
    fetchProject.mockResolvedValue(stale);
    deleteBranch.mockResolvedValue(
      view([branch({ name: "main", isBase: true, main: true })], {
        deleted: {
          branch: "bug/42-fix-login",
          commit: "5497aa0d1e2f",
          unique: 2,
          worktree: "/src/inkuna-worktrees/bug/42-fix-login",
        },
      }),
    );
    renderView();

    const dirty = await screen.findByRole("row", {
      name: "feature/in-progress",
    });
    expect(within(dirty).queryByRole("button")).toBeNull();
    expect(
      within(dirty).getByText(/commit or discard them first/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete feature/old-work" }),
    ).toBeEnabled();
    expect(
      within(screen.getByRole("row", { name: "main" })).queryByRole("button", {
        name: /Delete/,
      }),
    ).toBeNull();

    fireEvent.click(
      screen.getByRole("button", { name: "Delete bug/42-fix-login" }),
    );
    const confirm = screen.getByRole("dialog", {
      name: "Delete bug/42-fix-login?",
    });
    expect(confirm).toHaveTextContent("2 commit(s) exist only on this branch");
    expect(confirm).toHaveTextContent(
      "Its worktree at /src/inkuna-worktrees/bug/42-fix-login is removed first",
    );
    expect(deleteBranch).not.toHaveBeenCalled();
    fireEvent.click(
      within(confirm).getByRole("button", { name: "Delete branch" }),
    );

    expect(
      await screen.findByText(
        "Deleted bug/42-fix-login (was 5497aa0) and its worktree at /src/inkuna-worktrees/bug/42-fix-login. Restore it with git branch bug/42-fix-login 5497aa0.",
      ),
    ).toBeInTheDocument();
    expect(deleteBranch).toHaveBeenCalledWith(
      "project",
      "bug/42-fix-login",
      "5497aa0",
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("row", { name: "bug/42-fix-login" })).toBeNull();
  });

  it("shows Go's reason when a delete is refused", async () => {
    const stale = view([
      branch({
        name: "feature/old-work",
        upstream: undefined,
        state: "local",
        deletable: true,
      }),
    ]);
    getProjectBranches.mockResolvedValue(stale);
    fetchProject.mockResolvedValue(stale);
    deleteBranch.mockRejectedValue(
      "delete feature/old-work: feature/old-work now points at 1234567, not abc1234; look at it again before deleting it",
    );
    renderView();

    fireEvent.click(
      await screen.findByRole("button", { name: "Delete feature/old-work" }),
    );
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Delete branch",
      }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "look at it again before deleting it",
    );
    expect(
      screen.getByRole("row", { name: "feature/old-work" }),
    ).toBeInTheDocument();
  });

  it("lists settings edits and shares them as a pull request once confirmed", async () => {
    const edited = view(fetched.sync.branches, {
      settings: {
        baseRef: "origin/main",
        changes: [
          { path: ".inkuna/rules.yaml", kind: "modified" },
          { path: ".inkuna/agents/reviewer.md", kind: "added" },
          { path: ".inkuna/review.yaml", kind: "modified", onBase: true },
        ],
        digest: "d1g3st",
        shareable: true,
      },
    });
    getProjectBranches.mockResolvedValue(edited);
    fetchProject.mockResolvedValue(edited);
    shareSettings.mockResolvedValue(
      view(fetched.sync.branches, {
        settings: { ...edited.settings!, sharedOn: "chore/inkuna-settings" },
        shared: {
          mode: "pull-request",
          base: "main",
          commit: "abcdef1234",
          files: [".inkuna/rules.yaml", ".inkuna/agents/reviewer.md"],
          branch: "chore/inkuna-settings",
          url: "https://github.com/inkuna-ai/inkuna/pull/9",
          pullRequest: true,
        },
      }),
    );
    renderView();

    const panel = await screen.findByRole("region", { name: "Settings edits" });
    expect(within(panel).getByText(".inkuna/rules.yaml")).toBeInTheDocument();
    expect(
      within(panel).getByText("modified, already on the base"),
    ).toBeInTheDocument();
    // The edits no longer hold main back.
    expect(screen.getByRole("button", { name: "Update main" })).toBeEnabled();

    fireEvent.click(
      within(panel).getByRole("button", { name: "Open pull request" }),
    );
    const confirm = screen.getByRole("dialog", {
      name: "Open a pull request for these settings?",
    });
    expect(confirm).toHaveTextContent(".inkuna/agents/reviewer.md (added)");
    expect(confirm).not.toHaveTextContent(".inkuna/review.yaml");
    expect(confirm).toHaveTextContent("on top of origin/main");
    expect(shareSettings).not.toHaveBeenCalled();
    fireEvent.click(
      within(confirm).getByRole("button", { name: "Open pull request" }),
    );

    expect(
      await screen.findByText(/Pushed 2 files on chore\/inkuna-settings/),
    ).toHaveTextContent(
      "Opened a pull request into main. Once it merges, update main to take the edits as merged.",
    );
    expect(shareSettings).toHaveBeenCalledWith(
      "project",
      "pull-request",
      "d1g3st",
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(
      within(screen.getByRole("region", { name: "Settings edits" })).getByRole(
        "button",
        { name: "Open pull request" },
      ),
    ).toBeDisabled();
    fireEvent.click(
      screen.getByRole("button", { name: "View the pull request" }),
    );
    expect(openExternal).toHaveBeenCalledWith(
      "https://github.com/inkuna-ai/inkuna/pull/9",
    );
  });

  it("pushes settings to the base and shows Go's reason when it cannot share", async () => {
    const edited = view(fetched.sync.branches, {
      settings: {
        baseRef: "origin/main",
        changes: [{ path: ".inkuna/rules.yaml", kind: "modified" }],
        digest: "d1g3st",
        shareable: true,
      },
    });
    getProjectBranches.mockResolvedValue(edited);
    fetchProject.mockResolvedValue(edited);
    shareSettings.mockRejectedValue(
      "push settings to main: protected branch hook declined; open a pull request instead if main is protected",
    );
    renderView();

    fireEvent.click(
      await screen.findByRole("button", { name: "Push to main" }),
    );
    fireEvent.click(
      within(
        screen.getByRole("dialog", { name: "Push these settings to main?" }),
      ).getByRole("button", { name: "Push to main" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "open a pull request instead",
    );
    expect(shareSettings).toHaveBeenCalledWith("project", "push", "d1g3st");
  });

  it("explains why settings edits cannot be shared", async () => {
    const conflicted = view(fetched.sync.branches, {
      settings: {
        baseRef: "origin/main",
        changes: [
          { path: ".inkuna/rules.yaml", kind: "modified", conflict: true },
        ],
        digest: "d1g3st",
        shareable: false,
        reason:
          "origin/main changed .inkuna/rules.yaml after the checkout's commit, so sharing the edit would undo that change.",
      },
    });
    getProjectBranches.mockResolvedValue(conflicted);
    fetchProject.mockResolvedValue(conflicted);
    renderView();

    const panel = await screen.findByRole("region", { name: "Settings edits" });
    expect(
      within(panel).getByText("modified, also changed on the base"),
    ).toBeInTheDocument();
    expect(
      within(panel).getByText(/would undo that change/),
    ).toBeInTheDocument();
    expect(
      within(panel).getByRole("button", { name: "Open pull request" }),
    ).toBeDisabled();
    expect(
      within(panel).getByRole("button", { name: "Push to main" }),
    ).toBeDisabled();
  });

  it("says which settings an update took as merged", async () => {
    getProjectBranches.mockResolvedValue(fetched);
    fetchProject.mockResolvedValue(fetched);
    updateBranch.mockResolvedValue(
      view([branch({ isBase: true, main: true })], {
        update: {
          branch: "main",
          upstream: "origin/main",
          from: "1111111aaaa",
          to: "2222222bbbb",
          checkedOut: "/src/inkuna",
          kept: 1,
          settled: [".inkuna/rules.yaml"],
        },
      }),
    );
    renderView();

    fireEvent.click(await screen.findByRole("button", { name: "Update main" }));
    expect(
      await screen.findByText(
        "Fast-forwarded main to origin/main (1111111 → 2222222), and its checkout at /src/inkuna. origin/main already had .inkuna/rules.yaml, so it is no longer uncommitted. 1 uncommitted setting(s) or untracked file(s) stayed as they were.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Settings edits" })).toBeNull();
  });
});
