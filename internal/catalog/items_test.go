package catalog

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/planner"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

// Each shipped rule, added to a repository that had the built-in rules,
// takes the task it is written for and leaves an unrelated one, including
// one that shares a word with its keywords, to the flows the repository
// already had. The task it takes goes to the agent it is written for.
func TestShippedRulesMatchTheTasksTheyAreFor(t *testing.T) {
	cases := []struct {
		rule string
		// stage is the step the rule's specialist runs, and agent the one
		// that runs it.
		stage, agent string
		meant        []task
		unrelated    []task
	}{
		{
			rule: "security-sensitive", stage: "security-review", agent: "security-reviewer",
			meant: []task{
				{domain.TaskImplementation, "Add OAuth login with GitHub"},
				{domain.TaskBugfix, "Fix the password reset token that never expires"},
				{domain.TaskImplementation, "Encrypt stored API credentials at rest"},
			},
			unrelated: []task{
				{domain.TaskImplementation, "Show the author of each post on the post page"},
				{domain.TaskDocumentation, "Document how authentication works"},
				{domain.TaskReview, "Review the login page change"},
			},
		},
		{
			rule: "schema-change", stage: "migrate", agent: "migration-writer",
			meant: []task{
				{domain.TaskImplementation, "Add a database column for each user's time zone"},
				{domain.TaskImplementation, "Write the migration that splits the orders table"},
			},
			unrelated: []task{
				{domain.TaskImplementation, "Add a time zone picker to the profile page"},
				{domain.TaskImplementation, "Validate settings files against their JSON schema"},
				{domain.TaskResearch, "Investigate why the migration took an hour"},
			},
		},
		{
			rule: "dependency-upgrade", stage: "upgrade", agent: "upgrader",
			meant: []task{
				{domain.TaskImplementation, "Bump React to 19 and fix what breaks"},
				{domain.TaskImplementation, "Upgrade dependencies flagged by Dependabot"},
			},
			unrelated: []task{
				{domain.TaskImplementation, "Upgrade the settings page layout"},
				{domain.TaskImplementation, "Show each step's dependencies in the plan view"},
			},
		},
		{
			rule: "refactor", stage: "refactor", agent: "refactorer",
			meant: []task{
				{domain.TaskImplementation, "Refactor the billing module into smaller packages"},
				{domain.TaskImplementation, "Split the handler into one file per route without changing behaviour"},
			},
			unrelated: []task{
				{domain.TaskImplementation, "Add a CSV export to billing"},
				{domain.TaskBugfix, "Fix the crash after the refactor"},
			},
		},
		{
			rule: "test-coverage", stage: "write-tests", agent: "test-writer",
			meant: []task{
				{domain.TaskImplementation, "Raise test coverage of the parser"},
				{domain.TaskImplementation, "Add the missing tests for the scheduler"},
			},
			// Every task asks for passing tests in its acceptance criteria.
			unrelated: []task{
				{domain.TaskImplementation, "Add a parser for TOML files"},
			},
		},
	}
	catalog, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.rule, func(t *testing.T) {
			document, registry := addToEmptyRepository(t, catalog, test.rule)
			for _, meant := range test.meant {
				result := planTask(t, document, registry, meant)
				if result.RuleName != test.rule {
					t.Errorf("%q (%s) planned with %s, want %s", meant.objective, meant.kind, result.RuleName, test.rule)
					continue
				}
				if !slices.ContainsFunc(result.Workflow.Steps, func(step domain.WorkflowStep) bool {
					return step.ID == test.stage && step.Agent == test.agent
				}) {
					t.Errorf("%q: no step %s run by %s in %+v", meant.objective, test.stage, test.agent, result.Workflow.Steps)
				}
			}
			for _, unrelated := range test.unrelated {
				if result := planTask(t, document, registry, unrelated); result.RuleName == test.rule {
					t.Errorf("%q (%s) planned with %s, want the flows the repository had", unrelated.objective, unrelated.kind, test.rule)
				}
			}
		})
	}
}

// The shared review-and-approve group sends a person's changes back to the
// stage that wrote, before the group, in whichever flow uses it.
func TestReviewAndApproveSendsChangesBackToTheWriter(t *testing.T) {
	catalog, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	document, registry := addToEmptyRepository(t, catalog, "schema-change")
	result := planTask(t, document, registry, task{domain.TaskImplementation, "Add a database column for each user's time zone"})
	at := slices.IndexFunc(result.Workflow.Steps, func(step domain.WorkflowStep) bool { return step.Kind == domain.StepApproval })
	if at < 0 {
		t.Fatalf("no approval step in %+v", result.Workflow.Steps)
	}
	if approval := result.Workflow.Steps[at]; !approval.Review || approval.FeedbackTarget != "migrate" {
		t.Fatalf("approval = review %v to %q, want a review gate to migrate", approval.Review, approval.FeedbackTarget)
	}
}

type task struct {
	kind      domain.TaskKind
	objective string
}

// addToEmptyRepository adds the named item to a repository with no .inkuna
// directory and returns its rules and agents afterwards.
func addToEmptyRepository(t *testing.T, catalog Catalog, name string) (rules.Document, agent.Registry) {
	t.Helper()
	root := t.TempDir()
	target := Target{
		RulesPath:      filepath.Join(root, filepath.FromSlash(rules.RelativePath)),
		AgentDirectory: filepath.Join(root, ".inkuna", "agents"),
	}
	if _, err := catalog.Add(name, target); err != nil {
		t.Fatal(err)
	}
	document, _, err := rules.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := agent.Build(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return document, registry
}

// planTask plans the task by matching the rules, as a new task is, without
// an orchestrator.
func planTask(t *testing.T, document rules.Document, registry agent.Registry, spec task) planner.Result {
	t.Helper()
	result, err := planner.Build(context.Background(), planner.Input{
		Task: domain.TaskSpec{
			Version: 1, ID: "task", Kind: spec.kind, Objective: spec.objective, Repository: ".", Budget: domain.Budget{WallTimeSeconds: 1800},
			AcceptanceCriteria: []string{"tests pass"}, Source: domain.TaskSource{Kind: "request"}, Priority: 1,
		},
		Rules: document, Registry: registry, DefaultRuntime: domain.RuntimeClaude, Disabled: true, Now: time.Unix(0, 0),
	})
	if err != nil {
		t.Fatalf("plan %q: %v", spec.objective, err)
	}
	return result
}
