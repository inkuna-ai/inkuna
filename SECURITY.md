# Security policy

Inkuna runs coding agents on your machine with your repositories and your
credentials, so it promises to give each agent only what it was granted. A
way around that promise is a vulnerability, and we want to hear about it.

## Reporting a vulnerability

Email security@madcha.io. Do not open a public issue, pull request, or
discussion for it.

Include what you can of:

- what an attacker can do, and what they need first (a repository you clone,
  a rule, an agent, an MCP server, a tracker issue);
- the steps to reproduce it, and the Inkuna version or commit
  (`inkunactl version`);
- your operating system and the runtime involved (Codex, Claude, Copilot, or
  another).

We confirm we received the report, tell you whether we can reproduce it, and
agree a disclosure date with you once a fix is ready. Unless you ask us not
to, we credit you in the release notes.

## In scope

- An agent getting more than it was granted: shell access, files outside its
  worktree, write access a read-only stage was denied, or an MCP server or tool
  it was not given.
- A secret reaching a model, a log, the database, or a file Inkuna writes.
- A repository's own files (`.inkuna/`, provider settings, docs) changing
  what Inkuna allows, or running code, when that repository is opened or
  planned.
- A push, pull request, GitHub write, or tracker write made without the
  approval Inkuna asks for.
- Script running in the app's window, for example through a rendered doc or
  diagram.

## Out of scope

- Vulnerabilities in the agent CLIs Inkuna starts (Codex, Claude Code,
  Copilot) or in model providers; report those to their vendors.
- What an agent does inside the permissions it was granted.
- An MCP server or runtime the user opted in with full access, which Inkuna
  says it does not review.

## Supported versions

Fixes land on `main` and in the next release. Until releases are tagged, only
the latest `main` is supported.
