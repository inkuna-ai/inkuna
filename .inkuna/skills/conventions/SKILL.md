---
name: conventions
description: Inkuna's repository conventions (architecture boundaries, safety model, configuration and secrets, code and test gates, docs, and the change workflow). Every change to Inkuna follows them, whichever agent or person makes it.
---

# Inkuna conventions

These rules apply to every change in this repository. They are the single source of truth: `CLAUDE.md` and `AGENTS.md` point here, and Inkuna's own rules attach this skill to every agent step.

## Architecture

- Inkuna is a local Wails v2 desktop app. Go owns the domain, the application, and the adapters; React is a view over the Wails bindings. Codex, Claude, Copilot, and any runtime declared in `inkuna.yaml` share one runtime contract and run headlessly.
- Keep domain contracts (`internal/domain`) independent from SQLite, subprocesses, GitHub, Wails, and provider-specific formats.
- Planning is rules first: `.inkuna/rules.yaml` shapes the DAG, the agent registry (`internal/agent`) supplies candidates, and the orchestrator (a headless model) staffs `auto` stages.
- Go is the only validator. Every model decision and every edit sent from the view is checked in Go, with a deterministic fallback when a model is unavailable or wrong. The view shows Go's reason; it never keeps a second copy of a rule Go enforces.
- Agents talk only through the in-process A2A bus (`internal/a2a`): Agent Cards, Tasks, Messages, Parts, Artifacts. Workers get selected skills and compact, artifact-backed handoffs, never other workers' transcripts. Human input is an A2A task to the `human` agent, answered from the inbox.
- Planners score agent steps from 1 to 10; versioned routing (`internal/routing/policy.v2.json`) selects a model and reasoning effort inside the user-selected runtime, and explicit overrides are preserved.

## Safety

- Default to least privilege. No shell subprocesses: commands are argv arrays run directly, never through `sh -c`. No provider permission bypasses. A worker starts only the MCP servers `inkuna.yaml` declares and its agent or stage is granted, or the user's own when they opt a runtime in, never one implicitly. A server's secrets reach the server, never the model.
- Linear and GitHub issues are intake sources, and write-back targets only as far as the repository's `.inkuna/tracker.yaml` allows. Go makes every tracker write with the project's credential; an agent only asks, and its changes wait for a person's approval. A server of the user's own that they opt a runtime in to is the one exception: it is theirs, with every tool, and the approval screen says nothing reviews what it changes. GitHub is the approval-gated execution projection. See `docs/mcp-and-trackers.md`.
- Inkuna commits each writable step on the run's branch locally and never pushes. GitHub writes, pushes, pull requests, integration, and template saving stay approval-gated.
- Worktrees are removed only when the repository's branching configuration chooses it (`listener`, `prune`, or `merged`, once the branch's pull request merged) or the user does it from the run view, by deleting the worktree's branch from the Branches view, or by asking for it when deleting a finished task or run, or, for one that delete kept, from Settings' Disk usage. Removal takes only a clean worktree, and cleanup never deletes a branch. Only the user deletes one, and only one without an upstream that is not the base, at the commit they confirmed.
- Deleting history takes only finished tasks and runs, refused in Go, and never touches GitHub or Linear. With worktrees off, a run commits in the checkout only when it is clean and not on the base branch.

## Configuration and secrets

- Configuration lives in `inkuna.yaml`; environment variables override it.
- Secret values live only in the OS keychain or the environment, never in YAML or SQLite. Only declared secrets are exported to worker processes.
- File formats Inkuna reads (config, rules, branching, tracker, review) decode strictly and reject unknown keys. Keep new formats strict. Markdown frontmatter (agents, skills, docs) is the exception: the same files serve other tools, so a key Inkuna does not know is ignored, and an agent keeps it when Inkuna saves the agent.
- Files Inkuna writes (`inkuna.yaml`, rules, agents) are replaced atomically: write a temporary file in the same directory, then rename it.

## Code

- Wrap errors with what was being done: `fmt.Errorf("load rules: %w", err)`.
- Comments explain why the code is the way it is, not what the next line does. Exported identifiers get a doc comment.
- New behaviour comes with tests next to the code it covers: `*_test.go` in the same package, `*.test.ts(x)` beside the component.
- Keep changes focused on the objective. Do not reformat or refactor unrelated code.

## Verification

- `task fmt`, `task lint`, `task test`, and `task build`. `task ci` is the handoff gate and is what CI runs on macOS, Ubuntu, and Windows.
- A change that touches only `docs/`, the Markdown files in the repository root, or `NOTICE` skips that CI run. `.github/workflows/docs.yml` then runs only the checks that read the docs: the frontmatter contract and the diagrams. Markdown under `.inkuna/` still runs the full CI.
- `ui/dist` is built, never committed, and embedded by `main.go`. `task build`, `task ci`, and `task package` build it before Go needs it; without it Go still compiles, but the shipped-page test skips and the app refuses to start.

## Docs

- Design docs with Mermaid diagrams live in `docs/`. Every doc carries the frontmatter contract in `docs/docs-index.md`, and `internal/docs/self_test.go` fails the build when one does not.
- A change in behaviour updates the doc that describes it in the same change.

## The `.inkuna` directory

- `.inkuna/` holds this repository's rules, agents, skills, and branching configuration, in exactly the format Inkuna reads for any project (see `docs/inkuna-directory.md`). Inkuna plans its own work from it.
- Inkuna reads `.inkuna/` from the checkout it plans from, not from a task's worktree, so an edit takes effect only once it is merged. Review changes here like code: a rule can change which agents run and what they may do.
- `internal/planner/self_test.go` plans every flow in `.inkuna/rules.yaml` and fails the build if one would not plan, if an agent step would not receive this skill, or if a repository agent would take writable stages from `implementer` when the orchestrator is unavailable.
- `internal/branching/self_test.go` fails the build if `.inkuna/branching.yaml` is incomplete or invalid.
- `internal/diff/self_test.go` fails the build if `.inkuna/review.yaml` is invalid.
- `internal/tracker/self_test.go` fails the build if `.inkuna/tracker.yaml` is invalid.
- Agent outputs go under `.inkuna/out/`, which is ignored.

## Change workflow

Inkuna workers stop at the local commit on their task branch. People and interactive agents working on Inkuna directly follow the rest:

- One GitHub issue per change. Finish and merge it before starting the next; never stack more than two open pull requests.
- Label every issue with exactly one `type:` label (`epic`, `feature`, `bug`, `spike`, `design-doc`), an `epic:` label when it belongs to an epic, and one or more `area:` labels (`runtime`, `planning`, `rules`, `engine`, `context`, `storage`, `worktree`, `ui`, `inkunactl`, `config`). Never the plain `enhancement` or `bug` labels.
- Create branches with the `inkuna-branch` skill (`inkunactl branch new`), which names and places them by `.inkuna/branching.yaml`: `<type>/<issue>-<slug>`, or `<type>/<slug>` when there is no issue, from `origin/main`, in `../inkuna-worktrees/`.
- The pull request body opens with `Closes #<issue>.` (and `Part of #<epic>.`), then a **What** section of bullets and a **Verified** section naming `task ci` and the tests added. Pull requests are squash-merged under their title.
