package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

const branchUsage = `usage:
  inkunactl branch new --type TYPE [--story ID] [--base BRANCH] [--push] DESCRIPTION...
  inkunactl branch init [--write] [--no-orchestrator] [REPOSITORY]
  inkunactl branch show [REPOSITORY]`

// runBranch makes and inspects branches by the repository's branching
// configuration, with the same code Inkuna's runs use.
func runBranch(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(branchUsage)
	}
	switch args[0] {
	case "new":
		return runBranchNew(args[1:], stdout)
	case "init":
		return runBranchInit(args[1:], stdout)
	case "show":
		root, err := repositoryRoot(args[1:])
		if err != nil {
			return err
		}
		loaded, err := branching.Load(root)
		if err != nil {
			return err
		}
		result := map[string]any{"path": loaded.Path, "exists": loaded.Exists, "missing": loaded.Missing, "config": loaded.Config}
		if configuration, err := config.Load(); err == nil && loaded.Config.Worktrees.Enabled {
			if location, err := loaded.Config.Root(root, configuration.DataDir); err == nil {
				result["worktreeRoot"] = location
			}
		}
		return writeJSON(stdout, result)
	default:
		return errors.New(branchUsage)
	}
}

// runBranchNew creates a branch from the configured base and, when
// worktrees are on, checks it out in a new worktree at the configured
// location; when they are off, the current checkout switches to it. It
// pushes only when asked.
func runBranchNew(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("branch new", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	branchType := flags.String("type", "", "branch type from the configuration's types")
	story := flags.String("story", "", "tracker story id, such as MAD-12 or 23")
	base := flags.String("base", "", "branch to start from instead of the configured base")
	push := flags.Bool("push", false, "push the branch to origin and set its upstream")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, branchUsage)
	}
	if flags.NArg() == 0 {
		return errors.New("a description is required\n" + branchUsage)
	}
	ctx := context.Background()
	working, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := worktree.MainRoot(ctx, working)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}
	loaded, err := branching.Load(root)
	if err != nil {
		return err
	}
	if !loaded.Complete() {
		return fmt.Errorf("%s lacks %s; run `inkunactl branch init --write` to propose and add them", loaded.Path, strings.Join(loaded.Missing, ", "))
	}
	cfg := loaded.Config
	name, err := cfg.Name(branching.Story{Type: *branchType, ID: *story, Description: strings.Join(flags.Args(), " ")})
	if err != nil {
		return err
	}
	from := cfg.Branch.Base
	if *base != "" {
		from = *base
	}
	if err := branching.ValidateBase(from); err != nil {
		return err
	}
	result := map[string]any{"branch": name, "base": from}
	warnings := []string{}
	if worktree.HasOrigin(ctx, root) {
		if err := worktree.Fetch(ctx, root, from, worktree.FetchTimeout); err != nil {
			warnings = append(warnings, "could not fetch origin/"+from+": "+err.Error())
		}
	}
	start, err := worktree.StartPoint(ctx, root, from)
	if err != nil {
		return err
	}
	result["startPoint"] = start
	directory := root
	if cfg.Worktrees.Enabled {
		configuration, err := config.Load()
		if err != nil {
			return err
		}
		location, err := cfg.Root(root, configuration.DataDir)
		if err != nil {
			return err
		}
		created, err := worktree.AddBranch(ctx, root, name, branching.Path(location, name), start)
		if err != nil {
			return err
		}
		directory = created.Path
		result["worktree"] = created.Path
	} else {
		if err := worktree.Switch(ctx, root, name, start); err != nil {
			return err
		}
		result["checkout"] = root
	}
	if *push {
		if err := worktree.Push(ctx, directory, name); err != nil {
			return fmt.Errorf("created %s but could not push it: %w", name, err)
		}
		result["pushed"] = true
		if url := branchURL(ctx, root, name); url != "" {
			result["remote"] = url
		}
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return writeJSON(stdout, result)
}

// runBranchInit proposes the keys a repository's branching configuration
// lacks, from its branches and, unless told not to, the orchestrator. It
// prints the proposal and writes it only with --write.
func runBranchInit(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("branch init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	write := flags.Bool("write", false, "write the proposed keys into .inkuna/branching.yaml")
	deterministic := flags.Bool("no-orchestrator", false, "infer from the repository only, without a model call")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, branchUsage)
	}
	root, err := repositoryRoot(flags.Args())
	if err != nil {
		return err
	}
	loaded, err := branching.Load(root)
	if err != nil {
		return err
	}
	if loaded.Complete() {
		return writeJSON(stdout, map[string]any{"path": loaded.Path, "complete": true, "config": loaded.Config})
	}
	ctx := context.Background()
	evidence, err := worktree.Evidence(ctx, root)
	if err != nil {
		return err
	}
	notes := []string{}
	var offer *branching.Proposal
	if !*deterministic {
		proposal, err := proposeWithOrchestrator(ctx, root, loaded.Missing, evidence)
		if err != nil {
			notes = append(notes, "orchestrator unavailable ("+err.Error()+"); inferring from the repository")
		} else {
			offer = &proposal
		}
	}
	effective, values, resolved := branching.Resolve(loaded.Config, loaded.Missing, offer, evidence)
	result := map[string]any{"path": loaded.Path, "missing": loaded.Missing, "proposed": values, "notes": append(notes, resolved...), "config": effective}
	if *write {
		written, err := branching.Apply(root, values)
		if err != nil {
			return err
		}
		result["written"] = written
	}
	return writeJSON(stdout, result)
}

func proposeWithOrchestrator(ctx context.Context, root string, missing []string, evidence branching.Evidence) (branching.Proposal, error) {
	configuration, err := config.Load()
	if err != nil {
		return branching.Proposal{}, err
	}
	if configuration.Orchestrator.Disabled {
		return branching.Proposal{}, errors.New("the orchestrator is disabled in inkuna.yaml")
	}
	for _, spec := range configuration.Runtimes {
		if spec.Name != configuration.Orchestrator.Runtime {
			continue
		}
		adapter, err := agentruntime.New(spec)
		if err != nil {
			return branching.Proposal{}, err
		}
		// No task exists yet, so this call's usage has nowhere to be recorded.
		proposal, _, err := orchestrator.RuntimeRemote{Runtime: adapter}.ProposeBranching(ctx, orchestrator.BranchingRequest{Missing: missing, Evidence: evidence}, orchestrator.Options{
			Model: configuration.Orchestrator.Model, ReasoningEffort: configuration.Orchestrator.ReasoningEffort, MaxTurns: configuration.Orchestrator.MaxTurns,
			WallTimeSeconds: int(configuration.PlanningTimeout.Seconds()), WorkingDir: root, RunID: "branching-cli",
		}, nil)
		return proposal, err
	}
	return branching.Proposal{}, fmt.Errorf("orchestrator runtime %q is not declared", configuration.Orchestrator.Runtime)
}

// repositoryRoot resolves the main checkout of the repository named by the
// only argument, or of the current directory.
func repositoryRoot(args []string) (string, error) {
	if len(args) > 1 {
		return "", errors.New(branchUsage)
	}
	directory := "."
	if len(args) == 1 {
		directory = args[0]
	}
	root, err := worktree.MainRoot(context.Background(), directory)
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository: %w", directory, err)
	}
	return root, nil
}

var githubRemote = regexp.MustCompile(`^(?:git@github\.com:|ssh://git@github\.com/|https://github\.com/)([^/]+/[^/]+?)(?:\.git)?/?$`)

// branchURL is the branch's page on GitHub, for ssh or https remotes.
func branchURL(ctx context.Context, root, branch string) string {
	command := execGit(ctx, root, "remote", "get-url", "origin")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	if match := githubRemote.FindStringSubmatch(strings.TrimSpace(string(output))); match != nil {
		return "https://github.com/" + match[1] + "/tree/" + branch
	}
	return ""
}
