# Inkuna contributor guide

Before changing anything, read `.inkuna/skills/conventions/SKILL.md`. It holds this repository's conventions and is the single source of truth for every agent working on Inkuna: Codex, Claude Code, Copilot, and Inkuna's own workers. Change the conventions there; do not copy them into this file.

Task-specific skills live beside it in `.inkuna/skills/`. Read the one that matches the change:

- `inkuna-branch` for creating a branch and worktree
- `design-docs` for docs in `docs/`
- `wails-frontend` for the React view and Wails bindings
- `sqlite-migrations` for schema changes

`docs/inkuna-directory.md` explains how `.inkuna/` (rules, agents, and skills) is laid out.
