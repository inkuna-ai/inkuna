---
name: design-docs
description: Writing and updating Inkuna's design docs in docs/, covering the frontmatter contract, Mermaid diagrams, README links, and the self-test that enforces them.
---

# Design docs

Read the code before writing about it. A doc describes what the code does today, naming the packages, types, and functions (`internal/docs.Discover`) a reader would open next.

## Frontmatter

Every Markdown file under `docs/` starts with this header. `internal/docs/self_test.go` fails `task test` when one breaks it. A key the contract does not name is ignored, so keep to these.

```markdown
---
title: Rules engine
lastUpdated: 2026-09-25
author: Brandon Manning
tags: [rules, planning]
status: current
---
```

- `title`: what the doc is about, shown in place of the file name.
- `lastUpdated`: the `YYYY-MM-DD` date of this change. Bump it whenever the content changes.
- `author`: the human who approves the change, never the agent that wrote it. Keep the existing author unless someone else is approving.
- `tags`: a non-empty list.
- `status`: `draft`, `current`, or `deprecated`.

The full contract is in `docs/docs-index.md`.

## Shape

- Open with one paragraph that says what the thing is and why it exists.
- Use tables for fields and options (`| Field | Required | Meaning |`), and fenced YAML or JSON for real examples that would pass validation.
- Use Mermaid for structure: `flowchart` for components and decisions, `sequenceDiagram` for interactions over time, `stateDiagram-v2` for lifecycles. Label edges with the call or data that crosses them.
- Every diagram must draw. Never put `;` in a sequence diagram's message, since Mermaid reads it as the end of the statement; use a comma or parentheses. `frontend/src/views/docs/diagrams.test.ts` parses every diagram in `docs/` and fails, naming the file and line, when one does not.
- State the invariants later work must keep, and where Go enforces each one.
- Link related docs with relative links (`[architecture.md](architecture.md#trust-boundaries)`).

## Placement

- A new doc goes in `docs/` and is linked from the **Documentation** list in `README.md`.
- A behaviour change updates the existing doc that describes it in the same change, rather than adding a new one.
