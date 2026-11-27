// Package tracker decides what Inkuna may write to a repository's issue
// tracker and makes those writes. A repository's .inkuna/tracker.yaml is the
// ceiling, reviewed like code; a machine's inkuna.yaml can lower it; and a
// Guard refuses, and records, every write beyond what the two allow
// (docs/mcp-and-trackers.md).
package tracker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"gopkg.in/yaml.v3"
)

// RelativePath is where a repository keeps its tracker settings.
const RelativePath = ".inkuna/tracker.yaml"

// Version is the only tracker.yaml version this package reads.
const Version = 1

// Defaults for the GitHub Project's status field and its options, and
// Linear's In Progress state.
const (
	DefaultField      = "Status"
	DefaultInProgress = "In Progress"
	DefaultDone       = "Done"
)

// Config is a parsed .inkuna/tracker.yaml.
type Config struct {
	Version int `yaml:"version" json:"version"`
	// Writes are the kinds agents may ask for; none when empty.
	Writes  []domain.TrackerKind `yaml:"writes,omitempty" json:"writes,omitempty"`
	Updates Updates              `yaml:"updates,omitempty" json:"updates"`
	Linear  LinearConfig         `yaml:"linear,omitempty" json:"linear"`
	GitHub  GitHubConfig         `yaml:"github,omitempty" json:"github"`
}

// Updates turns Inkuna's own updates on; each is off by default.
type Updates struct {
	Start  bool `yaml:"start,omitempty" json:"start"`
	Result bool `yaml:"result,omitempty" json:"result"`
	Done   bool `yaml:"done,omitempty" json:"done"`
}

// On reports whether an update is on.
func (updates Updates) On(update domain.TrackerUpdate) bool {
	switch update {
	case domain.UpdateStart:
		return updates.Start
	case domain.UpdateResult:
		return updates.Result
	case domain.UpdateDone:
		return updates.Done
	}
	return false
}

// LinearConfig is what a Linear repository adds.
type LinearConfig struct {
	// RequireCurrentCycle refuses to plan a story outside its team's active
	// cycle.
	RequireCurrentCycle bool `yaml:"requireCurrentCycle,omitempty" json:"requireCurrentCycle,omitempty"`
	// InProgress names the state updates.start moves a story to. Empty is
	// the state named In Progress; a team without one names its own here.
	InProgress string `yaml:"inProgress,omitempty" json:"inProgress,omitempty"`
}

// GitHubConfig names the GitHub Project whose single-select field holds a
// GitHub issue's status, and the options for In Progress and Done.
type GitHubConfig struct {
	Project    *Project `yaml:"project,omitempty" json:"project,omitempty"`
	Field      string   `yaml:"field,omitempty" json:"field,omitempty"`
	InProgress string   `yaml:"inProgress,omitempty" json:"inProgress,omitempty"`
	Done       string   `yaml:"done,omitempty" json:"done,omitempty"`
}

// Project is a GitHub Project (v2), as gh project commands name it.
type Project struct {
	Owner  string `yaml:"owner" json:"owner"`
	Number int    `yaml:"number" json:"number"`
}

func (project Project) String() string { return fmt.Sprintf("%s/%d", project.Owner, project.Number) }

// StatusField is the Project field that holds the status.
func (config GitHubConfig) StatusField() string { return firstNonEmpty(config.Field, DefaultField) }

// InProgressOption is the field's option for In Progress.
func (config GitHubConfig) InProgressOption() string {
	return firstNonEmpty(config.InProgress, DefaultInProgress)
}

// DoneOption is the field's option for Done.
func (config GitHubConfig) DoneOption() string { return firstNonEmpty(config.Done, DefaultDone) }

// Loaded is a repository's tracker settings as found on disk.
type Loaded struct {
	Config Config
	// Exists is false when the repository has no tracker.yaml, which
	// allows nothing.
	Exists bool
	Path   string
}

// Load reads a repository's tracker.yaml and checks it against the tracker
// its branching.yaml names. A missing file is not an error; it allows
// nothing.
func Load(repositoryRoot string, tracker branching.Tracker) (Loaded, error) {
	path := filepath.Join(repositoryRoot, filepath.FromSlash(RelativePath))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Loaded{Config: Config{Version: Version}, Path: path}, nil
	}
	if err != nil {
		return Loaded{Path: path}, fmt.Errorf("read tracker settings: %w", err)
	}
	config, err := Parse(data)
	if err == nil {
		err = config.Validate(tracker)
	}
	if err != nil {
		return Loaded{Path: path}, fmt.Errorf("%s: %w", path, err)
	}
	return Loaded{Config: config, Exists: true, Path: path}, nil
}

// Parse decodes tracker.yaml strictly: an unknown key is an error, since a
// misspelt one would otherwise switch nothing on or off without a word.
func Parse(data []byte) (Config, error) {
	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		if errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("parse tracker settings: the file is empty; it needs version: %d", Version)
		}
		return Config{}, fmt.Errorf("parse tracker settings: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("parse tracker settings: the file must hold a single YAML document")
	}
	return config, nil
}

var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// Validate checks the settings for a repository whose stories live in
// tracker.
func (config Config) Validate(tracker branching.Tracker) error {
	if config.Version != Version {
		return fmt.Errorf("version must be %d", Version)
	}
	seen := make(map[domain.TrackerKind]bool, len(config.Writes))
	for _, kind := range config.Writes {
		if !domain.ValidTrackerKind(kind) {
			return fmt.Errorf("writes names %q; the kinds are status, comment, edit, and create", kind)
		}
		if seen[kind] {
			return fmt.Errorf("writes lists %s twice", kind)
		}
		seen[kind] = true
	}
	if project := config.GitHub.Project; project != nil {
		if !ownerPattern.MatchString(project.Owner) {
			return fmt.Errorf("github.project.owner %q must be a GitHub user or organization login", project.Owner)
		}
		if project.Number < 1 {
			return errors.New("github.project.number must be the project's number, 1 or more")
		}
	}
	for _, value := range []struct{ key, value string }{
		{"github.field", config.GitHub.Field}, {"github.inProgress", config.GitHub.InProgress},
		{"github.done", config.GitHub.Done}, {"linear.inProgress", config.Linear.InProgress},
	} {
		if strings.ContainsAny(value.value, "\r\n") || (value.value != "" && strings.TrimSpace(value.value) != value.value) {
			return fmt.Errorf("%s must be one line without surrounding spaces", value.key)
		}
	}
	// A GitHub issue's status is its Project's field, so every write of a
	// status needs the Project named.
	if tracker == branching.TrackerGitHub && config.GitHub.Project == nil {
		needs := make([]string, 0, 3)
		if seen[domain.TrackerStatus] {
			needs = append(needs, "writes: status")
		}
		if config.Updates.Start {
			needs = append(needs, "updates.start")
		}
		if config.Updates.Done {
			needs = append(needs, "updates.done")
		}
		if len(needs) > 0 {
			return fmt.Errorf("%s set a GitHub issue's status, which is the Status field of a GitHub Project; name it under github.project", strings.Join(needs, " and "))
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
