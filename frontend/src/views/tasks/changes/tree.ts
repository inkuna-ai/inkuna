import type { DiffFile } from "../../../types";

/** A folder or a file in the changed-file tree. */
export type TreeNode =
  | { kind: "folder"; path: string; name: string; children: TreeNode[] }
  | { kind: "file"; path: string; name: string; file: DiffFile };

/**
 * Whether a file matches the filter: every word of it appears in the path
 * or, for a rename or copy, the old path, ignoring case.
 */
export function matchesFilter(file: DiffFile, filter: string): boolean {
  const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
  const paths = `${file.path}\n${file.oldPath ?? ""}`.toLowerCase();
  return words.every((word) => paths.includes(word));
}

interface Folder {
  folders: Map<string, Folder>;
  files: DiffFile[];
}

/**
 * Groups files by folder, folders before files and each by name. A folder
 * whose only entry is another folder joins with it, as "src/app", so a deep
 * path does not take a row per level.
 */
export function buildTree(files: DiffFile[]): TreeNode[] {
  const root: Folder = { folders: new Map(), files: [] };
  for (const file of files) {
    const parts = file.path.split("/");
    let folder = root;
    for (const part of parts.slice(0, -1)) {
      let next = folder.folders.get(part);
      if (!next) {
        next = { folders: new Map(), files: [] };
        folder.folders.set(part, next);
      }
      folder = next;
    }
    folder.files.push(file);
  }
  return nodesOf(root, "");
}

function nodesOf(folder: Folder, prefix: string): TreeNode[] {
  const byName = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
  const nodes: TreeNode[] = [];
  for (const name of [...folder.folders.keys()].sort(byName)) {
    let joined = name;
    let inner = folder.folders.get(name)!;
    while (inner.files.length === 0 && inner.folders.size === 1) {
      const [only, next] = [...inner.folders][0];
      joined = `${joined}/${only}`;
      inner = next;
    }
    const path = prefix + joined;
    nodes.push({
      kind: "folder",
      path,
      name: joined,
      children: nodesOf(inner, `${path}/`),
    });
  }
  const files = [...folder.files].sort((a, b) => byName(a.path, b.path));
  for (const file of files)
    nodes.push({
      kind: "file",
      path: file.path,
      name: file.path.slice(prefix.length),
      file,
    });
  return nodes;
}

/** The files in the order the tree shows them, which the diff list follows. */
export function treeOrder(nodes: TreeNode[]): DiffFile[] {
  return nodes.flatMap((node) =>
    node.kind === "file" ? [node.file] : treeOrder(node.children),
  );
}
