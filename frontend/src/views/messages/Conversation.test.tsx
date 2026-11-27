import { fireEvent, render, screen, within } from "@testing-library/react";
import type { A2AArtifact, FeedbackItem } from "../../types";
import { ArtifactList, FeedbackMessage, FeedbackReplies } from "./Conversation";

const bridge = vi.hoisted(() => ({ readArtifact: vi.fn() }));
vi.mock("../../bridge", () => bridge);

const item = (fields: Partial<FeedbackItem>): FeedbackItem => ({
  executionId: "run-1",
  round: 2,
  id: "k1",
  target: "implement",
  from: "check",
  text: "go test ./... exited with code 1",
  status: "open",
  createdAt: "2026-10-27T09:00:00Z",
  ...fields,
});

describe("FeedbackMessage", () => {
  it("shows a failed check as the check's message, with the end of its output a click away", () => {
    render(
      <FeedbackMessage
        items={[
          item({ checkId: "run-1-verify-0", tail: "--- FAIL: TestName\nFAIL" }),
        ]}
      />,
    );
    const message = screen.getByLabelText("Feedback for round 2");
    expect(message).toHaveTextContent("check · feedback for round 2");
    expect(message).toHaveTextContent("k1go test ./... exited with code 1");
    expect(within(message).queryByText(/--- FAIL: TestName/)).toBeNull();
    fireEvent.click(
      within(message).getByRole("button", {
        name: "▸ End of the check's output",
      }),
    );
    expect(within(message).getByText(/--- FAIL: TestName/)).toBeVisible();
  });

  it("offers no output for a person's item", () => {
    render(
      <FeedbackMessage
        items={[item({ id: "f1", from: "human", text: "Rename the flag." })]}
      />,
    );
    expect(screen.getByLabelText("Feedback for round 2")).toHaveTextContent(
      "human · feedback for round 2",
    );
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows where a line comment is, with the diff it was sent with a click away", () => {
    render(
      <FeedbackMessage
        items={[
          item({
            id: "c1",
            from: "human",
            text: "Keep these.",
            commentId: "comment-1",
            path: "name.go",
            side: "new",
            line: 6,
            endLine: 7,
            excerpt: "> 6 6   line 6",
          }),
          item({
            id: "c2",
            from: "human",
            text: "Why remove it?",
            path: "old.go",
            side: "old",
            line: 3,
            endLine: 3,
          }),
        ]}
      />,
    );
    const message = screen.getByLabelText("Feedback for round 2");
    expect(message).toHaveTextContent("c1name.go:6–7Keep these.");
    expect(message).toHaveTextContent("c2old.go:removed 3Why remove it?");
    fireEvent.click(
      within(message).getByRole("button", { name: "▸ The diff there" }),
    );
    expect(within(message).getByText(/line 6/)).toBeVisible();
  });

  it("shows the target's reply on a check's item", () => {
    render(
      <FeedbackReplies
        agent="implementer"
        items={[item({ status: "fixed", note: "The name defaults to World." })]}
      />,
    );
    expect(screen.getByLabelText("Replies for round 2")).toHaveTextContent(
      "k1fixed — The name defaults to World.",
    );
  });
});

describe("ArtifactList", () => {
  const handoff: A2AArtifact = {
    artifactId: "artifact-1",
    name: "handoff",
    parts: [
      { kind: "text", text: "Planned the change" },
      { kind: "data", data: { status: "completed" } },
      {
        kind: "file",
        file: {
          name: "plan-turn1-handoff.json",
          mimeType: "application/json",
          uri: "inkuna://artifact/aaa",
        },
        metadata: { kind: "handoff", size: 2048, round: 1 },
      },
      {
        kind: "file",
        file: {
          name: "plan.md",
          mimeType: "text/markdown",
          uri: "inkuna://artifact/bbb",
        },
        metadata: { kind: "document", size: 40, round: 1 },
      },
      { kind: "file", file: { name: "advice.pdf" } },
    ],
  };

  it("keeps the summary and links every stored file, never its text", () => {
    render(<ArtifactList artifacts={[handoff]} />);
    expect(screen.getByText("Planned the change")).toBeInTheDocument();
    const files = screen.getByRole("list", { name: "Files of handoff" });
    expect(
      within(files).getByRole("button", { name: /plan-turn1-handoff\.json/ }),
    ).toHaveTextContent("handoffplan-turn1-handoff.json2.0 KB · round 1");
    expect(
      within(files).getByRole("button", { name: /plan\.md/ }),
    ).toHaveTextContent("documentplan.md40 B · round 1");
    // A file Inkuna did not store is named, not linked.
    expect(within(files).getByText("advice.pdf").tagName).toBe("SPAN");
    expect(within(files).getAllByRole("button")).toHaveLength(2);
  });

  it("opens a Markdown document rendered from the message", async () => {
    bridge.readArtifact.mockResolvedValue({
      digest: "bbb",
      taskId: "task-1",
      stepId: "plan",
      kind: "document",
      name: "plan.md",
      mediaType: "text/markdown",
      size: 40,
      createdAt: "2026-10-27T09:00:00Z",
      content: "# Approach\n\n- Change the parser.\n",
    });
    render(<ArtifactList artifacts={[handoff]} />);
    fireEvent.click(screen.getByRole("button", { name: /plan\.md/ }));
    const drawer = await screen.findByRole("dialog", { name: "plan.md" });
    expect(
      await within(drawer).findByRole("heading", { name: "Approach" }),
    ).toBeInTheDocument();
    expect(within(drawer).getByRole("listitem")).toHaveTextContent(
      "Change the parser.",
    );
    expect(bridge.readArtifact).toHaveBeenCalledWith("bbb");
  });
});
