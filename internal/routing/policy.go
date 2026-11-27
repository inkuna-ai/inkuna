package routing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

//go:embed policy.v2.json
var defaultPolicyJSON []byte

type Band struct {
	Minimum         int    `json:"minimum" yaml:"minimum"`
	Maximum         int    `json:"maximum" yaml:"maximum"`
	Model           string `json:"model" yaml:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty" yaml:"reasoningEffort,omitempty"`
}

type Policy struct {
	Version int                       `json:"version"`
	Bands   map[domain.Runtime][]Band `json:"bands"`
}

// DefaultPolicy chooses a model within the runtime selected by the project or
// an explicit step override. This keeps provider choice user-controlled while
// making the complexity-to-model decision reproducible and reviewable. The
// Claude Code and Codex bands give each score a band of its own: a model
// covers a run of scores and each point inside it raises the reasoning
// effort, so every point of a planner's score changes what the step spends.
func DefaultPolicy() Policy {
	var policy Policy
	if err := json.Unmarshal(defaultPolicyJSON, &policy); err != nil {
		panic(fmt.Sprintf("decode embedded routing policy: %v", err))
	}
	return policy
}

// ValidateBands requires bands that give every complexity score from 1 to 10
// exactly one model: in order, without gaps or overlaps, each naming a model.
func ValidateBands(bands []Band) error {
	next := 1
	for _, band := range bands {
		if strings.TrimSpace(band.Model) == "" {
			return fmt.Errorf("the %d-%d band needs a model", band.Minimum, band.Maximum)
		}
		if band.Minimum != next || band.Maximum < band.Minimum {
			return fmt.Errorf("bands must cover complexity 1 to 10 in order without gaps or overlaps; expected a band starting at %d, found %d-%d", next, band.Minimum, band.Maximum)
		}
		next = band.Maximum + 1
	}
	if next != 11 {
		return fmt.Errorf("bands must cover complexity 1 to 10; they stop at %d", next-1)
	}
	return nil
}

// WithBands returns the policy with some runtimes' bands replaced. The
// version is unchanged: each routed step records the model it was given.
func (policy Policy) WithBands(overrides map[domain.Runtime][]Band) Policy {
	merged := Policy{Version: policy.Version, Bands: make(map[domain.Runtime][]Band, len(policy.Bands))}
	for runtimeName, bands := range policy.Bands {
		merged.Bands[runtimeName] = bands
	}
	for runtimeName, bands := range overrides {
		if len(bands) > 0 {
			merged.Bands[runtimeName] = append([]Band(nil), bands...)
		}
	}
	return merged
}

func (policy Policy) Apply(workflow *domain.WorkflowSpec) error {
	if workflow == nil {
		return fmt.Errorf("workflow is required")
	}
	if policy.Version < 1 {
		return fmt.Errorf("routing policy version must be positive")
	}
	for index := range workflow.Steps {
		step := &workflow.Steps[index]
		if step.Kind != domain.StepAgent {
			continue
		}
		if step.Complexity == nil || step.Complexity.Score < 1 || step.Complexity.Score > 10 {
			return fmt.Errorf("agent step %q needs a complexity score from 1 to 10", step.ID)
		}
		if step.Runtime == domain.RuntimeRemote {
			// A remote agent runs no CLI and picks its own model.
			step.ModelSelection = &domain.ModelSelection{PolicyVersion: policy.Version, Source: domain.ModelSourceRemote, Rationale: "a remote agent chooses its own model"}
			continue
		}
		runtimeName := step.Runtime
		if runtimeName == "" {
			runtimeName = workflow.DefaultRuntime
		}
		step.Runtime = runtimeName
		if step.ModelSelection != nil && step.ModelSelection.Source == "override" && strings.TrimSpace(step.ModelSelection.Model) != "" {
			continue
		}
		bands, exists := policy.Bands[runtimeName]
		if !exists {
			// A runtime added in configuration has no built-in bands. Until
			// routing.bands names it, the CLI picks its own default model.
			step.ModelSelection = &domain.ModelSelection{
				PolicyVersion: policy.Version, Source: "policy",
				Rationale: fmt.Sprintf("no model bands are configured for %s, so the CLI uses its default model", runtimeName),
			}
			continue
		}
		var selected *Band
		for bandIndex := range bands {
			band := &bands[bandIndex]
			if step.Complexity.Score >= band.Minimum && step.Complexity.Score <= band.Maximum {
				selected = band
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("no %s model band covers complexity %d", runtimeName, step.Complexity.Score)
		}
		step.ModelSelection = &domain.ModelSelection{
			Model:           selected.Model,
			ReasoningEffort: selected.ReasoningEffort,
			PolicyVersion:   policy.Version,
			Source:          "policy",
			Rationale:       rationale(step.Complexity.Score, *selected, runtimeName),
		}
	}
	workflow.RoutingPolicyVersion = policy.Version
	return domain.ValidateRoutedWorkflow(*workflow)
}

// rationale says why a band was chosen. A band of one score names the model
// and effort, which are all that tells it from its neighbours.
func rationale(score int, band Band, runtimeName domain.Runtime) string {
	if band.Minimum != band.Maximum {
		return fmt.Sprintf("complexity %d falls in the %d-%d %s band", score, band.Minimum, band.Maximum, runtimeName)
	}
	if band.ReasoningEffort == "" {
		return fmt.Sprintf("complexity %d routes %s to %s", score, runtimeName, band.Model)
	}
	return fmt.Sprintf("complexity %d routes %s to %s at %s effort", score, runtimeName, band.Model, band.ReasoningEffort)
}
