package branching

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

var (
	keyStory    = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9][0-9]*$`)
	numberStory = regexp.MustCompile(`^[1-9][0-9]*$`)
	keyInText   = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*-[1-9][0-9]*)\b`)
	keyLeading  = regexp.MustCompile(`^\W*([A-Z][A-Z0-9_]*-[1-9][0-9]*)\b`)
	issueInURL  = regexp.MustCompile(`/issues?/([1-9][0-9]*)\b`)
	hashInText  = regexp.MustCompile(`(?:^|[^A-Za-z0-9&])#([1-9][0-9]*)\b`)
	slugBreaks  = regexp.MustCompile(`[^a-z0-9]+`)
)

// Story is what a branch is named after.
type Story struct {
	// Type is one of the configuration's branch types.
	Type string
	// ID is the tracker's id for the story, or empty when there is none.
	ID string
	// Description becomes the slug.
	Description string
}

// NormalizeStory returns the story id in the form branch names use for the
// tracker: MAD-12 for Linear and Jira, 12 for GitHub (from 12 or #12).
func NormalizeStory(tracker Tracker, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	switch tracker {
	case TrackerLinear, TrackerJira:
		upper := strings.ToUpper(id)
		if !keyStory.MatchString(upper) {
			return "", fmt.Errorf("%s story id %q must look like ABC-123", tracker, id)
		}
		return upper, nil
	case TrackerGitHub:
		number := strings.TrimPrefix(id, "#")
		if !numberStory.MatchString(number) {
			return "", fmt.Errorf("github story id %q must be an issue number such as 123 or #123", id)
		}
		return number, nil
	case TrackerNone:
		return "", fmt.Errorf("the repository's tracker is none, so branches carry no story id (got %q)", id)
	}
	return "", fmt.Errorf("unknown tracker %q", tracker)
}

// Name renders a branch name exactly as asked, for a person or agent naming
// a branch by hand: an unknown type, a malformed story id, or a name over
// the cap is an error rather than something to repair.
func (config Config) Name(story Story) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}
	if strings.Contains(config.Branch.Pattern, "{type}") && !contains(config.Branch.Types, story.Type) {
		return "", fmt.Errorf("type %q must be one of: %s", story.Type, strings.Join(config.Branch.Types, ", "))
	}
	id, err := NormalizeStory(config.Tracker, story.ID)
	if err != nil {
		return "", err
	}
	if id == "" && config.Tracker != TrackerNone && strings.Contains(config.Branch.Pattern, "{story}") {
		return "", fmt.Errorf("a %s story id is required by the pattern %s", config.Tracker, config.Branch.Pattern)
	}
	slug := Slug(story.Description)
	if slug == "" {
		return "", errors.New("the description must contain at least one letter or digit")
	}
	name := render(config.Branch.Pattern, story.Type, id, slug)
	if err := checkRefName(name); err != nil {
		return "", fmt.Errorf("branch name %q %w", name, err)
	}
	if limit := config.Branch.MaxLength; limit > 0 && len(name) > limit {
		return "", fmt.Errorf("branch name is %d characters; maximum is %d: %s", len(name), limit, name)
	}
	return name, nil
}

// ForTask derives a run's branch name from its task: the type from a label
// naming one or else the task kind, the story id from the task's source,
// labels, or objective, and a slug from the title shortened to fit. A task
// from before titles takes its slug from the objective's first line.
func (config Config) ForTask(task domain.TaskSpec) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}
	branchType := config.TypeFor(task)
	id := StoryFor(config.Tracker, task)
	description := task.Title
	if description == "" {
		// An untitled task's first line is held to a title's length, so its
		// slug stays one git can lock whatever the objective (#101).
		description, _, _ = strings.Cut(task.Objective, "\n")
		if runes := []rune(description); len(runes) > domain.MaxTitleRunes {
			description = string(runes[:domain.MaxTitleRunes])
		}
	}
	if id != "" {
		if config.Tracker == TrackerGitHub {
			// A GitHub id is bare digits, so only the #12 reference goes, with
			// the same boundary hashInText found it by; the same digits
			// elsewhere (120 FPS, #120, C#12) are part of the description.
			description = regexp.MustCompile(`(^|[^A-Za-z0-9&])#`+id+`\b`).ReplaceAllString(description, "${1} ")
		} else {
			description = strings.ReplaceAll(description, id, " ")
		}
	}
	slug := Slug(description)
	if slug == "" {
		slug = "change"
	}
	name := render(config.Branch.Pattern, branchType, id, slug)
	if limit := config.Branch.MaxLength; limit > 0 && len(name) > limit {
		room := limit - (len(name) - len(slug))
		if room < 1 {
			return "", fmt.Errorf("branch.maxLength %d leaves no room for a slug in %s", limit, render(config.Branch.Pattern, branchType, id, ""))
		}
		name = render(config.Branch.Pattern, branchType, id, shorten(slug, room))
	}
	if err := checkRefName(name); err != nil {
		return "", fmt.Errorf("branch name %q %w", name, err)
	}
	return name, nil
}

// TypeFor picks a task's branch type: a task label equal to a configured type
// wins, then the configured mapping for the task kind, then a conventional
// type for the kind when the configuration lists it, then the first type.
func (config Config) TypeFor(task domain.TaskSpec) string {
	for _, label := range task.Labels {
		for _, candidate := range config.Branch.Types {
			if strings.EqualFold(strings.TrimSpace(label), candidate) {
				return candidate
			}
		}
	}
	if mapped, ok := config.Branch.KindTypes[task.Kind]; ok {
		return mapped
	}
	conventional := map[domain.TaskKind][]string{
		domain.TaskImplementation: {"feature", "feat"},
		domain.TaskBugfix:         {"bug", "fix", "bugfix"},
		domain.TaskDocumentation:  {"docs", "doc", "enabler", "chore"},
		domain.TaskSkillAuthoring: {"chore", "enabler"},
		domain.TaskResearch:       {"spike", "chore"},
		domain.TaskReview:         {"chore"},
	}
	for _, candidate := range conventional[task.Kind] {
		if contains(config.Branch.Types, candidate) {
			return candidate
		}
	}
	return config.Branch.Types[0]
}

// StoryFor finds the task's story id in the tracker's format: from the
// imported issue first, then a task label, then the title, then the
// objective. A key-style id counts in the title or objective only when it
// leads it ("MAD-12: ..."), so words such as UTF-8 are not mistaken for
// stories. A task with none, or one in a different tracker's format, has no
// story.
func StoryFor(tracker Tracker, task domain.TaskSpec) string {
	candidates := append([]string{task.Source.Reference}, task.Labels...)
	for _, text := range candidates {
		if id := findStory(tracker, text, keyInText); id != "" {
			return id
		}
	}
	if id := findStory(tracker, task.Title, keyLeading); id != "" {
		return id
	}
	return findStory(tracker, task.Objective, keyLeading)
}

func findStory(tracker Tracker, text string, key *regexp.Regexp) string {
	switch tracker {
	case TrackerLinear, TrackerJira:
		if match := key.FindStringSubmatch(text); match != nil {
			return match[1]
		}
	case TrackerGitHub:
		if match := issueInURL.FindStringSubmatch(text); match != nil {
			return match[1]
		}
		if match := hashInText.FindStringSubmatch(text); match != nil {
			return match[1]
		}
	}
	return ""
}

// Slug turns a description into lower-case words joined by dashes.
func Slug(description string) string {
	return strings.Trim(slugBreaks.ReplaceAllString(strings.ToLower(description), "-"), "-")
}

// Unique returns name, or name with -2, -3, ... when taken, shortening the
// slug end so a suffixed name still fits the cap.
func Unique(name string, maxLength int, taken func(string) bool) (string, error) {
	if !taken(name) {
		return name, nil
	}
	for counter := 2; counter < 100; counter++ {
		suffix := "-" + strconv.Itoa(counter)
		stem := name
		if maxLength > 0 && len(stem)+len(suffix) > maxLength {
			stem = strings.TrimRight(stem[:maxLength-len(suffix)], "-./_")
		}
		candidate := stem + suffix
		if checkRefName(candidate) == nil && !taken(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("branch %s and its numbered variants are all taken", name)
}

// render substitutes the placeholders. Without a story id, {story} is
// dropped together with the separator ('-', '_', '.', or '/') that joined it
// to its neighbour, so "{type}/{story}-{slug}" becomes "feature/add-login"
// and "{story}/{slug}" becomes "add-login".
func render(pattern, branchType, story, slug string) string {
	if story == "" && strings.Contains(pattern, "{story}") {
		index := strings.Index(pattern, "{story}")
		end := index + len("{story}")
		switch {
		case end < len(pattern) && strings.ContainsRune("-_./", rune(pattern[end])):
			pattern = pattern[:index] + pattern[end+1:]
		case index > 0 && strings.ContainsRune("-_./", rune(pattern[index-1])):
			pattern = pattern[:index-1] + pattern[end:]
		default:
			pattern = pattern[:index] + pattern[end:]
		}
		pattern = strings.ReplaceAll(pattern, "//", "/")
	}
	replacer := strings.NewReplacer("{type}", branchType, "{story}", story, "{slug}", slug)
	return replacer.Replace(pattern)
}

// shorten cuts a slug to at most limit characters, at a word boundary when
// one is near enough to keep most of it.
func shorten(slug string, limit int) string {
	if len(slug) <= limit {
		return slug
	}
	cut := slug[:limit]
	if boundary := strings.LastIndex(cut, "-"); boundary >= limit/2 {
		cut = cut[:boundary]
	}
	return strings.Trim(cut, "-")
}

func contains(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}
