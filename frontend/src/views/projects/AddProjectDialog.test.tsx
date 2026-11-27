import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import type { Project } from "../../types";
import { AddProjectDialog } from "./AddProjectDialog";

const bridge = vi.hoisted(() => ({
  addProject: vi.fn(),
  chooseDirectory: vi.fn(),
  inspectRepository: vi.fn(),
  listRuntimes: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const project: Project = {
  id: "project-1",
  name: "bakery",
  repository: "/src/bakery",
  defaultRuntime: "claude",
  createdAt: "2026-11-14T09:00:00Z",
};

describe("AddProjectDialog", () => {
  beforeEach(() => bridge.listRuntimes.mockResolvedValue([]));
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    for (const mock of Object.values(bridge)) mock.mockReset();
  });

  it("fills the form from the chosen folder's repository and connects it", async () => {
    bridge.chooseDirectory.mockResolvedValue("/src/bakery/internal");
    bridge.inspectRepository.mockResolvedValue({
      path: "/src/bakery",
      name: "bakery",
    });
    bridge.addProject.mockResolvedValue(project);
    const onAdded = vi.fn();
    render(
      <AddProjectDialog
        defaultRuntime="claude"
        onAdded={onAdded}
        onClose={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Choose folder…" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Connects /src/bakery.",
    );
    expect(bridge.inspectRepository).toHaveBeenCalledWith(
      "/src/bakery/internal",
    );
    expect(screen.getByLabelText("Name")).toHaveValue("bakery");
    expect(screen.getByLabelText("Default runtime")).toHaveValue("claude");
    fireEvent.click(screen.getByRole("button", { name: "Add project" }));
    await vi.waitFor(() => expect(onAdded).toHaveBeenCalledWith(project));
    expect(bridge.addProject).toHaveBeenCalledWith(
      "bakery",
      "/src/bakery/internal",
      "claude",
    );
  });

  it("shows Go's reason a folder cannot be connected and keeps the add off", async () => {
    bridge.inspectRepository.mockResolvedValue({
      path: "/src/bakery",
      name: "bakery",
      refused: "/src/bakery is already connected as bakery.",
    });
    render(
      <AddProjectDialog
        defaultRuntime="codex"
        onAdded={() => {}}
        onClose={() => {}}
      />,
    );
    const folder = screen.getByLabelText("Repository folder");
    fireEvent.change(folder, { target: { value: "/src/bakery" } });
    fireEvent.blur(folder);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "already connected as bakery",
    );
    expect(screen.getByRole("button", { name: "Add project" })).toBeDisabled();
    expect(screen.getByLabelText("Name")).toHaveValue("");
  });

  it("keeps a name the person typed, and says when it brings a project back", async () => {
    bridge.inspectRepository.mockResolvedValue({
      path: "/src/bakery",
      name: "bakery",
      restores: "bakery",
    });
    render(
      <AddProjectDialog
        defaultRuntime="codex"
        onAdded={() => {}}
        onClose={() => {}}
      />,
    );
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Bakery app" },
    });
    const folder = screen.getByLabelText("Repository folder");
    fireEvent.change(folder, { target: { value: "/src/bakery" } });
    fireEvent.blur(folder);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "bringing back bakery and its history",
    );
    expect(screen.getByLabelText("Name")).toHaveValue("Bakery app");
  });

  it("shows a refusal from Go when adding fails", async () => {
    bridge.inspectRepository.mockResolvedValue({
      path: "/src/bakery",
      name: "bakery",
    });
    bridge.addProject.mockRejectedValue(
      "a project is already named bakery; choose another name",
    );
    const onAdded = vi.fn();
    render(
      <AddProjectDialog
        defaultRuntime="codex"
        onAdded={onAdded}
        onClose={() => {}}
      />,
    );
    const folder = screen.getByLabelText("Repository folder");
    fireEvent.change(folder, { target: { value: "/src/bakery" } });
    fireEvent.blur(folder);
    await screen.findByRole("status");
    fireEvent.click(screen.getByRole("button", { name: "Add project" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "already named bakery",
    );
    expect(onAdded).not.toHaveBeenCalled();
  });

  it("drops an inspection that answers after the folder changed", async () => {
    let answerFirst: (value: unknown) => void = () => {};
    bridge.inspectRepository
      .mockReturnValueOnce(
        new Promise((resolve) => {
          answerFirst = resolve;
        }),
      )
      .mockResolvedValueOnce({ path: "/src/bakery", name: "bakery" });
    render(
      <AddProjectDialog
        defaultRuntime="codex"
        onAdded={() => {}}
        onClose={() => {}}
      />,
    );
    const folder = screen.getByLabelText("Repository folder");
    fireEvent.change(folder, { target: { value: "/src/taken" } });
    fireEvent.blur(folder);
    fireEvent.change(folder, { target: { value: "/src/bakery" } });
    fireEvent.blur(folder);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Connects /src/bakery.",
    );
    // The first folder's answer comes last and must not replace it.
    await act(async () => {
      answerFirst({
        path: "/src/taken",
        name: "taken",
        refused: "/src/taken is already connected as taken.",
      });
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Connects /src/bakery.",
    );
    expect(screen.getByLabelText("Name")).toHaveValue("bakery");
    expect(screen.getByRole("button", { name: "Add project" })).toBeEnabled();
  });

  it("keeps a name typed while the inspection was pending", async () => {
    let answer: (value: unknown) => void = () => {};
    bridge.inspectRepository.mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );
    render(
      <AddProjectDialog
        defaultRuntime="codex"
        onAdded={() => {}}
        onClose={() => {}}
      />,
    );
    const folder = screen.getByLabelText("Repository folder");
    fireEvent.change(folder, { target: { value: "/src/bakery" } });
    fireEvent.blur(folder);
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Bakery app" },
    });
    answer({ path: "/src/bakery", name: "bakery" });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Connects /src/bakery.",
    );
    expect(screen.getByLabelText("Name")).toHaveValue("Bakery app");
  });
});
