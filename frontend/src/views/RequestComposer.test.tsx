import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Project, WorkflowPlan } from "../types";
import { RequestComposer } from "./RequestComposer";

const bridge = vi.hoisted(() => ({
  planWorkflow: vi.fn(),
  importLinearIssue: vi.fn(),
  getRules: vi.fn(),
}));
vi.mock("../bridge", () => bridge);

const project: Project = {
  id: "project-1",
  name: "Inkuna",
  repository: "/src/inkuna",
  defaultRuntime: "claude",
  createdAt: "2026-10-27T09:00:00Z",
};

function compose() {
  const onContinue = vi.fn();
  render(
    <RequestComposer
      projects={[project]}
      selectedProject={project}
      onProjectChange={() => {}}
      onContinue={onContinue}
    />,
  );
  return onContinue;
}

describe("RequestComposer", () => {
  beforeEach(() => {
    bridge.planWorkflow.mockReset();
    bridge.importLinearIssue.mockReset();
    bridge.getRules.mockReset();
    bridge.getRules.mockRejectedValue(new Error("no rules"));
    bridge.planWorkflow.mockResolvedValue({} as WorkflowPlan);
  });

  it("sends the title with the details, and a blank title for the orchestrator to write", async () => {
    const onContinue = compose();
    const generate = screen.getByRole("button", {
      name: "Generate workflow →",
    });
    expect(generate).toBeDisabled();
    // A title alone is enough to plan.
    fireEvent.change(screen.getByRole("textbox", { name: "Title" }), {
      target: { value: "  Rate limit the API  " },
    });
    expect(generate).toBeEnabled();
    fireEvent.change(
      screen.getByPlaceholderText("Describe the outcome you want…"),
      { target: { value: "Limit each key to 100 requests a minute." } },
    );
    fireEvent.click(generate);
    await waitFor(() => expect(onContinue).toHaveBeenCalled());
    expect(bridge.planWorkflow).toHaveBeenLastCalledWith(
      expect.objectContaining({
        title: "Rate limit the API",
        objective: "Limit each key to 100 requests a minute.",
      }),
    );

    fireEvent.change(screen.getByRole("textbox", { name: "Title" }), {
      target: { value: "   " },
    });
    fireEvent.click(generate);
    await waitFor(() => expect(bridge.planWorkflow).toHaveBeenCalledTimes(2));
    expect(bridge.planWorkflow.mock.calls[1][0].title).toBeUndefined();
  });

  it("takes a Linear issue's title as the title and its description as the details", async () => {
    bridge.importLinearIssue.mockResolvedValue({
      id: "issue-1",
      identifier: "ENG-31",
      title: "Rate limit the public API",
      description: "Requests from one key can flood the API.",
      url: "https://linear.app/acme/issue/ENG-31/rate-limit",
      priority: 2,
      team: "Core",
      state: "Todo",
      labels: [],
      suggestedKind: "implementation",
    });
    compose();
    fireEvent.click(screen.getByRole("tab", { name: "Linear issue" }));
    fireEvent.change(
      screen.getByPlaceholderText("ENG-123 or https://linear.app/…"),
      {
        target: { value: "ENG-31" },
      },
    );
    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Title" })).toHaveValue(
        "Rate limit the public API",
      ),
    );
    expect(
      screen.getByPlaceholderText("Import a Linear issue above…"),
    ).toHaveValue("Requests from one key can flood the API.");
  });
});
