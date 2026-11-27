import {
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  forceX,
  forceY,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force";
import type { DocGraph, DocGraphNode, Project } from "../../types";

/** A node that links to a doc, and how many links it makes. */
export interface Backlink {
  node: DocGraphNode;
  count: number;
}

/**
 * Every node with a link to one doc, most links first, then by label. The
 * doc is found by project and path, so the view never builds a node ID.
 */
export function backlinks(
  graph: DocGraph,
  project: string,
  path: string,
): Backlink[] {
  const target = graph.nodes.find(
    (node) =>
      node.kind === "doc" && node.project === project && node.path === path,
  );
  if (!target) return [];
  const byID = new Map(graph.nodes.map((node) => [node.id, node]));
  return graph.edges
    .filter((edge) => edge.to === target.id)
    .flatMap((edge) => {
      const node = byID.get(edge.from);
      return node ? [{ node, count: edge.count }] : [];
    })
    .sort(
      (a, b) => b.count - a.count || a.node.label.localeCompare(b.node.label),
    );
}

type Point = SimulationNodeDatum & { id: string };

export type Layout = Map<string, { x: number; y: number }>;

/** How long one slice of the layout may hold the page before it yields. */
const sliceMilliseconds = 12;

/**
 * Places the graph's nodes with a force layout, run to rest before it is
 * shown. The random source is seeded, so the same graph always lays out the
 * same way and nodes do not jump between reloads. Thousands of docs take
 * seconds to settle, so the simulation runs in short slices that yield to
 * the page between them; an aborted layout stops at the next slice.
 */
export function layoutGraph(
  graph: DocGraph,
  signal?: AbortSignal,
): Promise<Layout> {
  const points: Point[] = graph.nodes.map((node) => ({ id: node.id }));
  const known = new Set(points.map((point) => point.id));
  const links: SimulationLinkDatum<Point>[] = graph.edges
    .filter((edge) => known.has(edge.from) && known.has(edge.to))
    .map((edge) => ({ source: edge.from, target: edge.to }));
  let seed = 1;
  const random = () => {
    seed = (seed * 1664525 + 1013904223) % 4294967296;
    return seed / 4294967296;
  };
  const simulation = forceSimulation(points)
    .randomSource(random)
    .force(
      "link",
      forceLink<Point, SimulationLinkDatum<Point>>(links)
        .id((point) => point.id)
        .distance(150),
    )
    .force("charge", forceManyBody().strength(-500))
    .force("collide", forceCollide(85))
    // A weak pull to the middle keeps unlinked docs from drifting away.
    .force("x", forceX(0).strength(0.05))
    .force("y", forceY(0).strength(0.05))
    .stop();
  let remaining = Math.ceil(
    Math.log(simulation.alphaMin()) / Math.log(1 - simulation.alphaDecay()),
  );
  return new Promise((resolve, reject) => {
    const slice = () => {
      if (signal?.aborted) {
        reject(signal.reason);
        return;
      }
      const started = performance.now();
      while (remaining > 0 && performance.now() - started < sliceMilliseconds) {
        simulation.tick();
        remaining -= 1;
      }
      if (remaining > 0) {
        setTimeout(slice, 0);
        return;
      }
      resolve(
        new Map(
          points.map((point) => [
            point.id,
            { x: point.x ?? 0, y: point.y ?? 0 },
          ]),
        ),
      );
    };
    slice();
  });
}

type Point2 = { x: number; y: number };

/** How far each of two opposite links is drawn from the line between their docs. */
const pairOffset = 7;

/**
 * The line a link is drawn on between two node borders. When two docs link
 * to each other, both links would share one line, with both counts at its
 * midpoint, so a paired link moves to one side of its own direction; the
 * opposite link runs the other way and so lands on the other side.
 */
export function linkLine(
  start: Point2,
  end: Point2,
  paired: boolean,
): { start: Point2; end: Point2 } {
  const length = Math.hypot(end.x - start.x, end.y - start.y);
  if (!paired || length === 0) return { start, end };
  const shift = {
    x: (-(end.y - start.y) / length) * pairOffset,
    y: ((end.x - start.x) / length) * pairOffset,
  };
  return {
    start: { x: start.x + shift.x, y: start.y + shift.y },
    end: { x: end.x + shift.x, y: end.y + shift.y },
  };
}

/** Project hues, validated as a set against the graph's dark surface. */
export const projectHues = [
  "var(--color-series-1)",
  "var(--color-series-2)",
  "var(--color-series-3)",
];
export const otherProjectsHue = "var(--color-series-other)";

/**
 * A colour per project that follows the project, not the graph: the browsed
 * project first, then the other registered projects oldest first, so adding
 * a project never repaints the ones before it. In a force layout any two
 * nodes can sit side by side, and only three hues stay apart for every reader
 * when they do, so later projects share a neutral and are told apart by the
 * project name each node carries.
 */
export function projectColors(
  projects: Project[],
  browsing: string,
): Map<string, string> {
  // The dashboard lists projects newest first.
  const others = projects
    .filter((project) => project.id !== browsing)
    .sort(
      (a, b) =>
        // Times carry the local offset, so compare instants, not text.
        (Date.parse(a.createdAt) || 0) - (Date.parse(b.createdAt) || 0) ||
        a.id.localeCompare(b.id),
    )
    .map((project) => project.id);
  const ordered = [browsing, ...others];
  return new Map(
    ordered.map((id, index) => [id, projectHues[index] ?? otherProjectsHue]),
  );
}

/** What a node is called in tooltips and the links table. */
export function nodeTitle(
  node: DocGraphNode,
  projectName: (id: string) => string,
): string {
  switch (node.kind) {
    case "doc":
      return `${node.label} (${projectName(node.project ?? "")} · ${node.path})`;
    case "project":
      return `${node.label} repository`;
    default:
      return `${node.label} on GitHub`;
  }
}

/**
 * The nodes at most `hops` links from a node, following links either way:
 * what a doc draws on and what draws on it. The node itself is included.
 */
export function neighbourhood(
  graph: DocGraph,
  focus: string,
  hops: number,
): Set<string> {
  const adjacent = new Map<string, string[]>();
  const connect = (from: string, to: string) =>
    adjacent.set(from, [...(adjacent.get(from) ?? []), to]);
  for (const edge of graph.edges) {
    connect(edge.from, edge.to);
    connect(edge.to, edge.from);
  }
  const reached = new Set([focus]);
  let frontier = [focus];
  for (let hop = 0; hop < hops; hop += 1) {
    const next: string[] = [];
    for (const id of frontier) {
      for (const other of adjacent.get(id) ?? []) {
        if (reached.has(other)) continue;
        reached.add(other);
        next.push(other);
      }
    }
    frontier = next;
  }
  return reached;
}

/** One running pull of a focused node's neighbourhood toward it. */
export interface FocusMotion {
  /** Advances one step; returns where the moving nodes are now. */
  tick(): Map<string, Point2>;
  /** Moves the focused node, as a drag does; its neighbourhood follows. */
  pin(at: Point2): void;
  settled(): boolean;
}

/**
 * Pulls the nodes near a focused one toward it, a step at a time, so the
 * view can draw each step. Only the neighbourhood moves: the focused node
 * stays where it is (or where it is dragged) and every other node is pinned,
 * still pushing the moving ones away so nothing lands on top of it. Pinning
 * most of the graph keeps each step cheap at hundreds of docs.
 */
export function focusMotion(
  graph: DocGraph,
  positions: Layout,
  focus: string,
  near: Set<string>,
): FocusMotion {
  const points: Point[] = graph.nodes.map((node) => {
    const at = positions.get(node.id) ?? { x: 0, y: 0 };
    const pinned = node.id === focus || !near.has(node.id);
    return pinned
      ? { id: node.id, x: at.x, y: at.y, fx: at.x, fy: at.y }
      : { id: node.id, x: at.x, y: at.y };
  });
  const moving = points.filter((point) => point.fx === undefined);
  const focused = points.find((point) => point.id === focus);
  const links: SimulationLinkDatum<Point>[] = graph.edges
    .filter((edge) => near.has(edge.from) && near.has(edge.to))
    .map((edge) => ({ source: edge.from, target: edge.to }));
  const simulation = forceSimulation(points)
    .randomSource(() => 0.5)
    .force(
      "link",
      forceLink<Point, SimulationLinkDatum<Point>>(links)
        .id((point) => point.id)
        .distance(130)
        .strength(0.8),
    )
    .force("charge", forceManyBody().strength(-260).distanceMax(420))
    .force("collide", forceCollide(80))
    .alphaDecay(0.045)
    // Below this the steps are under a pixel; drawing them only costs frames.
    .alphaMin(0.01)
    .stop();
  return {
    tick() {
      simulation.tick();
      return new Map(
        moving.map((point) => [point.id, { x: point.x ?? 0, y: point.y ?? 0 }]),
      );
    },
    pin(at) {
      if (!focused) return;
      focused.fx = at.x;
      focused.fy = at.y;
      simulation.alpha(Math.max(simulation.alpha(), 0.3));
    },
    settled() {
      return simulation.alpha() < simulation.alphaMin();
    },
  };
}
