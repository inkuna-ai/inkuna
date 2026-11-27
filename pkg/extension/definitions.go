package extension

import "context"

// DefinitionKind is what a shared definition is.
type DefinitionKind string

// The kinds of definition a source can share. Later versions may add kinds;
// a build that does not know one refuses plans in the project rather than
// misread it.
const (
	KindAgent DefinitionKind = "agent"
	KindSkill DefinitionKind = "skill"
	KindStage DefinitionKind = "stage"
	KindGroup DefinitionKind = "group"
	KindRule  DefinitionKind = "rule"
)

// DefinitionMode says how Inkuna uses a definition. A build that does not
// apply a mode never applies the definition as anything weaker: it ignores
// a shared one and refuses plans in the project for an enforced one, and a
// mode it does not know refuses as enforced does.
type DefinitionMode string

const (
	// ModeCatalog offers the definition beside Inkuna's own catalog. It is
	// used only once someone adds it, which copies it into the repository.
	ModeCatalog DefinitionMode = "catalog"
	// ModeShared uses the definition live, below the repository, which can
	// override it by name.
	ModeShared DefinitionMode = "shared"
	// ModeEnforced uses the definition live, above the repository, which
	// cannot override it.
	ModeEnforced DefinitionMode = "enforced"
)

// Definition is one agent, skill, shared stage, group, or rule a source
// shares, in the formats Inkuna reads from files. Inkuna checks it as it
// checks the same file in a repository, and it is granted no more than such
// a file: the MCP servers and secrets it names must still be declared in
// inkuna.yaml.
type Definition struct {
	Kind DefinitionKind `json:"kind"`
	// Name is the definition's name, which its files must use too.
	Name string         `json:"name"`
	Mode DefinitionMode `json:"mode"`
	// Scope is who the source shares it with, in the source's own words,
	// such as "team:platform". Inkuna shows and records it.
	Scope string `json:"scope,omitempty"`
	// Version changes whenever the definition's content does.
	Version string `json:"version,omitempty"`
	// Files are the definition's files by slash-separated path:
	//   - an agent: "<name>.md", an agent file;
	//   - a skill: "SKILL.md" and any files beside it, as in its directory;
	//   - a shared stage or group: "<name>.yaml", a stage library file
	//     defining only Name;
	//   - a rule: "<name>.yaml", a rules file holding only that rule.
	Files map[string][]byte `json:"files"`
}

// Snapshot is everything a source shares for one person in one project.
type Snapshot struct {
	// Version changes whenever any definition in the snapshot does. Plans
	// record it.
	Version     string       `json:"version"`
	Definitions []Definition `json:"definitions"`
}

// Project identifies a repository to a definition source.
type Project struct {
	Name string `json:"name"`
	// Remote is the URL of the repository's origin, so a source can share
	// by repository. It is empty when the repository has none.
	Remote string `json:"remote,omitempty"`
}

// DefinitionSource shares definitions from outside the machine. Inkuna asks
// it for a project's snapshot when the app starts, before each plan, and
// when the catalog is listed, and keeps the last one it got in its data
// directory for when the source cannot be reached. The snapshot holds only
// what the principal may use in the project: who may see a definition is
// the source's business.
type DefinitionSource interface {
	Snapshot(context.Context, Principal, Project) (Snapshot, error)
}
