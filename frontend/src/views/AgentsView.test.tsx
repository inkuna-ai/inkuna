import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { AgentInfo, CatalogResult, Project } from "../types";
import { AgentsView } from "./AgentsView";

const bridge = vi.hoisted(() => ({
  addCatalogItem: vi.fn(),
  deleteAgent: vi.fn(),
  deleteRemoteAgent: vi.fn(),
  getAgentLocations: vi.fn(),
  getConfiguration: vi.fn(),
  listAgents: vi.fn(),
  listCatalog: vi.fn(),
  listRemoteAgents: vi.fn(),
  listRuntimes: vi.fn(),
  parseAgent: vi.fn(),
  previewCatalogItem: vi.fn(),
  previewRemoteAgent: vi.fn(),
  repinRemoteAgent: vi.fn(),
  saveAgent: vi.fn(),
  saveRemoteAgent: vi.fn(),
  setRemoteAgentEnabled: vi.fn(),
}));
vi.mock("../bridge", () => bridge);

const project: Project = {
  id: "project-1",
  name: "Bakery",
  repository: "/src/bakery",
  defaultRuntime: "claude",
  createdAt: "2026-01-01T00:00:00Z",
};

const agent = (name: string, source: string): AgentInfo =>
  ({
    definition: {
      name,
      description: `The ${name}.`,
      permission: "read-only",
      instructions: "",
      source,
    },
    card: {},
  }) as unknown as AgentInfo;

const added: CatalogResult = {
  item: "security-reviewer",
  added: [
    {
      kind: "agent",
      name: "security-reviewer",
      path: "/home/me/.config/inkuna/agents/security-reviewer.md",
    },
  ],
};

describe("AgentsView", () => {
  beforeEach(() => {
    bridge.listAgents.mockResolvedValue([agent("implementer", "builtin")]);
    bridge.getAgentLocations.mockResolvedValue({
      repository: "/src/bakery/.inkuna/agents",
      global: "/home/me/.config/inkuna/agents",
    });
    bridge.getConfiguration.mockResolvedValue({ file: {} });
    bridge.listRemoteAgents.mockResolvedValue([]);
    bridge.listRuntimes.mockResolvedValue([]);
    bridge.listCatalog.mockResolvedValue({
      items: [
        {
          item: {
            name: "security-reviewer",
            kind: "agent",
            description: "Reviews a change for security problems.",
            needs: {},
            permission: "read-only",
            taskKinds: ["review"],
            path: "agents/security-reviewer.md",
            content: "",
          },
        },
      ],
    });
    bridge.previewCatalogItem.mockResolvedValue(added);
    bridge.addCatalogItem.mockResolvedValue(added);
  });
  afterEach(() => {
    cleanup();
    for (const mock of Object.values(bridge)) mock.mockReset();
  });

  it("adds an agent from the catalog globally, and lists it like any other", async () => {
    render(
      <AgentsView
        projects={[project]}
        selectedProject={project}
        onProjectChange={() => {}}
      />,
    );
    await screen.findByRole("article", { name: "implementer" });
    const open = screen.getByRole("button", { name: "Add from catalog…" });
    await waitFor(() => expect(open).toBeEnabled());
    fireEvent.click(open);
    const dialog = await screen.findByRole("dialog");
    expect(bridge.listCatalog).toHaveBeenCalledWith("project-1");
    expect(
      await within(dialog).findByRole("button", {
        name: "Add security-reviewer to the repository",
      }),
    ).toBeEnabled();
    bridge.listAgents.mockResolvedValue([
      agent("implementer", "builtin"),
      agent("security-reviewer", "global"),
    ]);
    fireEvent.click(
      within(dialog).getByRole("button", {
        name: "Add security-reviewer globally",
      }),
    );
    fireEvent.click(
      await within(dialog).findByRole("button", {
        name: "Add security-reviewer",
      }),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Added security-reviewer from the catalog",
    );
    expect(bridge.addCatalogItem).toHaveBeenCalledWith(
      "project-1",
      "security-reviewer",
      "",
      true,
    );
    const listed = await screen.findByRole("article", {
      name: "security-reviewer",
    });
    // From then on it is an ordinary agent, edited like any other.
    expect(
      within(listed).getByRole("button", { name: "Edit security-reviewer" }),
    ).toBeInTheDocument();
  });
});
