package rules

import "github.com/inkuna-ai/inkuna/internal/domain"

// featureComplexity is where the feature rule starts on the deterministic
// initial score. Every implementation task scores 6 before its scope signals,
// so 7 is the first score only larger work reaches: four or more acceptance
// criteria, or three or more constraints.
const featureComplexity = 7

// Default returns the built-in rules used when a repository has no
// .inkuna/rules.yaml. They reproduce Inkuna's original fixed pipeline and add
// a two-agent bugfix flow, a planned and reviewed flow for larger features, a
// documentation flow, and read-only research and review flows. They use the
// built-in library's profile, verify, and approval stages and its
// verify-and-approve group (Builtin), so a global or repository definition
// with one of those names changes these flows too.
func Default() Document {
	return Document{
		// Version 2: the built-in rules use shared stages, so a file saved
		// from them needs it.
		Version: 2,
		Source:  "builtin",
		Rules: []Rule{
			{
				Name: "bugfix", Description: "Reproduce before fixing, then verify and review.",
				When: Condition{Kinds: []domain.TaskKind{domain.TaskBugfix}},
				Then: Flow{Stages: []Stage{
					{Use: "profile"},
					{ID: "reproduce", Title: "Reproduce and locate the defect", Kind: domain.StepAgent, Agent: "researcher", Permission: domain.PermissionReadOnly, DependsOn: []string{"profile"}},
					{ID: "fix", Title: "Implement the fix", Kind: domain.StepAgent, Agent: "auto", Permission: domain.PermissionWorktree, DependsOn: []string{"reproduce"}},
					{Use: "verify-and-approve", DependsOn: []string{"fix"}},
				}},
			},
			{
				Name: "feature", Description: "Plan larger features before writing them, then verify, review, and pause for a person.",
				When: Condition{Kinds: []domain.TaskKind{domain.TaskImplementation}, Complexity: &Range{Min: featureComplexity, Max: 10}},
				Then: Flow{Stages: []Stage{
					{Use: "profile"},
					{ID: "plan", Title: "Plan the implementation", Kind: domain.StepAgent, Agent: "planner", Permission: domain.PermissionReadOnly, DependsOn: []string{"profile"}},
					// A human-input stage rather than a review gate: a gate's
					// feedback needs a writable target, and plan is read-only.
					{
						ID: "confirm-plan", Title: "Confirm the plan", Kind: domain.StepHumanInput, DependsOn: []string{"plan"},
						Prompt: "Read the plan and say what should change before it is implemented, or reply that it is ready.",
					},
					// Steps see only the handoffs of the stages they depend on,
					// so implement names plan as well as the answer about it.
					{ID: "implement", Title: "Implement the plan", Kind: domain.StepAgent, Agent: "auto", Permission: domain.PermissionWorktree, DependsOn: []string{"plan", "confirm-plan"}},
					{Use: "verify", DependsOn: []string{"implement"}},
					{ID: "review", Title: "Review the change", Kind: domain.StepAgent, Agent: "reviewer", Permission: domain.PermissionReadOnly, DependsOn: []string{"implement", "verify"}},
					// Overrides is what parsing records for these keys, so the
					// document is the same once saved and loaded again.
					{Use: "approval", DependsOn: []string{"review"}, Review: true, Feedback: &Feedback{Target: "implement"}, Overrides: []string{"review", "feedback"}},
				}},
			},
			{
				Name: "documentation", Description: "Documentation goes to the documenter, then verify and approve.",
				When: Condition{Kinds: []domain.TaskKind{domain.TaskDocumentation}},
				Then: Flow{Stages: []Stage{
					{Use: "profile"},
					{ID: "write", Title: "Update the documentation", Kind: domain.StepAgent, Agent: "documenter", Permission: domain.PermissionWorktree, DependsOn: []string{"profile"}},
					{Use: "verify-and-approve", DependsOn: []string{"write"}},
				}},
			},
			{
				Name: "read-only", Description: "Research and review never write to the repository.",
				When: Condition{Kinds: []domain.TaskKind{domain.TaskResearch, domain.TaskReview}},
				Then: Flow{Stages: []Stage{
					{Use: "profile"},
					{ID: "worker", Title: "Investigate", Kind: domain.StepAgent, Agent: "auto", Permission: domain.PermissionReadOnly, DependsOn: []string{"profile"}},
					{Use: "approval", DependsOn: []string{"worker"}},
				}},
			},
		},
		Default: &Flow{Stages: []Stage{
			{Use: "profile"},
			{ID: "worker", Title: "Complete task", Kind: domain.StepAgent, Agent: "auto", DependsOn: []string{"profile"}},
			{Use: "verify-and-approve", DependsOn: []string{"worker"}},
		}},
	}
}
