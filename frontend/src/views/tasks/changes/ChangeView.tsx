import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { getRunFileExcerpt, getRunFilePatch } from "../../../bridge";
import type { DiffChange, DiffFile, DiffLine, DiffScope } from "../../../types";
import { FileDiff, type PatchState } from "./FileDiff";
import { FileTree } from "./FileTree";
import type { DiffMode } from "./mode";
import { revealFor, type Gap } from "./rows";
import { buildTree, matchesFilter, treeOrder } from "./tree";

/** Patches loading at once; the rest wait their turn. */
const parallelPatches = 4;

/**
 * One loaded change: the file tree beside every file's diff. A new change
 * gets a new view, keyed by its commits, so nothing loaded for the old one
 * carries over.
 */
export function ChangeView({
  runID,
  scope,
  change,
  mode,
  showTree,
  filter,
  onFilter,
  isViewed,
  isOpen,
  onToggleOpen,
  onToggleViewed,
  onDiffWidth,
}: {
  runID: string;
  /** The scope the change was listed for. */
  scope: DiffScope;
  change: DiffChange;
  mode: DiffMode;
  showTree: boolean;
  filter: string;
  onFilter(filter: string): void;
  isViewed(file: DiffFile): boolean;
  isOpen(file: DiffFile): boolean;
  onToggleOpen(file: DiffFile): void;
  onToggleViewed(file: DiffFile): void;
  /** Reports the diff column's width, which decides whether Split fits. */
  onDiffWidth(width: number): void;
}) {
  // The branch's patches load from the two commits it listed, so they match
  // the list even when the branch moves on. A commit's load from the commit
  // itself, which still resolves once the branch is deleted, where a range
  // would not.
  const patchScope = useMemo<DiffScope>(
    () =>
      scope.kind === "commit"
        ? scope
        : { kind: "range", from: change.from, to: change.to },
    [scope, change.from, change.to],
  );
  const tree = useMemo(
    () => buildTree(change.files.filter((file) => matchesFilter(file, filter))),
    [change.files, filter],
  );
  const files = useMemo(() => treeOrder(tree), [tree]);

  // Keyed by path in Maps: a plain object would find "constructor" or
  // "__proto__" on its prototype.
  const [patches, setPatches] = useState<ReadonlyMap<string, PatchState>>(
    new Map(),
  );
  const [revealed, setRevealed] = useState<
    ReadonlyMap<string, ReadonlyMap<number, DiffLine>>
  >(new Map());
  const [totals, setTotals] = useState<ReadonlyMap<string, number>>(new Map());
  const setPatch = (path: string, state: PatchState) =>
    setPatches((all) => new Map(all).set(path, state));
  const queue = useRef<Array<{ path: string; anyway: boolean }>>([]);
  const running = useRef(0);
  const asked = useRef(new Set<string>());
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    const waiting = queue.current;
    const requested = asked.current;
    return () => {
      alive.current = false;
      // Patches not started yet are dropped, so a view that has gone reads
      // nothing more from git. StrictMode's rehearsal unmount keeps the
      // view, so the dropped files are forgotten too and ask again.
      const dropped = waiting.splice(0);
      if (dropped.length === 0) return;
      for (const { path } of dropped) requested.delete(path);
      setPatches((all) => {
        const next = new Map(all);
        for (const { path } of dropped) next.delete(path);
        return next;
      });
    };
  }, []);

  const pump = useCallback(() => {
    while (running.current < parallelPatches && queue.current.length > 0) {
      const { path, anyway } = queue.current.shift()!;
      running.current++;
      getRunFilePatch(runID, patchScope, path, anyway)
        .then((patch) => {
          if (alive.current) setPatch(path, { state: "loaded", patch });
        })
        .catch((reason: unknown) => {
          asked.current.delete(path);
          if (alive.current)
            setPatch(path, { state: "failed", message: String(reason) });
        })
        .finally(() => {
          running.current--;
          if (alive.current) pump();
        });
    }
  }, [runID, patchScope]);

  const load = useCallback(
    (path: string, anyway: boolean) => {
      // A file asks once it is near and open, and asks again only to load
      // a too-large patch anyway or after a failure.
      if (!anyway && asked.current.has(path)) return;
      asked.current.add(path);
      setPatch(path, { state: "loading" });
      queue.current.push({ path, anyway });
      pump();
    },
    [pump],
  );

  const reveal = useCallback(
    async (file: DiffFile, gap: Gap, direction: "up" | "down" | "all") => {
      const { oldStart, newStart, count } = revealFor(gap, direction);
      const excerpt = await getRunFileExcerpt(
        runID,
        patchScope,
        file.path,
        oldStart,
        newStart,
        count,
      );
      if (!alive.current) return;
      setRevealed((all) => {
        const lines = new Map(all.get(file.path));
        for (const line of excerpt.lines ?? [])
          if (line.new) lines.set(line.new, line);
        return new Map(all).set(file.path, lines);
      });
      setTotals((all) => new Map(all).set(file.path, excerpt.newTotal));
    },
    [runID, patchScope],
  );

  // The current file is the last one whose top has scrolled past the top of
  // the screen; j and k step from it, and the tree marks it.
  const sections = useRef(new Map<string, HTMLElement>());
  const [current, setCurrent] = useState<string>();
  useEffect(() => {
    let frame = 0;
    const update = () => {
      frame = 0;
      let low = 0;
      let high = files.length - 1;
      let found = 0;
      while (low <= high) {
        const middle = (low + high) >> 1;
        const top =
          sections.current.get(files[middle].path)?.getBoundingClientRect()
            .top ?? Infinity;
        if (top <= 1) {
          found = middle;
          low = middle + 1;
        } else high = middle - 1;
      }
      setCurrent(files[found]?.path);
    };
    const onScroll = () => {
      if (!frame) frame = window.requestAnimationFrame(update);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
      if (frame) window.cancelAnimationFrame(frame);
    };
  }, [files]);

  const scrollTo = useCallback((path: string) => {
    sections.current.get(path)?.scrollIntoView?.({ block: "start" });
    setCurrent(path);
  }, []);

  // Marking a file viewed collapses it; when its header had stuck to the
  // top of the screen, bring it back so the reader stays at that file.
  const keepInView = useRef<string | undefined>(undefined);
  const toggleViewed = useCallback(
    (file: DiffFile) => {
      const top = sections.current.get(file.path)?.getBoundingClientRect().top;
      if (top !== undefined && top < 0) keepInView.current = file.path;
      onToggleViewed(file);
    },
    [onToggleViewed],
  );
  useLayoutEffect(() => {
    const path = keepInView.current;
    if (!path) return;
    keepInView.current = undefined;
    sections.current.get(path)?.scrollIntoView?.({ block: "start" });
  });

  const selected = files.some((file) => file.path === current)
    ? current
    : files[0]?.path;
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey
      )
        return;
      const target = event.target instanceof Element ? event.target : null;
      if (
        target?.closest(
          "input:not([type=checkbox]):not([type=radio]), textarea, select, [contenteditable=''], [contenteditable='true']",
        ) ||
        document.querySelector("[role=dialog]")
      )
        return;
      const index = files.findIndex((file) => file.path === selected);
      if (event.key === "j" || event.key === "k") {
        const next = index + (event.key === "j" ? 1 : -1);
        if (next < 0 || next >= files.length) return;
        event.preventDefault();
        scrollTo(files[next].path);
      } else if (event.key === "v" && index >= 0) {
        event.preventDefault();
        toggleViewed(files[index]);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [files, selected, scrollTo, toggleViewed]);

  // The tree scrolls on its own, so it follows the current file.
  const treeBox = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const box = treeBox.current;
    const item = box?.querySelector('[aria-current="true"]');
    if (!box || !item) return;
    const outer = box.getBoundingClientRect();
    const inner = item.getBoundingClientRect();
    if (inner.top < outer.top) box.scrollTop -= outer.top - inner.top + 8;
    else if (inner.bottom > outer.bottom)
      box.scrollTop += inner.bottom - outer.bottom + 8;
  }, [selected]);

  // Split fits by the diff column's own width, which the tree takes from
  // only when it sits beside the diff.
  const column = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const element = column.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(([entry]) =>
      onDiffWidth(entry.contentRect.width),
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [onDiffWidth]);

  const [closed, setClosed] = useState<ReadonlySet<string>>(new Set());
  return (
    <div
      className={`grid min-w-0 items-start gap-3 ${showTree ? "grid-cols-[minmax(180px,236px)_minmax(0,1fr)] max-[700px]:grid-cols-1" : "grid-cols-1"}`}
    >
      {showTree && (
        <div
          ref={treeBox}
          className="sticky top-0 max-h-screen min-w-0 overflow-y-auto py-1 max-[700px]:static max-[700px]:max-h-[40vh]"
        >
          <FileTree
            nodes={tree}
            filter={filter}
            current={selected}
            closed={closed}
            isViewed={isViewed}
            onFilter={onFilter}
            onToggleFolder={(path) =>
              setClosed((all) => {
                const next = new Set(all);
                if (!next.delete(path)) next.add(path);
                return next;
              })
            }
            onSelect={scrollTo}
          />
        </div>
      )}
      <div ref={column} className="grid min-w-0 gap-3">
        {files.length === 0 && (
          <p className="m-0 text-xs text-muted">No files match the filter.</p>
        )}
        {files.map((file) => (
          <FileDiff
            key={file.path}
            file={file}
            mode={mode}
            open={isOpen(file)}
            viewed={isViewed(file)}
            patch={patches.get(file.path)}
            revealed={revealed.get(file.path)}
            total={totals.get(file.path)}
            sectionRef={(element) => {
              if (element) sections.current.set(file.path, element);
              else sections.current.delete(file.path);
            }}
            onToggleOpen={() => onToggleOpen(file)}
            onToggleViewed={() => toggleViewed(file)}
            onLoad={(anyway) => load(file.path, anyway)}
            onReveal={(gap, direction) => reveal(file, gap, direction)}
          />
        ))}
      </div>
    </div>
  );
}
