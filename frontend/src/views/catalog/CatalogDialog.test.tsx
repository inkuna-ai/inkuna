import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import type { CatalogEntry, CatalogListing, CatalogResult } from "../../types";
import { CatalogAddedNotice, CatalogDialog } from "./CatalogDialog";

const bridge = vi.hoisted(() => ({
  listCatalog: vi.fn(),
  previewCatalogItem: vi.fn(),
  addCatalogItem: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const reviewer: CatalogEntry = {
  item: {
    name: "security-reviewer",
    kind: "agent",
    description: "Reviews a change for security problems.",
    needs: {},
    permission: "read-only",
    taskKinds: ["review", "implementation"],
    path: "agents/security-reviewer.md",
    content: "---\nname: security-reviewer\n---\n",
  },
};
const upgrader: CatalogEntry = {
  item: {
    name: "upgrader",
    kind: "agent",
    description: "Upgrades dependencies.",
    needs: {},
    permission: "worktree-write",
    path: "agents/upgrader.md",
    content: "---\nname: upgrader\n---\n",
  },
  repository: "/src/bakery/.inkuna/agents/upgrader.md",
};
const rule: CatalogEntry = {
  item: {
    name: "security-sensitive",
    kind: "rule",
    description: "Security-sensitive changes get a security review.",
    needs: {
      agents: ["security-reviewer"],
      stages: ["profile", "security-review"],
    },
    taskKinds: ["implementation"],
    path: "rules/security-sensitive.yaml",
    content: "version: 2\n",
  },
  globalRefusal:
    '"security-sensitive" is a rule, and rules live in a repository\'s rules file; add it to a repository',
};
const listing: CatalogListing = { items: [reviewer, upgrader, rule] };

function renderDialog(
  props: Partial<React.ComponentProps<typeof CatalogDialog>> = {},
) {
  const onAdded = vi.fn();
  render(
    <CatalogDialog
      title="Add an agent from the catalog"
      kinds={["agent"]}
      projectID="project-1"
      allowGlobal
      onClose={() => {}}
      onAdded={onAdded}
      {...props}
    />,
  );
  return onAdded;
}

describe("CatalogDialog", () => {
  beforeEach(() => {
    bridge.listCatalog.mockResolvedValue(listing);
  });
  afterEach(() => {
    cleanup();
    for (const mock of Object.values(bridge)) mock.mockReset();
  });

  it("lists the catalog's items of its kinds with their description, permission, and task kinds", async () => {
    renderDialog();
    const item = await screen.findByRole("listitem", {
      name: "security-reviewer",
    });
    expect(bridge.listCatalog).toHaveBeenCalledWith("project-1");
    expect(item).toHaveTextContent("Reviews a change for security problems.");
    expect(item).toHaveTextContent("read-only");
    expect(item).toHaveTextContent("review, implementation");
    // A rule is not an agent, so the Agents view does not list it.
    expect(
      screen.queryByRole("listitem", { name: "security-sensitive" }),
    ).not.toBeInTheDocument();
  });

  it("says when the repository already has an item, rather than offering to add it again", async () => {
    renderDialog();
    const item = await screen.findByRole("listitem", { name: "upgrader" });
    expect(item).toHaveTextContent(
      "Already in this repository: /src/bakery/.inkuna/agents/upgrader.md",
    );
    expect(
      within(item).queryByRole("button", {
        name: "Add upgrader to the repository",
      }),
    ).not.toBeInTheDocument();
    expect(
      within(item).getByRole("button", { name: "Add upgrader globally" }),
    ).toBeEnabled();
  });

  it("adds an agent to the repository after Go's preview", async () => {
    const result: CatalogResult = {
      item: "security-reviewer",
      added: [
        {
          kind: "agent",
          name: "security-reviewer",
          path: "/src/bakery/.inkuna/agents/security-reviewer.md",
        },
      ],
    };
    bridge.previewCatalogItem.mockResolvedValue(result);
    bridge.addCatalogItem.mockResolvedValue(result);
    const onAdded = renderDialog();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    );
    expect(
      await screen.findByText(
        "Nothing else: everything it uses is built in or already there.",
      ),
    ).toBeInTheDocument();
    expect(bridge.previewCatalogItem).toHaveBeenCalledWith(
      "project-1",
      "security-reviewer",
      "",
      false,
    );
    expect(bridge.addCatalogItem).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "Add security-reviewer" }),
    );
    await vi.waitFor(() => expect(onAdded).toHaveBeenCalledWith(result, false));
    expect(bridge.addCatalogItem).toHaveBeenCalledWith(
      "project-1",
      "security-reviewer",
      "",
      false,
    );
  });

  it("adds an agent globally, and offers only that without a project", async () => {
    const result: CatalogResult = {
      item: "security-reviewer",
      added: [
        {
          kind: "agent",
          name: "security-reviewer",
          path: "/home/me/.config/inkuna/agents/security-reviewer.md",
        },
      ],
    };
    bridge.previewCatalogItem.mockResolvedValue(result);
    bridge.addCatalogItem.mockResolvedValue(result);
    const onAdded = renderDialog({ projectID: "" });
    const item = await screen.findByRole("listitem", {
      name: "security-reviewer",
    });
    expect(
      within(item).queryByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      within(item).getByRole("button", {
        name: "Add security-reviewer globally",
      }),
    );
    expect(
      await screen.findByText(
        "/home/me/.config/inkuna/agents/security-reviewer.md",
      ),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Add security-reviewer" }),
    );
    await vi.waitFor(() => expect(onAdded).toHaveBeenCalledWith(result, true));
    expect(bridge.addCatalogItem).toHaveBeenCalledWith(
      "",
      "security-reviewer",
      "",
      true,
    );
  });

  it("lists what comes with a rule, and says when the built-in rules are written with it", async () => {
    bridge.previewCatalogItem.mockResolvedValue({
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
        {
          kind: "stage",
          name: "security-review",
          path: "/src/bakery/.inkuna/rules.yaml",
        },
      ],
      notes: [
        '/src/bakery/.inkuna/rules.yaml does not exist yet, so it will be written with the built-in rules after "security-sensitive", and the repository keeps the flows it has.',
      ],
    } satisfies CatalogResult);
    renderDialog({
      title: "Add a rule from the catalog",
      kinds: ["rule"],
      allowGlobal: false,
    });
    const item = await screen.findByRole("listitem", {
      name: "security-sensitive",
    });
    expect(item).toHaveTextContent(
      "Usesagent security-reviewer, profile, security-review",
    );
    // A rule lives in a repository's rules file, so it is never global.
    expect(
      within(item).queryByRole("button", { name: /globally/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      within(item).getByRole("button", {
        name: "Add security-sensitive to the repository",
      }),
    );
    const brought = await screen.findByRole("list", { name: "Comes with it" });
    expect(within(brought).getAllByRole("listitem")).toHaveLength(2);
    expect(brought).toHaveTextContent(
      "agent security-reviewer, to /src/bakery/.inkuna/agents/security-reviewer.md",
    );
    expect(brought).toHaveTextContent(
      "shared stage security-review, to /src/bakery/.inkuna/rules.yaml",
    );
    expect(
      screen.getByRole("note", { name: "What else changes" }),
    ).toHaveTextContent("will be written with the built-in rules");
  });

  it("shows Go's reason when it refuses an add, and adds nothing", async () => {
    bridge.previewCatalogItem.mockRejectedValue(
      'project.inkuna/agents/security-reviewer.md already has agent "security-reviewer"; adding never overwrites, so rename or remove that one first',
    );
    renderDialog();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      'Go refused the add: project.inkuna/agents/security-reviewer.md already has agent "security-reviewer"',
    );
    expect(
      screen.getByRole("button", { name: "Add security-reviewer" }),
    ).toBeDisabled();
    expect(bridge.addCatalogItem).not.toHaveBeenCalled();
  });

  it("shows Go's reason when the add itself is refused after the preview", async () => {
    bridge.previewCatalogItem.mockResolvedValue({
      item: "security-reviewer",
      added: [{ kind: "agent", name: "security-reviewer", path: "/a.md" }],
    });
    bridge.addCatalogItem.mockRejectedValue(
      "/src/bakery/.inkuna/rules.yaml changed while the add was being made, so nothing was added; add it again",
    );
    const onAdded = renderDialog();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Add security-reviewer" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "changed while the add was being made",
    );
    expect(onAdded).not.toHaveBeenCalled();
  });

  it("stays open while Go writes an add, so Escape cannot leave it half done", async () => {
    const result: CatalogResult = {
      item: "security-reviewer",
      added: [{ kind: "agent", name: "security-reviewer", path: "/a.md" }],
    };
    bridge.previewCatalogItem.mockResolvedValue(result);
    let finish: (value: CatalogResult) => void = () => {};
    bridge.addCatalogItem.mockReturnValue(
      new Promise<CatalogResult>((resolve) => {
        finish = resolve;
      }),
    );
    const onClose = vi.fn();
    const onAdded = renderDialog({ onClose });
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Add security-reviewer" }),
    );
    expect(
      await screen.findByRole("button", { name: "Adding…" }),
    ).toBeDisabled();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();
    finish(result);
    await vi.waitFor(() => expect(onAdded).toHaveBeenCalledWith(result, false));
    // Once the add settles, Escape closes it again.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("refuses repository adds while the view says it cannot take one, saying why", async () => {
    renderDialog({
      repositoryBlocked: "Save or discard your rule changes first.",
    });
    expect(
      await screen.findByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    ).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Save or discard your rule changes first.",
    );
  });

  it("says why the repository cannot be added to when its rules do not load", async () => {
    bridge.listCatalog.mockResolvedValue({
      ...listing,
      repositoryProblem:
        "rules.yaml does not load, so nothing is added until it is fixed",
    });
    renderDialog();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Nothing can be added to this repository until this is fixed: rules.yaml does not load",
    );
    expect(
      screen.getByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    ).toBeDisabled();
  });
});

describe("CatalogAddedNotice", () => {
  it("names each file the add wrote, and Go's notes", () => {
    render(
      <CatalogAddedNotice
        result={{
          item: "security-sensitive",
          added: [
            { kind: "rule", name: "security-sensitive", path: "/r/rules.yaml" },
            { kind: "agent", name: "security-reviewer", path: "/r/a.md" },
          ],
          notes: ['Rule "security-sensitive" is first in /r/rules.yaml.'],
        }}
      />,
    );
    const notice = screen.getByRole("status");
    expect(notice).toHaveTextContent(
      "Added security-sensitive from the catalog",
    );
    expect(notice).toHaveTextContent("agent security-reviewer in /r/a.md");
    expect(notice).toHaveTextContent('Rule "security-sensitive" is first');
    cleanup();
  });
});

describe("CatalogDialog with definition sources", () => {
  const shared: CatalogEntry = {
    item: {
      name: "api-reviewer",
      kind: "agent",
      description: "Reviews APIs.",
      needs: { skills: ["api-style"] },
      permission: "read-only",
      path: "agents/api-reviewer.md",
      content: "---\nname: api-reviewer\n---\n",
      source: "acme/source",
      scope: "team:platform",
    },
    globalRefusal:
      '"api-reviewer" comes from definition source acme/source, which shares it for this project; add it to the repository',
  };
  afterEach(() => {
    cleanup();
    for (const mock of Object.values(bridge)) mock.mockReset();
  });

  it("names a source's item by its source and scope, and adds it from that source", async () => {
    bridge.listCatalog.mockResolvedValue({
      items: [reviewer, shared],
      sources: [
        {
          source: "acme/source",
          version: "v1",
          definitions: [
            {
              kind: "agent",
              name: "api-reviewer",
              mode: "catalog",
              status: "offered",
            },
          ],
        },
      ],
    } satisfies CatalogListing);
    const result: CatalogResult = {
      item: "api-reviewer",
      added: [
        {
          kind: "agent",
          name: "api-reviewer",
          path: "/r/.inkuna/agents/api-reviewer.md",
        },
        {
          kind: "skill",
          name: "api-style",
          path: "/r/.inkuna/skills/api-style",
        },
      ],
    };
    bridge.previewCatalogItem.mockResolvedValue(result);
    bridge.addCatalogItem.mockResolvedValue(result);
    const onAdded = renderDialog();
    const item = await screen.findByRole("listitem", { name: "api-reviewer" });
    expect(item).toHaveTextContent(
      "from acme/source, shared with team:platform",
    );
    expect(item).toHaveTextContent("skill api-style");
    // Go says it cannot be added globally, so the view does not offer it.
    expect(
      within(item).queryByRole("button", { name: /globally/ }),
    ).not.toBeInTheDocument();
    // Nothing is withheld, so no source is called out.
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    fireEvent.click(
      within(item).getByRole("button", {
        name: "Add api-reviewer to the repository",
      }),
    );
    expect(
      await screen.findByText("/r/.inkuna/skills/api-style"),
    ).toBeInTheDocument();
    expect(bridge.previewCatalogItem).toHaveBeenCalledWith(
      "project-1",
      "api-reviewer",
      "acme/source",
      false,
    );
    fireEvent.click(screen.getByRole("button", { name: "Add api-reviewer" }));
    await vi.waitFor(() => expect(onAdded).toHaveBeenCalledWith(result, false));
    expect(bridge.addCatalogItem).toHaveBeenCalledWith(
      "project-1",
      "api-reviewer",
      "acme/source",
      false,
    );
  });

  it("shows Go's reason for each definition it does not offer, and when a source could not be reached", async () => {
    bridge.listCatalog.mockResolvedValue({
      items: [reviewer],
      sources: [
        {
          source: "acme/source",
          fetchedAt: "2026-11-20T09:00:00Z",
          cached: true,
          unreachable: "the definitions server is down",
          definitions: [
            {
              kind: "rule",
              name: "release",
              mode: "shared",
              scope: "team:platform",
              status: "not applied",
              reason:
                "this build applies only catalog definitions, so it is ignored",
            },
          ],
        },
        {
          source: "acme/source-2",
          definitions: [
            {
              kind: "rule",
              name: "audit",
              mode: "enforced",
              status: "refuses",
              reason:
                "this build does not apply enforced definitions, so plans in this project are refused",
            },
          ],
        },
      ],
    } satisfies CatalogListing);
    renderDialog();
    const note = await screen.findByRole("note");
    expect(note).toHaveTextContent("Definition source acme/source");
    expect(note).toHaveTextContent(
      "It could not be reached (the definitions server is down), so this is its snapshot from 2026-11-20T09:00:00Z.",
    );
    expect(note).toHaveTextContent(
      "rule release (shared, team:platform): this build applies only catalog definitions, so it is ignored",
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(
      "Definition source acme/source-2 shares what this build cannot apply, so plans in this project are refused.",
    );
    expect(alert).toHaveTextContent("rule audit (enforced)");
  });
});
