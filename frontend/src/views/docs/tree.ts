import type { DocEntry, DocFolder } from "../../types";

/** How each frontmatter status reads; any other value is shown as a warning. */
export const docStatusTones: Record<string, "green" | "neutral" | "muted"> = {
  current: "green",
  draft: "neutral",
  deprecated: "muted",
};

/** Narrows the tree; an empty value matches every doc. */
export interface DocFilter {
  /** A frontmatter status, or "problems" for docs Go flagged. */
  status: string;
  tag: string;
}

export const anyDoc: DocFilter = { status: "", tag: "" };

export function allDocs(folder: DocFolder): DocEntry[] {
  return [...folder.docs, ...folder.folders.flatMap(allDocs)];
}

/** The statuses and tags the tree's docs use, for the filter menus. */
export function facets(folder: DocFolder): {
  statuses: string[];
  tags: string[];
} {
  const docs = allDocs(folder);
  const sorted = (values: string[]) =>
    [...new Set(values.filter(Boolean))].sort();
  return {
    statuses: sorted(docs.map((doc) => doc.status)),
    tags: sorted(docs.flatMap((doc) => doc.tags ?? [])),
  };
}

export function matches(doc: DocEntry, filter: DocFilter): boolean {
  const status =
    filter.status === "" ||
    (filter.status === "problems"
      ? (doc.problems?.length ?? 0) > 0
      : doc.status === filter.status);
  return status && (filter.tag === "" || (doc.tags ?? []).includes(filter.tag));
}

/** The tree with only matching docs; folders left empty are dropped. */
export function filterTree(folder: DocFolder, filter: DocFilter): DocFolder {
  return {
    ...folder,
    docs: folder.docs.filter((doc) => matches(doc, filter)),
    folders: folder.folders
      .map((child) => filterTree(child, filter))
      .filter((child) => child.docs.length > 0 || child.folders.length > 0),
  };
}

export function findDoc(folder: DocFolder, path: string): DocEntry | undefined {
  return allDocs(folder).find((doc) => doc.path === path);
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

/** Where a link inside a doc leads. */
export type DocLink =
  | { kind: "external"; url: string }
  | { kind: "doc"; path: string; fragment: string }
  | { kind: "anchor"; fragment: string }
  | { kind: "outside" };

const scheme = /^[a-z][a-z0-9+.-]*:/i;

/**
 * Resolves a link the way a repository host renders it: relative to the
 * doc's own folder. A result that climbs above the docs root, starts at the
 * repository root, or is not a Markdown file stays outside the view. This
 * only picks the target; Go decides whether it may be read.
 */
export function resolveLink(from: string, href: string): DocLink {
  if (href.startsWith("#")) return { kind: "anchor", fragment: href.slice(1) };
  if (scheme.test(href)) {
    return /^(https?|mailto):/i.test(href)
      ? { kind: "external", url: href }
      : { kind: "outside" };
  }
  if (href === "" || href.startsWith("/")) return { kind: "outside" };
  const [target, fragment = ""] = href.split("#", 2);
  let decoded: string;
  try {
    decoded = decodeURIComponent(target.split("?", 1)[0]);
  } catch {
    return { kind: "outside" };
  }
  const segments = from.split("/").slice(0, -1);
  for (const segment of decoded.split("/")) {
    if (segment === "" || segment === ".") continue;
    if (segment === "..") {
      if (segments.length === 0) return { kind: "outside" };
      segments.pop();
    } else {
      segments.push(segment);
    }
  }
  const path = segments.join("/");
  if (decoded.endsWith("/") || !/\.md$/i.test(path)) return { kind: "outside" };
  return { kind: "doc", path, fragment };
}
