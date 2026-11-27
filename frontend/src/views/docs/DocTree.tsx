import type { DocEntry, DocFolder } from "../../types";
import { Chip } from "../common";
import { baseName, docStatusTones } from "./tree";

interface TreeProps {
  selected: string;
  collapsed: ReadonlySet<string>;
  onToggle(folderPath: string): void;
  onSelect(docPath: string): void;
}

const indent = (depth: number) => ({ paddingLeft: `${8 + depth * 14}px` });

/** The docs folder as a collapsible tree, folders before docs at each level. */
export function DocTree({ tree, ...props }: TreeProps & { tree: DocFolder }) {
  return (
    <ul aria-label="Docs" className="m-0 grid list-none gap-px p-0">
      <FolderItems folder={tree} depth={0} {...props} />
    </ul>
  );
}

function FolderItems({
  folder,
  depth,
  ...props
}: TreeProps & { folder: DocFolder; depth: number }) {
  return (
    <>
      {folder.folders.map((child) => {
        const open = !props.collapsed.has(child.path);
        return (
          <li key={child.path} className="min-w-0">
            <button
              type="button"
              aria-expanded={open}
              style={indent(depth)}
              className="flex w-full min-w-0 cursor-pointer items-center gap-1.5 rounded-sm border-0 bg-transparent py-1.5 pr-2 text-left text-xs text-muted hover:text-ink-strong"
              onClick={() => props.onToggle(child.path)}
            >
              <span aria-hidden="true" className="w-3 flex-none font-mono">
                {open ? "▾" : "▸"}
              </span>
              <span className="min-w-0 truncate font-mono">{child.name}/</span>
            </button>
            {open && (
              <ul
                aria-label={child.path}
                className="m-0 grid list-none gap-px p-0"
              >
                <FolderItems folder={child} depth={depth + 1} {...props} />
              </ul>
            )}
          </li>
        );
      })}
      {folder.docs.map((doc) => (
        <li key={doc.path} className="min-w-0">
          <DocItem
            doc={doc}
            depth={depth}
            selected={props.selected === doc.path}
            onSelect={props.onSelect}
          />
        </li>
      ))}
    </>
  );
}

function DocItem({
  doc,
  depth,
  selected,
  onSelect,
}: {
  doc: DocEntry;
  depth: number;
  selected: boolean;
  onSelect(docPath: string): void;
}) {
  const problems = doc.problems ?? [];
  return (
    <button
      type="button"
      aria-current={selected ? "page" : undefined}
      title={doc.title || doc.path}
      style={indent(depth)}
      className={`flex w-full min-w-0 cursor-pointer items-center gap-2 rounded-sm border-0 py-1.5 pr-2 text-left text-xs ${
        selected
          ? "bg-raised text-ink-strong"
          : "bg-transparent text-ink-soft hover:text-ink-strong"
      }`}
      onClick={() => onSelect(doc.path)}
    >
      <span aria-hidden="true" className="w-3 flex-none" />
      <span className="min-w-0 flex-1 truncate font-mono">
        {baseName(doc.path)}
      </span>
      {problems.length > 0 && (
        <span
          role="img"
          aria-label={`Problems: ${problems.join("; ")}`}
          title={problems.join("\n")}
          className="flex-none text-warn"
        >
          ⚠
        </span>
      )}
      {doc.status && (
        <Chip tone={docStatusTones[doc.status] ?? "warn"}>{doc.status}</Chip>
      )}
    </button>
  );
}
