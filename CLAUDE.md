# Inkuna contributor guide

Inkuna's conventions live in `.inkuna/`, the same directory Inkuna reads for any project, so one set of rules governs every agent: Claude Code through this file, Codex and other tools through `AGENTS.md`, and Inkuna's own workers through the `conventions` skill that `.inkuna/rules.yaml` attaches to every agent step. Change the conventions there, never here.

@.inkuna/skills/conventions/SKILL.md

Task-specific skills live beside it in `.inkuna/skills/`: `inkuna-branch` for creating a branch and worktree, `design-docs` for docs in `docs/`, `wails-frontend` for the React view and Wails bindings, and `sqlite-migrations` for schema changes. Read the one that matches the change. `docs/inkuna-directory.md` explains the layout.
