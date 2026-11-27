package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/inkuna-ai/inkuna/internal/artifact"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/report"
)

const diffUsage = "usage: inkunactl diff [--commit SHA | --step ID] [--out FILE] [--open] [--split] [--include-large] RUN_ID"

// openPage opens a written page in the system's browser, from argv and
// never through a shell; tests replace it.
var openPage = func(path string) error {
	var opener string
	switch runtime.GOOS {
	case "darwin":
		opener = "open"
	case "linux", "freebsd", "openbsd", "netbsd":
		opener = "xdg-open"
	default:
		return fmt.Errorf("--open is not supported on %s; open %s yourself", runtime.GOOS, path)
	}
	command := exec.Command(opener, path)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open %s with %s: %w", path, opener, err)
	}
	return command.Process.Release()
}

// runDiff writes a run's changes as one HTML page, like a pull request's:
// all of the branch's changes, or one commit's, with its commits and
// checks. It reads Inkuna's database and artifacts as the app does but
// starts nothing, so it can run beside the app.
func runDiff(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("diff", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	commit := flags.String("commit", "", "show one commit on the run's branch")
	step := flags.String("step", "", "show the commit a step of the run made")
	out := flags.String("out", "", "write the page here instead of <data dir>/reports/RUN_ID.html")
	open := flags.Bool("open", false, "open the page in the browser")
	split := flags.Bool("split", false, "open the page with the two versions side by side")
	includeLarge := flags.Bool("include-large", false, "include patches over 1 MB or 20,000 lines, up to 10 MB or 200,000")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, diffUsage)
	}
	if flags.NArg() != 1 {
		return errors.New(diffUsage)
	}
	if *commit != "" && *step != "" {
		return fmt.Errorf("--commit and --step each choose a commit; give one\n%s", diffUsage)
	}
	runID := flags.Arg(0)
	configuration, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	store, path, err := openDatabase()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if _, err := store.Execution(ctx, runID); errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("run %q was not found in %s", runID, path)
	}

	scope := diff.Scope{Kind: diff.ScopeBranch}
	switch {
	case *commit != "":
		scope = diff.Scope{Kind: diff.ScopeCommit, Commit: *commit}
	case *step != "":
		records, err := store.Commits(ctx, runID)
		if err != nil {
			return fmt.Errorf("load the run's commits: %w", err)
		}
		for _, record := range records {
			if record.StepID == *step {
				scope = diff.Scope{Kind: diff.ScopeCommit, Commit: record.Hash}
			}
		}
		if scope.Kind != diff.ScopeCommit {
			return fmt.Errorf("step %q made no commit in run %s", *step, runID)
		}
	}
	artifacts, err := artifact.New(filepath.Join(configuration.DataDir, "artifacts"))
	if err != nil {
		return err
	}
	page, err := report.Build(ctx, store, artifacts, runID, report.Options{Scope: scope, Split: *split, IncludeLarge: *includeLarge, Generated: time.Now()})
	if err != nil {
		return err
	}

	target := *out
	if target == "" {
		if filepath.Base(runID) != runID {
			return fmt.Errorf("run id %q cannot name a file; pass --out", runID)
		}
		target = filepath.Join(configuration.DataDir, "reports", runID+".html")
	}
	if err := writePage(target, page); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, target); err != nil {
		return err
	}
	if *open {
		return openPage(target)
	}
	return nil
}

// writePage replaces path with the page through a temporary file beside
// it, so a page that fails part way leaves any earlier one whole.
func writePage(path string, page report.Page) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".inkuna-diff-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := report.Render(temporary, page); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("render the page: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
