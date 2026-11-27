import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import type {
  DocContent,
  DocEntry,
  DocGraph,
  DocsInfo,
  Project,
} from "../types";
import { DocsView } from "./DocsView";

const mermaid = vi.hoisted(() => ({
  initialize: vi.fn(),
  render: vi.fn(async (id: string, source: string) => ({
    svg: `<svg id="${id}" data-testid="drawn-diagram"><text>${source.trim().split("\n")[0]}</text></svg>`,
  })),
}));
vi.mock("mermaid", () => ({ default: mermaid }));

const project: Project = {
  id: "project-1",
  name: "Bakery",
  repository: "/src/bakery",
  defaultRuntime: "claude",
  createdAt: "2026-01-01T00:00:00Z",
};

const handbook: Project = {
  ...project,
  id: "project-2",
  name: "Handbook",
  repository: "/src/handbook",
};

const entry = (path: string, extra: Partial<DocEntry> = {}): DocEntry => ({
  path,
  title: "",
  lastUpdated: "",
  author: "",
  tags: [],
  status: "",
  ...extra,
});

const readme = entry("README.md", {
  title: "Bakery handbook",
  status: "current",
  tags: ["intro"],
  lastUpdated: "2026-10-17",
  author: "Brandon Manning",
});
const setup = entry("guides/setup.md", {
  title: "Setup guide",
  status: "draft",
  tags: ["setup"],
});
const todo = entry("guides/todo.md", { problems: ["missing frontmatter"] });

const info: DocsInfo = {
  path: "/src/bakery/docs",
  tree: {
    name: "",
    path: "",
    docs: [readme],
    folders: [
      { name: "guides", path: "guides", docs: [setup, todo], folders: [] },
    ],
  },
};

const contents: Record<string, DocContent> = {
  "README.md": {
    doc: readme,
    markdown: [
      "# Welcome",
      "",
      "Read the [setup guide](guides/setup.md#install), the [Go code](../internal/app.go), or [the site](https://example.com/bakery).",
      "",
      "| Stage | Owner |",
      "| --- | --- |",
      "| plan | orchestrator |",
      "",
      "<b>raw html</b>",
      "",
      "```mermaid",
      "flowchart LR",
      "  A --> B",
      "```",
      "",
      "```go",
      "fmt.Println(1)",
      "```",
    ].join("\n"),
  },
  "guides/setup.md": {
    doc: setup,
    markdown: [
      "## Install",
      "",
      "From a release[^1].",
      "",
      "## Install",
      "",
      "From source. See the [second install](#install-1).",
      "",
      "Back to the [handbook](../README.md).",
      "",
      "[^1]: Releases are signed.",
    ].join("\n"),
  },
  "guides/todo.md": { doc: todo, markdown: "# Todo\n" },
  "style.md": {
    doc: entry("style.md", { title: "Style", status: "current" }),
    markdown: "# Style\n",
  },
};

const projectGraph: DocGraph = {
  nodes: [
    {
      id: "doc:project-1:README.md",
      kind: "doc",
      label: "Bakery handbook",
      project: "project-1",
      path: "README.md",
      status: "current",
    },
    {
      id: "doc:project-1:guides/setup.md",
      kind: "doc",
      label: "Setup guide",
      project: "project-1",
      path: "guides/setup.md",
      status: "draft",
    },
    {
      id: "doc:project-1:guides/todo.md",
      kind: "doc",
      label: "todo.md",
      project: "project-1",
      path: "guides/todo.md",
      problems: 1,
    },
    {
      id: "doc:project-2:style.md",
      kind: "doc",
      label: "Style",
      project: "project-2",
      path: "style.md",
      status: "current",
    },
    {
      id: "external:github.com/other/lib",
      kind: "external",
      label: "other/lib",
      url: "https://github.com/other/lib",
    },
  ],
  edges: [
    {
      from: "doc:project-1:README.md",
      to: "doc:project-1:guides/setup.md",
      count: 1,
    },
    {
      from: "doc:project-1:README.md",
      to: "external:github.com/other/lib",
      count: 1,
    },
    {
      from: "doc:project-1:guides/setup.md",
      to: "doc:project-1:README.md",
      count: 1,
    },
    {
      from: "doc:project-2:style.md",
      to: "doc:project-1:guides/setup.md",
      count: 2,
    },
  ],
  warnings: ["Gone: repository: lstat /src/gone: no such file or directory"],
};

const allGraph: DocGraph = {
  nodes: [
    ...projectGraph.nodes,
    {
      id: "doc:project-2:other.md",
      kind: "doc",
      label: "Other",
      project: "project-2",
      path: "other.md",
    },
  ],
  edges: projectGraph.edges,
};

function renderDocs() {
  return render(
    <DocsView
      projects={[project, handbook]}
      selectedProject={project}
      onProjectChange={() => {}}
    />,
  );
}

describe("DocsView", () => {
  const read = vi.fn(async (_projectID: string, path: string) => {
    const content = contents[path];
    if (!content) throw new Error(`read doc: doc ${path} does not exist`);
    return content;
  });

  const graph = vi.fn(async (_projectID: string, scope: string) =>
    scope === "all" ? allGraph : projectGraph,
  );
  const docReads = vi.fn(async (_projectID: string, path: string) =>
    path === "README.md"
      ? [
          {
            taskId: "task-200",
            stepId: "research",
            runId: "run-2",
            projectId: "project-1",
            agent: "researcher",
            runtime: "claude",
            docsDir: "docs",
            docPath: "README.md",
            tool: "Read",
            openedAt: "2026-10-18T09:30:00Z",
          },
          {
            taskId: "task-100",
            stepId: "implement",
            runId: "run-1",
            projectId: "project-1",
            agent: "implementer",
            runtime: "codex",
            docsDir: "docs",
            docPath: "README.md",
            tool: "shell:cat",
            openedAt: "2026-10-17T09:00:00Z",
          },
        ]
      : [],
  );

  beforeEach(() => {
    read.mockClear();
    graph.mockClear();
    mermaid.initialize.mockClear();
    mermaid.render.mockClear();
    window.go = {
      app: {
        Service: {
          Docs: async () => info,
          DocContent: read,
          DocGraph: graph,
          DocReads: docReads,
        },
      },
    } as unknown as Window["go"];
  });

  afterEach(() => {
    // Unmount before the bindings go and the mocks restore, so an effect
    // still pending when a test ends never calls either.
    cleanup();
    delete window.go;
    vi.restoreAllMocks();
  });

  it("shows the docs folder as a tree with statuses and frontmatter problems", async () => {
    renderDocs();
    const tree = await screen.findByRole("list", { name: "Docs" });
    const folder = within(tree).getByRole("button", { name: "guides/" });
    expect(folder).toHaveAttribute("aria-expanded", "true");
    expect(
      within(tree).getByRole("button", { name: /README\.md/ }),
    ).toHaveTextContent("current");
    const flagged = within(tree).getByRole("button", { name: /todo\.md/ });
    expect(
      within(flagged).getByRole("img", {
        name: "Problems: missing frontmatter",
      }),
    ).toBeInTheDocument();

    fireEvent.click(folder);
    expect(folder).toHaveAttribute("aria-expanded", "false");
    expect(
      within(tree).queryByRole("button", { name: /setup\.md/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(folder);

    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "problems" },
    });
    expect(
      within(tree)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["▾guides/", "todo.md⚠"]);
    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "" },
    });
    fireEvent.change(screen.getByLabelText("Tag"), {
      target: { value: "intro" },
    });
    expect(
      within(screen.getByRole("list", { name: "Docs" }))
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["README.mdcurrent"]);
  });

  it("renders a doc's frontmatter, GitHub Markdown, and Mermaid diagrams without raw HTML", async () => {
    renderDocs();
    fireEvent.click(await screen.findByRole("button", { name: /README\.md/ }));
    const article = await screen.findByRole("article", {
      name: "Bakery handbook",
    });
    expect(
      await within(article).findByRole("heading", { name: "Welcome" }),
    ).toHaveAttribute("id", "user-content-welcome");
    expect(article).toHaveTextContent(
      "Updated 2026-10-17 · approved by Brandon Manning",
    );
    expect(within(article).getByText("intro")).toBeInTheDocument();
    expect(
      within(article).getByRole("cell", { name: "orchestrator" }),
    ).toBeInTheDocument();
    // The tags are dropped and their text stays plain.
    expect(within(article).getByText("raw html").tagName).toBe("P");
    expect(article.querySelector("b")).toBeNull();
    expect(within(article).getByText("fmt.Println(1)")).toBeInTheDocument();

    expect(
      await within(article).findByTestId("drawn-diagram"),
    ).toHaveTextContent("flowchart LR");
    expect(mermaid.initialize).toHaveBeenCalledWith(
      expect.objectContaining({ securityLevel: "strict", startOnLoad: false }),
    );
    expect(mermaid.render).toHaveBeenCalledWith(
      expect.stringMatching(/^doc-diagram-/),
      "flowchart LR\n  A --> B\n",
    );
  });

  it("keeps a drawn diagram when the tree changes around it", async () => {
    renderDocs();
    fireEvent.click(await screen.findByRole("button", { name: /README\.md/ }));
    const diagram = await screen.findByTestId("drawn-diagram");
    fireEvent.click(screen.getByRole("button", { name: "guides/" }));
    fireEvent.change(screen.getByLabelText("Tag"), {
      target: { value: "intro" },
    });
    expect(screen.getByTestId("drawn-diagram")).toBe(diagram);
    expect(mermaid.render).toHaveBeenCalledTimes(1);
  });

  it("follows links to other docs inside the view and sends web links to the browser", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderDocs();
    fireEvent.click(await screen.findByRole("button", { name: /README\.md/ }));
    const article = await screen.findByRole("article", {
      name: "Bakery handbook",
    });

    // A link outside the docs root is not followed.
    expect(
      within(article).queryByRole("link", { name: "Go code" }),
    ).not.toBeInTheDocument();
    expect(within(article).getByText("Go code")).toHaveAttribute(
      "title",
      "../internal/app.go is outside the docs",
    );

    fireEvent.click(within(article).getByRole("link", { name: /the site/ }));
    expect(open).toHaveBeenCalledWith(
      "https://example.com/bakery",
      "_blank",
      "noopener,noreferrer",
    );
    expect(read).toHaveBeenCalledTimes(1);

    fireEvent.click(within(article).getByRole("link", { name: "setup guide" }));
    const guide = await screen.findByRole("article", { name: "Setup guide" });
    expect(read).toHaveBeenLastCalledWith("project-1", "guides/setup.md");
    expect(
      within(screen.getByRole("list", { name: "Docs" })).getByRole("button", {
        name: /setup\.md/,
      }),
    ).toHaveAttribute("aria-current", "page");

    fireEvent.click(
      await within(guide).findByRole("link", { name: "handbook" }),
    );
    await screen.findByRole("article", { name: "Bakery handbook" });
    expect(read).toHaveBeenLastCalledWith("project-1", "README.md");

    fireEvent.click(screen.getByRole("button", { name: "← Back" }));
    await screen.findByRole("article", { name: "Setup guide" });
  });

  it("jumps to repeated headings and between footnotes and their references", async () => {
    const scrolled: string[] = [];
    Element.prototype.scrollIntoView = function (this: Element) {
      scrolled.push(this.id);
    };
    try {
      renderDocs();
      fireEvent.click(await screen.findByRole("button", { name: /setup\.md/ }));
      const guide = await screen.findByRole("article", {
        name: "Setup guide",
      });
      const installs = await within(guide).findAllByRole("heading", {
        name: "Install",
      });
      expect(installs.map((heading) => heading.id)).toEqual([
        "user-content-install",
        "user-content-install-1",
      ]);

      fireEvent.click(
        within(guide).getByRole("link", { name: "second install" }),
      );
      expect(scrolled).toEqual(["user-content-install-1"]);

      const reference = within(guide).getByRole("link", { name: "1" });
      expect(reference).toHaveAttribute("id", "user-content-fnref-1");
      expect(
        document.getElementById(
          reference.getAttribute("aria-describedby") ?? "",
        ),
      ).toHaveTextContent("Footnotes");
      fireEvent.click(reference);
      expect(scrolled.at(-1)).toBe("user-content-fn-1");
      expect(document.getElementById("user-content-fn-1")).toHaveTextContent(
        "Releases are signed.",
      );

      fireEvent.click(
        within(guide).getByRole("link", { name: "Back to reference 1" }),
      );
      expect(scrolled.at(-1)).toBe("user-content-fnref-1");
      expect(read).toHaveBeenCalledTimes(1);
    } finally {
      delete (Element.prototype as Partial<Element>).scrollIntoView;
    }
  });

  it("shows Go's frontmatter problems and its reason when a doc cannot be read", async () => {
    renderDocs();
    fireEvent.click(await screen.findByRole("button", { name: /todo\.md/ }));
    expect(
      await screen.findByRole("note", { name: "Problems" }),
    ).toHaveTextContent("missing frontmatter");

    read.mockRejectedValueOnce(
      new Error(
        "read doc: doc guides/setup.md is reached through a symbolic link",
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: /setup\.md/ }));
    expect(
      await screen.findByText(/reached through a symbolic link/),
    ).toBeInTheDocument();
  });

  it("explains where docs come from when a project has none", async () => {
    window.go = {
      app: {
        Service: {
          Docs: async () => ({
            path: "/src/bakery/docs",
            tree: { name: "", path: "", docs: [], folders: [] },
          }),
          DocContent: read,
          DocGraph: graph,
        },
      },
    } as unknown as Window["go"];
    renderDocs();
    expect(await screen.findByText("No docs yet")).toBeInTheDocument();
    expect(screen.getByText("/src/bakery/docs")).toBeInTheDocument();
  });

  it("draws the doc graph, lists its links as a table, and opens a doc on double-click", async () => {
    renderDocs();
    fireEvent.click(await screen.findByRole("tab", { name: "Graph" }));
    const panel = await screen.findByRole("tabpanel", { name: "Graph" });
    expect(
      await within(panel).findByText("4 docs · 5 links"),
    ).toBeInTheDocument();
    expect(within(panel).getByText(/Gone: repository/)).toBeInTheDocument();
    // The counts come with the graph; the legend and the drawing wait for
    // its layout, which runs in slices after rendering.
    expect(
      within(
        await screen.findByRole("list", { name: "Graph legend" }),
      ).getByText("Handbook"),
    ).toBeInTheDocument();

    const drawn = screen.getByLabelText("Doc graph");
    for (const label of [
      "Bakery handbook",
      "Setup guide",
      "Style",
      "other/lib",
    ])
      expect(await within(drawn).findByText(label)).toBeInTheDocument();
    // A doc in another project carries its project's name.
    expect(within(drawn).getByText("Handbook")).toBeInTheDocument();

    fireEvent.click(
      within(panel).getByRole("button", { name: /Links as a table/ }),
    );
    const rows = within(
      within(panel).getByRole("table", { name: "Doc links" }),
    ).getAllByRole("row");
    expect(rows.map((row) => row.textContent)).toEqual([
      "FromToLinks",
      "Bakery handbook (Bakery · README.md)Setup guide (Bakery · guides/setup.md)1",
      "Bakery handbook (Bakery · README.md)other/lib on GitHub1",
      "Setup guide (Bakery · guides/setup.md)Bakery handbook (Bakery · README.md)1",
      "Style (Handbook · style.md)Setup guide (Bakery · guides/setup.md)2",
    ]);

    fireEvent.doubleClick(within(drawn).getByText("Setup guide"));
    expect(
      await screen.findByRole("article", { name: "Setup guide" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Browse" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("loads every project's graph only when asked", async () => {
    renderDocs();
    fireEvent.click(await screen.findByRole("tab", { name: "Graph" }));
    await screen.findByText("4 docs · 5 links");
    expect(graph.mock.calls.map((call) => call[1])).toEqual(["project"]);
    fireEvent.click(screen.getByRole("button", { name: "All projects" }));
    expect(await screen.findByText("5 docs · 5 links")).toBeInTheDocument();
    expect(graph).toHaveBeenLastCalledWith("project-1", "all");
    expect(
      screen.getByRole("button", { name: "All projects" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("lists the docs that link to the open one", async () => {
    renderDocs();
    fireEvent.click(await screen.findByRole("button", { name: /setup\.md/ }));
    const linked = await screen.findByRole("region", { name: "Linked from" });
    expect(
      (await within(linked).findAllByRole("listitem")).map(
        (item) => item.textContent,
      ),
    ).toEqual([
      "StyleHandbook · style.md · 2 links",
      "Bakery handbookREADME.md",
    ]);
    fireEvent.click(
      within(linked).getByRole("button", { name: "Bakery handbook" }),
    );
    expect(
      await screen.findByRole("article", { name: "Bakery handbook" }),
    ).toBeInTheDocument();
  });

  it("opens a doc in another project by switching to it, and a GitHub repository in the browser", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    const changed: Array<Project | null> = [];
    const view = render(
      <DocsView
        projects={[project, handbook]}
        selectedProject={project}
        onProjectChange={(next) => changed.push(next)}
      />,
    );
    fireEvent.click(await screen.findByRole("tab", { name: "Graph" }));
    const drawn = await screen.findByLabelText("Doc graph");
    fireEvent.doubleClick(await within(drawn).findByText("other/lib"));
    expect(open).toHaveBeenCalledWith(
      "https://github.com/other/lib",
      "_blank",
      "noopener,noreferrer",
    );
    fireEvent.doubleClick(within(drawn).getByText("Style"));
    expect(changed).toEqual([handbook]);
    view.rerender(
      <DocsView
        projects={[project, handbook]}
        selectedProject={handbook}
        onProjectChange={(next) => changed.push(next)}
      />,
    );
    expect(
      await screen.findByRole("article", { name: "Style" }),
    ).toBeInTheDocument();
    expect(read).toHaveBeenLastCalledWith("project-2", "style.md");
  });

  it("lists which runs read a doc and opens one in the Tasks view", async () => {
    const opened: string[] = [];
    render(
      <DocsView
        projects={[project, handbook]}
        selectedProject={project}
        onProjectChange={() => {}}
        onOpenRun={(runID) => opened.push(runID)}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: /README\.md/ }));
    const readBy = await screen.findByRole("region", { name: "Read by" });
    const entries = await within(readBy).findAllByRole("listitem");
    // Newest first, each with its task, step, agent, and runtime.
    expect(entries).toHaveLength(2);
    expect(entries[0]).toHaveTextContent(
      "task-200research · researcher · claude",
    );
    expect(entries[1]).toHaveTextContent(
      "task-100implement · implementer · codex",
    );
    expect(entries[0].querySelector("time")).toHaveAttribute(
      "dateTime",
      "2026-10-18T09:30:00Z",
    );
    expect(docReads).toHaveBeenLastCalledWith("project-1", "README.md");
    fireEvent.click(within(readBy).getByRole("button", { name: "task-100" }));
    expect(opened).toEqual(["run-1"]);

    fireEvent.click(screen.getByRole("button", { name: /setup\.md/ }));
    expect(
      await within(
        await screen.findByRole("region", { name: "Read by" }),
      ).findByText("No agent has opened this doc in a recorded run."),
    ).toBeInTheDocument();
  });
});
