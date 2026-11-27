import type { DiffFile } from "../../../types";
import { buildTree, matchesFilter, treeOrder, type TreeNode } from "./tree";

const file = (path: string, fields: Partial<DiffFile> = {}): DiffFile => ({
  path,
  status: "modified",
  additions: 1,
  deletions: 0,
  ...fields,
});

function outline(nodes: TreeNode[], depth = 0): string[] {
  return nodes.flatMap((node) => [
    `${"  ".repeat(depth)}${node.kind === "folder" ? `${node.name}/` : node.name}`,
    ...(node.kind === "folder" ? outline(node.children, depth + 1) : []),
  ]);
}

describe("the changed-file tree", () => {
  it("groups files by folder, folders first, and joins single-folder chains", () => {
    const tree = buildTree([
      file("README.md"),
      file("internal/app/changes.go"),
      file("internal/app/setup.go"),
      file("frontend/src/views/tasks/changes/tree.ts"),
      file("internal/diff/load.go"),
    ]);
    expect(outline(tree)).toEqual([
      "frontend/src/views/tasks/changes/",
      "  tree.ts",
      "internal/",
      "  app/",
      "    changes.go",
      "    setup.go",
      "  diff/",
      "    load.go",
      "README.md",
    ]);
    expect(treeOrder(tree).map((entry) => entry.path)).toEqual([
      "frontend/src/views/tasks/changes/tree.ts",
      "internal/app/changes.go",
      "internal/app/setup.go",
      "internal/diff/load.go",
      "README.md",
    ]);
  });

  it("matches every word of the filter against the path or a rename's old path", () => {
    const renamed = file("src/new_name.go", { oldPath: "src/old_name.go" });
    expect(matchesFilter(renamed, "OLD")).toBe(true);
    expect(matchesFilter(renamed, "src new")).toBe(true);
    expect(matchesFilter(renamed, "src lib")).toBe(false);
    expect(matchesFilter(renamed, "  ")).toBe(true);
  });
});
