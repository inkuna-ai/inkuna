package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	inkuna "github.com/inkuna-ai/inkuna/internal/app"
	"github.com/inkuna-ai/inkuna/internal/artifact"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

const tasksUsage = "usage: inkunactl tasks list [--finished] | inkunactl tasks delete [--remove-worktrees] [--delete-branches] TASK [TASK...]"

// runTasks lists Inkuna's tasks or deletes finished ones. It reads and
// writes the database and artifact store as the app does, so it can run
// beside the app: the delete reads each run's state again under SQLite's
// write lock, and the app may go on listing a deleted run it holds in
// memory until it restarts.
func runTasks(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(tasksUsage)
	}
	switch args[0] {
	case "list":
		return listTasks(args[1:], stdout)
	case "delete":
		return deleteTasks(args[1:], stdin, stdout)
	}
	return errors.New(tasksUsage)
}

// listTasks prints every task, the newest first, or only the finished ones,
// which are those a delete accepts.
func listTasks(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("tasks list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	finished := flags.Bool("finished", false, "list only tasks none of whose runs is active")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		return errors.New(tasksUsage)
	}
	store, _, err := openDatabase()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	tasks, err := store.HistoryTasks(ctx, nil)
	if err != nil {
		return err
	}
	names, err := projectNames(ctx, store)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "TASK\tPROJECT\tRUNS\tSTATE\tLAST RUN\tTITLE")
	listed := 0
	for _, task := range tasks {
		if *finished && !task.Finished() {
			continue
		}
		listed++
		_, _ = fmt.Fprintf(table, "%s\t%s\t%d\t%s\t%s\t%s\n", task.ID, names[task.ProjectID], len(task.Runs), taskState(task), lastRun(task), oneLine(task.Title, 72))
	}
	if listed == 0 {
		if *finished {
			_, err := fmt.Fprintln(stdout, "No finished tasks.")
			return err
		}
		_, err := fmt.Fprintln(stdout, "No tasks.")
		return err
	}
	return table.Flush()
}

// deleteTasks shows what deleting the tasks would remove and keep, asks
// before it deletes them, and asks again for each branch it would delete,
// naming the commit and what only that branch has.
func deleteTasks(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("tasks delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	removeWorktrees := flags.Bool("remove-worktrees", false, "remove each run's worktree that has no uncommitted changes")
	deleteBranches := flags.Bool("delete-branches", false, "offer to delete each run's branch, one at a time")
	// The tasks may come before the flags, after them, or both.
	var ids []string
	for remaining := args; ; {
		if err := flags.Parse(remaining); err != nil {
			return fmt.Errorf("%w\n%s", err, tasksUsage)
		}
		if flags.NArg() == 0 {
			break
		}
		ids = append(ids, flags.Arg(0))
		remaining = flags.Args()[1:]
	}
	if len(ids) == 0 {
		return errors.New(tasksUsage)
	}
	store, path, err := openDatabase()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	artifacts, err := artifact.New(filepath.Join(filepath.Dir(path), "artifacts"))
	if err != nil {
		return err
	}
	ctx := context.Background()
	history := inkuna.TaskHistory{Store: store, Artifacts: artifacts}
	previews, err := history.Preview(ctx, ids)
	if err != nil {
		return err
	}
	refused := 0
	for _, preview := range previews {
		if preview.Refused != "" {
			refused++
			_, _ = fmt.Fprintln(stdout, preview.Refused)
		}
	}
	if refused > 0 {
		return errors.New("nothing was deleted")
	}
	writeDeletePreview(stdout, previews, *removeWorktrees, *deleteBranches)
	answers := bufio.NewReader(strings.NewReader(""))
	if stdin != nil {
		answers = bufio.NewReader(stdin)
	}
	if !confirm(answers, stdout, fmt.Sprintf("Delete %d task(s) and their history?", len(previews))) {
		_, err := fmt.Fprintln(stdout, "Nothing was deleted.")
		return err
	}
	options := inkuna.DeleteOptions{RemoveWorktrees: *removeWorktrees}
	if *deleteBranches {
		options.DeleteBranches = make(map[string]map[string]string)
		// Tasks deleted together can share a branch; it is asked about once.
		asked := make(map[[2]string]bool)
		for _, preview := range previews {
			for _, run := range preview.Runs {
				branch := run.Branch
				if branch == nil || !branch.Deletable {
					continue
				}
				key := [2]string{branch.ProjectID, branch.Name}
				if asked[key] {
					continue
				}
				asked[key] = true
				question := fmt.Sprintf("Delete branch %s at %s? %s", branch.Name, branch.Commit, uniqueCommits(branch.Unique))
				if !confirm(answers, stdout, question) {
					continue
				}
				if options.DeleteBranches[branch.ProjectID] == nil {
					options.DeleteBranches[branch.ProjectID] = make(map[string]string)
				}
				options.DeleteBranches[branch.ProjectID][branch.Name] = branch.Commit
			}
		}
	}
	report, err := history.DeleteTasks(ctx, ids, options)
	if err != nil {
		return err
	}
	writeDeleteReport(stdout, report)
	return nil
}

func writeDeletePreview(stdout io.Writer, previews []inkuna.TaskDeletePreview, removeWorktrees, deleteBranches bool) {
	for _, preview := range previews {
		_, _ = fmt.Fprintf(stdout, "Task %s  %s\n", preview.TaskID, oneLine(preview.Title, 72))
		if len(preview.Runs) == 0 {
			_, _ = fmt.Fprintln(stdout, "  never ran")
		}
		for _, run := range preview.Runs {
			_, _ = fmt.Fprintf(stdout, "  run %s  %s\n", run.RunID, run.State)
			if tree := run.Worktree; tree != nil {
				switch {
				case !removeWorktrees:
					_, _ = fmt.Fprintf(stdout, "    worktree %s stays (--remove-worktrees removes it)\n", tree.Path)
				case tree.Removable:
					_, _ = fmt.Fprintf(stdout, "    worktree %s will be removed\n", tree.Path)
				default:
					_, _ = fmt.Fprintf(stdout, "    worktree %s stays: %s\n", tree.Path, tree.Reason)
				}
			}
			if branch := run.Branch; branch != nil {
				at := branch.Name
				if branch.Commit != "" {
					at += " at " + branch.Commit
				}
				switch {
				case !branch.Deletable:
					_, _ = fmt.Fprintf(stdout, "    branch %s stays: %s\n", at, branch.Reason)
				case !deleteBranches:
					_, _ = fmt.Fprintf(stdout, "    branch %s stays (--delete-branches offers to delete it). %s\n", at, uniqueCommits(branch.Unique))
				default:
					_, _ = fmt.Fprintf(stdout, "    branch %s: you will be asked. %s\n", at, uniqueCommits(branch.Unique))
				}
			}
		}
	}
}

func writeDeleteReport(stdout io.Writer, report inkuna.DeleteReport) {
	for _, task := range report.Tasks {
		total := int64(0)
		parts := make([]string, 0, len(task.Rows))
		for _, rows := range task.Rows {
			total += rows.Rows
			parts = append(parts, fmt.Sprintf("%s %d", rows.Table, rows.Rows))
		}
		_, _ = fmt.Fprintf(stdout, "Deleted task %s: %d rows (%s)\n", task.TaskID, total, strings.Join(parts, ", "))
		blobs := fmt.Sprintf("  freed %d artifact(s), %s", task.Blobs, byteSize(task.BlobBytes))
		if task.KeptBlobs > 0 {
			blobs += fmt.Sprintf("; kept %d another task still uses", task.KeptBlobs)
		}
		_, _ = fmt.Fprintln(stdout, blobs)
		for _, tree := range task.Worktrees {
			if tree.Removed {
				_, _ = fmt.Fprintf(stdout, "  removed worktree %s\n", tree.Path)
			} else {
				_, _ = fmt.Fprintf(stdout, "  kept worktree %s: %s\n", tree.Path, tree.Reason)
			}
		}
		for _, branch := range task.Branches {
			if branch.Deleted {
				_, _ = fmt.Fprintf(stdout, "  deleted branch %s; until git collects its commits, git branch %s %s brings it back\n", branch.Name, branch.Name, branch.Commit)
				continue
			}
			at := branch.Name
			if branch.Commit != "" {
				at += " at " + branch.Commit
			}
			_, _ = fmt.Fprintf(stdout, "  kept branch %s: %s\n", at, branch.Reason)
		}
		for _, link := range task.Links {
			_, _ = fmt.Fprintf(stdout, "  forgot the link to %s issue %s; the issue is unchanged\n", link.Tracker, link.ID)
		}
		for _, problem := range task.Problems {
			_, _ = fmt.Fprintf(stdout, "  problem: %s\n", problem)
		}
	}
}

// confirm asks a yes-or-no question, and takes anything but yes, including
// no answer at all, as no.
func confirm(answers *bufio.Reader, stdout io.Writer, question string) bool {
	_, _ = fmt.Fprintf(stdout, "%s [y/N] ", question)
	line, err := answers.ReadString('\n')
	if err != nil && line == "" {
		_, _ = fmt.Fprintln(stdout)
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func uniqueCommits(unique int) string {
	switch unique {
	case 0:
		return "Every commit on it is on another branch or tag too."
	case 1:
		return "1 commit is only on it and would be lost."
	}
	return fmt.Sprintf("%d commits are only on it and would be lost.", unique)
}

func taskState(task storage.HistoryTask) string {
	for _, run := range task.Runs {
		if !storage.RunEnded(run.State) {
			return run.State
		}
	}
	if len(task.Runs) == 0 {
		return "not run"
	}
	return task.Runs[len(task.Runs)-1].State
}

func lastRun(task storage.HistoryTask) string {
	if len(task.Runs) == 0 {
		return "-"
	}
	last := task.Runs[len(task.Runs)-1]
	when := last.FinishedAt
	if when.IsZero() {
		when = last.StartedAt
	}
	return when.Local().Format(time.DateTime[:16])
}

func projectNames(ctx context.Context, store *storage.Store) (map[string]string, error) {
	// A removed project's tasks are still listed, under its name.
	projects, err := store.AllProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("load projects: %w", err)
	}
	names := make(map[string]string, len(projects))
	for _, project := range projects {
		names[project.ID] = project.Name
	}
	return names, nil
}

func oneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

func byteSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", bytes)
}
