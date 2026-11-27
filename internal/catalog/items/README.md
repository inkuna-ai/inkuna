# The Inkuna catalog

Items here ship in the binary and are used only once someone adds them with `inkunactl catalog add` or from the app. See `docs/inkuna-directory.md#the-catalog`.

Each kind has its own directory, and a file's name is its item's name. Names are shared by every kind and may not be a built-in's.

| Directory | Holds | Format |
| --- | --- | --- |
| `agents/<name>.md` | one agent | an `.inkuna/agents/*.md` file, with only the frontmatter keys Inkuna reads |
| `skills/<name>/` | one skill | an `.inkuna/skills/<name>/` directory: its `SKILL.md` and any files beside it |
| `stages/<name>.yaml` | one shared stage | a stage library file (`version: 2`, `stages:`) defining only `<name>`, opening with a comment that describes it |
| `groups/<name>.yaml` | one group | a stage library file (`version: 2`, `groups:`) defining only `<name>`, opening with a comment that describes it |
| `rules/<name>.yaml` | one rule | a rules file (`version: 2`, `rules:`) holding only that rule, with a `description` and a `when` that narrows it |

A rule or group may use the built-in shared stages and groups and the catalog's, and name the built-in agents and the catalog's. An agent brings the catalog's skills it names. `internal/catalog/self_test.go` fails the build when an item does not load or a rule does not plan.
