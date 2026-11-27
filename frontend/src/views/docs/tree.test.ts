import type { DocEntry, DocFolder } from "../../types";
import { facets, filterTree, findDoc, resolveLink } from "./tree";

const entry = (path: string, extra: Partial<DocEntry> = {}): DocEntry => ({
  path,
  title: "",
  lastUpdated: "",
  author: "",
  tags: [],
  status: "",
  ...extra,
});

const tree: DocFolder = {
  name: "",
  path: "",
  docs: [entry("README.md", { status: "current", tags: ["intro"] })],
  folders: [
    {
      name: "guides",
      path: "guides",
      docs: [
        entry("guides/setup.md", { status: "draft", tags: ["setup"] }),
        entry("guides/todo.md", { problems: ["missing frontmatter"] }),
      ],
      folders: [],
    },
  ],
};

describe("doc tree helpers", () => {
  it("lists the statuses and tags in use", () => {
    expect(facets(tree)).toEqual({
      statuses: ["current", "draft"],
      tags: ["intro", "setup"],
    });
  });

  it("filters docs and drops folders left empty", () => {
    expect(filterTree(tree, { status: "current", tag: "" }).folders).toEqual(
      [],
    );
    const flagged = filterTree(tree, { status: "problems", tag: "" });
    expect(flagged.docs).toEqual([]);
    expect(flagged.folders[0].docs.map((doc) => doc.path)).toEqual([
      "guides/todo.md",
    ]);
    expect(
      filterTree(tree, { status: "", tag: "setup" }).folders[0].docs,
    ).toHaveLength(1);
    expect(findDoc(tree, "guides/setup.md")?.status).toBe("draft");
    expect(findDoc(tree, "guides/missing.md")).toBeUndefined();
  });

  it("resolves links against the doc's folder", () => {
    expect(resolveLink("guides/setup.md", "../README.md#start")).toEqual({
      kind: "doc",
      path: "README.md",
      fragment: "start",
    });
    expect(resolveLink("guides/setup.md", "./deep/My%20Notes.md")).toEqual({
      kind: "doc",
      path: "guides/deep/My Notes.md",
      fragment: "",
    });
    expect(resolveLink("README.md", "#usage")).toEqual({
      kind: "anchor",
      fragment: "usage",
    });
    expect(resolveLink("README.md", "https://example.com/a")).toEqual({
      kind: "external",
      url: "https://example.com/a",
    });
    for (const href of [
      "../internal/app.go",
      "../outside.md",
      "/docs/README.md",
      "guides/",
      "file:///etc/passwd",
      "%E0%A4%A.md",
      "",
    ]) {
      expect(resolveLink("README.md", href)).toEqual({ kind: "outside" });
    }
  });
});
