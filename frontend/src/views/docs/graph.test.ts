import type { DocGraph, Project } from "../../types";
import {
  backlinks,
  focusMotion,
  neighbourhood,
  layoutGraph,
  linkLine,
  nodeTitle,
  otherProjectsHue,
  projectColors,
  projectHues,
} from "./graph";

const project = (id: string, createdAt = "2026-01-01T00:00:00Z"): Project => ({
  id,
  name: id.toUpperCase(),
  repository: `/src/${id}`,
  defaultRuntime: "claude",
  createdAt,
});

const graph: DocGraph = {
  nodes: [
    { id: "doc:a:x.md", kind: "doc", label: "X", project: "a", path: "x.md" },
    { id: "doc:a:y.md", kind: "doc", label: "Y", project: "a", path: "y.md" },
    { id: "doc:b:z.md", kind: "doc", label: "Z", project: "b", path: "z.md" },
    { id: "project:a", kind: "project", label: "A", project: "a" },
    {
      id: "external:github.com/o/r",
      kind: "external",
      label: "o/r",
      url: "https://github.com/o/r",
    },
  ],
  edges: [
    { from: "doc:a:x.md", to: "doc:a:y.md", count: 1 },
    { from: "doc:b:z.md", to: "doc:a:y.md", count: 3 },
    { from: "doc:a:x.md", to: "external:github.com/o/r", count: 1 },
  ],
};

describe("doc graph helpers", () => {
  it("lays the same graph out the same way, with every node placed apart", async () => {
    const first = await layoutGraph(graph);
    expect(await layoutGraph(graph)).toEqual(first);
    const points = [...first.values()];
    expect(points).toHaveLength(graph.nodes.length);
    for (const point of points) {
      expect(Number.isFinite(point.x) && Number.isFinite(point.y)).toBe(true);
    }
    for (const [i, a] of points.entries()) {
      for (const b of points.slice(i + 1)) {
        expect(Math.hypot(a.x - b.x, a.y - b.y)).toBeGreaterThan(100);
      }
    }
  });

  it("stops when the layout is no longer wanted", async () => {
    const controller = new AbortController();
    controller.abort();
    await expect(layoutGraph(graph, controller.signal)).rejects.toBeDefined();
  });

  it("ignores an edge to a node the graph does not have", async () => {
    await expect(
      layoutGraph({
        ...graph,
        edges: [
          ...graph.edges,
          { from: "doc:a:x.md", to: "missing", count: 1 },
        ],
      }),
    ).resolves.toHaveProperty("size", graph.nodes.length);
  });

  it("draws two docs that link to each other on separate lines", () => {
    const a = { x: 0, y: 0 };
    const b = { x: 100, y: 0 };
    expect(linkLine(a, b, false)).toEqual({ start: a, end: b });
    const there = linkLine(a, b, true);
    const back = linkLine(b, a, true);
    const middle = (line: typeof there) => ({
      x: (line.start.x + line.end.x) / 2,
      y: (line.start.y + line.end.y) / 2,
    });
    // Each direction keeps its own line, so the two counts never share a spot.
    expect(
      Math.hypot(
        middle(there).x - middle(back).x,
        middle(there).y - middle(back).y,
      ),
    ).toBeGreaterThanOrEqual(14);
    expect(there.end.x - there.start.x).toBe(100);
    expect(linkLine(a, a, true)).toEqual({ start: a, end: a });
  });

  it("colours the browsed project first, then the others oldest first, folding past three", () => {
    // The dashboard lists projects newest first; offsets differ across DST.
    const listed = [
      project("d", "2026-10-04T09:00:00-07:00"),
      project("c", "2026-10-03T09:00:00-07:00"),
      project("b", "2026-03-02T09:00:00-08:00"),
      project("a", "2026-03-01T09:00:00-08:00"),
    ];
    const colours = projectColors(listed, "c");
    expect([...colours.entries()]).toEqual([
      ["c", projectHues[0]],
      ["a", projectHues[1]],
      ["b", projectHues[2]],
      ["d", otherProjectsHue],
    ]);
    // Adding a project never repaints the ones before it.
    const added = projectColors(
      [project("e", "2026-10-05T09:00:00-07:00"), ...listed],
      "c",
    );
    for (const id of ["a", "b", "c", "d"])
      expect(added.get(id)).toBe(colours.get(id));
    expect(added.get("e")).toBe(otherProjectsHue);
  });

  it("lists what links to a doc, most links first", () => {
    expect(
      backlinks(graph, "a", "y.md").map(({ node, count }) => [node.id, count]),
    ).toEqual([
      ["doc:b:z.md", 3],
      ["doc:a:x.md", 1],
    ]);
    expect(backlinks(graph, "a", "x.md")).toEqual([]);
    expect(backlinks(graph, "a", "missing.md")).toEqual([]);
  });

  it("names nodes for tooltips and the links table", () => {
    const name = (id: string) => id.toUpperCase();
    expect(nodeTitle(graph.nodes[2], name)).toBe("Z (B · z.md)");
    expect(nodeTitle(graph.nodes[3], name)).toBe("A repository");
    expect(nodeTitle(graph.nodes[4], name)).toBe("o/r on GitHub");
  });

  it("finds what is within one or two links of a node, either way", () => {
    const chain: DocGraph = {
      nodes: ["a", "b", "c", "d", "e"].map((id) => ({
        id,
        kind: "doc" as const,
        label: id,
      })),
      edges: [
        { from: "a", to: "b", count: 1 },
        { from: "c", to: "b", count: 1 },
        { from: "c", to: "d", count: 1 },
      ],
    };
    expect([...neighbourhood(chain, "a", 1)].sort()).toEqual(["a", "b"]);
    expect([...neighbourhood(chain, "a", 2)].sort()).toEqual(["a", "b", "c"]);
    expect([...neighbourhood(chain, "b", 1)].sort()).toEqual(["a", "b", "c"]);
    expect([...neighbourhood(chain, "e", 2)]).toEqual(["e"]);
  });

  it("pulls only the focused node's neighbourhood toward it", () => {
    const spread: DocGraph = {
      nodes: ["hub", "near", "far", "other"].map((id) => ({
        id,
        kind: "doc" as const,
        label: id,
      })),
      edges: [
        { from: "hub", to: "near", count: 1 },
        { from: "near", to: "far", count: 1 },
      ],
    };
    const start = new Map([
      ["hub", { x: 0, y: 0 }],
      ["near", { x: 900, y: 0 }],
      ["far", { x: 1400, y: 300 }],
      ["other", { x: -600, y: 400 }],
    ]);
    const distance = (
      a: { x: number; y: number },
      b: { x: number; y: number },
    ) => Math.hypot(a.x - b.x, a.y - b.y);
    const motion = focusMotion(
      spread,
      start,
      "hub",
      neighbourhood(spread, "hub", 1),
    );
    let moved = new Map<string, { x: number; y: number }>();
    for (let step = 0; step < 500 && !motion.settled(); step += 1)
      moved = motion.tick();
    expect(motion.settled()).toBe(true);
    // Only the neighbour moves: the focus and everything else stay pinned.
    expect([...moved.keys()]).toEqual(["near"]);
    expect(distance(moved.get("near")!, start.get("hub")!)).toBeLessThan(300);

    // Dragging the focus away brings the neighbour after it.
    motion.pin({ x: 2000, y: 2000 });
    expect(motion.settled()).toBe(false);
    for (let step = 0; step < 500 && !motion.settled(); step += 1)
      moved = motion.tick();
    expect(distance(moved.get("near")!, { x: 2000, y: 2000 })).toBeLessThan(
      300,
    );
  });
});
