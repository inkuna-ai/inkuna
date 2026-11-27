---
name: inkuna-branch
description: Creates the branch, and the worktree when enabled, for a story or change in this repository by its branching configuration (.inkuna/branching.yaml), whichever tracker the stories come from. Inkuna workers never create branches; this is for people and interactive agents.
---

# Inkuna branch

Branches in this repository are named and placed by `.inkuna/branching.yaml`: the tracker stories come from, the base branch, the name pattern, the allowed types, and whether work happens in a worktree, where it is created, and how it is cleaned up. Inkuna's runs and `inkunactl branch new` read the same file, so a branch made by hand matches one made by a run.

In this repository run inkunactl as `go run ./cmd/inkunactl`; elsewhere use an installed `inkunactl`.

## If you are an Inkuna worker

Inkuna created your branch and working directory from this configuration before your step started. Do not create, rename, switch, or push branches, and do not add worktrees. Work where you are; Inkuna commits your step.

## Creating a branch by hand

1. Read `.inkuna/branching.yaml`. If it is missing or `inkunactl branch new` reports missing keys, run `inkunactl branch init` to see a proposal drawn from the repository's branches. Show it to the user and only run `inkunactl branch init --write` once they agree.
2. Get the story from the configured `tracker`, with whatever tool this session has for it:
   - `linear`: the Linear issue (key like `MAD-12`).
   - `jira`: the Jira issue (key like `PROJ-12`).
   - `github`: the GitHub issue (`gh issue view <number>`); the id is the number.
   - `none`: there is no story id; use the user's description.
   If the tracker tool fails, tell the user and stop rather than guessing the story.
3. Choose the type from `branch.types`. The usual meanings:
   - `feature`: new functionality or an improvement to existing functionality.
   - `bug`: a fix for a defect.
   - `chore`: maintenance or housekeeping.
   - `refactor`: better structure or design without a change in behaviour.
   - `hotfix`: a critical fix that must ship immediately.
   - `enabler`: docs, coverage, tooling, or release-gate work that unlocks later work.
   If the type is genuinely ambiguous, ask.
4. Write a short description for the slug. The whole name must fit `branch.maxLength` when one is set; shorten the description, never the story id.
5. Run:

   ```sh
   inkunactl branch new --type <type> [--story <id>] <short description>
   ```

   It fetches `origin/<base>`, creates the branch from it, and, with worktrees enabled, checks it out in a new worktree under `worktrees.location` without touching the current checkout. With worktrees disabled it switches the current checkout to the new branch. Add `--push` only when the user asks for the branch to be published.
6. Report the branch name, the worktree path (or the checkout), and the remote URL when pushed. Do all further work for the story there.

If `inkunactl` refuses (an unknown type, a malformed story id, a name over the cap, a branch that already exists), fix the input it names rather than working around it.
