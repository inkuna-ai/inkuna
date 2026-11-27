/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import mermaid from "mermaid";

// The repository's own docs, as written. The Docs view draws their diagrams
// with this mermaid, and GitHub draws them too. A diagram that does not parse
// shows only as an error in both, and nothing else checks diagrams:
// `inkunactl docs validate` reads frontmatter and links. A message in a
// sequence diagram that contains ";" is the usual cause, since Mermaid reads
// it as the end of the statement (#207). The files are read from disk because
// Vite serves nothing outside frontend/.
// A string, not new URL(): under jsdom the global URL is not Node's.
const repository = resolve(
  dirname(fileURLToPath(import.meta.url)),
  "../../../..",
);
const docs = readdirSync(join(repository, "docs"), { recursive: true })
  .map(String)
  .filter((name) => name.endsWith(".md"))
  .map((name) => {
    const path = join(repository, "docs", name);
    return [relative(repository, path), readFileSync(path, "utf8")] as const;
  });

interface Diagram {
  where: string;
  source: string;
}

/** Every ```mermaid block in a Markdown source, with the line it opens on. */
function diagrams(path: string, markdown: string): Diagram[] {
  const lines = markdown.split("\n");
  const found: Diagram[] = [];
  for (let index = 0; index < lines.length; index++) {
    if (lines[index].trim() !== "```mermaid") continue;
    const opening = index + 1;
    const body: string[] = [];
    for (
      index++;
      index < lines.length && lines[index].trim() !== "```";
      index++
    ) {
      body.push(lines[index]);
    }
    found.push({ where: `${path}:${opening}`, source: body.join("\n") });
  }
  return found;
}

describe("the docs' Mermaid diagrams", () => {
  it("all parse", async () => {
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
    const all = docs.flatMap(([path, markdown]) => diagrams(path, markdown));
    // A glob that matched nothing would pass without checking anything.
    expect(all.length).toBeGreaterThan(20);
    const broken: string[] = [];
    for (const diagram of all) {
      try {
        await mermaid.parse(diagram.source);
      } catch (reason) {
        const first = String((reason as Error)?.message ?? reason).split(
          "\n",
        )[0];
        broken.push(`${diagram.where}: ${first}`);
      }
    }
    expect(broken, "fix these diagrams").toEqual([]);
  });

  it("are found with the line they open on", () => {
    const markdown =
      "# Doc\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\ntext\n```mermaid\nsequenceDiagram\n```\n";
    expect(diagrams("docs/x.md", markdown)).toEqual([
      { where: "docs/x.md:3", source: "flowchart LR\n  A --> B" },
      { where: "docs/x.md:9", source: "sequenceDiagram" },
    ]);
  });

  it("fail when a sequence message holds a semicolon", async () => {
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
    await expect(
      mermaid.parse("sequenceDiagram\n  A->>B: one; two"),
    ).rejects.toThrow();
  });
});
