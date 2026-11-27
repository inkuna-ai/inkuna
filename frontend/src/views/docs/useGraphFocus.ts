import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNodesState, type Node } from "@xyflow/react";
import type { DocGraph } from "../../types";
import { focusMotion, neighbourhood, type Layout } from "./graph";
import { nextFrame } from "./motion";

type Point = { x: number; y: number };
export type Hops = 1 | 2;

/** How long clearing the focus takes to ease the graph back, when animating. */
const returnMilliseconds = 300;

/**
 * The most simulation steps one frame may take. Steps follow elapsed time,
 * so a graph whose frames are slow to draw settles in the same time as a
 * small one, in fewer, longer strides.
 */
const maxStepsPerFrame = 8;
const frameMilliseconds = 1000 / 60;

/**
 * The class a node carries while a node is focused. Only the neighbourhood
 * is marked; the graph's container fades everything else, so focusing a doc
 * among hundreds changes a handful of nodes rather than all of them.
 */
export function focusClass(
  near: Set<string> | null,
  focus: string | null,
  id: string,
): string | undefined {
  if (!near) return undefined;
  if (id === focus) return "is-focus";
  return near.has(id) ? "is-near" : undefined;
}

/**
 * The doc graph's nodes and the focus on one of them. Focusing marks the
 * node and its neighbourhood within `hops`; the rest fades. When
 * animating, the neighbourhood is pulled toward the focused node and follows
 * it while it is dragged; clearing the focus eases every node back to the
 * layout. Without animation nothing moves except a node being dragged, and
 * clearing puts it back.
 */
export function useGraphFocus<N extends Node>(
  graph: DocGraph | null,
  layout: Layout | null,
  initial: N[] | null,
  animate: boolean,
) {
  const [nodes, setNodes, onNodesChange] = useNodesState<N>([]);
  const [focus, setFocus] = useState<string | null>(null);
  const [hops, setHops] = useState<Hops>(1);
  const near = useMemo(
    () => (graph && focus ? neighbourhood(graph, focus, hops) : null),
    [graph, focus, hops],
  );
  // The motion reads where nodes are when it starts, without restarting on
  // every frame it draws.
  const latest = useRef(nodes);
  useEffect(() => {
    latest.current = nodes;
  });
  const pinFocus = useRef<((at: Point) => void) | null>(null);

  // A new layout starts unfocused, every node where the layout put it.
  useEffect(() => {
    if (!initial) return;
    setNodes(initial);
    setFocus(null);
  }, [initial, setNodes]);

  // React Flow's selection follows the focus, so Enter on a node that was
  // focused and then cleared selects it again, and focuses it again.
  useEffect(() => {
    setNodes((current) =>
      current.map((node) => {
        const className = focusClass(near, focus, node.id);
        const selected = node.id === focus;
        return node.className === className &&
          Boolean(node.selected) === selected
          ? node
          : { ...node, className, selected };
      }),
    );
  }, [near, focus, setNodes]);

  useEffect(() => {
    if (!graph || !layout) return;
    const from = new Map(
      latest.current.map((node) => [node.id, node.position]),
    );
    const place = (moved: Map<string, Point>) =>
      setNodes((current) =>
        current.map((node) => {
          const at = moved.get(node.id);
          return at ? { ...node, position: at } : node;
        }),
      );
    let cancel = () => {};
    if (focus && near && animate) {
      const motion = focusMotion(graph, from, focus, near);
      let running = false;
      let last = 0;
      const step = () => {
        const now = performance.now();
        const steps = Math.min(
          maxStepsPerFrame,
          Math.max(1, Math.round((now - last) / frameMilliseconds)),
        );
        last = now;
        let moved: Map<string, Point> | undefined;
        for (let taken = 0; taken < steps && !motion.settled(); taken += 1)
          moved = motion.tick();
        if (moved) place(moved);
        if (motion.settled()) {
          running = false;
          return;
        }
        cancel = nextFrame(step);
      };
      const start = () => {
        if (running) return;
        running = true;
        last = performance.now();
        cancel = nextFrame(step);
      };
      pinFocus.current = (at) => {
        motion.pin(at);
        start();
      };
      start();
      return () => {
        pinFocus.current = null;
        cancel();
      };
    }
    pinFocus.current = null;
    // Unfocused, or not animating: every node goes back to the layout.
    const moved = new Map(
      [...layout].filter(([id, at]) => {
        const now = from.get(id);
        return now !== undefined && (now.x !== at.x || now.y !== at.y);
      }),
    );
    if (moved.size === 0) return;
    if (!animate) {
      place(moved);
      return;
    }
    const started = performance.now();
    const step = () => {
      const progress = Math.min(
        1,
        (performance.now() - started) / returnMilliseconds,
      );
      const eased = 1 - (1 - progress) ** 3;
      place(
        new Map(
          [...moved].map(([id, at]) => {
            const start = from.get(id) ?? at;
            return [
              id,
              {
                x: start.x + (at.x - start.x) * eased,
                y: start.y + (at.y - start.y) * eased,
              },
            ];
          }),
        ),
      );
      if (progress < 1) cancel = nextFrame(step);
    };
    cancel = nextFrame(step);
    return () => cancel();
  }, [graph, layout, focus, near, animate, setNodes]);

  /** A drag of the focused node moves it; its neighbourhood follows. */
  const dragFocus = useCallback(
    (id: string, at: Point) => {
      if (id === focus) pinFocus.current?.(at);
    },
    [focus],
  );

  return {
    nodes,
    onNodesChange,
    focus,
    setFocus,
    hops,
    setHops,
    near,
    dragFocus,
  };
}
