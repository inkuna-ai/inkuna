<img src="build/appicon.svg" alt="" width="96" align="left" />

# Inkuna

Inkuna is a local, cross-platform desktop harness for running several coding agents on one repository. A request becomes a workflow shaped by declarative rules, an orchestrator staffs each stage with a specialized agent, and Codex CLI, Claude Code, or GitHub Copilot CLI run headlessly inside isolated git worktrees. Agents talk to each other and to you through an in-process bus modelled on the Agent2Agent (A2A) protocol. SQLite stores structured state; large traces, handoffs, diffs, and agent outputs live in a content-addressed artifact store shared by every agent.

Documentation:

- [Architecture](docs/architecture.md) with component, sequence, state, and data-model diagrams
- [The A2A protocol](docs/a2a.md) and how Inkuna implements it
- [Installing Inkuna](docs/install.md): `inkunactl install`, the prerequisites on each platform, and where the app and CLI go
- [Configuration](docs/configuration.md): `inkuna.yaml`, environment overrides, keychain secrets, the setup wizard
- [Agents](docs/agents.md): authoring specialized agents and the handoff contract
- [Rules](docs/rules.md): authoring `.inkuna/rules.yaml`
- [Feedback rounds](docs/feedback-loop.md): sending a run's work back to the stages that wrote it, at a review gate or after it ends
- [MCP servers and tracker write-back](docs/mcp-and-trackers.md): granting workers MCP servers, and writing back to a run's Linear or GitHub issue
- [The .inkuna directory](docs/inkuna-directory.md): what a repository's `.inkuna/` holds, how its conventions reach every runtime, and Inkuna's own
- [Branching and worktrees](docs/branching.md): `.inkuna/branching.yaml`, branch naming per tracker, worktree location and cleanup, `inkunactl branch`
- [Docs index](docs/docs-index.md): the frontmatter contract for project documentation and how Inkuna discovers it
- [Extension API](docs/extensions.md): how another Go module embeds Inkuna and plugs in sinks, policies, identity, and definition sources through `pkg/inkuna` and `pkg/extension`

## Install

Continuous integration runs every check, and installs the app, on macOS, Ubuntu, and Windows.

Each [release](https://github.com/inkuna-ai/inkuna/releases) has a macOS disk image, a Windows installer, and a Linux archive, which need nothing else installed ([Downloading a release](docs/install.md#downloading-a-release)). To build it yourself instead, with Git and Go 1.25+ installed, one line installs Inkuna:

```sh
git clone https://github.com/inkuna-ai/inkuna.git && cd inkuna && go run ./cmd/inkunactl install
```

The wizard says what each step does and asks first:

1. **Prerequisites.** It checks Git, Go 1.25+, Node 22+ with npm, Task, the platform's desktop libraries (GTK 3 and WebKitGTK 4.1 on Linux, the WebView2 runtime on Windows), a signed-in GitHub CLI, and at least one signed-in provider CLI: Codex CLI, Claude Code, or GitHub Copilot CLI. It gives your platform's install command for anything missing, runs a user-level install such as `brew`, `winget`, or `npm -g` only when you agree, and shows `sudo` commands for you to run.
2. **Configuration.** When there is no `inkuna.yaml`, it runs the setup wizard (below). An existing one is left as it is.
3. **Build.** It builds the app and `inkunactl` from the clone you ran it in, or from a checkout it keeps in the data directory, with `task deps`, `task package`, and `task build`.
4. **Put in place.** The app goes in `/Applications` on macOS, `~/.local/bin` with a desktop entry on Linux, and `%LOCALAPPDATA%\Programs\Inkuna` with a Start menu shortcut on Windows. `inkunactl` goes in `~/.local/bin`, or beside the app on Windows, and that folder is added to `PATH`. The build it replaces is kept as `*.previous`.

Running it again picks up where it stopped, and does nothing when everything is done. To update, run `inkunactl update`: it lists the pull requests merged since your build, then fast-forwards the checkout, builds, and swaps the new build in. `inkunactl update --rollback` goes back to the build it replaced. Each provider CLI owns its own login; Inkuna never stores provider credentials. [Installing Inkuna](docs/install.md) lists every prerequisite and where everything goes.

## Configuration

`inkunactl install` runs the setup wizard when there is no configuration. To run it again yourself:

```sh
inkunactl setup
```

It writes `inkuna.yaml` under the data directory and stores any secrets you name, such as a Linear API key, in the OS keychain.

The same file can be edited in the desktop **Settings** screen. When the wizard chooses a data directory other than the default, it writes a locator `inkuna.yaml` at the default location, so Inkuna finds the configuration on startup without environment variables ([custom data directories](docs/configuration.md#custom-data-directories)). Environment variables override the file for a single launch; [`.env.example`](.env.example) lists them and [docs/configuration.md](docs/configuration.md) documents every field. Inkuna does not load `.env` files.

| Setting | Purpose | Default |
| --- | --- | --- |
| `INKUNA_CONFIG` | Path to `inkuna.yaml` | `<data dir>/inkuna.yaml` |
| `INKUNA_DATA_DIR` | SQLite, artifacts, worktrees, global agents | OS user config directory |
| `INKUNA_ORCHESTRATOR_RUNTIME` | Runtime that plans and staffs stages | `claude` |
| `INKUNA_ORCHESTRATOR_MODEL` | Orchestrator model override | policy band |
| `INKUNA_MAX_TASKS` | Concurrently executing workflows | `3` |
| `INKUNA_MAX_CONCURRENCY` | Concurrent steps inside one workflow | `2` |
| `INKUNA_DESKTOP_NOTIFICATIONS` | Native notifications for input requests and completion | `true` |
| `LINEAR_API_KEY` | Linear credential for importing issues and the tracker writes a repository's `.inkuna/tracker.yaml` allows (also resolvable from the keychain) | unset |
| `INKUNA_PLANNING_TIMEOUT`, `INKUNA_WORKER_TIMEOUT`, `INKUNA_VERIFIER_TIMEOUT`, `INKUNA_APPROVAL_TIMEOUT` | Budgets | `5m`, `30m`, `15m`, `15m` |
| `INKUNA_CODEX_BINARY`, `INKUNA_CLAUDE_BINARY`, `INKUNA_COPILOT_BINARY` | Runtime executables | `codex`, `claude`, `copilot` |
| `INKUNA_GITHUB_HOST`, `INKUNA_GITHUB_GIT_PROTOCOL` | GitHub host and transport | `github.com`, `https` |

Model routing lives in the versioned [`policy.v2.json`](internal/routing/policy.v2.json) artifact; `routing.bands` in `inkuna.yaml` (or Settings → Model routing) can replace a runtime's bands. The effective policy is sent to the React view so the UI never keeps a second model table.

## Run Inkuna

```sh
task bootstrap
task doctor
task dev          # desktop app with frontend live reload
task package      # build/bin/Inkuna.app on macOS; inkunactl install puts it in place
```

GitHub synchronization needs an authenticated GitHub CLI (`gh auth login -h github.com`). The **Connections** screen starts the `gh` and Claude Code browser flows and accepts a Linear personal API key for the session. The request composer accepts a Linear issue key such as `ENG-123` or a full `linear.app` issue URL; import is read-only. Connections also says whether the Linear key and gh's token can make the tracker writes a repository's `.inkuna/tracker.yaml` allows ([docs/mcp-and-trackers.md](docs/mcp-and-trackers.md#trackers)).

## How a task flows

1. **Request**: objective, acceptance criteria, constraints, labels, and kind, typed or imported from Linear.
2. **Rules**: `.inkuna/rules.yaml` (or the built-in defaults) picks the flow: profile, agent stages, verification, human-input checkpoints, approval.
3. **Orchestrator**: the configured runtime runs headlessly, read-only, and returns JSON assigning an agent and skills to every `auto` stage, and a 1 to 10 complexity score to every agent stage for that stage's own work, knowing the model and effort each score routes to. Go validates the decision and falls back deterministically if the model is unavailable or wrong.
4. **Routing**: a versioned policy maps the score to a model and reasoning effort inside the selected runtime; agent definitions may pin a runtime or model, and explicit overrides stay visible.
5. **Approval**: the plan is reviewed before anything runs.
6. **Execution**: each agent step becomes an A2A task. The worker receives a compact context envelope, never other workers' transcripts. Writable steps run in a dedicated worktree and are committed per step on a branch named by the repository's `.inkuna/branching.yaml` ([Branching](docs/branching.md)); a missing configuration is proposed at planning and written on approval. Workers can pause for your input or ask a peer agent for help.
7. **Verification and review**: deterministic checks run, then the approval gate.

On Codex and Claude Code each score has its own model and reasoning effort: a model covers a run of scores, and each point inside it raises the effort, up to the top model at `xhigh`. Copilot's bands set no effort.

| Score | Codex CLI | Claude Code | GitHub Copilot CLI |
| --- | --- | --- | --- |
| 1 | `gpt-6-luna` medium | `haiku` medium | `claude-haiku-4.5` |
| 2 | `gpt-6-luna` high | `haiku` high | `claude-haiku-4.5` |
| 3 | `gpt-6-luna` xhigh | `haiku` xhigh | `claude-haiku-4.5` |
| 4 | `gpt-6.1-terra` low | `sonnet` low | `claude-sonnet-4.6` |
| 5 | `gpt-6.1-terra` medium | `sonnet` medium | `claude-sonnet-4.6` |
| 6 | `gpt-6.1-terra` high | `sonnet` high | `claude-sonnet-4.6` |
| 7 | `gpt-6.1-terra` xhigh | `sonnet` xhigh | `claude-sonnet-4.6` |
| 8 | `gpt-6.1-sol` medium | `opus` medium | `gpt-5.3-codex` |
| 9 | `gpt-6.1-sol` high | `opus` high | `gpt-5.3-codex` |
| 10 | `gpt-6.1-sol` xhigh | `opus` xhigh | `gpt-5.3-codex` |

## Desktop screens

- **Projects**: repositories, whether each is out of sync with origin, dependency health, configuration status, pending inputs. A repository is connected from a folder picker and removed with or without its history, and both write `inkuna.yaml`'s `repositories`, the one list of projects ([projects](docs/configuration.md#projects)).
- **New request** and **Workflow**: compose (a label naming a rule, such as `bugfix`, or the Workflow picker chooses the flow), review the staffed DAG, give any agent step another agent, runtime, or score, re-plan under another rule, approve.
- **Tasks**: every run, live and historical, with per-step state, the agent's context envelope and prompt, the docs the agent read compared with the doc index it was given, the handoff, the last commit with its diff, artifacts, and the A2A message history. Each task shows the pull request its branch was opened in and whether it merged, which a repository can have remove merged runs' worktrees ([pull requests](docs/branching.md#pull-requests)). The list filters by state, a merged pull request, project, and age, and finished tasks can be selected and deleted after a confirmation that says what goes, what stays, and why; Settings shows the disk space history takes and the worktrees a delete kept ([data lifecycle](docs/architecture.md#in-the-tasks-view)).
- **Docs**: a project's docs folder as a tree, filtered by status and tag, beside the selected doc rendered with its frontmatter, Markdown tables, Mermaid diagrams, and the docs that link to it. Docs with missing or invalid frontmatter or broken links are flagged with Go's reason. The Graph tab shows how docs link to each other and to other registered projects' docs; selecting or dragging a doc focuses it and pulls in the docs it links with. Under each doc, Read by lists the runs whose agents opened it.
- **Branches**: a project's local branches against origin (behind, ahead, diverged, upstream gone, or local only, and how far behind the base), fetched with the user's own git credentials. A branch that is only behind can be fast-forwarded, and one without an upstream deleted after confirmation ([branching](docs/branching.md#keeping-branches-in-sync)). Settings edits sitting in the project checkout are listed there too, and shared through **Open pull request** or **Push to `<base>`** ([sharing settings edits](docs/branching.md#sharing-settings-edits)).
- **Messages**: one permanent thread per agent conversation, grouped by run. When an agent or a rule checkpoint asks a question, replying in its thread resumes the paused task; answered questions stay on the thread as a record.
- **Costs**: what each task cost, broken down by run, step, agent, runtime, or model.
- **Agents**: the merged registry of built-in, global, and repository agents as A2A cards.
- **Rules**: a project's rules, and its shared stages and groups, edited and checked by Go before they are saved.
- **Runtimes**: the declared runtimes, added from a preset or a template, tested, and removed ([declaring runtimes](docs/configuration.md#declaring-runtimes)).
- **Settings**: `inkuna.yaml` editor, keychain secret status, and disk usage.
- **Connections**: provider logins.

`inkunactl` provides the install and setup wizards and `init` plus deterministic config, secret, agent, rule, stage library, branch, docs, health, skill, artifact, workflow, usage, cost, task history, and diff operations. Run `go run ./cmd/inkunactl help`. `inkunactl usage RUN_ID` prints the tokens each model call of a run used, the size of its prompt, and what it cost with where that figure came from ([usage accounting](docs/architecture.md#usage-accounting), [cost accounting](docs/architecture.md#cost-accounting)). `inkunactl cost` lists what each task cost, the most expensive first, and `inkunactl cost TASK` breaks one down by run and by step, agent, runtime, or model ([cost per task](docs/architecture.md#cost-per-task)). `inkunactl tasks list --finished` lists the tasks none of whose runs is active, and `inkunactl tasks delete TASK...` deletes them and their history after asking, keeping their worktrees and branches unless `--remove-worktrees` or `--delete-branches` asks, and then asking again for each branch ([data lifecycle](docs/architecture.md#data-lifecycle)). `inkunactl docs validate REPOSITORY` checks a repository's doc frontmatter and links and exits 1 when a doc needs fixing, so it can gate CI ([docs index](docs/docs-index.md#from-the-terminal)). `inkunactl diff RUN_ID` writes a run's changes as one HTML page, like a pull request's, to `<data dir>/reports/RUN_ID.html` and prints the path: its files with their diffs, its commits, and its checks, with each failed check's log. `--commit SHA` or `--step ID` shows one commit, `--split` opens it side by side, `--include-large` includes patches over 1 MB or 20,000 lines, `--out FILE` writes it elsewhere, and `--open` opens it in the browser. From the repository, `task diff RUN=<id>` runs it, with flags after `--` ([the HTML page](docs/architecture.md#the-html-page)).

## Development

```sh
task fmt
task lint
task test
task build
task ci       # the handoff gate
```

`task build`, `task ci`, and `task package` build the frontend into `ui/dist`, which `main.go` embeds and git ignores.

## Safety model

Planning is read-only, including the orchestrator call. Every model decision (staffing, skills, scores, handoffs, peer requests, output paths) is validated by Go before it has any effect. Writable steps are confined to dedicated worktrees (or, when a repository turns worktrees off, to a clean checkout on a branch other than its base); commits stay local. Workers receive only the secrets declared for their runtime and agent in `inkuna.yaml`; secret values never appear in YAML or SQLite. Workers start without the user's personal CLI setup (hooks, plugins, skills, memory, global instructions), except what a CLI cannot switch off: Copilot's user hooks and Codex's global `AGENTS.md`. Workers start only the MCP servers their step is granted from `mcpServers`, plus those their runtime's `userMcp` names, whether or not `userConfig` is on: Codex is told to switch off every other server in `config.toml`, and Copilot every other server it lists. Copilot runs shell commands in its OS sandbox (bubblewrap on Linux) and never with `--allow-all`. Agents cannot widen their permission through rules and cannot ask peers more than one level deep; every turn runs within the worker wall-time budget, and a `maxTurns` on an agent definition is the only per-invocation model turn limit. GitHub writes, pushes, pull requests, integration, and template saving remain approval-gated. Worktrees are removed only by the cleanup policy a repository chooses, from the run view, with their branch when the user deletes it from the Branches view, or when the user asks while deleting a finished task or later from Settings, and only when clean; cleanup never deletes a branch, and the user can delete only one without an upstream. Runs interrupted by a crash are marked as such and never resume silently. The app's page loads images, stylesheets, fonts, and media only from the app itself, so repository content it renders, such as a doc or a Mermaid diagram, cannot contact another server.

## License

Inkuna is fair source under the [Functional Source License 1.1](LICENSE.md) (FSL-1.1-ALv2). Use it, modify it, and build on it for anything except a competing commercial product or service. Each version becomes available under the Apache License 2.0 two years after it is made available.

Contributions are welcome under a contributor license agreement: see [CONTRIBUTING.md](CONTRIBUTING.md).
