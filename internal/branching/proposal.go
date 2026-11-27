package branching

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Evidence is what a repository shows about how it already branches. It is
// gathered read-only from git and is all the orchestrator sees when it
// proposes missing configuration.
type Evidence struct {
	// Repository is the repository's directory name.
	Repository string `json:"repository"`
	// RemoteHost is the host of origin, such as github.com.
	RemoteHost string `json:"remoteHost,omitempty"`
	// DefaultBranch is what origin/HEAD points at, without "origin/".
	DefaultBranch string `json:"defaultBranch,omitempty"`
	CurrentBranch string `json:"currentBranch,omitempty"`
	// Branches are recent local and origin branch names, newest first,
	// without the "origin/" prefix.
	Branches []string `json:"branches"`
	// Worktrees are the other worktrees' paths relative to the repository.
	Worktrees []string `json:"worktrees"`
}

// Proposal holds values for missing keys, from the orchestrator or from
// Infer. Only the keys a configuration lacks are ever used.
type Proposal struct {
	Tracker   Tracker `json:"tracker,omitempty"`
	Base      string  `json:"base,omitempty"`
	Pattern   string  `json:"pattern,omitempty"`
	Location  string  `json:"location,omitempty"`
	Rationale string  `json:"rationale,omitempty"`
}

var (
	keyBranch    = regexp.MustCompile(`(^|/)[A-Z][A-Z0-9_]*-[1-9][0-9]*([-_/]|$)`)
	numberBranch = regexp.MustCompile(`(^|/)[1-9][0-9]*-`)
	typedBranch  = regexp.MustCompile(`^[a-z][a-z0-9-]*/`)
)

// Infer proposes values deterministically from the evidence. It is the
// fallback when the orchestrator is disabled, unavailable, or wrong.
func Infer(evidence Evidence) Proposal {
	keyed, numbered, typed := 0, 0, 0
	for _, branch := range evidence.Branches {
		if keyBranch.MatchString(branch) {
			keyed++
		}
		if numberBranch.MatchString(branch) {
			numbered++
		}
		if typedBranch.MatchString(branch) {
			typed++
		}
	}
	proposal := Proposal{Tracker: TrackerNone}
	host := strings.ToLower(evidence.RemoteHost)
	switch {
	case keyed > 0 && (strings.Contains(host, "atlassian") || strings.Contains(host, "bitbucket")):
		proposal.Tracker = TrackerJira
	case keyed > 0:
		proposal.Tracker = TrackerLinear
	case numbered > 0 && strings.Contains(host, "github"):
		proposal.Tracker = TrackerGitHub
	}
	proposal.Base = inferBase(evidence)
	switch {
	case typed > 0 && proposal.Tracker != TrackerNone:
		proposal.Pattern = "{type}/{story}-{slug}"
	case typed > 0:
		proposal.Pattern = "{type}/{slug}"
	case proposal.Tracker != TrackerNone:
		proposal.Pattern = "{story}-{slug}"
	default:
		proposal.Pattern = "{type}/{slug}"
	}
	sibling := filepath.Join("..", evidence.Repository+"-worktrees")
	for _, path := range evidence.Worktrees {
		if path == sibling || strings.HasPrefix(path, sibling+string(filepath.Separator)) {
			proposal.Location = "../{repo}-worktrees"
			break
		}
	}
	proposal.Rationale = fmt.Sprintf("inferred from %d recent branches", len(evidence.Branches))
	return proposal
}

func inferBase(evidence Evidence) string {
	if evidence.DefaultBranch != "" {
		return evidence.DefaultBranch
	}
	for _, candidate := range []string{"main", "master", "trunk", "develop"} {
		if contains(evidence.Branches, candidate) {
			return candidate
		}
	}
	if evidence.CurrentBranch != "" {
		return evidence.CurrentBranch
	}
	return "main"
}

// Resolve fills each missing key from the orchestrator's proposal when that
// value is valid, and from Infer otherwise. A proposed base must be a branch
// the repository has. The notes say where every value came from.
func Resolve(config Config, missing []string, proposed *Proposal, evidence Evidence) (Config, map[string]string, []string) {
	inferred := Infer(evidence)
	values := make(map[string]string, len(missing))
	notes := make([]string, 0, len(missing))
	choose := func(key, offered, fallback string, check func(string) error) string {
		switch {
		case proposed == nil || offered == "":
			notes = append(notes, fmt.Sprintf("%s: %s (inferred from the repository)", key, fallback))
		case check(offered) == nil:
			values[key] = offered
			notes = append(notes, fmt.Sprintf("%s: %s (proposed by the orchestrator)", key, offered))
			return offered
		default:
			notes = append(notes, fmt.Sprintf("%s: the orchestrator's %q was rejected (%v); using %s", key, offered, check(offered), fallback))
		}
		values[key] = fallback
		return fallback
	}
	offer := Proposal{}
	if proposed != nil {
		offer = *proposed
	}
	baseExists := func(value string) error {
		if err := ValidateBase(value); err != nil {
			return err
		}
		if value != evidence.DefaultBranch && value != evidence.CurrentBranch && !contains(evidence.Branches, value) {
			return fmt.Errorf("the repository has no branch %q", value)
		}
		return nil
	}
	for _, key := range missing {
		switch key {
		case "tracker":
			config.Tracker = Tracker(choose(key, string(offer.Tracker), string(inferred.Tracker), func(value string) error { return ValidateTracker(Tracker(value)) }))
		case "branch.base":
			config.Branch.Base = choose(key, offer.Base, inferred.Base, baseExists)
		case "branch.pattern":
			config.Branch.Pattern = choose(key, offer.Pattern, inferred.Pattern, ValidatePattern)
		}
	}
	// A whole new file also takes a location, the one optional key the
	// repository's existing worktrees can say something about.
	if len(missing) == len(Required) {
		location := inferred.Location
		if proposed != nil && offer.Location != "" && ValidateLocation(offer.Location) == nil {
			location = offer.Location
		}
		if location != "" {
			config.Worktrees.Location = location
			values["worktrees.location"] = location
			notes = append(notes, "worktrees.location: "+location)
		}
	}
	return config, values, notes
}

// WithValues is config with the values a plan proposed for its missing keys
// set, as Apply will write them, so the plan can name a branch again before
// they are written.
func (config Config) WithValues(values map[string]string) Config {
	if value, ok := values["tracker"]; ok {
		config.Tracker = Tracker(value)
	}
	if value, ok := values["branch.base"]; ok {
		config.Branch.Base = value
	}
	if value, ok := values["branch.pattern"]; ok {
		config.Branch.Pattern = value
	}
	if value, ok := values["worktrees.location"]; ok {
		config.Worktrees.Location = value
	}
	return config
}

// Root resolves where a repository's worktrees are created: under the
// configured location, or under <dataDir>/worktrees/<repo> when none is set.
// A location inside the repository is refused, because its worktrees would
// show up as untracked files in the checkout.
func (config Config) Root(repositoryRoot, dataDir string) (string, error) {
	repositoryRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return "", err
	}
	name := filepath.Base(repositoryRoot)
	location := strings.ReplaceAll(strings.TrimSpace(config.Worktrees.Location), "{repo}", name)
	var root string
	switch {
	case location == "":
		if strings.TrimSpace(dataDir) == "" {
			return "", errors.New("worktrees.location is not set and Inkuna's data directory is unknown")
		}
		root = filepath.Join(dataDir, "worktrees", name)
	case location == "~" || strings.HasPrefix(location, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve worktrees.location: %w", err)
		}
		root = filepath.Join(home, strings.TrimPrefix(location, "~"))
	case filepath.IsAbs(location):
		root = location
	default:
		root = filepath.Join(repositoryRoot, filepath.FromSlash(location))
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if relative, err := filepath.Rel(repositoryRoot, root); err == nil && (relative == "." || !strings.HasPrefix(relative, "..")) {
		return "", fmt.Errorf("worktrees.location %q resolves to %s, inside the repository", config.Worktrees.Location, root)
	}
	return root, nil
}

// Path is where the worktree for a branch goes under root.
func Path(root, branch string) string {
	return filepath.Join(root, filepath.FromSlash(branch))
}

// Apply writes proposed values into a repository's configuration file. A
// missing file is created with every key and a comment for each; an
// existing file only gains the keys it still lacks, with its own content
// and comments untouched. It returns the keys it wrote.
func Apply(repositoryRoot string, values map[string]string) ([]string, error) {
	path := filepath.Join(repositoryRoot, filepath.FromSlash(RelativePath))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		config := Defaults()
		config.Tracker = Tracker(values["tracker"])
		config.Branch.Base = values["branch.base"]
		config.Branch.Pattern = values["branch.pattern"]
		config.Worktrees.Location = values["worktrees.location"]
		if err := config.Validate(); err != nil {
			return nil, fmt.Errorf("proposed branching config: %w", err)
		}
		if err := writeAtomic(path, Render(config)); err != nil {
			return nil, err
		}
		return append([]string(nil), Required...), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read branching config: %w", err)
	}
	loaded, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(loaded.Missing) == 0 {
		return nil, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if document.Kind == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	written := make([]string, 0, len(loaded.Missing))
	for _, key := range loaded.Missing {
		value, ok := values[key]
		if !ok || value == "" {
			return nil, fmt.Errorf("no value proposed for %s", key)
		}
		if err := insert(&document, key, value); err != nil {
			return nil, err
		}
		written = append(written, key)
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	if _, err := Parse(buffer.Bytes()); err != nil {
		return nil, fmt.Errorf("completed branching config: %w", err)
	}
	return written, writeAtomic(path, buffer.Bytes())
}

// Render writes a complete configuration as a commented file.
func Render(config Config) []byte {
	var out strings.Builder
	quote := func(value string) string {
		encoded, _ := yaml.Marshal(value)
		return strings.TrimSpace(string(encoded))
	}
	fmt.Fprintf(&out, "# How branches and worktrees are made for this repository (docs/branching.md\n# in Inkuna). Inkuna runs and `inkunactl branch new` both follow it.\nversion: %d\n", config.Version)
	fmt.Fprintf(&out, "# Where stories come from: linear, jira, github, or none.\ntracker: %s\n", config.Tracker)
	fmt.Fprintf(&out, "branch:\n  # New work starts from origin/<base> (or <base> when origin has none).\n  base: %s\n", quote(config.Branch.Base))
	fmt.Fprintf(&out, "  # {type}, {story}, and {slug}; without a story id, {story} and its separator drop out.\n  pattern: %s\n", quote(config.Branch.Pattern))
	fmt.Fprintf(&out, "  types: [%s]\n", strings.Join(config.Branch.Types, ", "))
	if config.Branch.MaxLength > 0 {
		fmt.Fprintf(&out, "  maxLength: %d\n", config.Branch.MaxLength)
	}
	fmt.Fprintf(&out, "worktrees:\n  # false: writable steps work in the checkout, on the branch it has checked out.\n  enabled: %t\n", config.Worktrees.Enabled)
	fmt.Fprintf(&out, "  # Relative to the repository; {repo} is its directory name; empty is Inkuna's data directory.\n  location: %s\n", quote(config.Worktrees.Location))
	fmt.Fprintf(&out, "  # listener: remove when the run finishes; prune: remove finished runs' worktrees\n  # when a new run starts; merged: once the branch's pull request merged; manual:\n  # only from the run view. Branches are always kept.\n  cleanup: %s\n", config.Worktrees.Cleanup)
	fmt.Fprintf(&out, "out:\n  # What a completed run does with .inkuna/out: archive (keep what is left as one\n  # artifact of the run, then empty it), remove, or keep.\n  cleanup: %s\n", config.Out.Cleanup)
	return []byte(out.String())
}

// insert sets a dotted key in a YAML document, creating parent mappings.
func insert(document *yaml.Node, key, value string) error {
	node := document
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			node.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
		node = node.Content[0]
	}
	parts := strings.Split(key, ".")
	for index, part := range parts {
		if node.Kind != yaml.MappingNode {
			return fmt.Errorf("cannot set %s: %s is not a mapping", key, strings.Join(parts[:index], "."))
		}
		var next *yaml.Node
		for position := 0; position+1 < len(node.Content); position += 2 {
			if node.Content[position].Value == part {
				next = node.Content[position+1]
				break
			}
		}
		last := index == len(parts)-1
		if next != nil && !last && next.Kind == yaml.ScalarNode && (next.Tag == "!!null" || next.Value == "") {
			// "branch:" with nothing under it becomes the mapping it was meant to be.
			next.Kind, next.Tag, next.Value, next.Content = yaml.MappingNode, "", "", nil
		}
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode}
			if last {
				next = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: part}, next)
		} else if last {
			next.Kind, next.Tag, next.Value, next.Content = yaml.ScalarNode, "!!str", value, nil
		}
		node = next
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".branching-*.yaml")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
