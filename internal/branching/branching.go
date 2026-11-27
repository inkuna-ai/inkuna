// Package branching reads a repository's branching configuration
// (.inkuna/branching.yaml): which tracker its stories come from, how a
// branch is named, which branch new work starts from, and whether work
// happens in worktrees, where they live, and how they are cleaned up.
// Inkuna's engine and inkunactl both name branches through this package, so
// a branch made by a run and one made by hand follow the same rules.
package branching

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"gopkg.in/yaml.v3"
)

// RelativePath is where a repository keeps its branching configuration.
const RelativePath = ".inkuna/branching.yaml"

// Version is the only configuration version this package reads.
const Version = 1

// Tracker names the system a repository's stories come from. It decides the
// story id format in branch names and which tool an interactive agent uses
// to read a story; Inkuna's own intake is unaffected.
type Tracker string

const (
	TrackerLinear Tracker = "linear"
	TrackerJira   Tracker = "jira"
	TrackerGitHub Tracker = "github"
	TrackerNone   Tracker = "none"
)

// Cleanup says when Inkuna removes the worktree a run created. Removal never
// deletes the branch, and a worktree with uncommitted changes is kept.
type Cleanup string

const (
	// CleanupListener removes a run's worktree as soon as the run finishes.
	CleanupListener Cleanup = "listener"
	// CleanupPrune removes the worktrees of the repository's finished runs
	// whenever a new run starts.
	CleanupPrune Cleanup = "prune"
	// CleanupMerged removes a finished run's worktree once a pull request
	// from its branch has merged, which Inkuna learns when it checks the
	// repository's pull requests.
	CleanupMerged Cleanup = "merged"
	// CleanupManual never removes a worktree on its own; the user does it
	// from the run view.
	CleanupManual Cleanup = "manual"
)

// OutCleanup says what Inkuna does with .inkuna/out of a run's working
// directory once the run completes. Every stage's document and every file
// a step declared are among the run's artifacts already.
type OutCleanup string

const (
	// OutArchive packs what is left in .inkuna/out that the run's
	// artifacts do not hold into one archive artifact, then empties it.
	OutArchive OutCleanup = "archive"
	// OutRemove empties .inkuna/out.
	OutRemove OutCleanup = "remove"
	// OutKeep leaves .inkuna/out as the run left it.
	OutKeep OutCleanup = "keep"
)

// DefaultTypes are the branch types used when the configuration lists none.
var DefaultTypes = []string{"feature", "bug", "chore", "refactor", "hotfix", "enabler"}

// Config is a parsed branching configuration.
type Config struct {
	Version   int       `yaml:"version" json:"version"`
	Tracker   Tracker   `yaml:"tracker" json:"tracker"`
	Branch    Branch    `yaml:"branch" json:"branch"`
	Worktrees Worktrees `yaml:"worktrees" json:"worktrees"`
	Out       Out       `yaml:"out,omitempty" json:"out,omitempty"`
}

// Branch decides a new branch's name and starting point.
type Branch struct {
	// Base is the branch new work starts from. Inkuna prefers origin/<base>
	// so a stale local copy never hides merged work.
	Base string `yaml:"base" json:"base"`
	// Pattern names the branch from {type}, {story}, and {slug}.
	Pattern string `yaml:"pattern" json:"pattern"`
	// Types are the allowed {type} values. A task label naming one picks it.
	Types []string `yaml:"types,omitempty" json:"types,omitempty"`
	// KindTypes maps a task kind to the type its runs use when no label
	// names one.
	KindTypes map[domain.TaskKind]string `yaml:"kindTypes,omitempty" json:"kindTypes,omitempty"`
	// MaxLength caps the whole branch name; zero means no cap.
	MaxLength int `yaml:"maxLength,omitempty" json:"maxLength,omitempty"`
}

// Worktrees decides where writable work happens.
type Worktrees struct {
	// Enabled false runs writable steps in the project checkout, on the
	// branch it has checked out.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Location is the directory worktrees are created under. Relative paths
	// resolve against the repository, {repo} is its directory name, and
	// empty means Inkuna's data directory.
	Location string  `yaml:"location,omitempty" json:"location,omitempty"`
	Cleanup  Cleanup `yaml:"cleanup,omitempty" json:"cleanup,omitempty"`
}

// Out decides what happens to .inkuna/out, where Inkuna and its agents
// leave files for the steps after them.
type Out struct {
	// Cleanup is what a completed run does with it; archive by default.
	Cleanup OutCleanup `yaml:"cleanup,omitempty" json:"cleanup,omitempty"`
}

// Required lists the keys a configuration must set. When any is absent the
// configuration is incomplete and planning proposes the missing values.
var Required = []string{"tracker", "branch.base", "branch.pattern"}

// Loaded is a configuration as found on disk.
type Loaded struct {
	Config Config
	// Exists is false when the repository has no configuration file.
	Exists bool
	// Missing lists the required keys the file does not set.
	Missing []string
	Path    string
}

// Complete reports whether nothing needs to be proposed.
func (loaded Loaded) Complete() bool { return loaded.Exists && len(loaded.Missing) == 0 }

// Load reads a repository's configuration. A missing file is not an error:
// the result reports every required key as missing. Keys that are present
// must be valid; defaults are applied to the optional ones.
func Load(repositoryRoot string) (Loaded, error) {
	path := filepath.Join(repositoryRoot, filepath.FromSlash(RelativePath))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		config := Defaults()
		return Loaded{Config: config, Missing: append([]string(nil), Required...), Path: path}, nil
	}
	if err != nil {
		return Loaded{}, fmt.Errorf("read branching config: %w", err)
	}
	loaded, err := Parse(data)
	if err != nil {
		return Loaded{}, fmt.Errorf("%s: %w", path, err)
	}
	loaded.Path = path
	return loaded, nil
}

// Parse decodes a configuration strictly, applies defaults, and validates
// every key it sets.
func Parse(data []byte) (Loaded, error) {
	config := Defaults()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	// An empty or comment-only file is a file with every key missing.
	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return Loaded{}, fmt.Errorf("parse branching config: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return Loaded{}, fmt.Errorf("parse branching config: %w", err)
	}
	missing := make([]string, 0)
	for _, key := range Required {
		if lookup(&document, key) == nil {
			missing = append(missing, key)
		}
	}
	if err := config.validate(missing); err != nil {
		return Loaded{}, err
	}
	return Loaded{Config: config, Exists: true, Missing: missing}, nil
}

// Defaults returns the configuration a repository starts from: no tracker
// or naming yet, worktrees on in Inkuna's data directory, cleaned up by
// hand, and .inkuna/out archived when a run completes.
func Defaults() Config {
	return Config{
		Version:   Version,
		Branch:    Branch{Types: append([]string(nil), DefaultTypes...)},
		Worktrees: Worktrees{Enabled: true, Cleanup: CleanupManual},
		Out:       Out{Cleanup: OutArchive},
	}
}

// Validate checks a complete configuration.
func (config Config) Validate() error { return config.validate(nil) }

// validate checks every key except the listed missing ones.
func (config Config) validate(missing []string) error {
	skip := make(map[string]bool, len(missing))
	for _, key := range missing {
		skip[key] = true
	}
	if config.Version != Version {
		return fmt.Errorf("version must be %d", Version)
	}
	if !skip["tracker"] {
		if err := ValidateTracker(config.Tracker); err != nil {
			return err
		}
	}
	if !skip["branch.base"] {
		if err := ValidateBase(config.Branch.Base); err != nil {
			return err
		}
	}
	if !skip["branch.pattern"] {
		if err := ValidatePattern(config.Branch.Pattern); err != nil {
			return err
		}
	}
	if len(config.Branch.Types) == 0 {
		return errors.New("branch.types needs at least one type")
	}
	seen := make(map[string]bool, len(config.Branch.Types))
	for _, value := range config.Branch.Types {
		if !typePattern.MatchString(value) {
			return fmt.Errorf("branch type %q must be lower-case letters, digits, and dashes", value)
		}
		if seen[value] {
			return fmt.Errorf("branch type %q is listed twice", value)
		}
		seen[value] = true
	}
	for kind, value := range config.Branch.KindTypes {
		if !validKind(kind) {
			return fmt.Errorf("branch.kindTypes names unknown task kind %q", kind)
		}
		if !seen[value] {
			return fmt.Errorf("branch.kindTypes maps %s to %q, which branch.types does not list", kind, value)
		}
	}
	if config.Branch.MaxLength < 0 || (config.Branch.MaxLength > 0 && config.Branch.MaxLength < 12) {
		return errors.New("branch.maxLength must be 0 (no cap) or at least 12")
	}
	if err := ValidateLocation(config.Worktrees.Location); err != nil {
		return err
	}
	switch config.Worktrees.Cleanup {
	case CleanupListener, CleanupPrune, CleanupMerged, CleanupManual:
	default:
		return fmt.Errorf("worktrees.cleanup %q must be listener, prune, merged, or manual", config.Worktrees.Cleanup)
	}
	switch config.Out.Cleanup {
	case OutArchive, OutRemove, OutKeep:
	default:
		return fmt.Errorf("out.cleanup %q must be archive, remove, or keep", config.Out.Cleanup)
	}
	return nil
}

var (
	typePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	baseComponent  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	patternLiteral = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	placeholder    = regexp.MustCompile(`\{[^}]*\}`)
)

// ValidateTracker accepts the trackers branch names know how to format.
func ValidateTracker(tracker Tracker) error {
	switch tracker {
	case TrackerLinear, TrackerJira, TrackerGitHub, TrackerNone:
		return nil
	}
	return fmt.Errorf("tracker %q must be linear, jira, github, or none", tracker)
}

// ValidateBase accepts a branch name, never an option or a revision
// expression, because it is passed to git as a ref.
func ValidateBase(base string) error {
	if strings.TrimSpace(base) == "" {
		return errors.New("branch.base is required")
	}
	if err := checkRefName(base); err != nil {
		return fmt.Errorf("branch.base %q: %w", base, err)
	}
	for _, component := range strings.Split(base, "/") {
		if !baseComponent.MatchString(component) {
			return fmt.Errorf("branch.base %q may contain only letters, digits, '.', '_', '-', and '/'", base)
		}
	}
	return nil
}

// ValidatePattern accepts a pattern that names every branch uniquely by
// slug and can only render a valid branch name.
func ValidatePattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return errors.New("branch.pattern is required")
	}
	if !strings.Contains(pattern, "{slug}") {
		return errors.New("branch.pattern must contain {slug} so every branch name is distinct")
	}
	for _, found := range placeholder.FindAllString(pattern, -1) {
		if found != "{type}" && found != "{story}" && found != "{slug}" {
			return fmt.Errorf("branch.pattern placeholder %s must be {type}, {story}, or {slug}", found)
		}
	}
	if !patternLiteral.MatchString(placeholder.ReplaceAllString(pattern, "")) {
		return errors.New("branch.pattern may contain only letters, digits, '.', '_', '-', '/', and placeholders")
	}
	// A run without a story id renders the pattern without one, so both
	// forms must be valid branch names.
	for _, story := range []string{"ABC-1", ""} {
		sample := render(pattern, "feature", story, "sample")
		if err := checkRefName(sample); err != nil {
			return fmt.Errorf("branch.pattern renders %q: %w", sample, err)
		}
	}
	return nil
}

// ValidateLocation accepts an empty location (Inkuna's data directory), a
// path relative to the repository, an absolute path, or one under ~/.
func ValidateLocation(location string) error {
	for _, found := range placeholder.FindAllString(location, -1) {
		if found != "{repo}" {
			return fmt.Errorf("worktrees.location placeholder %s must be {repo}", found)
		}
	}
	if strings.ContainsAny(location, "\r\n") {
		return errors.New("worktrees.location must be one line")
	}
	return nil
}

// checkRefName applies git's rules for branch names (git check-ref-format)
// so a rendered name never fails at git or reads as an option.
func checkRefName(name string) error {
	switch {
	case name == "" || name == "@" || name == "HEAD":
		return errors.New("is empty or reserved")
	case strings.HasPrefix(name, "-"):
		return errors.New("starts with '-'")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return errors.New("starts or ends with '/'")
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock"):
		return errors.New("ends with '.' or '.lock'")
	case strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{"):
		return errors.New("contains '..', '//', or '@{'")
	case strings.ContainsAny(name, " ~^:?*[\\\t\r\n"):
		return errors.New("contains a space or one of ~^:?*[\\")
	}
	components := strings.Split(name, "/")
	for index, component := range components {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return fmt.Errorf("component %q starts with '.' or ends with '.lock'", component)
		}
		limit := maxFileName
		if index == len(components)-1 {
			limit = maxRefComponent
		}
		if len(component) > limit {
			return fmt.Errorf("has a component of %d bytes; at most %d fit in a file name", len(component), limit)
		}
	}
	return nil
}

// A ref is a file under a directory for each of its earlier components, and
// file names are held to maxFileName bytes on common file systems. Its last
// component is also the name of the <component>.lock git writes beside it
// while it changes, so it has five bytes fewer; a longer one fails as "File
// name too long" (#101).
const (
	maxFileName     = 255
	maxRefComponent = maxFileName - len(".lock")
)

func validKind(kind domain.TaskKind) bool {
	switch kind {
	case domain.TaskResearch, domain.TaskBugfix, domain.TaskImplementation, domain.TaskReview, domain.TaskDocumentation, domain.TaskSkillAuthoring:
		return true
	}
	return false
}

// lookup finds a dotted key in a decoded YAML document.
func lookup(document *yaml.Node, key string) *yaml.Node {
	node := document
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, part := range strings.Split(key, ".") {
		if node == nil || node.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for index := 0; index+1 < len(node.Content); index += 2 {
			if node.Content[index].Value == part {
				next = node.Content[index+1]
				break
			}
		}
		node = next
	}
	return node
}
