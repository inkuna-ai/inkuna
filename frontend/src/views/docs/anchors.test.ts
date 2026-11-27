import { anchorID, rehypeHeadingIDs, slug } from "./anchors";

type Node = Parameters<ReturnType<typeof rehypeHeadingIDs>>[0];

const heading = (text: string, id?: string): Node => ({
  type: "element",
  tagName: "h2",
  properties: id ? { id } : {},
  children: [{ type: "text", value: text }],
});

describe("doc anchors", () => {
  it("writes heading anchors the way links name them", () => {
    expect(slug("What a worker receives")).toBe("what-a-worker-receives");
    expect(slug("  Docs index: v1.2 (draft)  ")).toBe("docs-index-v12-draft");
    expect(slug("snake_case and Ünïcode")).toBe("snake_case-and-ünïcode");
  });

  it("points a fragment at the prefixed id, once", () => {
    expect(anchorID("install")).toBe("user-content-install");
    expect(anchorID("user-content-fn-1")).toBe("user-content-fn-1");
  });

  it("numbers repeated headings in document order and keeps ids already set", () => {
    const install = heading("Install");
    const again = heading("Install");
    const literal = heading("Install-1");
    const label = heading("Footnotes", "footnote-label");
    const nested: Node = {
      type: "element",
      tagName: "blockquote",
      children: [heading("Install")],
    };
    rehypeHeadingIDs()({
      type: "root",
      children: [install, again, literal, nested, label],
    });
    expect(
      [install, again, literal, nested.children![0], label].map(
        (node) => node.properties?.id,
      ),
    ).toEqual([
      "user-content-install",
      "user-content-install-1",
      "user-content-install-1-1",
      "user-content-install-2",
      "footnote-label",
    ]);
  });
});
