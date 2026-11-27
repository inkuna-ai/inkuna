import type { DiffFile } from "../../../types";
import { inputClass } from "../../common";
import { LineCounts, StatusBadge } from "./FileDiff";
import type { TreeNode } from "./tree";

/**
 * The changed files grouped by folder, with a filter that narrows the tree
 * and the diff alike. Choosing a file scrolls the diff to it.
 */
export function FileTree({
  nodes,
  filter,
  current,
  closed,
  isViewed,
  onFilter,
  onToggleFolder,
  onSelect,
}: {
  nodes: TreeNode[];
  filter: string;
  current?: string;
  /** Folders the reader closed, by path. */
  closed: ReadonlySet<string>;
  isViewed(file: DiffFile): boolean;
  onFilter(filter: string): void;
  onToggleFolder(path: string): void;
  onSelect(path: string): void;
}) {
  return (
    <nav aria-label="Changed files" className="grid min-w-0 gap-2">
      <input
        type="search"
        aria-label="Filter changed files"
        placeholder="Filter files…"
        value={filter}
        onChange={(event) => onFilter(event.target.value)}
        className={`${inputClass} py-1.5 text-xs`}
      />
      {nodes.length === 0 ? (
        <p className="m-0 px-1 text-xs text-muted">No files match.</p>
      ) : (
        <Branch
          nodes={nodes}
          depth={0}
          current={current}
          closed={closed}
          isViewed={isViewed}
          onToggleFolder={onToggleFolder}
          onSelect={onSelect}
        />
      )}
    </nav>
  );
}

function Branch({
  nodes,
  depth,
  current,
  closed,
  isViewed,
  onToggleFolder,
  onSelect,
}: {
  nodes: TreeNode[];
  depth: number;
  current?: string;
  closed: ReadonlySet<string>;
  isViewed(file: DiffFile): boolean;
  onToggleFolder(path: string): void;
  onSelect(path: string): void;
}) {
  const indent = { paddingLeft: `${depth * 12 + 4}px` };
  const row =
    "flex w-full min-w-0 cursor-pointer items-center gap-1.5 rounded border-0 bg-transparent py-1 pr-1 text-left text-xs";
  return (
    <ul className="m-0 grid list-none gap-px p-0">
      {nodes.map((node) =>
        node.kind === "folder" ? (
          <li key={node.path} className="min-w-0">
            <button
              type="button"
              aria-expanded={!closed.has(node.path)}
              className={`${row} text-ink-soft hover:text-ink-strong`}
              style={indent}
              title={node.path}
              onClick={() => onToggleFolder(node.path)}
            >
              <span aria-hidden="true" className="w-3 flex-none text-faint">
                {closed.has(node.path) ? "▸" : "▾"}
              </span>
              <span className="min-w-0 truncate">{node.name}/</span>
            </button>
            {!closed.has(node.path) && (
              <Branch
                nodes={node.children}
                depth={depth + 1}
                current={current}
                closed={closed}
                isViewed={isViewed}
                onToggleFolder={onToggleFolder}
                onSelect={onSelect}
              />
            )}
          </li>
        ) : (
          <li key={node.path} className="min-w-0">
            <button
              type="button"
              aria-current={current === node.path ? "true" : undefined}
              className={`${row} ${
                current === node.path
                  ? "bg-raised text-ink-strong shadow-[inset_2px_0_0_var(--color-accent)]"
                  : "text-ink hover:bg-raised"
              }`}
              style={indent}
              title={node.path}
              onClick={() => onSelect(node.path)}
            >
              <StatusBadge status={node.file.status} />
              <span
                className={`min-w-0 flex-1 truncate ${isViewed(node.file) ? "text-muted" : ""}`}
              >
                {node.name}
              </span>
              {isViewed(node.file) && (
                <span role="img" aria-label="viewed" className="text-ok">
                  ✓
                </span>
              )}
              <LineCounts file={node.file} />
            </button>
          </li>
        ),
      )}
    </ul>
  );
}
