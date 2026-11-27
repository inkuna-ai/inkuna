/**
 * Anchors follow the repository host's convention: a rendered doc's ids
 * carry this prefix, which GFM already gives footnotes, so a doc cannot
 * collide with the app's own ids, and a link names a heading without it.
 */
const prefix = "user-content-";

/** The element id a link's fragment points at. */
export function anchorID(fragment: string): string {
  return fragment.startsWith(prefix) ? fragment : prefix + fragment;
}

/** A heading's anchor as repository hosts write it in links. */
export function slug(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{M}\p{N}\p{Pc}\s-]/gu, "")
    .replace(/\s/g, "-");
}

/** The parts of a hast node the heading pass reads. */
interface HastNode {
  type: string;
  tagName?: string;
  value?: string;
  properties?: Record<string, unknown>;
  children?: HastNode[];
}

function textOf(node: HastNode): string {
  if (node.type === "text") return node.value ?? "";
  return (node.children ?? []).map(textOf).join("");
}

/**
 * Takes the first free anchor for a heading, numbering repeats -1, -2, …
 * in document order the way repository hosts do, so a link to a repeated
 * heading lands on the one it names.
 */
function unique(base: string, taken: Map<string, number>): string {
  let anchor = base;
  while (taken.has(anchor)) {
    const count = (taken.get(base) ?? 0) + 1;
    taken.set(base, count);
    anchor = `${base}-${count}`;
  }
  taken.set(anchor, 0);
  return anchor;
}

/**
 * A rehype plugin that gives every heading without an id its anchor. Ids
 * GFM already set, such as the footnote section's label, are kept.
 */
export function rehypeHeadingIDs() {
  return (tree: HastNode) => {
    const taken = new Map<string, number>();
    const visit = (node: HastNode) => {
      if (node.type === "element" && /^h[1-6]$/.test(node.tagName ?? "")) {
        node.properties ??= {};
        if (node.properties.id === undefined)
          node.properties.id = prefix + unique(slug(textOf(node)), taken);
      }
      node.children?.forEach(visit);
    };
    visit(tree);
  };
}
