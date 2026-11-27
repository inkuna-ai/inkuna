import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Project, ProjectRemovalPreview } from "../../types";
import { RemoveProjectDialog } from "./RemoveProjectDialog";

const bridge = vi.hoisted(() => ({
  previewRemoveProject: vi.fn(),
  removeProject: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const project: Project = {
  id: "project-1",
  name: "bakery",
  repository: "/src/bakery",
  defaultRuntime: "codex",
  createdAt: "2026-11-14T09:00:00Z",
};

const preview = (
  changes: Partial<ProjectRemovalPreview> = {},
): ProjectRemovalPreview => ({
  projectId: "project-1",
  name: "bakery",
  repository: "/src/bakery",
  tasks: 2,
  runs: 3,
  worktrees: ["/src/bakery-worktrees/bug/42-fix"],
  ...changes,
});

describe("RemoveProjectDialog", () => {
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    bridge.previewRemoveProject.mockReset();
    bridge.removeProject.mockReset();
  });

  it("says what stays, keeps the history by default, and removes", async () => {
    bridge.previewRemoveProject.mockResolvedValue(preview());
    const removal = {
      project: { ...project, removedAt: "2026-11-14T10:00:00Z" },
      deletedTasks: 0,
    };
    bridge.removeProject.mockResolvedValue(removal);
    const onRemoved = vi.fn();
    render(
      <RemoveProjectDialog
        project={project}
        onRemoved={onRemoved}
        onClose={() => {}}
      />,
    );
    expect(
      await screen.findByText(/its runs' worktrees stay on disk/),
    ).toBeInTheDocument();
    expect(
      screen.getByText("/src/bakery-worktrees/bug/42-fix"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Its history: 2 tasks, 3 runs"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^Keep it/)).toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: "Remove project" }));
    await vi.waitFor(() => expect(onRemoved).toHaveBeenCalledWith(removal));
    expect(bridge.removeProject).toHaveBeenCalledWith("project-1", false);
  });

  it("deletes the history when the person chooses to", async () => {
    bridge.previewRemoveProject.mockResolvedValue(preview());
    bridge.removeProject.mockResolvedValue({ project, deletedTasks: 2 });
    render(
      <RemoveProjectDialog
        project={project}
        onRemoved={() => {}}
        onClose={() => {}}
      />,
    );
    fireEvent.click(await screen.findByLabelText(/^Delete it/));
    fireEvent.click(screen.getByRole("button", { name: "Remove project" }));
    await vi.waitFor(() =>
      expect(bridge.removeProject).toHaveBeenCalledWith("project-1", true),
    );
  });

  it("shows Go's refusal and keeps the removal off", async () => {
    bridge.previewRemoveProject.mockResolvedValue(
      preview({
        refused:
          "bakery cannot be removed while run run-7 is running; let it end or cancel it first.",
      }),
    );
    render(
      <RemoveProjectDialog
        project={project}
        onRemoved={() => {}}
        onClose={() => {}}
      />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "while run run-7 is running",
    );
    expect(
      screen.getByRole("button", { name: "Remove project" }),
    ).toBeDisabled();
  });

  it("offers no history choice for a project with none", async () => {
    bridge.previewRemoveProject.mockResolvedValue(
      preview({ tasks: 0, runs: 0, worktrees: [] }),
    );
    render(
      <RemoveProjectDialog
        project={project}
        onRemoved={() => {}}
        onClose={() => {}}
      />,
    );
    await screen.findByRole("button", { name: "Remove project" });
    await screen.findByText(/leaves Projects and New request/);
    expect(screen.queryByLabelText(/^Keep it/)).not.toBeInTheDocument();
  });
});
