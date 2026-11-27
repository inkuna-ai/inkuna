package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	inkuna "github.com/inkuna-ai/inkuna/internal/app"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/docs"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

const docsUsage = `usage:
  inkunactl docs list REPOSITORY
  inkunactl docs validate [--changed-since REF] REPOSITORY`

// docs validate exit statuses. They are part of its contract so CI can tell
// a doc that needs fixing from a check that never ran.
const (
	docsInvalid   = 1
	docsCannotRun = 2
)

// exitError ends inkunactl with a status other than 1. An empty message
// prints nothing, for a command that has already reported on stdout.
type exitError struct {
	code int
	err  error
}

func (failure exitError) Error() string {
	if failure.err == nil {
		return ""
	}
	return failure.err.Error()
}

func (failure exitError) Unwrap() error { return failure.err }

// runDocs lists and checks a repository's doc index.
func runDocs(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(docsUsage)
	}
	switch args[0] {
	case "list":
		if len(args) != 2 {
			return errors.New(docsUsage)
		}
		indexed, err := indexRepositoryDocs(args[1])
		if err != nil {
			return err
		}
		return writeJSON(stdout, inkuna.DocIndexView{Path: indexed.root, Docs: indexed.docs})
	case "validate":
		err := runDocsValidate(args[1:], stdout)
		var failure exitError
		if err != nil && !errors.As(err, &failure) {
			return exitError{code: docsCannotRun, err: err}
		}
		return err
	default:
		return errors.New(docsUsage)
	}
}

// repositoryDocs is a repository's doc index and where it was read from.
type repositoryDocs struct {
	repository string
	root       string
	docs       []docs.Doc
}

// indexRepositoryDocs indexes a repository's docs as the app does. The docs
// root comes from the repository's inkuna.yaml entry or, for one of its
// worktrees, from the main checkout's entry, the same one a run's worktree
// uses.
func indexRepositoryDocs(path string) (repositoryDocs, error) {
	repository, err := filepath.Abs(path)
	if err != nil {
		return repositoryDocs{}, fmt.Errorf("resolve repository: %w", err)
	}
	configuration, err := config.Load()
	if err != nil {
		return repositoryDocs{}, fmt.Errorf("load config: %w", err)
	}
	entry, declared := configuration.File.Repository(repository)
	if !declared {
		if checkout, err := worktree.MainRoot(context.Background(), repository); err == nil {
			entry, _ = configuration.File.Repository(checkout)
		}
	}
	root, err := docs.Dir(repository, entry.Docs)
	if err != nil {
		return repositoryDocs{}, fmt.Errorf("index docs: %w", err)
	}
	index, err := docs.Discover(repository, entry.Docs)
	if err != nil {
		return repositoryDocs{}, fmt.Errorf("index docs: %w", err)
	}
	return repositoryDocs{repository: repository, root: root, docs: index}, nil
}

// runDocsValidate prints each problem in a repository's docs and a summary,
// and fails when any doc has one. With --changed-since, missing frontmatter
// and broken links fail only in docs changed since the ref, so a repository
// can fix its docs as each one is touched; invalid frontmatter always fails.
func runDocsValidate(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("docs validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	since := flags.String("changed-since", "", "require frontmatter and working links only on docs changed since this git ref")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, docsUsage)
	}
	if flags.NArg() != 1 {
		return errors.New(docsUsage)
	}
	indexed, err := indexRepositoryDocs(flags.Arg(0))
	if err != nil {
		return err
	}
	phased := *since != ""
	var changed map[string]bool
	if phased {
		if changed, err = changedDocs(context.Background(), indexed.repository, indexed.root, *since); err != nil {
			return err
		}
	}
	prefix, err := filepath.Rel(indexed.repository, indexed.root)
	if err != nil {
		return fmt.Errorf("docs root: %w", err)
	}
	var report strings.Builder
	failing, unchanged := 0, 0
	for _, doc := range indexed.docs {
		if len(doc.Problems) == 0 {
			continue
		}
		if phased && phasable(doc.Problems) && !changed[doc.Path] {
			unchanged++
			continue
		}
		failing++
		path := filepath.ToSlash(filepath.Join(prefix, filepath.FromSlash(doc.Path)))
		for _, problem := range doc.Problems {
			// A YAML error spans lines; one line per problem keeps the
			// report greppable.
			fmt.Fprintf(&report, "%s: %s\n", path, strings.Join(strings.Fields(problem), " "))
		}
	}
	noun := "docs"
	if len(indexed.docs) == 1 {
		noun = "doc"
	}
	fmt.Fprintf(&report, "%s/: %d %s, %d failing", filepath.ToSlash(prefix), len(indexed.docs), noun, failing)
	if unchanged > 0 {
		fmt.Fprintf(&report, ", %d unchanged since %s with missing frontmatter or broken links", unchanged, *since)
	}
	report.WriteString("\n")
	if _, err := io.WriteString(stdout, report.String()); err != nil {
		return err
	}
	if failing > 0 {
		return exitError{code: docsInvalid}
	}
	return nil
}

// phasable reports whether --changed-since lets an unchanged doc keep all of
// its problems: no frontmatter at all, or broken links.
func phasable(problems []string) bool {
	for _, problem := range problems {
		if problem != docs.ProblemMissing && !docs.IsLinkProblem(problem) {
			return false
		}
	}
	return true
}

// changedDocs lists the docs under root, relative to it, that the checked-out
// branch has touched since it left ref: changed in commits since the merge
// base of ref and HEAD, staged or unstaged, or untracked. Measuring from the
// merge base keeps docs that changed only on ref's side from counting
// against the branch.
func changedDocs(ctx context.Context, repository, root, ref string) (map[string]bool, error) {
	if strings.HasPrefix(ref, "-") {
		return nil, fmt.Errorf("--changed-since %q is not a git ref", ref)
	}
	output, err := gitOutput(ctx, repository, "merge-base", ref, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("find where HEAD left %s: %w", ref, err)
	}
	base := strings.TrimSpace(output)
	changed := map[string]bool{}
	// A missing docs root indexed as empty, so nothing under it can count.
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return changed, nil
	}
	// Each stage is diffed against the one before it rather than the working
	// tree against the base: a change staged and then reverted on disk, or
	// committed and then reverted, leaves the file matching the base but is
	// still the branch's. Run from the docs root so --relative and ls-files
	// give paths relative to it, the form the index uses; -z keeps unusual
	// names unquoted.
	diff := []string{"diff", "--name-only", "-z", "--no-renames", "--relative"}
	for _, args := range [][]string{
		append(slices.Clone(diff), base, "HEAD", "--"),
		append(slices.Clone(diff), "--cached", "HEAD", "--"),
		append(slices.Clone(diff), "--"),
		{"ls-files", "-z", "--others", "--exclude-standard"},
	} {
		output, err := gitOutput(ctx, root, args...)
		if err != nil {
			return nil, fmt.Errorf("list docs changed since %s: %w", ref, err)
		}
		for _, path := range strings.Split(output, "\x00") {
			if path != "" {
				changed[path] = true
			}
		}
	}
	return changed, nil
}

// gitOutput runs git in a directory and returns its standard output. A
// failure carries git's own message.
func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	output, err := execGit(ctx, directory, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if message := bytes.TrimSpace(exit.Stderr); len(message) > 0 {
				return "", fmt.Errorf("git %s: %s", args[0], message)
			}
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(output), nil
}
