import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type {
  CatalogListing,
  CatalogResult,
  Project,
  RuleEdits,
  RuleStage,
  RulesDocument,
  RulesInfo,
  StageLibrary,
} from "../types";
import { RulesView } from "./RulesView";

const bridge = vi.hoisted(() => ({
  addCatalogItem: vi.fn(),
  listCatalog: vi.fn(),
  previewCatalogItem: vi.fn(),
  checkRules: vi.fn(),
  expandRules: vi.fn(),
  getStageLibrary: vi.fn(),
  globalDefinitionUsers: vi.fn(),
  saveGlobalDefinition: vi.fn(),
  getRules: vi.fn(),
  listAgents: vi.fn(),
  listRemoteAgents: vi.fn(),
  listRuntimes: vi.fn(),
  rulesFromYAML: vi.fn(),
  rulesToYAML: vi.fn(),
  saveRules: vi.fn(),
}));
vi.mock("../bridge", () => bridge);

const project: Project = {
  id: "project-1",
  name: "Bakery",
  repository: "/src/bakery",
  defaultRuntime: "claude",
  createdAt: "2026-01-01T00:00:00Z",
};

const stages = (commands?: string[][]): RuleStage[] => [
  {
    id: "fix",
    kind: "agent",
    agent: "auto",
    permission: "worktree-write",
    ...(commands ? { commands } : {}),
  },
  { id: "approval", kind: "approval", dependsOn: ["fix"] },
];

const rulesDocument = (commands?: string[][]): RulesDocument => ({
  version: 1,
  rules: [
    {
      name: "bugfix",
      when: { kind: ["bugfix"] },
      then: { stages: stages(commands) },
    },
  ],
  default: { stages: stages() },
  source: "",
});

const info: RulesInfo = {
  document: rulesDocument(),
  exists: true,
  path: "/src/bakery/.inkuna/rules.yaml",
};

const proposal: RuleEdits = {
  projectId: "project-1",
  ruleName: "bugfix",
  ruleIndex: 0,
  rules: { ...info, document: rulesDocument([["go", "test", "./..."]]) },
  changes: [
    {
      stepId: "fix",
      field: "commands",
      text: 'Stage fix: commands "go test ./..."; the rule had none.',
    },
  ],
  leftOut: [
    {
      stepId: "verify",
      field: "commands",
      text: `Step verify's commands: rule "bugfix" has no stage verify any more.`,
    },
  ],
};

function renderRules(shown: RuleEdits = proposal) {
  render(
    <RulesView
      projects={[project]}
      selectedProject={project}
      onProjectChange={() => {}}
      proposal={shown}
    />,
  );
}

describe("RulesView", () => {
  beforeEach(() => {
    bridge.getRules.mockResolvedValue(info);
    bridge.listAgents.mockResolvedValue([]);
    bridge.listRemoteAgents.mockResolvedValue([]);
    bridge.listRuntimes.mockResolvedValue([]);
    bridge.getStageLibrary.mockResolvedValue({
      definitions: [],
      problems: [],
      overridable: [],
    });
    bridge.checkRules.mockResolvedValue({ message: "" });
    bridge.saveRules.mockImplementation(
      async (_projectID: string, saved: RulesDocument) => ({
        ...info,
        document: saved,
      }),
    );
  });
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    for (const mock of Object.values(bridge)) mock.mockReset();
  });

  it("shows a rerun's changes to its rule unsaved, and saves them only when the person does", async () => {
    renderRules();
    const notice = await screen.findByRole("region", {
      name: "Changes from a rerun",
    });
    expect(notice).toHaveTextContent(
      "Changes from a rerun, made to rule bugfix",
    );
    expect(
      within(notice).getByRole("list", { name: "Changes made" }),
    ).toHaveTextContent(
      'Stage fix: commands "go test ./..."; the rule had none.',
    );
    expect(
      within(notice).getByRole("list", { name: "Changes left out" }),
    ).toHaveTextContent(`rule "bugfix" has no stage verify any more`);
    expect(screen.getByText("unsaved changes")).toBeInTheDocument();
    // The rule opens on its stages, with the change made there.
    expect(screen.getByLabelText("Commands for fix")).toHaveValue(
      "go test ./...",
    );
    expect(bridge.saveRules).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(bridge.saveRules).toHaveBeenCalledTimes(1));
    const [projectID, saved] = bridge.saveRules.mock.calls[0] as [
      string,
      RulesDocument,
    ];
    expect(projectID).toBe("project-1");
    expect(saved.rules[0].then.stages[0].commands).toEqual([
      ["go", "test", "./..."],
    ]);
    await screen.findByText(/^Saved to/);
    expect(
      screen.queryByRole("region", { name: "Changes from a rerun" }),
    ).toBeNull();
  });

  it("opens the rule Go found, even one called default, or else the fallback flow", async () => {
    const named = rulesDocument([["go", "test", "./..."]]);
    named.rules[0].name = "default";
    renderRules({
      ...proposal,
      ruleName: "default",
      rules: { ...info, document: named },
    });
    expect(
      await screen.findByRole("region", { name: "Changes from a rerun" }),
    ).toHaveTextContent("made to rule default");
    expect(
      screen.getByRole("group", { name: "Stages of default" }),
    ).toBeInTheDocument();
  });

  it("opens the fallback flow for a run planned from it", async () => {
    const fallback = rulesDocument();
    fallback.default = { stages: stages([["go", "test", "./..."]]) };
    renderRules({
      ...proposal,
      ruleName: "default",
      ruleIndex: undefined,
      rules: { ...info, document: fallback },
    });
    expect(
      await screen.findByRole("region", { name: "Changes from a rerun" }),
    ).toHaveTextContent("made to the fallback flow");
    expect(
      within(
        screen.getByRole("group", { name: "Stages of the fallback flow" }),
      ).getByLabelText("Commands for fix"),
    ).toHaveValue("go test ./...");
  });

  it("shows what a use inherits and overrides, and saves it back as it came", async () => {
    const use: RuleStage = {
      use: "fix",
      id: "",
      kind: "",
      commands: [["go", "test", "./..."]],
      overrides: ["commands"],
    };
    const shared: RulesDocument = {
      version: 2,
      stages: { fix: { id: "", kind: "agent", agent: "auto" } },
      rules: [
        {
          name: "bugfix",
          when: { kind: ["bugfix"] },
          then: {
            stages: [
              use,
              { id: "approval", kind: "approval", dependsOn: ["fix"] },
            ],
          },
        },
      ],
      source: "",
    };
    const library: StageLibrary = {
      definitions: [
        {
          name: "fix",
          scope: "repository",
          file: info.path,
          kind: "stage",
          stage: shared.stages!.fix,
          usedBy: ["bugfix"],
        },
      ],
      problems: [],
      overridable: [
        { name: "title" },
        { name: "agent", kinds: ["agent", "human-input"] },
        { name: "commands", kinds: ["agent", "evaluation"] },
        { name: "review", kinds: ["approval"] },
      ],
    };
    bridge.getStageLibrary.mockResolvedValue(library);
    renderRules({ ...proposal, rules: { ...info, document: shared } });
    const stage = await screen.findByRole("listitem", { name: "Stage fix" });
    expect(stage).toHaveTextContent("uses fix");
    expect(
      await within(stage).findByText(/Uses the repository shared stage/),
    ).toBeInTheDocument();
    // The agent comes from the shared stage; the commands are this use's.
    expect(within(stage).getByText("agent inherited")).toBeInTheDocument();
    expect(within(stage).getByText("commands overridden")).toBeInTheDocument();
    expect(within(stage).getByLabelText("Agent")).toHaveValue("auto");
    // Only what Go lets a use override is offered.
    expect(within(stage).queryByLabelText("Kind")).toBeNull();
    expect(within(stage).queryByLabelText("Runtime")).toBeNull();
    // The panel says where the shared stage is used.
    expect(
      screen.getByRole("listitem", { name: "repository stage fix" }),
    ).toHaveTextContent("Used by bugfix.");

    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(bridge.saveRules).toHaveBeenCalledTimes(1));
    const [, saved] = bridge.saveRules.mock.calls[0] as [string, RulesDocument];
    expect(saved.version).toBe(2);
    expect(saved.stages).toEqual(shared.stages);
    expect(saved.rules[0].then.stages[0]).toEqual(use);
  });

  it("previews a flow as Go expands it", async () => {
    bridge.expandRules.mockResolvedValue({
      ...info.document,
      rules: [
        {
          ...info.document.rules[0],
          then: {
            stages: [
              {
                id: "verify",
                kind: "evaluation",
                from: {
                  stage: "verify",
                  groups: ["verify-and-approve"],
                  scope: "builtin",
                },
              },
            ],
          },
        },
      ],
    });
    renderRules();
    const flow = await screen.findByRole("group", { name: "Stages of bugfix" });
    fireEvent.click(
      within(flow).getByRole("button", { name: "Preview as planned" }),
    );
    expect(
      await within(flow).findByRole("region", {
        name: "Stages of bugfix as planned",
      }),
    ).toHaveTextContent(
      "from shared stage verify in group verify-and-approve, built-in",
    );
  });

  it("drops a preview once the rules change, and ignores one asked for before", async () => {
    const expanded = (id: string): RulesDocument => ({
      ...info.document,
      rules: [
        {
          ...info.document.rules[0],
          then: { stages: [{ id, kind: "approval" }] },
        },
      ],
    });
    let finish: (document: RulesDocument) => void = () => {};
    bridge.expandRules
      .mockResolvedValueOnce(expanded("first"))
      .mockImplementationOnce(
        () =>
          new Promise<RulesDocument>((resolve) => {
            finish = resolve;
          }),
      )
      .mockResolvedValueOnce(expanded("current"));
    renderRules();
    const flow = await screen.findByRole("group", { name: "Stages of bugfix" });
    const region = { name: "Stages of bugfix as planned" };
    const preview = () =>
      within(flow).getByRole("button", { name: "Preview as planned" });

    fireEvent.click(preview());
    expect(await within(flow).findByRole("region", region)).toHaveTextContent(
      "first",
    );
    // Any edit makes the shown preview out of date.
    fireEvent.change(within(flow).getAllByLabelText("Title (optional)")[0], {
      target: { value: "Fix it" },
    });
    expect(within(flow).queryByRole("region", region)).toBeNull();

    // A preview asked for, then an edit, then one for the edited rules: the
    // first answer arrives last and is not shown over the current one.
    fireEvent.click(preview());
    fireEvent.change(within(flow).getAllByLabelText("Title (optional)")[0], {
      target: { value: "Fix it now" },
    });
    fireEvent.click(preview());
    expect(await within(flow).findByRole("region", region)).toHaveTextContent(
      "current",
    );
    await act(async () => finish(expanded("stale")));
    expect(within(flow).getByRole("region", region)).toHaveTextContent(
      "current",
    );
    expect(within(flow).getByRole("region", region)).not.toHaveTextContent(
      "stale",
    );
  });

  it("opens a new shared stage and keeps every edit made before Go lists it again", async () => {
    // Go lists the draft's own definitions with the rules file as their file.
    bridge.getStageLibrary.mockImplementation(
      async (_projectID: string, document: RulesDocument) => ({
        definitions: Object.entries(document.stages ?? {}).map(
          ([name, stage]) => ({
            name,
            scope: "repository",
            file: info.path,
            kind: "stage",
            stage,
          }),
        ),
        problems: [],
        overridable: [],
      }),
    );
    renderRules();
    fireEvent.change(
      await screen.findByLabelText("New shared definition name"),
      { target: { value: "lint" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "+ New shared stage" }));
    const row = await screen.findByRole("listitem", {
      name: "repository stage lint",
    });
    expect(
      within(row).getByRole("button", { name: "Close repository stage lint" }),
    ).toHaveAttribute("aria-expanded", "true");
    // Two edits in a row, before the listing catches up with either.
    fireEvent.change(within(row).getByLabelText("Kind of lint"), {
      target: { value: "evaluation" },
    });
    fireEvent.change(within(row).getByLabelText("Commands for lint"), {
      target: { value: "task lint" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(bridge.saveRules).toHaveBeenCalledTimes(1));
    const [, saved] = bridge.saveRules.mock.calls[0] as [string, RulesDocument];
    expect(saved.stages).toEqual({
      lint: { id: "", kind: "evaluation", commands: [["task", "lint"]] },
    });
  });

  it("says when the stage library cannot be loaded", async () => {
    bridge.getStageLibrary.mockRejectedValue(new Error("read library: denied"));
    renderRules();
    const panel = await screen.findByRole("region", {
      name: "Shared stages and groups",
    });
    expect(
      await within(panel).findByText(
        /could not be loaded: Error: read library: denied/,
      ),
    ).toBeInTheDocument();
    expect(within(panel).queryByText("Loading the stage library…")).toBeNull();
  });

  it("does not show another project's library while this one's loads", async () => {
    const other: Project = { ...project, id: "project-2", name: "Kiln" };
    bridge.getStageLibrary.mockImplementation(async (projectID: string) => ({
      definitions:
        projectID === "project-1"
          ? [
              {
                name: "bakery-only",
                scope: "repository",
                file: info.path,
                kind: "stage",
                stage: { id: "", kind: "approval" },
              },
            ]
          : [],
      problems: [],
      overridable: [],
    }));
    const { rerender } = render(
      <RulesView
        projects={[project, other]}
        selectedProject={project}
        onProjectChange={() => {}}
      />,
    );
    await screen.findByRole("listitem", {
      name: "repository stage bakery-only",
    });
    rerender(
      <RulesView
        projects={[project, other]}
        selectedProject={other}
        onProjectChange={() => {}}
      />,
    );
    // As soon as Kiln's rules show, before its library is asked for.
    await waitFor(() =>
      expect(bridge.getRules).toHaveBeenCalledWith("project-2"),
    );
    await screen.findByRole("region", { name: "Shared stages and groups" });
    expect(
      screen.queryByRole("listitem", { name: "repository stage bakery-only" }),
    ).toBeNull();
    expect(screen.getByText("Loading the stage library…")).toBeInTheDocument();
  });

  it("drops a rerun's changes on Discard, saving nothing", async () => {
    renderRules();
    await screen.findByRole("region", { name: "Changes from a rerun" });
    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(
      screen.queryByRole("region", { name: "Changes from a rerun" }),
    ).toBeNull();
    expect(screen.queryByText("unsaved changes")).toBeNull();
    expect(bridge.saveRules).not.toHaveBeenCalled();
  });

  describe("adding from the catalog", () => {
    const catalog: CatalogListing = {
      items: [
        {
          item: {
            name: "security-sensitive",
            kind: "rule",
            description: "Security-sensitive changes get a security review.",
            needs: {
              agents: ["security-reviewer"],
              stages: ["security-review"],
            },
            path: "rules/security-sensitive.yaml",
            content: "",
          },
        },
        {
          item: {
            name: "security-review",
            kind: "stage",
            description: "Reviews a change for security problems.",
            needs: { agents: ["security-reviewer"] },
            path: "stages/security-review.yaml",
            content: "",
          },
        },
      ],
    };
    const added: CatalogResult = {
      item: "security-sensitive",
      added: [
        {
          kind: "rule",
          name: "security-sensitive",
          path: "/src/bakery/.inkuna/rules.yaml",
        },
        {
          kind: "agent",
          name: "security-reviewer",
          path: "/src/bakery/.inkuna/agents/security-reviewer.md",
        },
      ],
      notes: [
        'Rule "security-sensitive" is first in /src/bakery/.inkuna/rules.yaml, so it is tried before the rules already there; move it to change that.',
      ],
    };
    beforeEach(() => {
      bridge.listCatalog.mockResolvedValue(catalog);
      bridge.previewCatalogItem.mockResolvedValue(added);
      bridge.addCatalogItem.mockResolvedValue(added);
    });

    it("adds a rule to the repository, then reads the rules and agents again", async () => {
      render(
        <RulesView
          projects={[project]}
          selectedProject={project}
          onProjectChange={() => {}}
        />,
      );
      fireEvent.click(
        await screen.findByRole("button", { name: "Add rule from catalog…" }),
      );
      const dialog = await screen.findByRole("dialog");
      // Only rules are offered here, and never globally.
      expect(
        within(dialog).queryByRole("listitem", { name: "security-review" }),
      ).toBeNull();
      expect(
        within(dialog).queryByRole("button", { name: /globally/ }),
      ).toBeNull();
      bridge.getRules.mockResolvedValue({
        ...info,
        document: {
          ...info.document,
          rules: [
            {
              name: "security-sensitive",
              when: { kind: ["implementation"] },
              then: { stages: stages() },
            },
            ...info.document.rules,
          ],
        },
      });
      fireEvent.click(
        within(dialog).getByRole("button", {
          name: "Add security-sensitive to the repository",
        }),
      );
      fireEvent.click(
        await within(dialog).findByRole("button", {
          name: "Add security-sensitive",
        }),
      );
      const notice = await screen.findByRole("status");
      expect(notice).toHaveTextContent(
        "Added security-sensitive from the catalog",
      );
      expect(notice).toHaveTextContent('Rule "security-sensitive" is first');
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(
        await screen.findByRole("heading", { name: "security-sensitive" }),
      ).toBeInTheDocument();
      expect(bridge.getRules).toHaveBeenCalledTimes(2);
      await waitFor(() => expect(bridge.listAgents).toHaveBeenCalledTimes(2));
      expect(bridge.saveRules).not.toHaveBeenCalled();
    });

    it("adds no rule to the repository while the rules have unsaved changes", async () => {
      renderRules();
      await screen.findByRole("region", { name: "Changes from a rerun" });
      fireEvent.click(
        screen.getByRole("button", { name: "Add rule from catalog…" }),
      );
      const dialog = await screen.findByRole("dialog");
      expect(
        await within(dialog).findByRole("button", {
          name: "Add security-sensitive to the repository",
        }),
      ).toBeDisabled();
      expect(within(dialog).getByRole("status")).toHaveTextContent(
        "Save or discard your rule changes first",
      );
    });

    it("offers shared stages and groups from the library panel, globally too", async () => {
      render(
        <RulesView
          projects={[project]}
          selectedProject={project}
          onProjectChange={() => {}}
        />,
      );
      const panel = await screen.findByRole("region", {
        name: "Shared stages and groups",
      });
      fireEvent.click(
        within(panel).getByRole("button", { name: "Add from catalog…" }),
      );
      const dialog = await screen.findByRole("dialog");
      expect(
        await within(dialog).findByRole("listitem", {
          name: "security-review",
        }),
      ).toBeInTheDocument();
      expect(
        within(dialog).queryByRole("listitem", { name: "security-sensitive" }),
      ).toBeNull();
      expect(
        within(dialog).getByRole("button", {
          name: "Add security-review globally",
        }),
      ).toBeEnabled();
    });

    it("lists the agents again after a global add, which can bring global agents, without reading the rules again", async () => {
      const global: CatalogResult = {
        item: "security-review",
        added: [
          { kind: "stage", name: "security-review", path: "/g/stages.yaml" },
          { kind: "agent", name: "security-reviewer", path: "/g/a.md" },
        ],
      };
      bridge.previewCatalogItem.mockResolvedValue(global);
      bridge.addCatalogItem.mockResolvedValue(global);
      render(
        <RulesView
          projects={[project]}
          selectedProject={project}
          onProjectChange={() => {}}
        />,
      );
      const panel = await screen.findByRole("region", {
        name: "Shared stages and groups",
      });
      await waitFor(() => expect(bridge.listAgents).toHaveBeenCalledTimes(1));
      fireEvent.click(
        within(panel).getByRole("button", { name: "Add from catalog…" }),
      );
      const dialog = await screen.findByRole("dialog");
      fireEvent.click(
        await within(dialog).findByRole("button", {
          name: "Add security-review globally",
        }),
      );
      fireEvent.click(
        await within(dialog).findByRole("button", {
          name: "Add security-review",
        }),
      );
      await screen.findByText("Added security-review from the catalog");
      await waitFor(() => expect(bridge.listAgents).toHaveBeenCalledTimes(2));
      expect(bridge.getRules).toHaveBeenCalledTimes(1);
    });

    /** Adds the catalog rule to project-1, holding Go's reread of its rules
     * until the returned function is called. */
    async function addWithHeldReread() {
      let reread: (value: RulesInfo) => void = () => {};
      const withRule: RulesInfo = {
        ...info,
        document: {
          ...info.document,
          rules: [
            {
              name: "security-sensitive",
              when: { kind: ["implementation"] },
              then: { stages: stages() },
            },
            ...info.document.rules,
          ],
        },
      };
      fireEvent.click(
        await screen.findByRole("button", { name: "Add rule from catalog…" }),
      );
      const dialog = await screen.findByRole("dialog");
      bridge.getRules.mockImplementation((projectID: string) =>
        projectID === "project-1"
          ? new Promise<RulesInfo>((resolve) => {
              reread = resolve;
            })
          : Promise.resolve(info),
      );
      fireEvent.click(
        await within(dialog).findByRole("button", {
          name: "Add security-sensitive to the repository",
        }),
      );
      fireEvent.click(
        await within(dialog).findByRole("button", {
          name: "Add security-sensitive",
        }),
      );
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      return () => act(async () => reread(withRule));
    }

    it("does not load one project's rules into another chosen while they were read again", async () => {
      const other: Project = { ...project, id: "project-2", name: "Kiln" };
      const { rerender } = render(
        <RulesView
          projects={[project, other]}
          selectedProject={project}
          onProjectChange={() => {}}
        />,
      );
      const finish = await addWithHeldReread();
      rerender(
        <RulesView
          projects={[project, other]}
          selectedProject={other}
          onProjectChange={() => {}}
        />,
      );
      await waitFor(() =>
        expect(bridge.getRules).toHaveBeenCalledWith("project-2"),
      );
      await screen.findByRole("button", { name: "Add rule from catalog…" });
      await finish();
      expect(
        screen.queryByRole("heading", { name: "security-sensitive" }),
      ).toBeNull();
      expect(screen.queryByRole("alert")).toBeNull();
    });

    it("keeps edits made while the rules were read again, and Discard shows the rules the add wrote and clears the message", async () => {
      render(
        <RulesView
          projects={[project]}
          selectedProject={project}
          onProjectChange={() => {}}
        />,
      );
      const finish = await addWithHeldReread();
      fireEvent.click(screen.getByRole("button", { name: "+ Add rule" }));
      expect(screen.getByText("unsaved changes")).toBeInTheDocument();
      await finish();
      expect(screen.getByText("unsaved changes")).toBeInTheDocument();
      expect(
        screen.queryByRole("heading", { name: "security-sensitive" }),
      ).toBeNull();
      expect(
        screen.getByText(/the rules were edited while they were read again/),
      ).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Discard" }));
      expect(
        screen.getByRole("heading", { name: "security-sensitive" }),
      ).toBeInTheDocument();
      // The message was about the discarded edits, so it goes with them.
      expect(
        screen.queryByText(/the rules were edited while they were read again/),
      ).toBeNull();
      expect(screen.queryByText("unsaved changes")).toBeNull();
      expect(bridge.saveRules).not.toHaveBeenCalled();
    });
  });
});
