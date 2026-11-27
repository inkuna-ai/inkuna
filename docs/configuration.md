---
title: Configuration
lastUpdated: 2026-11-25
author: Brandon Manning
tags: [config]
status: current
---

# Configuration

Inkuna reads its configuration in three layers. Built-in defaults come first, `inkuna.yaml` overrides them, and environment variables override the file. Secret values never live in the file; the file only declares which named secrets Inkuna should resolve.

## Where inkuna.yaml lives

- `$INKUNA_CONFIG` if set, otherwise
- `<data dir>/inkuna.yaml`, where the data dir is `$INKUNA_DATA_DIR` or the OS user config directory plus `Inkuna` (for example `~/Library/Application Support/Inkuna` on macOS).

The file is written with owner-only permissions (0600). Unknown keys are rejected on load so credentials or typos never silently pass through, and the file must hold a single YAML document.

### Custom data directories

When the setup wizard, or a save of `dataDir` in Settings, chooses a directory other than the default, it also writes a locator `inkuna.yaml` at the default location whose `dataDir` names the chosen directory. `config.Load` follows that pointer when neither `INKUNA_DATA_DIR` nor `INKUNA_CONFIG` is set, so the configuration is found on the next launch without any environment setup. Setting either variable bypasses the locator.

Saving a different `dataDir` in Settings follows the same layout: the configuration is written into the new directory, the locator is pointed at it, and clearing the field puts the configuration back at the default location. A directory that already has its own `inkuna.yaml` is refused before anything is written, because startup would read that file and the settings being saved would not apply; remove the file, or start Inkuna with `INKUNA_DATA_DIR` to use that directory as it is. Further saves before the restart keep the copy in the new directory current. The running app keeps the directory it has open and reports that a restart is needed; the database, artifacts, and worktrees are not moved. With `INKUNA_DATA_DIR` or `INKUNA_CONFIG` set, the field is shown as overridden and a save leaves the locator alone.

## inkuna.yaml reference

```yaml
version: 1
dataDir: ""                      # optional override of the data directory
orchestrator:
  runtime: claude                # codex | claude | copilot
  model: ""                      # explicit model; blank uses the routing policy
  reasoningEffort: ""            # low | medium | high | xhigh (runtime-specific)
  maxTurns: 0                    # 0 or absent = unlimited within the planning timeout
  readTools: false               # true = staffing may read the repository with read-only tools
  disabled: false                # true = deterministic staffing only
runtimes:
  codex:
    binary: codex
    secrets: []                  # secret names exported to codex workers
  claude:
    binary: claude
    secrets: []
    userConfig: false            # true = workers load your own CLI setup (hooks, skills, memory, plugins)
    userMcp: []                  # your own MCP servers, as `claude mcp list` names them, for every step
  copilot:
    binary: copilot
    secrets: []
  claude-work:                   # another copy of a built-in CLI
    preset: claude
    binary: /opt/claude-work/bin/claude
  gemini:                        # a CLI described by an argument template
    label: Gemini CLI
    binary: gemini
    stdin: none                  # prompt | none
    output: text                 # claude-stream-json | codex-json | copilot-json | text
    args: ["--prompt", "{prompt}", "--model", "{model}"]
    readOnlyArgs: ["--approval-mode", "plan"]
    writeArgs: []                # empty = this runtime only runs read-only steps
    versionArgs: ["--version"]
    secrets: []
concurrency:
  maxTasks: 3                    # concurrently executing workflows
  maxWorkers: 2                  # concurrent steps inside one workflow
timeouts:                        # Go durations
  planning: 5m
  worker: 30m
  verifier: 15m
  approval: 15m
  health: 3s
  auth: 5m
  linear: 15s
notifications:
  desktop: true
secrets:
  - name: LINEAR_API_KEY
    description: Linear personal API key
    required: false
repositories:
  - name: inkuna
    path: /absolute/path/to/repository
    defaultRuntime: claude
    rules: ""                    # optional rules file, relative to the repository or absolute; default .inkuna/rules.yaml
    docs: ""                     # optional docs root, relative to the repository and inside it; default docs
    tracker:                     # optional; lowers what .inkuna/tracker.yaml allows, on this machine only
      writes: [comment]          # keep only these kinds; [] allows none; absent keeps the repository's
      updates: { start: false }  # false turns an update off; absent keeps the repository's
agents:
  directories: []                # global agent directories; default <data dir>/agents
stages:
  directories: []                # global stage library directories; default <data dir>/stages
linear:
  endpoint: https://api.linear.app/graphql
github:
  host: github.com
  protocol: https                # https | ssh
  binary: gh
routing:                         # optional; omit to keep the built-in bands
  bands:
    claude:                      # replaces the built-in bands for this runtime only
      - { minimum: 1, maximum: 6, model: sonnet, reasoningEffort: low }
      - { minimum: 7, maximum: 10, model: opus }
remoteAgents:                    # optional; written by the Agents view
  - name: security-advisor
    cardUrl: https://advisor.example.com/.well-known/agent-card.json
    enabled: true
    secret: ADVISOR_TOKEN        # a name from secrets:
    auto: false
    sendArtifacts: false
    pinned: { digest: "sha256:…", url: https://advisor.example.com/a2a, protocolVersion: "0.3.0", auth: bearer }
mcpServers:                      # optional; MCP servers a rule stage or an agent can grant its steps
  - name: docs-search
    description: Searches the team's design archive
    command: [npx, -y, "@example/docs-mcp@2.1.0"]   # argv, never a shell
    env:
      DOCS_TOKEN: DOCS_TOKEN     # the server's variable: a name from secrets:
    tools: [search, read_page]   # optional; the tools a worker may call, default all
  - name: linear
    url: https://mcp.linear.app/mcp
    bearer: LINEAR_API_KEY       # a name from secrets:, sent as the bearer token
    tracker: linear              # a tracker's own server: tools required, read tools only
    tools: [get_issue, list_issues, list_comments]
checks:
  logBytes: 1048576              # optional; bytes of each evaluation command's log kept
budgets:                         # optional; bytes of text a worker's context envelope may carry
  objective: 16384
  stagePrompt: 8192              # the prompt of the step's stage in the rules
  acceptanceCriteria: 8192       # in all
  constraints: 8192              # in all; the limits the user set on the task
  dependencySummary: 4096        # each
  decisions: 8192                # in all
  risks: 8192                    # in all
  skill: 12288                   # each
  humanResponses: 8192           # in all
  peerMessages: 16384            # in all
  previousTurn: 8192             # a turn that cannot resume its session gets the last turn's summary
  feedback: 16384                # in all; what a feedback round sends its target, every item kept
  previousRound: 8192            # a round that cannot resume its target's session gets its last run's summary
  envelope: 98304                # the whole envelope
pricing:                         # optional; adds to or replaces the built-in model prices
  models:
    gpt-5.6-sol:                 # US dollars per million tokens
      input: 4
      cacheWrite: 5              # five-minute cache; default input
      longCacheWrite: 5          # one-hour cache; default cacheWrite
      cacheRead: 0.4             # default input
      output: 20
  premiumRequest: 0.04           # US dollars per Copilot premium request
extensions:                      # optional; one section per extension an embedding binary builds in
  example:                       # the extension's name; the extension decodes the rest
    endpoint: https://records.example.com/inkuna
```

| Field | Default | Notes |
| --- | --- | --- |
| `version` | `1` | Must be `1`. |
| `dataDir` | empty | Used only when `INKUNA_DATA_DIR` and `INKUNA_CONFIG` are unset. |
| `orchestrator.runtime` | `claude` | Which headless CLI plans and staffs `auto` stages. |
| `orchestrator.model` | empty | Pins the orchestrator model; otherwise the routing policy band applies. |
| `orchestrator.reasoningEffort` | empty | Passed to the runtime when set. |
| `orchestrator.maxTurns` | unlimited | Model turn budget for the planning call; `0` or absent leaves it to the planning timeout. |
| `orchestrator.readTools` | `false` | Lets the staffing call read the repository: it runs in the repository with read-only tools, and every plan calls it, since its decision depends on files and is never reused. Without it the call has no tools and runs in an empty directory under the data directory ([agents.md](agents.md#how-the-orchestrator-chooses)). |
| `orchestrator.disabled` | `false` | Skip the model and staff stages deterministically. |
| `runtimes.<name>.binary` | `codex`, `claude`, `copilot` | Executable name or absolute path. Required for a template. |
| `runtimes.<name>.secrets` | empty | Secret names exported into that runtime's worker processes. Each name must appear in `secrets`. |
| `runtimes.<name>.label` | the name | Shown in the app. |
| `runtimes.<name>.preset` | | `codex`, `claude`, or `copilot`: run this entry through that built-in adapter. See [Declaring runtimes](#declaring-runtimes). |
| `runtimes.<name>.userConfig` | `false` | Built-in adapters only: workers load the user's own CLI setup instead of starting without it. See [What a worker's CLI loads](#what-a-workers-cli-loads). Refused on a template. |
| `runtimes.<name>.userMcp` | empty | Built-in adapters only: the user's own MCP servers, by the names the CLI lists them under, that every step on this runtime is given with all their tools. See [Your own MCP servers](#your-own-mcp-servers). Refused on a template, twice in one list, or under a name `mcpServers` declares. |
| `runtimes.<name>.args`, `readOnlyArgs`, `writeArgs`, `stdin`, `output`, `versionArgs` | | The argument template of a runtime without a preset. |
| `concurrency.maxTasks` | `3` | Runs beyond this wait in the `queued` state. |
| `concurrency.maxWorkers` | `2` | Parallel steps within a run (also capped at 2 by the scheduler). |
| `timeouts.*` | see above | Positive Go durations. |
| `notifications.desktop` | `true` | Native notifications for input requests and run completion. |
| `secrets[].name` | | Upper-case environment-variable style name. Declared twice is an error. |
| `secrets[].description`, `required` | | Informational. |
| `repositories[]` | empty | The connected projects ([Projects](#projects)). `name` and `path` are required, and no two entries share a name; `defaultRuntime` falls back to the orchestrator runtime. `rules` overrides `.inkuna/rules.yaml`. `docs` is the documentation root indexed for the project (see [docs-index.md](docs-index.md)); it must be relative to the repository and stay inside it. |
| `repositories[].tracker` | empty | Lowers, on this machine, what the repository's `.inkuna/tracker.yaml` lets Inkuna write to its tracker; it can switch a kind or update off, never on. `writes` keeps only the kinds it lists (`status`, `comment`, `edit`, `create`), and `[]` allows none; `updates.start`, `.result`, and `.done` set to `false` turn that update off. A key left out keeps the repository's setting. The Settings view edits it from the repository's row, beside what the repository allows and what is left on this machine. See [mcp-and-trackers.md](mcp-and-trackers.md#the-machines-override). |
| `agents.directories` | `<data dir>/agents` | Extra global agent directories, searched after built-ins and before the repository. |
| `stages.directories` | `<data dir>/stages` | Global stage library directories. Each `*.yaml` file directly in one holds `version: 2`, `stages:`, and `groups:` in the rules schema, and no rules; every registered repository's rules can use what they define. A definition there replaces a built-in one with the same name, and a repository's own replaces it ([rules.md](rules.md#where-definitions-come-from)). A directory that does not exist adds nothing. A blank entry is refused. A file that does not parse, a definition that fails its checks, or a name two files define is shown in Settings under **Stage library** and by `inkunactl stages list`, but never blocks a save: the files are edited outside `inkuna.yaml`, and a plan that uses a broken definition is refused instead. The files are read again for every plan, approval, and rules edit, so an edit applies without a restart. |
| `linear.endpoint` | Linear production API | GraphQL endpoint. |
| `github.host`, `protocol`, `binary` | `github.com`, `https`, `gh` | Passed to the GitHub CLI. |
| `routing.bands.<runtime>` | built-in [`policy.v2.json`](../internal/routing/policy.v2.json) | Complexity-to-model bands for one runtime. The bands must cover scores 1 to 10 in order with no gaps or overlaps, and each needs a `model`; `reasoningEffort` is optional. A runtime left out keeps the built-in bands. The built-in Codex and Claude Code bands are one score wide: a model covers a run of scores and each point inside it raises the effort, from `gpt-6-luna` or `haiku` at `medium` for 1 to `gpt-6.1-sol` or `opus` at `xhigh` for 10 (the table is in the [README](../README.md#how-a-task-flows)). Custom bands do not change the policy version recorded on a plan; each routed step records the model and effort it was given. A new built-in policy does change it, and a plan made under the old one must be planned again before it is approved. |
| `checks.logBytes` | `1048576` (1 MB) | Bytes of each evaluation command's output kept as its check log: the first quarter and the last three quarters, with a marker for what was left out between them ([rules.md](rules.md#stages)). `0` or absent keeps the default; a value set must be between 4096 and 67108864 (64 MB). Runs started before a change keep the cap they started with. |
| `budgets.*` | see above | Caps, in bytes, on what a worker's context envelope carries ([agents.md](agents.md#budgets)). `0` or absent keeps the default; a value set must be at least 1024. Runs started before a change keep the budgets they started with. |
| `pricing.models.<model>` | built-in [`prices.v1.json`](../internal/pricing/prices.v1.json) | A model's prices in US dollars per million tokens, by the id the runtime reports, in lower case. The entry replaces the model's built-in price or adds one. `input` and `output` are required; `cacheWrite` defaults to `input`, `longCacheWrite` to `cacheWrite`, and `cacheRead` to `input`. Calls are recorded under the built-in table's version. See [Model prices](#model-prices). |
| `pricing.premiumRequest` | not priced | US dollars per Copilot premium request. Without it Copilot calls have no cost; with it they are estimated from the premium requests Copilot reports. `0` prices them at nothing. |
| `mcpServers[]` | empty | MCP servers a rule stage or an agent grants its steps by name; see [MCP servers](#mcp-servers). |
| `extensions.<name>` | empty | Settings for an extension a binary that embeds Inkuna builds in ([extensions.md](extensions.md#configuration)). Inkuna keeps each section as written, except that aliases in it are expanded when the file is read, so a save never writes a reference to an anchor elsewhere in the file, and hands it to the extension of that name when the app starts; the extension decodes it strictly and its errors stop startup, naming `extensions.<name>`. A section for an extension that takes no configuration also stops startup. A section for an extension the build does not have, such as in Inkuna's own app or `inkunactl` reading an embedding product's file, is kept and ignored, and Doctor reports it as missing. A changed section takes effect at the next start. |
| `remoteAgents[]` | empty | External A2A agents connected as read-only advisors; see [remote-a2a.md](remote-a2a.md). `name` cannot be a built-in agent's. `cardUrl` and the pinned endpoint must be `https` on the same origin. `secret` must be declared under `secrets` and is never exported to a worker. `auto` lets the orchestrator pick the agent for read-only `auto` stages; without it only a rule naming the agent uses it. `sendArtifacts` inlines upstream text artifacts up to `maxArtifactBytes` (default 262144, at most 1048576). `pinned`, `description`, and `skills` are written when the card is reviewed; an entry without `pinned` cannot be enabled. `allowInsecure: true` permits plain HTTP and private addresses for an agent under development and can only be set by hand. |

Validation rejects: unsupported versions, a budget between 1 and 1023 bytes or below zero, runtimes that are not declared, runtime declarations that break the rules under [Declaring runtimes](#declaring-runtimes), `userConfig` or `userMcp` on a template, a `userMcp` that names a server twice or names one `mcpServers` declares, negative limits, lower-case or duplicate secret names, runtime secrets not declared in `secrets`, repositories without a name or path, duplicate repository names, a `docs` directory that is absolute or escapes the repository, a repository `tracker.writes` that names an unknown kind or one twice, invalid GitHub protocols, malformed durations, routing bands that name an unknown runtime or do not cover 1 to 10 exactly once, a model price without `input` or `output`, with a negative or non-finite rate, or under an id that is not lower case, a negative premium request price, a runtime named `remote` (reserved for remote agents), and remote agents with a reserved, built-in, or duplicate name, a card URL or pinned endpoint that is not `https` (without `allowInsecure`) or carries credentials, a pinned endpoint on another origin than the card, an undeclared secret, or `enabled` without a pinned card, and MCP servers that break the rules under [MCP servers](#mcp-servers).

## MCP servers

`mcpServers` lists the MCP servers this machine offers workers. A rule stage's `mcp` list ([rules.md](rules.md#stages)) and an agent's ([agents.md](agents.md#file-format)) grant them by name; a step gets both lists, and Inkuna starts, or connects to, those servers and no others for it ([architecture.md](architecture.md#mcp-servers-a-step-is-granted)), unless a runtime names some of the user's own servers in `runtimes.<name>.userMcp` ([Your own MCP servers](#your-own-mcp-servers)). The design is [mcp-and-trackers.md](mcp-and-trackers.md).

| Field | Required | Meaning and validation |
| --- | --- | --- |
| `name` | yes | Lower-case letters, digits, and dashes, starting with a letter, at most 63 characters, unique. Not a name a CLI gives a tool or a permission kind (`bash`, `view`, `create`, `edit`, `glob`, `grep`, `task`, `skill`, `sql`, `shell`, `write`, `read`, `url`, `memory`, `powershell`), because Copilot reads a server's name alone as all its tools. |
| `description` | no | Shown on the approval screen and to the worker. At most 500 bytes. |
| `command` | one of `command` and `url` | An argv array Inkuna's runtime starts directly, never through a shell. |
| `url` | one of `command` and `url` | A streamable HTTP endpoint: `https`, or `http` only to `localhost` or a loopback address, without credentials in it. |
| `env` | no | For a `command`: the server's environment variable, an upper-case name that is not one every CLI is started with (`PATH`, `HOME`, …), mapped to a declared secret. |
| `bearer` | no | For a `url`: a declared secret sent as `Authorization: Bearer`. |
| `tools` | no | The server's tools a worker may call: letters, digits, underscores, dots, and dashes. Empty means every tool. A Claude Code allow rule in your own settings for the server's other tools is left out of a step given it. A Claude Code worker never loads the working directory's `.claude/settings.json` or `.claude/settings.local.json`, so a rule there allows nothing. |
| `tracker` | no | `linear` or `github`: the server reaches that issue tracker. It must list its `tools`, and only ones that read, because every change to a tracker waits for a person's approval ([mcp-and-trackers.md](mcp-and-trackers.md#changes-an-agent-asks-for-155)). Which tools only read is the declaring person's call; the approval screen shows the list. |

A server's secrets resolve, like any secret, from the environment and then the keychain, when a step granted the server starts. A step whose server's secret is not set fails before its CLI starts, and so does one whose secret is shorter than 8 bytes, which Inkuna could not hide in the events and output it keeps, or one granted a server `inkuna.yaml` no longer declares. A server a step is granted that changed in `inkuna.yaml` after the plan was made, even under the same name, is refused at approval; plan the task again. A secret a server uses cannot be removed from `secrets` until the server stops using it. A Claude Code step granted a server with secrets runs its shell commands in Claude Code's sandbox, which keeps them from reading those secrets ([architecture.md](architecture.md#mcp-servers-a-step-is-granted)); on Linux that needs bubblewrap and socat installed, and on Windows it is not available, so such a step fails before it starts and says why. The MCP servers panel in Settings edits this list; the Agents view and the Rules editor each have an MCP servers field beside Skills.

## Model prices

Every model call records a cost and where that figure came from ([architecture.md](architecture.md#cost-accounting)). When the runtime reports no cost, Inkuna estimates one from the call's tokens with a pricing table. The built-in table, [`internal/pricing/prices.v1.json`](../internal/pricing/prices.v1.json), holds Anthropic's and OpenAI's published prices, each with the page it was read from and the day it was read; a model it does not list has no price, and its calls are recorded as unknown rather than free. Prices that change get a new table version.

`pricing.models` adds models the table lacks, such as a model a template runtime runs, or replaces a built-in price that differs for you. A dated snapshot id (`claude-haiku-4-5-20251001`) takes its model's price unless it has its own. Some built-in prices hold only up to a prompt length (Haiku 5.5 up to 100,000 tokens, OpenAI's models up to 272,000 input tokens); a call is always priced at the shorter tier.

Copilot bills premium requests, not tokens: each prompt costs its model's multiplier (0.33 for Claude Haiku 4.5, 1 for Claude Sonnet 5), whatever tokens it used. Inkuna records the premium requests Copilot reports at the end of each call and leaves the cost unknown until `pricing.premiumRequest` names a price. A call cut off before that report, or on a model that takes no premium requests, reports none and stays unknown.

The Model prices panel in Settings lists the built-in prices with their source, marks each model `inkuna.yaml` prices as an override or an addition, and edits them; clearing a cache rate leaves it out so its default applies.

## Declaring runtimes

A runtime is a headless CLI that agents run on. `codex`, `claude`, and `copilot` are built in: they exist whether or not `runtimes:` lists them, and their entries only adjust the binary, the exported secrets, `userConfig`, and `userMcp`. Any other name (lowercase letters, digits, and dashes, 32 at most) adds a runtime, in one of two forms.

**A preset copy** sets `preset` to a built-in name. Inkuna builds the arguments itself, exactly as it does for that built-in, including the CLI's sandbox and tool limits for read-only and worktree-write steps. Use it for a second installation or a wrapper that accepts the same flags. It takes no template fields.

**An argument template** describes a CLI Inkuna has no adapter for:

| Field | Meaning |
| --- | --- |
| `args` | Arguments for every run. |
| `readOnlyArgs` | Appended for read-only steps: the flags that keep the CLI from changing files. Required: without them a read-only step would run the CLI in whatever mode it defaults to, so a template that leaves them out is refused. |
| `writeArgs` | Appended for worktree-write steps. Left empty, the runtime is read-only: plans and runs that give it a step that writes are refused. |
| `stdin` | `prompt` writes the prompt to standard input; `none` expects `{prompt}` in the arguments. One of the two must deliver the prompt for read-only steps and, when `writeArgs` are declared, for steps that write. |
| `output` | How stdout is read: one of the built-in event streams (`claude-stream-json`, `codex-json`, `copilot-json`), or `text`, where every line is a message and the last JSON object in the output is the step's structured result. |
| `versionArgs` | The health probe, `--version` by default. |

Templates accept a fixed set of placeholders, each replaced inside a single argument: `{prompt}`, `{model}`, `{reasoning_effort}`, `{schema}` (the output JSON schema as text), and `{schema_path}` (a temporary file holding it). An argument whose placeholder has no value is dropped, together with the flag before it when the argument is only the placeholder, so `--model {model}` disappears when routing leaves the model to the CLI.

Least privilege is kept for declared runtimes:

- The binary is started directly with the expanded arguments, never through a shell, so nothing in a prompt or a model name can add arguments or run commands.
- A template that contains a flag or value that switches a CLI's own permission checks off (for example `--dangerously-skip-permissions`, `--yolo`, `--allow-all-tools`, `--no-sandbox`, `bypassPermissions`, `danger-full-access`) is refused when it is saved or loaded. The list is a guard rail, not a proof: what a template's flags allow is the responsibility of whoever declares it, which is why write access has to be declared explicitly.
- The worker environment is the same non-secret baseline as for the built-ins plus the secrets listed for that runtime.
- A runtime's capabilities say whether it starts no MCP server beyond those a step is granted, and name the user's own servers it gives every step (`userMcp`). It starts none beyond them on every built-in CLI unless `userMcp` names some, `userConfig` or not. Inkuna cannot know it about a template, and says so. A template cannot be given MCP servers at all (`mcpServers: false` in its capabilities).

### What a worker's CLI loads

A worker starts from the CLI's own system prompt plus Inkuna's prompt and the repository's instructions (`CLAUDE.md`, `AGENTS.md`). Without `userConfig`, the built-in adapters leave the user's personal setup out:

- **Claude Code** keeps loading the user's settings, because an `apiKeyHelper` or provider environment there is how some users sign in, but overrides what they would add: hooks are off (the repository's too), as are auto-memory, the global `CLAUDE.md` and rules in the configuration directory, an output style, skills, and slash commands.
- **Codex** skips `config.toml`, and with it the MCP servers, hooks, and default model it declares; authentication still comes from `CODEX_HOME`. Its skill listing is off. A global `~/.codex/AGENTS.md` still loads; Codex has no switch for it.
- **Copilot** loads none of the user's installed plugins and starts none of their MCP servers: before each call Inkuna asks Copilot which servers it would start (`copilot mcp list --json`) and disables each one, and when Copilot cannot list them the worker does not start. User hooks still load; Copilot has no switch for them. Its shell commands run in its OS sandbox, which on Linux needs bubblewrap (`bwrap` 0.5 or later); without it they fail.

Set `userConfig: true` on a runtime whose workers need that setup, for example a Codex provider or a team's hooks. Some limits apply whatever the setting: Inkuna still picks the tools, workers get no subagents or web access, and workers start only the MCP servers their step is granted and those `userMcp` names. Codex is told to switch off every other server in `config.toml`, and Copilot every other server it lists. With `userConfig`, an orchestrator that has no `orchestrator.model` uses the model in the CLI's own configuration; without it, Codex uses its built-in default. See [architecture.md](architecture.md#what-a-workers-cli-loads) for the flags and the token savings.

### Your own MCP servers

`runtimes.<name>.userMcp` gives every step on one runtime some of the MCP servers the user set up in that CLI, with all their tools, separately from `userConfig`. Names are the CLI's own: what `claude mcp list` prints before the colon (`linear-server`, `claude.ai Gmail`, `plugin:postman:postman`), or the names `codex mcp list` and `copilot mcp list` print. In the Runtimes view, editing a built-in runtime or a preset copy lists the CLI's servers on request, with what the CLI says of each, a box to tick each into `userMcp`, and a Sign in button for a server that needs it. Listing waits on Claude Code, which connects to every server to list them.

Nothing reviews what a step changes through these servers, a tracker's own included: the approval screen says so beside each step's list. A server that should only read, or only expose some tools, belongs in [`mcpServers`](#mcp-servers) with a `tools` list instead.

- **Signing in.** Before a step starts, Inkuna asks its runtime about each server. One that needs signing in pauses the step with an inbox request: Sign in runs the CLI's own sign-in (`claude mcp login`, `codex mcp login`), which opens the browser, and the step starts with the server; Skip starts it without. Copilot has no sign-in command; sign in to a Copilot server with `/mcp` in `copilot`. A server that is gone, switched off, or failing is left out with a note on the timeline, and so is one the CLI says did not start once the call is under way.
- **Secrets.** Claude Code and Copilot start the servers from their own configuration, which Inkuna does not read. A Claude Code worker given any of them runs its shell commands in Claude Code's sandbox, which keeps them from `~/.claude.json` and from `ps`, so it needs macOS's `sandbox-exec`, or bubblewrap and socat on Linux, and does not start without them. Codex workers skip `config.toml`, so Inkuna reads each server's entry from `codex mcp get --json` and hands it to Codex as overrides, with its secrets in Codex's own environment, kept from the model's shell as for a declared server; a variable the entry only names comes from Inkuna's own environment. With `userConfig`, Codex loads the servers from `config.toml` itself.
- **Plans.** A plan records each runtime's `userMcp`, and approval refuses one whose runtimes name other servers by then; plan the task again. A step moved to another runtime on the approval screen takes that runtime's servers. Planning calls and peers are given none.

An added runtime has no built-in model bands. Until `routing.bands.<name>` lists some, steps on it carry an empty model and the CLI uses its default. `orchestrator.runtime`, a repository's `defaultRuntime`, and `routing.bands` must name declared runtimes. Agents, rules, and project defaults carry only the name; planning and starting a run resolve it and refuse a runtime that is not declared.

The Runtimes view under Configure lists every runtime, adds one from a preset or a blank template, edits it, tests the connection (binary resolution plus the version probe), and removes it. Removing an added runtime also removes its routing bands; "removing" a built-in resets it to its defaults.

## Editing in the app

Settings covers every field above except `runtimes`, which has its own view (see [Declaring runtimes](#declaring-runtimes)), `remoteAgents`, which the Agents view manages because a pinned card can only come from fetching and reviewing one, and `budgets` and `checks`, which have no control and are edited in the file. A Settings save always carries both as last saved; for `remoteAgents` the Go side enforces it, whatever the form submits. `extensions` never reaches the view at all, and a save keeps every section as it was when the app started. Save sends the whole file through the same Go validation as a load; when it is refused, each problem is shown beside the control it is about and nothing is written. A blank field shows the value it resolves to as its placeholder.

A field that an environment variable currently overrides carries an `overridden by $VARIABLE=value` badge and is read-only, because an edit would not take effect in this process; the file value is kept as it is.

Repository and agent directory paths can be chosen with the native folder picker. A secret's row offers two removals: **Remove value** deletes only the keychain entry, and **Forget** (after a confirmation) also removes the declaration from `inkuna.yaml` and every runtime export of it. Storing or removing a value never discards unsaved edits elsewhere on the page, and the view reloads when the configuration is saved from somewhere else unless it holds unsaved edits.

## Locating binaries

Inkuna resolves bare executable names (`gh`, `claude`, `codex`, `copilot`, `git`, `go`, `node`, `npm`) through `PATH`. A desktop app launched from Finder, Spotlight, or `wails dev` inherits the minimal login `PATH` rather than the shell's, so on startup Inkuna appends well-known install directories that exist on disk: first the version managers (Volta, asdf, mise, fnm, and the nvm default version, whose `default` alias is followed through `$NVM_DIR/alias`, including custom aliases and `lts/*`), then Homebrew (`/opt/homebrew/bin`, `/usr/local/bin`), `~/.local/bin`, `~/.claude/local`, `~/.npm-global/bin`, Bun, Cargo, and Go. Version managers come first because the shell puts them ahead of Homebrew too: with both an nvm node and a Homebrew one installed, workers run nvm's, as the terminal does. The exception is an nvm `default` alias that names no installed version (`system`, a version that is not installed, or no `default` at all): the shell then runs the node outside nvm, so nvm's newest version is added last, as a fallback. For `GOPATH` lists, only the first entry's `bin` is added, matching `go install`. Existing `PATH` entries keep priority and nothing is prepended. Worker processes inherit the same widened `PATH`. If a tool lives somewhere else, set its absolute path:

| Tool | `inkuna.yaml` | Environment variable |
|---|---|---|
| `codex`, `claude`, `copilot` | `runtimes.<name>.binary` | `INKUNA_CODEX_BINARY`, `INKUNA_CLAUDE_BINARY`, `INKUNA_COPILOT_BINARY` |
| `gh` | `github.binary` | `INKUNA_GITHUB_BINARY` |
| `git`, `go`, `node`, `npm` | not configurable in the file | `INKUNA_GIT_BINARY`, `INKUNA_GO_BINARY`, `INKUNA_NODE_BINARY`, `INKUNA_NPM_BINARY` |

The environment variable wins when both are set. The health check on the Projects page names the applicable override for each missing tool.

## Environment variable overrides

Every variable is optional and wins over the file.

| Variable | Overrides |
| --- | --- |
| `INKUNA_CONFIG` | Path to `inkuna.yaml` |
| `INKUNA_DATA_DIR` | Data directory (SQLite, artifacts, worktrees, global agents) |
| `LINEAR_API_KEY` | Linear credential for this launch (also resolvable from the keychain) |
| `INKUNA_LINEAR_ENDPOINT` | `linear.endpoint` |
| `INKUNA_LINEAR_TIMEOUT` | `timeouts.linear` |
| `INKUNA_HEALTH_TIMEOUT` | `timeouts.health` |
| `INKUNA_AUTHENTICATION_TIMEOUT` | `timeouts.auth` |
| `INKUNA_MAX_CONCURRENCY` | `concurrency.maxWorkers` |
| `INKUNA_MAX_TASKS` | `concurrency.maxTasks` |
| `INKUNA_CHECK_LOG_BYTES` | `checks.logBytes` |
| `INKUNA_PLANNING_TIMEOUT` | `timeouts.planning` |
| `INKUNA_WORKER_TIMEOUT` | `timeouts.worker` |
| `INKUNA_VERIFIER_TIMEOUT` | `timeouts.verifier` |
| `INKUNA_APPROVAL_TIMEOUT` | `timeouts.approval` (also used for human-input stages) |
| `INKUNA_ORCHESTRATOR_RUNTIME` | `orchestrator.runtime` |
| `INKUNA_ORCHESTRATOR_MODEL` | `orchestrator.model` |
| `INKUNA_DESKTOP_NOTIFICATIONS` | `notifications.desktop` (boolean) |
| `INKUNA_STAGE_DIRECTORIES` | `stages.directories`, replaced whole by a list separated like `PATH` (`:` on macOS and Linux) |
| `INKUNA_CODEX_BINARY` | `runtimes.codex.binary` |
| `INKUNA_CLAUDE_BINARY` | `runtimes.claude.binary` |
| `INKUNA_COPILOT_BINARY` | `runtimes.copilot.binary` |
| `INKUNA_GIT_BINARY` | Git executable |
| `INKUNA_GITHUB_BINARY` | `github.binary` |
| `INKUNA_GITHUB_HOST` | `github.host` |
| `INKUNA_GITHUB_GIT_PROTOCOL` | `github.protocol` |
| `INKUNA_GO_BINARY`, `INKUNA_NODE_BINARY`, `INKUNA_NPM_BINARY` | Tool executables used by health checks and verification |
| `INKUNA_APPLICATIONS_DIR` | The folder `inkunactl install` puts `Inkuna.app` in on macOS, instead of `/Applications` ([install.md](install.md#where-everything-goes)) |

Inkuna does not load `.env` files. `.env.example` documents the variables for shell profiles and launchers.

## Secrets

Secrets are resolved by `secrets.Resolver` in this order:

1. The environment variable with the same name, if non-empty.
2. The OS keychain item under service `ai.inkuna.inkuna` (macOS Keychain, Windows Credential Manager, or a libsecret service on Linux), or, in a binary that embeds Inkuna with a secret store of its own, that store ([extensions.md](extensions.md#secret-store)).
3. Otherwise the secret is reported as `missing`; features that need it stay disabled.

Rules that follow from this:

- Values are never written to `inkuna.yaml` or SQLite. The file, the `Configuration()` binding, and `inkunactl config` only expose name, source (`environment`, `keychain`, or an embedding extension's name, and `missing`), and availability.
- A worker process receives extra environment variables only for names listed under `runtimes.<runtime>.secrets` or in the agent's `secrets` frontmatter, and only if those names are declared in the `secrets` block. Anything else is ignored.
- The Linear credential is looked up as `LINEAR_API_KEY`: a session key entered in the Connections screen wins, then the environment, then the keychain. It imports issues and makes the writes a repository's `.inkuna/tracker.yaml` allows; Connections says which writes it may make, and a key that can only read keeps importing.
- Runtime event streams are redacted before storage (patterns like `api_key`, `token`, `secret`, `password`, `authorization`).
- The values of the secrets a worker is given, its exported variables and its MCP servers' secrets, are replaced with `[REDACTED]` wherever the worker repeats them in what Inkuna keeps: its events, its failure, and its handoff. A value shorter than 8 bytes could not be told from ordinary text, so a step whose secret is that short does not start, and says which secret to replace with a full credential.

## Setup wizard and CLI

```sh
go run ./cmd/inkunactl setup
```

The wizard asks for the data directory, orchestrator runtime and model, maximum concurrent tasks, desktop notifications, repositories to register, and secrets to store. Secret values are read with terminal echo disabled where a TTY is available and go straight to the keychain; the wizard then writes `inkuna.yaml`.

Other commands:

```sh
inkunactl config show            # effective configuration plus secret statuses (no values)
inkunactl config example         # starter inkuna.yaml on stdout
inkunactl init [REPOSITORY]      # add .inkuna/out/ to the repository's .gitignore
printf '%s' "$VALUE" | inkunactl secret set NAME   # value read from stdin, never argv
inkunactl secret delete NAME
inkunactl secret list
inkunactl agents list REPOSITORY
inkunactl agent validate FILE
inkunactl rules show [--expanded] REPOSITORY   # --expanded: shared stages and groups resolved, as planning sees them
inkunactl rules validate FILE
inkunactl stages list [REPOSITORY]             # built-in, global, and (with REPOSITORY) repository shared stages and groups
inkunactl install [--check] [--yes]            # prerequisites, then build and install the app and CLI (docs/install.md)
inkunactl update [--check | --rollback] [--yes] # bring an install up to date with origin/main, or swap the previous build back (docs/install.md#updating)
inkunactl doctor                               # dependencies, authentication, and which build this is
inkunactl version [--json]                     # the version and commit this build was made from
```

`secret set` also declares the name in `inkuna.yaml` when it is new.

## Finding the version

Every build of the app and `inkunactl` records the version and commit it was built from. To see them:

- `inkunactl version` prints both; `inkunactl version --json` prints `{"version": "...", "commit": "..."}` for the install and update commands.
- `inkunactl doctor` lists them first, as the `Inkuna` check.
- The app's **Settings** screen shows them under its title, beside the data directory.

The version is `git describe --tags --always --dirty` at build time: the nearest tag, or the short commit until the repository has tags, ending in `-dirty` when the checkout had uncommitted changes. The commit is the full hash. `task build` and `task package` stamp both into `internal/version` with `-ldflags -X` (`LDFLAGS` in `Taskfile.yml`); a build without stamps, such as `go run` or a plain `go build`, reports `dev` for both.

The packaged app also carries a release version, which the operating system shows: macOS in the bundle's Get Info, Windows in the executable's properties. Both take numbers only, so it is not `git describe` but `info.productVersion` in `wails.json`, kept equal to the frontend's `version` in `frontend/package.json` and bumped with it for a release. The bundle ID is `ai.inkuna.inkuna`, the service Inkuna's keychain items use; a `wails dev` build is `ai.inkuna.inkuna.dev`, so macOS does not take it for the installed app. The ID must not change once the app is released, since macOS keeps an app's preferences, WebKit storage, and permission grants under it. `package_test.go` fails the build when any of these drifts back to Wails' placeholders.

## Projects

`repositories[]` is the list of connected projects. The database keeps each project's id, which its tasks and runs refer to, and Go brings it in line with the list whenever the file is saved and at startup (`reconcileProjects` in `internal/app/projects.go`), so the two never drift:

- An entry connects its repository, creating its project or bringing back a removed one, and gives the project the entry's name and runtime.
- A connected project whose entry is gone is removed, keeping its history. That covers an entry deleted by hand, which takes effect at the next start, and one a Settings save drops.
- A save that would remove a project with a run that has not ended is refused, and nothing is written.
- A project added to the database before `inkuna.yaml` held the list, which no entry has ever named, is written into the file at startup rather than removed (`projects.listed`, migration 032).

**Projects → Add project** connects a repository. **Choose folder…** opens the native picker, or a path can be typed. Go works out what the folder connects (`InspectRepository`): the main checkout of the git repository the folder is in, which may be a subdirectory or a worktree, and a name, origin's repository name or else the folder's. It refuses a path that is not absolute, not a folder, or not in a git repository, one already connected, naming its project, and a name another entry has. A removed project's repository brings that project back, with its history. `AddProject` then saves the new entry through the same validation and atomic write as a Settings save.

**Remove…** on a project card opens what removal would do (`PreviewRemoveProject`): the project's tasks and runs, and the worktrees of its runs still on disk. `RemoveProject(projectID, deleteHistory)` takes its entry out of `inkuna.yaml` and marks it removed (`projects.removed_at`). It is refused while any of its runs has not ended, in memory or as stored, since a run paused for review outlives the process. Removal holds the configuration lock, which starting a run also takes, so none can start meanwhile.

- **Keeping its history**, the default: its tasks and runs stay readable in Tasks and Costs under its name, marked removed.
- **Deleting its history**: its finished tasks are deleted as Tasks deletes them ([data lifecycle](architecture.md#data-lifecycle)), keeping worktrees and branches.

Either way the repository on disk, its branches, its runs' worktrees, GitHub, and Linear are left alone. A removed project takes no new work: planning, approving, resuming, rerunning, retitling, rules and agent edits, branch changes, and tracker writes are refused with a reason that says to connect it again (`workableProject`). Removing a run's worktree from the run view, cancelling, and reading its history still work.

## Desktop Settings screen

The Settings screen in the app edits the same file through the `Configuration`, `SaveConfiguration`, `SaveSecret`, and `DeleteSecret` bindings. Saving validates the file, writes it, reloads the effective configuration, and brings the projects in line with its repositories ([Projects](#projects)). Secret fields show only whether a value is available and where it came from. Changes apply to new runs; runs already in flight keep the runtimes they started with.
