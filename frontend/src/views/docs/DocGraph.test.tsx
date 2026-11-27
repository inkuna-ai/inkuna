import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { DocGraph, DocGraphNode, Project } from "../../types";
import { DocGraphPanel } from "./DocGraph";
import { animateKey } from "./motion";

const project: Project = {
  id: "p1",
  name: "Bakery",
  repository: "/src/bakery",
  defaultRuntime: "claude",
  createdAt: "2026-01-01T00:00:00Z",
};

const doc = (name: string): DocGraphNode => ({
  id: `doc:p1:${name}.md`,
  kind: "doc",
  label: name,
  project: "p1",
  path: `${name}.md`,
});

// alpha links to bravo, charlie links to bravo and delta; echo stands alone.
const graph: DocGraph = {
  nodes: ["alpha", "bravo", "charlie", "delta", "echo"].map(doc),
  edges: [
    { from: "doc:p1:alpha.md", to: "doc:p1:bravo.md", count: 1 },
    { from: "doc:p1:charlie.md", to: "doc:p1:bravo.md", count: 1 },
    { from: "doc:p1:charlie.md", to: "doc:p1:delta.md", count: 1 },
  ],
};

function renderGraph(onOpen = vi.fn()) {
  render(
    <DocGraphPanel
      graph={graph}
      error=""
      scope="project"
      onScopeChange={() => {}}
      projects={[project]}
      projectID="p1"
      onOpen={onOpen}
    />,
  );
  return onOpen;
}

/**
 * Each node's focus state, by label: focus, near, faded, or none. While a
 * node is focused, the graph fades every node it has not marked.
 */
function states() {
  const focused = document
    .querySelector(".doc-graph")!
    .classList.contains("has-focus");
  const result: Record<string, string> = {};
  for (const node of document.querySelectorAll<HTMLElement>(
    ".doc-graph .react-flow__node",
  )) {
    const state = ["is-focus", "is-near"].find((name) =>
      node.classList.contains(name),
    );
    result[node.textContent ?? ""] =
      state?.slice(3) ?? (focused ? "faded" : "none");
  }
  return result;
}

const transform = (label: string) =>
  screen.getByText(label).closest<HTMLElement>(".react-flow__node")!.style
    .transform;

describe("DocGraphPanel focus", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => {
    // Unmount before matchMedia goes, so an effect still pending when a test
    // ends never calls it.
    cleanup();
    delete (window as { matchMedia?: unknown }).matchMedia;
  });

  it("focuses a node's neighbours one or two links away and clears with Escape or the background", async () => {
    renderGraph();
    const drawn = await screen.findByLabelText("Doc graph");
    fireEvent.click(await within(drawn).findByText("alpha"));
    expect(states()).toEqual({
      alpha: "focus",
      bravo: "near",
      charlie: "faded",
      delta: "faded",
      echo: "faded",
    });
    const bar = screen.getByRole("group", { name: "Focus" });
    expect(bar).toHaveTextContent("Focused on alpha · 1 linked");

    fireEvent.click(within(bar).getByRole("button", { name: "2 hops" }));
    expect(states()).toMatchObject({ charlie: "near", delta: "faded" });
    expect(bar).toHaveTextContent("· 2 linked");

    fireEvent.keyDown(window, { key: "Escape" });
    expect(new Set(Object.values(states()))).toEqual(new Set(["none"]));
    expect(bar).toHaveTextContent("Select or drag a node");

    fireEvent.click(within(drawn).getByText("charlie"));
    // Focus reach stays where it was set.
    expect(states()).toMatchObject({
      charlie: "focus",
      alpha: "near",
      echo: "faded",
    });
    fireEvent.click(drawn.querySelector(".react-flow__pane")!);
    expect(new Set(Object.values(states()))).toEqual(new Set(["none"]));
  });

  it("focuses a node selected from the keyboard, again after clearing", async () => {
    renderGraph();
    const drawn = await screen.findByLabelText("Doc graph");
    const bravo = (
      await within(drawn).findByText("bravo")
    ).closest<HTMLElement>(".react-flow__node")!;
    bravo.focus();
    fireEvent.keyDown(bravo, { key: "Enter" });
    expect(states()).toMatchObject({
      bravo: "focus",
      alpha: "near",
      charlie: "near",
      delta: "faded",
    });
    fireEvent.keyDown(window, { key: "Escape" });
    expect(new Set(Object.values(states()))).toEqual(new Set(["none"]));
    fireEvent.keyDown(bravo, { key: " " });
    expect(states()).toMatchObject({ bravo: "focus" });
  });

  it("keeps every doc when Backspace is pressed on a selected node", async () => {
    renderGraph();
    const drawn = await screen.findByLabelText("Doc graph");
    const alpha = await within(drawn).findByText("alpha");
    fireEvent.click(alpha);
    alpha.closest<HTMLElement>(".react-flow__node")!.focus();
    fireEvent.keyDown(document.activeElement!, { key: "Backspace" });
    fireEvent.keyUp(document.activeElement!, { key: "Backspace" });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(drawn.querySelectorAll(".react-flow__node").length).toBe(
      graph.nodes.length,
    );
    expect(states()).toMatchObject({ alpha: "focus" });
  });

  it("opens a doc from the focus bar or by double-click", async () => {
    const onOpen = renderGraph();
    const drawn = await screen.findByLabelText("Doc graph");
    fireEvent.click(await within(drawn).findByText("delta"));
    fireEvent.click(
      within(screen.getByRole("group", { name: "Focus" })).getByRole("button", {
        name: "Open",
      }),
    );
    expect(onOpen).toHaveBeenLastCalledWith(graph.nodes[3]);
    fireEvent.doubleClick(within(drawn).getByText("echo"));
    expect(onOpen).toHaveBeenLastCalledWith(graph.nodes[4]);
  });

  it("pulls neighbours in when animating, and eases them back when the focus clears", async () => {
    renderGraph();
    const drawn = await screen.findByLabelText("Doc graph");
    await within(drawn).findByText("bravo");
    const before = transform("bravo");
    fireEvent.click(within(drawn).getByText("alpha"));
    await waitFor(() => expect(transform("bravo")).not.toBe(before));
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(transform("bravo")).toBe(before));
  });

  it("keeps every node still when animation is off", async () => {
    renderGraph();
    const drawn = await screen.findByLabelText("Doc graph");
    await within(drawn).findByText("bravo");
    fireEvent.click(screen.getByRole("checkbox", { name: "Animate focus" }));
    const before = transform("bravo");
    fireEvent.click(within(drawn).getByText("alpha"));
    expect(states()).toMatchObject({ alpha: "focus", bravo: "near" });
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(transform("bravo")).toBe(before);
  });

  it("remembers the animation choice, defaulting off for reduced motion", async () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true });
    renderGraph();
    const toggle = await screen.findByRole("checkbox", {
      name: "Animate focus",
    });
    expect(toggle).not.toBeChecked();
    expect(window.matchMedia).toHaveBeenCalledWith(
      "(prefers-reduced-motion: reduce)",
    );

    fireEvent.click(toggle);
    expect(toggle).toBeChecked();
    expect(window.localStorage.getItem(animateKey)).toBe("on");
  });

  it("uses a remembered choice over the system setting, and animates by default otherwise", async () => {
    window.localStorage.setItem(animateKey, "on");
    window.matchMedia = vi.fn().mockReturnValue({ matches: true });
    renderGraph();
    expect(
      await screen.findByRole("checkbox", { name: "Animate focus" }),
    ).toBeChecked();
  });

  it("animates by default when the system does not ask for reduced motion", async () => {
    renderGraph();
    expect(
      await screen.findByRole("checkbox", { name: "Animate focus" }),
    ).toBeChecked();
  });
});
