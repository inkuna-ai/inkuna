package diff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReviewPath is the repository file that lists more generated paths.
const ReviewPath = ".inkuna/review.yaml"

// ReviewVersion is the only review.yaml version Inkuna reads.
const ReviewVersion = 1

// DefaultGenerated are the paths every repository's views collapse:
// lockfiles at any depth, and the build output and bindings a Wails app
// generates.
var DefaultGenerated = []string{
	"go.sum", "go.work.sum",
	"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb",
	"Cargo.lock", "Gemfile.lock", "composer.lock", "poetry.lock", "Pipfile.lock", "uv.lock",
	"Podfile.lock", "pubspec.lock", "mix.lock", "flake.lock", "packages.lock.json", "gradle.lockfile",
	"ui/dist/**", "frontend/wailsjs/**",
}

// Review is a repository's .inkuna/review.yaml.
type Review struct {
	Version int `yaml:"version"`
	// Generated lists more paths to collapse, in gitignore's pattern
	// syntax. A pattern starting with ! clears what an earlier one, a
	// default, or .gitattributes flagged.
	Generated []string `yaml:"generated"`
}

// LoadReview reads a repository's review.yaml; a repository without one
// gets the zero Review.
func LoadReview(repositoryRoot string) (Review, error) {
	path := filepath.Join(repositoryRoot, filepath.FromSlash(ReviewPath))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Review{}, nil
	}
	if err != nil {
		return Review{}, fmt.Errorf("read review config: %w", err)
	}
	review, err := ParseReview(data)
	if err != nil {
		return Review{}, fmt.Errorf("%s: %w", path, err)
	}
	return review, nil
}

// ParseReview decodes review.yaml strictly: unknown keys and invalid
// patterns are errors.
func ParseReview(data []byte) (Review, error) {
	var review Review
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&review); err != nil && !errors.Is(err, io.EOF) {
		return Review{}, fmt.Errorf("parse review config: %w", err)
	}
	if review.Version != ReviewVersion {
		return Review{}, fmt.Errorf("review config version %d is not %d", review.Version, ReviewVersion)
	}
	for _, pattern := range review.Generated {
		if _, err := compilePattern(pattern); err != nil {
			return Review{}, err
		}
	}
	return review, nil
}

// pattern is one compiled gitignore-style pattern.
type pattern struct {
	match  *regexp.Regexp
	negate bool
}

// compilePattern turns a gitignore-style pattern into a regular expression
// over slash-separated paths. A pattern without a slash before its end
// matches a name at any depth; one with a slash is relative to the
// repository root. A pattern ending in a slash matches only what is inside
// that directory, and any other pattern matches a file or everything
// inside a directory of that name.
func compilePattern(raw string) (pattern, error) {
	text := strings.TrimSpace(raw)
	result := pattern{}
	if rest, ok := strings.CutPrefix(text, "!"); ok {
		result.negate, text = true, rest
	}
	directory := strings.HasSuffix(text, "/")
	text = strings.TrimSuffix(text, "/")
	anchored := strings.Contains(text, "/")
	text = strings.TrimPrefix(text, "/")
	if text == "" {
		return pattern{}, fmt.Errorf("generated pattern %q matches nothing", raw)
	}
	var expression strings.Builder
	expression.WriteString("^")
	if !anchored {
		expression.WriteString("(?:.*/)?")
	}
	for index := 0; index < len(text); index++ {
		switch character := text[index]; {
		case character == '*':
			stars := index
			for stars < len(text) && text[stars] == '*' {
				stars++
			}
			// As in gitignore, stars cross slashes only as a whole path
			// component: a leading **/, a /**/ inside, or a trailing /**.
			// Any other run of stars, such as the one in src/**.go, is one *.
			component := stars-index > 1 && (index == 0 || text[index-1] == '/') && (stars == len(text) || text[stars] == '/')
			switch {
			case !component:
				expression.WriteString("[^/]*")
				index = stars - 1
			case stars < len(text):
				expression.WriteString("(?:.*/)?")
				index = stars
			default:
				expression.WriteString(".*")
				index = stars - 1
			}
		case character == '?':
			expression.WriteString("[^/]")
		case character == '[':
			end := strings.IndexByte(text[index+1:], ']')
			if end < 0 {
				return pattern{}, fmt.Errorf("generated pattern %q has an unclosed [", raw)
			}
			class := text[index+1 : index+1+end]
			if negated, ok := strings.CutPrefix(class, "!"); ok {
				class = "^" + negated
			}
			expression.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			index += end + 1
		case character == '\\' && index+1 < len(text):
			index++
			expression.WriteString(regexp.QuoteMeta(text[index : index+1]))
		default:
			expression.WriteString(regexp.QuoteMeta(text[index : index+1]))
		}
	}
	if directory {
		expression.WriteString("/.*$")
	} else {
		expression.WriteString("(?:/.*)?$")
	}
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return pattern{}, fmt.Errorf("generated pattern %q: %w", raw, err)
	}
	result.match = compiled
	return result, nil
}

// generated decides which paths views collapse: the defaults, then
// linguist-generated in .gitattributes, then the repository's list, each
// able to override the one before.
type generated struct {
	defaults, review []pattern
	// attributes maps a path to its linguist-generated value: true when set,
	// false when unset; absent when .gitattributes says nothing.
	attributes map[string]bool
}

func newGenerated(review Review) (generated, error) {
	var result generated
	for _, raw := range DefaultGenerated {
		compiled, err := compilePattern(raw)
		if err != nil {
			return generated{}, err
		}
		result.defaults = append(result.defaults, compiled)
	}
	for _, raw := range review.Generated {
		compiled, err := compilePattern(raw)
		if err != nil {
			return generated{}, err
		}
		result.review = append(result.review, compiled)
	}
	return result, nil
}

func (rules generated) flags(path string) bool {
	flagged := false
	for _, rule := range rules.defaults {
		if rule.match.MatchString(path) {
			flagged = !rule.negate
		}
	}
	if value, ok := rules.attributes[path]; ok {
		flagged = value
	}
	for _, rule := range rules.review {
		if rule.match.MatchString(path) {
			flagged = !rule.negate
		}
	}
	return flagged
}

// readAttributes asks git for linguist-generated on each path as the
// commit's .gitattributes set it, so the answer is the branch's, not the
// checkout's.
func readAttributes(ctx context.Context, repository, commit string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	input := []byte(strings.Join(paths, "\x00") + "\x00")
	output, err := git(ctx, repository, input, listLimit, "check-attr", "--source="+commit, "-z", "--stdin", "linguist-generated")
	if err != nil {
		return nil, fmt.Errorf("read linguist-generated attributes: %w", err)
	}
	fields := splitZ(output)
	attributes := make(map[string]bool)
	// Each answer is three fields: the path, the attribute, and its value.
	for index := 0; index+2 < len(fields); index += 3 {
		switch value := fields[index+2]; value {
		case "set", "true":
			attributes[fields[index]] = true
		case "unset", "false":
			attributes[fields[index]] = false
		}
	}
	return attributes, nil
}
