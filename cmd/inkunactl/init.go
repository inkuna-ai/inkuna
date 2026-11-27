package main

import (
	"context"
	"errors"
	"io"

	"github.com/inkuna-ai/inkuna/internal/worktree"
)

const initUsage = "usage: init [REPOSITORY]"

// runInit prepares a repository for Inkuna: its .gitignore ignores
// .inkuna/out, where Inkuna writes a stage's plan document and agents leave
// files for later steps. Inkuna's own commits leave that directory out
// either way; the ignore keeps it out of everyone else's git too. The
// change is the user's to commit.
func runInit(args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return errors.New(initUsage)
	}
	directory := "."
	if len(args) == 1 {
		directory = args[0]
	}
	path, added, err := worktree.IgnoreOut(context.Background(), directory)
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"ignored": worktree.OutDir, "gitignore": path, "added": added})
}
