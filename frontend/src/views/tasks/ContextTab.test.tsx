import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { StepContext, UsageRecord, WorkflowRun } from "../../types";
import { ContextTab } from "./ContextTab";

const getRunContext = vi.hoisted(() => vi.fn());
const readArtifact = vi.hoisted(() => vi.fn());
vi.mock("../../bridge", () => ({ getRunContext, readArtifact }));

const run: WorkflowRun = {
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "completed",
  title: "Docs in the envelope",
  startedAt: "2026-10-17T10:00:00Z",
  events: [],
  steps: [
    {
      id: "research",
      title: "Research",
      kind: "agent",
      agent: "researcher",
      state: "completed",
    },
  ],
};

function stepContext(envelope: Partial<StepContext["envelope"]>): StepContext {
  return {
    stepId: "research",
    agent: "researcher",
    prompt: "",
    envelope: {
      version: 1,
      taskId: "task-1",
      stepId: "research",
      runtime: "claude",
      objective: "Find the design",
      acceptanceCriteria: [],
      ...envelope,
    },
  };
}

const docRead = (
  docPath: string,
  delivered: boolean,
  tool: string,
  second: number,
) => ({
  taskId: "task-1",
  stepId: "research",
  runId: "run-1",
  projectId: "project-1",
  agent: "researcher",
  runtime: "claude" as const,
  docsDir: "docs",
  docPath,
  tool,
  delivered,
  openedAt: `2026-10-17T10:00:0${second}Z`,
});

const call = (id: number, fields: Partial<UsageRecord>): UsageRecord => ({
  id,
  taskId: "task-1",
  executionId: "run-1",
  stepId: "research",
  callId: `run-1-research-${id}`,
  turn: 1,
  kind: "worker",
  agent: "researcher",
  runtime: "claude",
  usage: { inputTokens: 8, cacheReadTokens: 24000, outputTokens: 300 },
  promptBytes: 0,
  outcome: "completed",
  startedAt: "2026-10-17T10:00:00Z",
  finishedAt: "2026-10-17T10:01:00Z",
  ...fields,
});

const promptArtifact = (digest: string, content: string) => ({
  digest,
  taskId: "task-1",
  kind: "prompt",
  name: "research-turn1-prompt.md",
  mediaType: "text/markdown",
  size: content.length,
  createdAt: "2026-10-17T10:00:00Z",
  content,
});

describe("ContextTab", () => {
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    getRunContext.mockReset();
    readArtifact.mockReset();
  });

  it("lists the doc index the step received", async () => {
    getRunContext.mockResolvedValue(
      stepContext({
        docs: [
          {
            path: "docs/architecture.md",
            title: "Inkuna architecture",
            tags: ["architecture"],
            status: "current",
            lastUpdated: "2026-10-16",
            author: "A. Author",
          },
          {
            path: "docs/legacy.md",
            title: "Legacy",
            status: "deprecated",
            lastUpdated: "2025-01-01",
          },
          { path: "docs/notes.md", problems: ["missing frontmatter"] },
        ],
      }),
    );
    render(<ContextTab run={run} />);
    const list = await screen.findByRole("list", { name: "Delivered docs" });
    const entries = within(list).getAllByRole("listitem");
    expect(entries).toHaveLength(3);
    expect(entries[0]).toHaveTextContent("docs/architecture.md");
    expect(entries[0]).toHaveTextContent(
      "Inkuna architecture · updated 2026-10-16 · A. Author · architecture",
    );
    expect(entries[1]).toHaveTextContent("deprecated");
    expect(entries[2]).toHaveTextContent("Untitled");
    expect(entries[2]).toHaveTextContent("missing frontmatter");
    expect(screen.getByText(/· 3 docs/)).toBeInTheDocument();
    expect(screen.queryByText(/Cut to fit/)).not.toBeInTheDocument();
  });

  it("lists the docs the step read, in order, against the index it was given", async () => {
    getRunContext.mockResolvedValue({
      ...stepContext({
        docs: [
          { path: "docs/architecture.md", title: "Inkuna architecture" },
          { path: "docs/rules.md", title: "Rules" },
        ],
      }),
      docReads: [
        docRead("architecture.md", true, "Read", 1),
        docRead("notes/", false, "Grep", 2),
        docRead("drafts/plan.md", false, "shell:cat", 3),
        docRead("architecture.md", true, "shell:sed", 4),
        docRead("drafts/plan.md", false, "Read", 5),
      ],
    });
    render(<ContextTab run={run} />);
    const list = await screen.findByRole("list", { name: "Docs read" });
    const entries = within(list).getAllByRole("listitem");
    expect(
      entries.map((entry) => entry.querySelector("span")?.textContent),
    ).toEqual([
      "architecture.md",
      "searched notes/",
      "drafts/plan.md",
      "architecture.md",
      "drafts/plan.md",
    ]);
    expect(entries[0]).toHaveTextContent("delivered");
    expect(entries[0]).toHaveTextContent("Read");
    expect(entries[1]).not.toHaveTextContent("delivered");
    expect(entries[2]).toHaveTextContent("not delivered");
    expect(entries[2]).toHaveTextContent("shell:cat");
    expect(entries[0].querySelector("time")).toHaveAttribute(
      "dateTime",
      "2026-10-17T10:00:01Z",
    );
    // A doc counts once however often it was opened, delivered or not.
    expect(
      screen.getByText("Opened 1 of the 2 docs delivered · 1 not in the index"),
    ).toBeInTheDocument();
  });

  it("picks up reads as an active run refreshes", async () => {
    const active: WorkflowRun = { ...run, state: "running" };
    getRunContext.mockResolvedValueOnce({
      ...stepContext({ docs: [{ path: "docs/guide.md" }] }),
      docReads: [],
    });
    const { rerender } = render(<ContextTab run={active} />);
    expect(
      await screen.findByText(
        "The agent opened no docs that Inkuna could see.",
      ),
    ).toBeInTheDocument();
    getRunContext.mockResolvedValueOnce({
      ...stepContext({ docs: [{ path: "docs/guide.md" }] }),
      docReads: [docRead("guide.md", true, "Read", 1)],
    });
    // The Tasks view replaces the run on each refresh; its state is the same.
    rerender(<ContextTab run={{ ...active }} />);
    expect(
      await screen.findByText("Opened 1 of the 1 doc delivered"),
    ).toBeInTheDocument();
    expect(getRunContext).toHaveBeenCalledTimes(2);
  });

  it("does not refetch a finished run on every refresh", async () => {
    getRunContext.mockResolvedValue({
      ...stepContext({ docs: [] }),
      docReads: [],
    });
    const { rerender } = render(<ContextTab run={run} />);
    await screen.findByText("The agent opened no docs that Inkuna could see.");
    rerender(<ContextTab run={{ ...run }} />);
    await waitFor(() => expect(getRunContext).toHaveBeenCalledTimes(1));
  });

  it("says when a step opened no docs", async () => {
    getRunContext.mockResolvedValue({
      ...stepContext({ docs: [] }),
      docReads: [],
    });
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText(
        "The agent opened no docs that Inkuna could see.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("list", { name: "Docs read" }),
    ).not.toBeInTheDocument();
  });

  it("says when Go cut the index to fit", async () => {
    getRunContext.mockResolvedValue(
      stepContext({
        docs: [{ path: "docs/guide.md", title: "Guide", status: "current" }],
        metadata: { docsTruncated: "true" },
      }),
    );
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText(/Cut to fit: deprecated and the least recently/),
    ).toBeInTheDocument();
  });

  it("says when no docs were delivered", async () => {
    getRunContext.mockResolvedValue(stepContext({}));
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText("No docs were delivered to this step."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("list", { name: "Delivered docs" }),
    ).not.toBeInTheDocument();
  });

  it("breaks down the prompt of the step's latest call, or of the one chosen", async () => {
    getRunContext.mockResolvedValue({
      ...stepContext({}),
      usage: [
        call(1, {
          promptBytes: 4000,
          sections: [
            { section: "schema", bytes: 1000 },
            { section: "skill", name: "conventions", bytes: 3000 },
          ],
        }),
        call(2, {
          kind: "peer",
          agent: "reviewer",
          promptBytes: 2000,
          sections: [{ section: "peerMessages", bytes: 2000 }],
        }),
        call(3, {
          turn: 2,
          promptBytes: 10000,
          sections: [
            { section: "docs", bytes: 2500 },
            { section: "humanResponses", bytes: 7500 },
          ],
        }),
      ],
    });
    render(<ContextTab run={run} />);
    const table = await screen.findByRole("table", { name: "Prompt sections" });
    // The latest turn, largest section first.
    const rows = within(table).getAllByRole("row");
    expect(rows[1]).toHaveTextContent(/Human responses.*7,500.*1,875.*75%/);
    expect(rows[2]).toHaveTextContent(/Doc index.*2,500.*625.*25%/);
    expect(
      screen.getByText(
        /Turn 2 · researcher sent 10,000 bytes \(about 2,500 tokens\)/,
      ),
    ).toHaveTextContent("the model read 24k in · 100% cached · 300 out");
    fireEvent.change(screen.getByLabelText("Prompt call"), {
      target: { value: "run-1-research-1" },
    });
    const first = within(
      screen.getByRole("table", { name: "Prompt sections" }),
    ).getAllByRole("row");
    expect(first[1]).toHaveTextContent(/Skill · conventions.*3,000.*750.*75%/);
    expect(first[2]).toHaveTextContent(/Output schema.*1,000/);
    expect(screen.getByLabelText("Prompt call")).toHaveTextContent(
      "Turn 1 · peer reviewer",
    );
  });

  it("shows the prompt each call sent, as Go stored it", async () => {
    readArtifact.mockImplementation((digest: string) =>
      Promise.resolve(
        promptArtifact(digest, `# Agent: researcher\n\nprompt ${digest}`),
      ),
    );
    getRunContext.mockResolvedValue({
      ...stepContext({}),
      usage: [
        call(1, { promptDigest: "digest-turn1", promptBytes: 100 }),
        call(2, { turn: 2, promptDigest: "digest-turn2", promptBytes: 200 }),
      ],
    });
    render(<ContextTab run={run} />);
    expect(await screen.findByText(/prompt digest-turn2/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Prompt call"), {
      target: { value: "run-1-research-1" },
    });
    expect(await screen.findByText(/prompt digest-turn1/)).toBeInTheDocument();
    expect(screen.queryByText(/prompt digest-turn2/)).not.toBeInTheDocument();
    expect(readArtifact.mock.calls).toEqual([
      ["digest-turn2"],
      ["digest-turn1"],
    ]);
    // A local step's prompts are per call; only a remote step has a payload.
    expect(
      screen.queryByText("Sent to the remote agent"),
    ).not.toBeInTheDocument();
  });

  it("shows a running call with the prompt it sent", async () => {
    readArtifact.mockResolvedValue(
      promptArtifact("digest-running", "# Agent: researcher\n\nturn two"),
    );
    getRunContext.mockResolvedValue({
      ...stepContext({}),
      usage: [
        call(1, { promptBytes: 100 }),
        call(0, {
          callId: "run-1-research-turn2",
          turn: 2,
          promptBytes: 3000,
          promptDigest: "digest-running",
          usage: {},
          outcome: "running",
        }),
      ],
    });
    render(<ContextTab run={{ ...run, state: "running" }} />);
    expect(await screen.findByText(/turn two/)).toBeInTheDocument();
    expect(
      screen.getByText(/Turn 2 · researcher sent 3,000 bytes/),
    ).toHaveTextContent(/tokens\) · running$/);
    fireEvent.change(screen.getByLabelText("Prompt call"), {
      target: { value: "run-1-research-1" },
    });
    expect(
      await screen.findByText(/Inkuna did not keep this call's prompt/),
    ).toBeInTheDocument();
  });

  it("says when a stored prompt is longer than the preview", async () => {
    readArtifact.mockResolvedValue({
      ...promptArtifact("digest-long", "# Agent: researcher"),
      truncated: true,
    });
    getRunContext.mockResolvedValue({
      ...stepContext({}),
      usage: [call(1, { promptDigest: "digest-long" })],
    });
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText(/the stored prompt is longer/),
    ).toBeInTheDocument();
  });

  it("shows what a remote advisor was sent", async () => {
    getRunContext.mockResolvedValue({
      ...stepContext({}),
      agent: "security-advisor",
      prompt: '{"objective":"Review the plan"}',
      usage: [],
    });
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText("Sent to the remote agent"),
    ).toBeInTheDocument();
    expect(
      screen.getByText('{"objective":"Review the plan"}'),
    ).toBeInTheDocument();
  });

  it("marks what the budgets cut, in the envelope and on the call", async () => {
    getRunContext.mockResolvedValue({
      ...stepContext({
        metadata: { truncated: "objective,skill:conventions" },
      }),
      usage: [
        call(1, {
          promptBytes: 3000,
          sections: [
            { section: "objective", bytes: 2000, truncated: true },
            { section: "schema", bytes: 1000 },
          ],
        }),
      ],
    });
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText(
        /Cut to fit the budgets: Objective, Skill · conventions\./,
      ),
    ).toBeInTheDocument();
    const rows = within(
      screen.getByRole("table", { name: "Prompt sections" }),
    ).getAllByRole("row");
    expect(rows[1]).toHaveTextContent(/Objective\s*cut to fit/);
    expect(rows[2]).not.toHaveTextContent("cut to fit");
  });

  it("shows the feedback each round sent the step, newest first, with its replies", async () => {
    const item = (
      round: number,
      id: string,
      text: string,
      status: string,
      note?: string,
    ) => ({
      executionId: "run-1",
      round,
      id,
      target: "research",
      from: "human",
      text,
      status,
      note,
      createdAt: "2026-10-26T10:00:00Z",
    });
    getRunContext.mockResolvedValue({
      ...stepContext({}),
      feedback: [
        {
          round: 2,
          items: [
            item(
              2,
              "f1",
              "Handle an empty name.",
              "fixed",
              "Empty names now return an error.",
            ),
            item(2, "f2", "Log the failure.", "no-reply"),
          ],
        },
        {
          round: 3,
          items: [
            item(
              3,
              "f1",
              "Keep the old date format?",
              "question",
              "Which format?",
            ),
          ],
        },
      ],
    });
    render(<ContextTab run={run} />);
    const rounds = await screen.findAllByRole("region", {
      name: /Feedback for round/,
    });
    expect(rounds.map((round) => round.getAttribute("aria-label"))).toEqual([
      "Feedback for round 3",
      "Feedback for round 2",
    ]);
    const second = within(rounds[1]).getAllByRole("listitem");
    expect(second[0]).toHaveTextContent(
      /f1\s*from the user\s*fixed\s*Handle an empty name\.\s*Empty names now return an error\./,
    );
    expect(second[1]).toHaveTextContent(/f2.*no reply.*Log the failure\./);
    expect(within(rounds[0]).getByText("question")).toBeInTheDocument();
  });

  it("shows no feedback for a step no round sent back", async () => {
    getRunContext.mockResolvedValue({ ...stepContext({}), feedback: [] });
    render(<ContextTab run={run} />);
    await screen.findByText("Context envelope");
    expect(screen.queryByText("Feedback")).not.toBeInTheDocument();
  });

  it("shows the stage and the constraints the step was sent", async () => {
    getRunContext.mockResolvedValue({
      ...stepContext({
        stageTitle: "Reproduce and locate the defect",
        stagePrompt: "Find where the save fails.\nDo not fix it.",
        constraints: ["Keep the public API", "Keep the public API"],
        metadata: { truncated: "stage,constraints" },
      }),
    });
    render(<ContextTab run={run} />);
    const stage = await screen.findByRole("region", { name: "Stage" });
    expect(
      within(stage).getByRole("heading", {
        name: "Stage · Reproduce and locate the defect",
      }),
    ).toBeInTheDocument();
    expect(stage).toHaveTextContent(
      "Find where the save fails. Do not fix it.",
    );
    const constraints = within(
      screen.getByRole("region", { name: "Constraints" }),
    ).getAllByRole("listitem");
    expect(constraints.map((item) => item.textContent)).toEqual([
      "Keep the public API",
      "Keep the public API",
    ]);
    expect(
      screen.getByText(/0 acceptance criteria · 2 constraints/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Cut to fit the budgets: Stage, Constraints\./),
    ).toBeInTheDocument();
  });

  it("says when a stage has no instructions, and shows neither part when the envelope has none", async () => {
    getRunContext.mockResolvedValueOnce({
      ...stepContext({ stageTitle: "Implement the fix" }),
    });
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText("The rules give this stage no instructions."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("region", { name: "Constraints" }),
    ).not.toBeInTheDocument();
    cleanup();
    getRunContext.mockResolvedValueOnce({ ...stepContext({}) });
    render(<ContextTab run={run} />);
    await screen.findByText("Context envelope");
    expect(
      screen.queryByRole("region", { name: "Stage" }),
    ).not.toBeInTheDocument();
  });

  it("says what the step could ask of the tracker", async () => {
    getRunContext.mockResolvedValueOnce({
      ...stepContext({
        tracker: {
          tracker: "linear",
          kinds: ["comment", "create"],
          story: "MAD-12",
        },
      }),
    });
    render(<ContextTab run={run} />);
    const tracker = await screen.findByLabelText("Tracker changes");
    expect(tracker).toHaveTextContent(
      'The step could ask for comment, create changes in Linear, naming its story MAD-12 as "story".',
    );
    expect(screen.getByText(/· tracker changes/)).toBeInTheDocument();
    cleanup();
    getRunContext.mockResolvedValueOnce({ ...stepContext({}) });
    render(<ContextTab run={run} />);
    await screen.findByText("Context envelope");
    expect(screen.queryByLabelText("Tracker changes")).toBeNull();
  });

  it("shows the MCP servers the step was given", async () => {
    getRunContext.mockResolvedValueOnce({
      ...stepContext({
        mcpServers: [
          {
            name: "docs-search",
            description: "Searches the design archive",
            url: "https://docs.example/mcp",
          },
        ],
        metadata: { permission: "read-only" },
      }),
    });
    render(<ContextTab run={run} />);
    const servers = await screen.findByRole("region", { name: "MCP servers" });
    expect(servers).toHaveTextContent(
      "docs-search · Searches the design archiveAll its tools · https://docs.example/mcp",
    );
    expect(servers).toHaveTextContent("Read-only covers the worktree only");
    expect(screen.getByText(/0 docs · 1 MCP servers/)).toBeInTheDocument();
    cleanup();
    getRunContext.mockResolvedValueOnce({ ...stepContext({}) });
    render(<ContextTab run={run} />);
    await screen.findByText("Context envelope");
    expect(
      screen.queryByRole("region", { name: "MCP servers" }),
    ).not.toBeInTheDocument();
  });

  it("shows the user's own servers and what became of each", async () => {
    getRunContext.mockResolvedValueOnce({
      ...stepContext({
        userMcpServers: [
          {
            name: "linear-server",
            source: "config",
            url: "https://mcp.linear.app/mcp",
            status: "ready",
          },
          {
            name: "gone",
            status: "missing",
            detail: "claude no longer lists it",
          },
        ],
      }),
    });
    render(<ContextTab run={run} />);
    const own = await screen.findByRole("region", { name: "Your MCP servers" });
    expect(own).toHaveTextContent(
      "linear-server yours · all tools readyconfig · https://mcp.linear.app/mcp",
    );
    expect(own).toHaveTextContent(
      "gone yours · all tools not configuredclaude no longer lists it",
    );
    expect(screen.getByText(/2 of your MCP servers/)).toBeInTheDocument();
    expect(
      screen.queryByRole("region", { name: "MCP servers" }),
    ).not.toBeInTheDocument();
  });

  it("says when a step has no recorded calls", async () => {
    getRunContext.mockResolvedValue({ ...stepContext({}), usage: [] });
    render(<ContextTab run={run} />);
    expect(
      await screen.findByText(
        "Inkuna has recorded no model call for this step.",
      ),
    ).toBeInTheDocument();
  });
});
