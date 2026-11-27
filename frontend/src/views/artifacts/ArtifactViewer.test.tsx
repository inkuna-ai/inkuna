import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { ArtifactInfo } from "../../types";
import { ArtifactLink, ArtifactViewerProvider } from "./ArtifactViewer";

const bridge = vi.hoisted(() => ({
  readArtifact: vi.fn(),
  copyText: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const stored = (fields: Partial<ArtifactInfo>): ArtifactInfo => ({
  digest: "d1",
  taskId: "task-1",
  stepId: "plan",
  kind: "document",
  name: "plan.md",
  mediaType: "text/markdown",
  size: 40,
  createdAt: "2026-10-27T09:00:00Z",
  content: "# Approach\n\n1. Change the parser.\n",
  ...fields,
});

beforeEach(() => {
  bridge.readArtifact.mockReset();
  bridge.copyText.mockReset();
});

describe("ArtifactLink", () => {
  it("says what it opens and opens a Markdown document rendered", async () => {
    bridge.readArtifact.mockResolvedValue(stored({}));
    bridge.copyText.mockResolvedValue(undefined);
    render(
      <ArtifactLink
        target={{
          digest: "d1",
          name: "plan.md",
          kind: "document",
          size: 40,
          round: 2,
        }}
      />,
    );
    const link = screen.getByRole("button", { name: /plan\.md/ });
    expect(link).toHaveTextContent("documentplan.md40 B · round 2");
    // Opened from the keyboard; a click does not focus a button in WebKit.
    link.focus();
    fireEvent.click(link);
    const drawer = await screen.findByRole("dialog", { name: "plan.md" });
    expect(bridge.readArtifact).toHaveBeenCalledWith("d1");
    expect(
      await within(drawer).findByRole("heading", { name: "Approach" }),
    ).toBeInTheDocument();
    expect(within(drawer).queryByText(/# Approach/)).toBeNull();
    expect(drawer).toHaveTextContent(
      "round 2 · plan · document · text/markdown · 40 B",
    );

    fireEvent.click(within(drawer).getByRole("button", { name: "Copy" }));
    await waitFor(() =>
      expect(bridge.copyText).toHaveBeenCalledWith(
        "# Approach\n\n1. Change the parser.\n",
      ),
    );
    expect(
      await within(drawer).findByRole("button", { name: "Copied" }),
    ).toBeInTheDocument();

    // Escape closes the drawer and gives the link its focus back.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(link).toHaveFocus();
  });

  it("shows other text as it is, JSON indented, and no binary at all", async () => {
    bridge.readArtifact.mockResolvedValueOnce(
      stored({
        name: "plan-turn1-handoff.json",
        kind: "handoff",
        mediaType: "application/json",
        content: '{"summary":"Planned"}',
      }),
    );
    render(
      <ArtifactLink
        target={{ digest: "d2", name: "plan-turn1-handoff.json" }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /handoff\.json/ }));
    const drawer = await screen.findByRole("dialog");
    expect(
      await within(drawer).findByText(/"summary": "Planned"/),
    ).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Close" }));

    bridge.readArtifact.mockResolvedValueOnce(
      stored({
        name: "inkuna-out-run-1.tar.gz",
        kind: "archive",
        mediaType: "application/gzip",
        content: undefined,
        binary: true,
      }),
    );
    render(
      <ArtifactLink
        target={{ digest: "d3", name: "inkuna-out-run-1.tar.gz" }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /tar\.gz/ }));
    const binary = await screen.findByRole("dialog");
    expect(
      await within(binary).findByText(/binary, so it is not shown/),
    ).toBeInTheDocument();
    expect(within(binary).queryByRole("button", { name: "Copy" })).toBeNull();
  });

  it("opens one drawer for the app, and the next link replaces it", async () => {
    bridge.readArtifact.mockImplementation(async (digest: string) =>
      stored({ digest, name: `${digest}.md`, content: `# From ${digest}\n` }),
    );
    render(
      <ArtifactViewerProvider>
        <ArtifactLink target={{ digest: "first", name: "first.md" }} />
        <ArtifactLink target={{ digest: "second", name: "second.md" }} />
      </ArtifactViewerProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /first\.md/ }));
    expect(
      await screen.findByRole("heading", { name: "From first" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /second\.md/ }));
    expect(
      await screen.findByRole("heading", { name: "From second" }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
  });

  it("says why an artifact could not be read", async () => {
    bridge.readArtifact.mockRejectedValue(new Error("open artifact: gone"));
    render(<ArtifactLink target={{ digest: "d4", name: "gone.md" }} />);
    fireEvent.click(screen.getByRole("button", { name: /gone\.md/ }));
    const drawer = await screen.findByRole("dialog");
    expect(await within(drawer).findByRole("alert")).toHaveTextContent(
      "open artifact: gone",
    );
  });
});
