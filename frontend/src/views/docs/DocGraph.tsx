import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  BaseEdge,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  getStraightPath,
  useInternalNode,
  type Edge,
  type EdgeProps,
  type InternalNode,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import type {
  DocGraph,
  DocGraphNode,
  DocGraphScope,
  Project,
} from "../../types";
import {
  Button,
  Disclosure,
  Notice,
  TableScroll,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import {
  layoutGraph,
  linkLine,
  nodeTitle,
  otherProjectsHue,
  projectColors,
  type Layout,
} from "./graph";
import { initialAnimate, rememberAnimate } from "./motion";
import { useGraphFocus, type Hops } from "./useGraphFocus";

type NodeData = {
  node: DocGraphNode;
  hue: string;
  /** Shown under the label when the node is not one of the browsed project's docs. */
  subtitle: string;
  title: string;
};
type DocFlowNode = Node<NodeData, "doc">;
type LinkFlowEdge = Edge<{ count: number; paired: boolean }, "link">;

const scopes: Array<{ id: DocGraphScope; label: string }> = [
  { id: "project", label: "This project" },
  { id: "all", label: "All projects" },
];
const hopChoices: Array<{ hops: Hops; label: string }> = [
  { hops: 1, label: "1 hop" },
  { hops: 2, label: "2 hops" },
];
const segment = (pressed: boolean) =>
  `cursor-pointer border-0 px-3 py-1.5 text-xs ${
    pressed
      ? "bg-raised text-ink-strong"
      : "bg-transparent text-muted hover:text-ink-strong"
  }`;

/**
 * The doc link graph: docs, the repositories they point into, and the links
 * between them, laid out by force. Selecting or dragging a node focuses it
 * and its neighbours; double-clicking, or Open in the focus bar, opens it.
 * The table below lists the same links as text.
 */
export function DocGraphPanel({
  graph,
  error,
  scope,
  onScopeChange,
  projects,
  projectID,
  onOpen,
}: {
  graph: DocGraph | null;
  error: string;
  scope: DocGraphScope;
  onScopeChange(scope: DocGraphScope): void;
  projects: Project[];
  projectID: string;
  onOpen(node: DocGraphNode): void;
}) {
  const projectName = useMemo(() => {
    const names = new Map(
      projects.map((project) => [project.id, project.name]),
    );
    return (id: string) => names.get(id) ?? id;
  }, [projects]);
  const hues = useMemo(
    () => projectColors(projects, projectID),
    [projects, projectID],
  );
  // The layout runs in slices after rendering, so a large graph never holds
  // the page, and is kept with the graph it was computed for.
  const [layout, setLayout] = useState<{
    graph: DocGraph;
    positions: Layout;
  } | null>(null);
  const [layoutError, setLayoutError] = useState("");
  useEffect(() => {
    if (!graph) return;
    const controller = new AbortController();
    setLayoutError("");
    layoutGraph(graph, controller.signal)
      .then((positions) => setLayout({ graph, positions }))
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setLayoutError(String(reason));
      });
    return () => controller.abort();
  }, [graph]);
  const positions = layout?.graph === graph ? layout.positions : null;

  const flow = useMemo(() => {
    if (!graph || !positions) return null;
    const linked = new Set(
      graph.edges.map((edge) => `${edge.from}\n${edge.to}`),
    );
    const nodes: DocFlowNode[] = graph.nodes.map((node) => {
      const position = positions.get(node.id) ?? { x: 0, y: 0 };
      const subtitle =
        node.kind === "external"
          ? "GitHub"
          : node.kind === "project"
            ? "repository"
            : node.project === projectID
              ? ""
              : projectName(node.project ?? "");
      return {
        id: node.id,
        type: "doc",
        position,
        data: {
          node,
          hue:
            node.kind === "external"
              ? "transparent"
              : (hues.get(node.project ?? "") ?? otherProjectsHue),
          subtitle,
          title: nodeTitle(node, projectName),
        },
      };
    });
    const edges: LinkFlowEdge[] = graph.edges.map((edge) => ({
      id: `${edge.from}->${edge.to}`,
      source: edge.from,
      target: edge.to,
      type: "link",
      data: {
        count: edge.count,
        paired: linked.has(`${edge.to}\n${edge.from}`),
      },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: "var(--color-faint)",
        width: 14,
        height: 14,
      },
    }));
    return { nodes, edges };
  }, [graph, positions, hues, projectID, projectName]);

  const [animate, setAnimate] = useState(initialAnimate);
  const {
    nodes,
    onNodesChange,
    focus,
    setFocus,
    hops,
    setHops,
    near,
    dragFocus,
  } = useGraphFocus(graph, positions, flow?.nodes ?? null, animate);
  // Only edges within the neighbourhood change; the rest keep their objects,
  // so React Flow leaves them alone and the container's class fades them.
  const edges = useMemo(
    () =>
      (flow?.edges ?? []).map((edge) =>
        near?.has(edge.source) && near.has(edge.target)
          ? { ...edge, className: "is-near" }
          : edge,
      ),
    [flow, near],
  );
  // React Flow hands its handlers to every node, so new ones on each frame of
  // a focus animation would re-render every node instead of the few moving.
  const focusNode = useCallback(
    (_: unknown, node: DocFlowNode) => setFocus(node.id),
    [setFocus],
  );
  const openNode = useCallback(
    (_: unknown, node: DocFlowNode) => onOpen(node.data.node),
    [onOpen],
  );
  const dragNode = useCallback(
    (_: unknown, node: DocFlowNode) => dragFocus(node.id, node.position),
    [dragFocus],
  );
  const clearFocus = useCallback(() => setFocus(null), [setFocus]);
  // Enter or Space on a node selects it without a click; a single selected
  // node is focused however it was selected.
  const followSelection = useCallback(
    ({ nodes: selected }: { nodes: DocFlowNode[] }) => {
      if (selected.length === 1) setFocus(selected[0].id);
    },
    [setFocus],
  );
  // Escape clears the focus wherever keyboard focus is on the page.
  useEffect(() => {
    if (!focus) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setFocus(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [focus, setFocus]);

  const byID = useMemo(
    () => new Map((graph?.nodes ?? []).map((node) => [node.id, node])),
    [graph],
  );
  const focused = focus ? byID.get(focus) : undefined;
  const docCount = graph?.nodes.filter((node) => node.kind === "doc").length;
  const linkCount = graph?.edges.reduce((sum, edge) => sum + edge.count, 0);
  const shownProjects = [
    ...new Set(
      (graph?.nodes ?? [])
        .filter((node) => node.kind !== "external")
        .map((node) => node.project ?? ""),
    ),
  ].sort((a, b) => [...hues.keys()].indexOf(a) - [...hues.keys()].indexOf(b));

  return (
    <div className="grid min-w-0 gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div
          role="group"
          aria-label="Graph scope"
          className="flex overflow-hidden rounded-md border border-line-strong"
        >
          {scopes.map((option) => (
            <button
              key={option.id}
              type="button"
              aria-pressed={scope === option.id}
              className={segment(scope === option.id)}
              onClick={() => onScopeChange(option.id)}
            >
              {option.label}
            </button>
          ))}
        </div>
        {graph && (
          <span className="font-mono text-[11px] text-muted">
            {docCount} {docCount === 1 ? "doc" : "docs"} · {linkCount}{" "}
            {linkCount === 1 ? "link" : "links"}
          </span>
        )}
      </div>
      {error && (
        <p role="alert" className="m-0 text-xs wrap-anywhere text-danger">
          {error}
        </p>
      )}
      {graph?.warnings?.length ? (
        <Notice title="Left out of the graph" className="!mt-0">
          <ul className="m-0 list-disc pl-5 wrap-anywhere">
            {graph.warnings.map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </Notice>
      ) : null}
      {layoutError && (
        <p role="alert" className="m-0 text-xs wrap-anywhere text-danger">
          {layoutError}
        </p>
      )}
      {!graph && !error && (
        <p className="m-0 text-sm text-muted">Linking docs…</p>
      )}
      {graph && !flow && !layoutError && (
        <p className="m-0 text-sm text-muted">
          Laying out {docCount} {docCount === 1 ? "doc" : "docs"}…
        </p>
      )}
      {graph && flow && (
        <>
          <Legend
            projects={shownProjects.map((id) => ({
              name: projectName(id),
              hue: hues.get(id) ?? otherProjectsHue,
            }))}
          />
          <div
            role="group"
            aria-label="Focus"
            className="flex min-h-8 flex-wrap items-center gap-x-3 gap-y-2 text-xs"
          >
            {focused && near ? (
              <>
                <span className="min-w-0 wrap-anywhere text-ink-soft">
                  Focused on{" "}
                  <b className="font-semibold text-ink-strong">
                    {focused.label}
                  </b>{" "}
                  · {near.size - 1} linked
                </span>
                <div
                  role="group"
                  aria-label="Focus reach"
                  className="flex overflow-hidden rounded-md border border-line-strong"
                >
                  {hopChoices.map((choice) => (
                    <button
                      key={choice.hops}
                      type="button"
                      aria-pressed={hops === choice.hops}
                      className={segment(hops === choice.hops)}
                      onClick={() => setHops(choice.hops)}
                    >
                      {choice.label}
                    </button>
                  ))}
                </div>
                {focused.kind !== "project" && (
                  <Button
                    variant="ghost"
                    className="!px-1 !py-0.5 text-xs"
                    onClick={() => onOpen(focused)}
                  >
                    Open
                  </Button>
                )}
                <Button
                  variant="ghost"
                  className="!px-1 !py-0.5 text-xs"
                  onClick={() => setFocus(null)}
                >
                  Clear
                </Button>
              </>
            ) : (
              <span className="text-muted">
                Select or drag a node to focus the docs it links with;
                double-click a doc to open it.
              </span>
            )}
            <label className="ml-auto flex cursor-pointer items-center gap-1.5 text-ink-soft">
              <input
                type="checkbox"
                checked={animate}
                onChange={(event) => {
                  setAnimate(event.target.checked);
                  rememberAnimate(event.target.checked);
                }}
              />
              Animate focus
            </label>
          </div>
          <div
            aria-label="Doc graph"
            className={`doc-graph h-[560px] min-w-0 border border-line bg-field max-[900px]:h-[420px] ${near ? "has-focus" : ""}`}
          >
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onSelectionChange={followSelection}
              // A read-only view: Backspace must not remove a doc from it.
              deleteKeyCode={null}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              colorMode="dark"
              nodesConnectable={false}
              minZoom={0.1}
              fitView
              onNodeClick={focusNode}
              onNodeDoubleClick={openNode}
              onNodeDragStart={focusNode}
              onNodeDrag={dragNode}
              onPaneClick={clearFocus}
            >
              <Background color="#263238" />
              <Controls showInteractive={false} />
            </ReactFlow>
          </div>
          <Disclosure label={`Links as a table (${graph.edges.length})`}>
            <TableScroll flush>
              <table className={tableClass} aria-label="Doc links">
                <thead>
                  <tr>
                    <th className={thClass}>From</th>
                    <th className={thClass}>To</th>
                    <th className={thClass}>Links</th>
                  </tr>
                </thead>
                <tbody>
                  {graph.edges.map((edge) => (
                    <tr key={`${edge.from}->${edge.to}`}>
                      {[edge.from, edge.to].map((id) => (
                        <td key={id} className={tdClass}>
                          <NodeCell
                            node={byID.get(id)}
                            title={(node) => nodeTitle(node, projectName)}
                            onOpen={onOpen}
                          />
                        </td>
                      ))}
                      <td className={tdClass}>{edge.count}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
          </Disclosure>
        </>
      )}
    </div>
  );
}

function NodeCell({
  node,
  title,
  onOpen,
}: {
  node: DocGraphNode | undefined;
  title(node: DocGraphNode): string;
  onOpen(node: DocGraphNode): void;
}) {
  if (!node) return null;
  if (node.kind === "project") return <>{title(node)}</>;
  return (
    <button
      type="button"
      className="cursor-pointer border-0 bg-transparent p-0 text-left text-accent underline decoration-accent-line underline-offset-2"
      onClick={() => onOpen(node)}
    >
      {title(node)}
    </button>
  );
}

function Legend({
  projects,
}: {
  projects: Array<{ name: string; hue: string }>;
}) {
  const swatch = "inline-block size-2.5 flex-none rounded-[2px]";
  return (
    <ul
      aria-label="Graph legend"
      className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1.5 p-0 text-[11px] text-ink-soft"
    >
      {projects.map((project) => (
        <li key={project.name} className="flex items-center gap-1.5">
          <span
            aria-hidden="true"
            className={swatch}
            style={{ background: project.hue }}
          />
          {project.name}
        </li>
      ))}
      <li className="flex items-center gap-1.5">
        <span
          aria-hidden="true"
          className={`${swatch} border border-dashed border-line-strong`}
        />
        draft or GitHub repository
      </li>
      <li className="flex items-center gap-1.5">
        <span
          aria-hidden="true"
          className={`${swatch} bg-line-strong opacity-50`}
        />
        deprecated
      </li>
      <li className="flex items-center gap-1.5">
        <span aria-hidden="true" className="text-warn">
          ⚠
        </span>
        problems
      </li>
    </ul>
  );
}

function DocNodeView({ data }: NodeProps<DocFlowNode>) {
  const { node, hue, subtitle, title } = data;
  const dashed = node.status === "draft" || node.kind === "external";
  return (
    <div
      title={title}
      className={`flex min-w-0 cursor-pointer items-stretch overflow-hidden rounded-[5px] border border-line-strong ${
        node.kind === "doc" ? "bg-raised" : "bg-panel"
      } ${dashed ? "border-dashed" : ""} ${node.status === "deprecated" ? "opacity-55" : ""}`}
    >
      <Handle type="target" position={Position.Top} isConnectable={false} />
      <span
        aria-hidden="true"
        className="w-1 flex-none"
        style={{ background: hue }}
      />
      <span className="grid min-w-0 gap-0.5 px-2 py-1.5">
        <span className="truncate text-[11px] text-ink-strong">
          {node.kind === "project" && <span aria-hidden="true">◇ </span>}
          {node.label}
          {node.problems ? (
            <span className="text-warn" aria-label=", has problems">
              {" "}
              ⚠
            </span>
          ) : null}
        </span>
        {subtitle && (
          <small className="truncate text-[9px] text-muted">
            {subtitle}
            {node.kind === "external" && <span aria-hidden="true"> ↗</span>}
          </small>
        )}
      </span>
      <Handle type="source" position={Position.Bottom} isConnectable={false} />
    </div>
  );
}

/** Where the line from a node's centre towards another point leaves its box. */
function border(node: InternalNode, toward: { x: number; y: number }) {
  const width = node.measured.width ?? 0;
  const height = node.measured.height ?? 0;
  const x = node.internals.positionAbsolute.x + width / 2;
  const y = node.internals.positionAbsolute.y + height / 2;
  const dx = toward.x - x;
  const dy = toward.y - y;
  const scale = Math.min(
    dx === 0 ? Infinity : width / 2 / Math.abs(dx),
    dy === 0 ? Infinity : height / 2 / Math.abs(dy),
  );
  return Number.isFinite(scale)
    ? { x: x + dx * scale, y: y + dy * scale }
    : { x, y };
}

function centre(node: InternalNode) {
  return {
    x: node.internals.positionAbsolute.x + (node.measured.width ?? 0) / 2,
    y: node.internals.positionAbsolute.y + (node.measured.height ?? 0) / 2,
  };
}

/**
 * A straight edge between node borders, so the arrowhead shows which doc
 * links to which wherever the layout put them; it thickens with the count,
 * and two docs that link to each other get a line each.
 */
function LinkEdgeView({
  id,
  source,
  target,
  markerEnd,
  data,
}: EdgeProps<LinkFlowEdge>) {
  const from = useInternalNode(source);
  const to = useInternalNode(target);
  if (!from || !to) return null;
  const { start, end } = linkLine(
    border(from, centre(to)),
    border(to, centre(from)),
    data?.paired ?? false,
  );
  const [path, labelX, labelY] = getStraightPath({
    sourceX: start.x,
    sourceY: start.y,
    targetX: end.x,
    targetY: end.y,
  });
  const count = data?.count ?? 1;
  return (
    <BaseEdge
      id={id}
      path={path}
      markerEnd={markerEnd}
      style={{ strokeWidth: 1 + Math.min(3, Math.log2(count)) }}
      label={count > 1 ? `×${count}` : undefined}
      labelX={labelX}
      labelY={labelY}
      labelStyle={{ fill: "var(--color-ink-soft)", fontSize: 10 }}
      labelBgStyle={{ fill: "var(--color-field)" }}
    />
  );
}

const nodeTypes = { doc: DocNodeView };
const edgeTypes = { link: LinkEdgeView };
