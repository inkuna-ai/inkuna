// Package orchestrator asks a headless model to staff the workflow: for each
// stage a rule left as "auto", it chooses an agent and the skills that agent
// should receive, and for every agent stage, the ones a rule staffed too, a
// complexity score for that stage's own work. The model only proposes; Go
// validates every field against the registry and the stage's permission
// before anything is scheduled.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/repository"
	"github.com/inkuna-ai/inkuna/internal/routing"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// StageBrief is what the orchestrator sees about one agent stage: one to
// staff and score, or, when Agent is set, one the rule staffed, to score.
type StageBrief struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Permission domain.Permission `json:"permission"`
	DependsOn  []string          `json:"dependsOn,omitempty"`
	Skills     []string          `json:"requiredSkills,omitempty"`
	// Agent is the agent the rule named for the stage, which the decision
	// must keep; empty for a stage left as "auto".
	Agent string `json:"agent,omitempty"`
	// Runtime is the runtime the stage names, or its agent's when the rule
	// named one that pins a runtime; empty runs on the plan's runtime.
	Runtime domain.Runtime `json:"runtime,omitempty"`
}

// SkillBrief summarizes a skill package for selection. Its description is
// sent once, here, however many agents declare the skill.
type SkillBrief struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// TaskBrief is what staffing needs of the task. Its id, paths, budget, and
// metadata are left out: the model has no use for them, and the id changes
// with every plan, which would keep an identical plan from being recognized.
type TaskBrief struct {
	Kind               domain.TaskKind `json:"kind"`
	Title              string          `json:"title,omitempty"`
	Objective          string          `json:"objective"`
	AcceptanceCriteria []string        `json:"acceptanceCriteria,omitempty"`
	Constraints        []string        `json:"constraints,omitempty"`
	Labels             []string        `json:"labels,omitempty"`
}

// NewTaskBrief takes the brief from a task.
func NewTaskBrief(task domain.TaskSpec) TaskBrief {
	return TaskBrief{Kind: task.Kind, Title: task.Title, Objective: task.Objective, AcceptanceCriteria: task.AcceptanceCriteria, Constraints: task.Constraints, Labels: task.Labels}
}

// ManifestLimit is how many manifests a repository brief lists.
const ManifestLimit = 50

// RepositoryBrief is the repository's languages and manifests, without where
// it is on disk or what is checked out.
type RepositoryBrief struct {
	Languages []string `json:"languages,omitempty"`
	Manifests []string `json:"manifests,omitempty"`
	// MoreManifests counts the manifests past ManifestLimit.
	MoreManifests int `json:"moreManifests,omitempty"`
}

// NewRepositoryBrief takes the brief from a profile. A large repository can
// have hundreds of manifests, so the brief keeps the ManifestLimit nearest
// the root, which say the most about it, and counts the rest.
func NewRepositoryBrief(profile repository.Profile) RepositoryBrief {
	manifests := append([]string(nil), profile.Manifests...)
	sort.SliceStable(manifests, func(i, j int) bool {
		left, right := strings.Count(manifests[i], "/"), strings.Count(manifests[j], "/")
		if left != right {
			return left < right
		}
		return manifests[i] < manifests[j]
	})
	brief := RepositoryBrief{Languages: profile.Languages, Manifests: manifests}
	if len(manifests) > ManifestLimit {
		brief.Manifests, brief.MoreManifests = manifests[:ManifestLimit], len(manifests)-ManifestLimit
	}
	return brief
}

// AgentBrief is what the orchestrator chooses an agent by. The agent's A2A
// card stays the bus contract; most of a card is the same for every agent,
// and it repeats each skill's description.
type AgentBrief struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Permission  domain.Permission `json:"permission"`
	// Runtime is the runtime the agent pins, or remote for an external
	// advisor; empty means it runs on the plan's runtime.
	Runtime domain.Runtime `json:"runtime,omitempty"`
	// Kinds are the task kinds it specializes in; none means general-purpose.
	Kinds  []domain.TaskKind `json:"kinds,omitempty"`
	Skills []string          `json:"skills,omitempty"`
	Tags   []string          `json:"tags,omitempty"`
}

// NewAgentBrief takes the brief from a definition.
func NewAgentBrief(definition agent.Definition) AgentBrief {
	brief := AgentBrief{
		Name: definition.Name, Description: definition.Description, Permission: definition.Permission, Runtime: definition.Runtime,
		Kinds: definition.TaskKinds, Skills: definition.Skills, Tags: definition.Tags,
	}
	if definition.Remote() {
		brief.Runtime = domain.RuntimeRemote
	}
	return brief
}

// Request is the JSON document the orchestrator receives.
type Request struct {
	Task       TaskBrief       `json:"task"`
	Repository RepositoryBrief `json:"repository"`
	// Runtime is the plan's runtime, which a stage runs on unless it or its
	// agent names another.
	Runtime domain.Runtime `json:"runtime,omitempty"`
	Stages  []StageBrief   `json:"stages"`
	Agents  []AgentBrief   `json:"agents"`
	Skills  []SkillBrief   `json:"skills"`
	// Routing is the model and reasoning effort each score gets on each
	// runtime a stage may run on, so a score is chosen knowing what it
	// spends. A runtime with no bands is left out: its CLI picks the model.
	Routing map[domain.Runtime][]routing.Band `json:"routing,omitempty"`
}

// Assignment is one staffing decision.
type Assignment struct {
	StageID    string                      `json:"stageId"`
	Agent      string                      `json:"agent"`
	Skills     []string                    `json:"skills"`
	Complexity domain.ComplexityAssessment `json:"complexity"`
	Rationale  string                      `json:"rationale"`
}

// Decision is the orchestrator's validated output.
type Decision struct {
	Assignments []Assignment `json:"assignments"`
	Notes       string       `json:"notes,omitempty"`
}

// Schema constrains the model's output.
const Schema = `{
  "type": "object",
  "required": ["assignments"],
  "additionalProperties": false,
  "properties": {
    "assignments": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["stageId", "agent", "skills", "complexity", "rationale"],
        "additionalProperties": false,
        "properties": {
          "stageId": {"type": "string"},
          "agent": {"type": "string"},
          "skills": {"type": "array", "items": {"type": "string"}},
          "complexity": {
            "type": "object",
            "required": ["score", "rationale"],
            "additionalProperties": false,
            "properties": {
              "score": {"type": "integer", "minimum": 1, "maximum": 10},
              "rationale": {"type": "string"},
              "signals": {"type": "array", "items": {"type": "string"}}
            }
          },
          "rationale": {"type": "string"}
        }
      }
    },
    "notes": {"type": "string"}
  }
}`

// Options configures a run of the orchestrator model.
type Options struct {
	Runtime         domain.Runtime
	Model           string
	ReasoningEffort string
	MaxTurns        int
	WallTimeSeconds int
	// WorkingDir is where the staffing call runs: an empty directory, so
	// that no repository's CLAUDE.md or AGENTS.md loads, or the repository
	// when ReadTools lets the call read it.
	WorkingDir   string
	RunID        string
	Instructions string
	// ReadTools gives the staffing call read-only file tools. Without it the
	// call has none: the request carries what staffing needs.
	ReadTools bool
}

// Remote runs the orchestrator model and returns its raw decision with what
// the call cost. The production implementation wraps an AgentRuntime; tests
// substitute a fake.
type Remote interface {
	Decide(ctx context.Context, request Request, options Options, sink agentruntime.EventSink) (Decision, Call, error)
}

// Call is one orchestrator model call: the run id its runtime was given, the
// usage it reported, the model and sign-in its stream named, how it ended,
// and the size of each part of its prompt. A call that failed still reports
// the usage streamed before it stopped. The zero Call means no model was
// called.
type Call struct {
	RunID      string
	Usage      domain.Usage
	ModelUsed  string
	AuthMode   string
	Sections   []domain.PromptSection
	Outcome    string
	StartedAt  time.Time
	FinishedAt time.Time
}

// staffingDirective sits between the instructions and the request. It says
// what Validate requires of every decision, so instructions a repository
// wrote for its own orchestrator before every stage was scored still get it.
const staffingDirective = "\n\nStaff the stages that name no agent, keep the agent of each that names one, and score every stage for its own work. Reply with JSON only.\n\n"

// staffingPrompt is the prompt a staffing call sends, and the encoded
// request in it.
func staffingPrompt(request Request, instructions string) (string, []byte, error) {
	body, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("encode staffing request: %w", err)
	}
	return instructions + staffingDirective + string(body), body, nil
}

// Key identifies a staffing call with no tools by everything that decides
// its answer: the prompt, the schema, and the runtime, model, and reasoning
// effort. Plans with the same key ask the same model the same question, so
// one's validated decision can serve the other. A call with ReadTools also
// answers from the repository's files, which no key covers, so its decision
// must not be reused. Options must carry the instructions the call will send.
func Key(request Request, options Options) (string, error) {
	prompt, _, err := staffingPrompt(request, options.Instructions)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, part := range []string{Schema, prompt, string(options.Runtime), options.Model, options.ReasoningEffort} {
		// Length-prefixed, so no two different sets of parts hash alike.
		_, _ = fmt.Fprintf(hash, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// RuntimeRemote runs the orchestrator through a headless CLI with read-only
// permission, the strict output schema, and no tools unless
// Options.ReadTools gives staffing its read tools.
type RuntimeRemote struct{ Runtime agentruntime.AgentRuntime }

func (remote RuntimeRemote) Decide(ctx context.Context, request Request, options Options, sink agentruntime.EventSink) (Decision, Call, error) {
	if remote.Runtime == nil {
		return Decision{}, Call{}, errors.New("orchestrator runtime is not configured")
	}
	prompt, body, err := staffingPrompt(request, options.Instructions)
	if err != nil {
		return Decision{}, Call{}, err
	}
	fields := []requestField{{"task", request.Task}, {"repository", request.Repository}}
	// A field the request leaves out when empty is not a section either, or
	// the sections would add up to more than the prompt.
	if request.Runtime != "" {
		fields = append(fields, requestField{"runtime", request.Runtime})
	}
	fields = append(fields, requestField{"stages", request.Stages}, requestField{"agents", request.Agents}, requestField{"skills", request.Skills})
	if len(request.Routing) > 0 {
		fields = append(fields, requestField{"routing", request.Routing})
	}
	sections := promptSections(Schema, options.Instructions, staffingDirective, len(body), fields)
	if options.WallTimeSeconds <= 0 {
		options.WallTimeSeconds = 300
	}
	if sink == nil {
		sink = func(domain.RunEvent) error { return nil }
	}
	call := Call{RunID: options.RunID, Sections: sections, StartedAt: time.Now().UTC()}
	result, err := remote.Runtime.Start(ctx, agentruntime.RunRequest{
		RunID: options.RunID, WorkingDir: options.WorkingDir, Prompt: []byte(prompt), Schema: []byte(Schema),
		Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: options.WallTimeSeconds, MaxTurns: options.MaxTurns, MaxOutputBytes: 4 << 20},
		Model: options.Model, ReasoningEffort: options.ReasoningEffort, NoTools: !options.ReadTools,
	}, sink)
	call.Usage, call.ModelUsed, call.AuthMode, call.Outcome, call.FinishedAt = result.Usage, result.Model, result.AuthMode, agentruntime.Outcome(err), time.Now().UTC()
	if err != nil {
		return Decision{}, call, err
	}
	if len(result.Output) == 0 {
		return Decision{}, call, errors.New("orchestrator returned no structured decision")
	}
	var decision Decision
	if err := json.Unmarshal(result.Output, &decision); err != nil {
		return Decision{}, call, fmt.Errorf("decode orchestrator decision: %w", err)
	}
	return decision, call, nil
}

// requestField is one top-level field of a request the orchestrator is sent.
type requestField struct {
	name  string
	value any
}

// promptSections measures an orchestrator prompt: the schema sent beside it,
// the instructions, the directive after them, and the request encoded with
// json.MarshalIndent in size bytes, one section per field. Whatever of the
// request no field claims, its keys and braces, is the "request" section.
func promptSections(schema, instructions, directive string, size int, fields []requestField) []domain.PromptSection {
	sections := []domain.PromptSection{
		{Section: domain.SectionSchema, Bytes: len(schema)},
		{Section: domain.SectionInstructions, Bytes: len(instructions)},
		{Section: domain.SectionProtocol, Bytes: len(directive)},
	}
	rest := size
	for _, field := range fields {
		bytes := domain.FieldBytes(field.value)
		sections = append(sections, domain.PromptSection{Section: field.name, Bytes: bytes})
		rest -= bytes
	}
	return append(sections, domain.PromptSection{Section: "request", Bytes: rest})
}

// Validate checks a decision against the registry, the stages, and the
// available skills. It returns a normalized copy with assignments in stage
// order so downstream code never trusts the model's ordering. A stage the
// rule staffed must keep its agent, and takes only its own skills: the rule
// and that agent chose them, and planning checked them, so the model only
// scores it.
func Validate(decision Decision, stages []StageBrief, registry agent.Registry, availableSkills map[string]bool) (Decision, error) {
	byStage := make(map[string]Assignment, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		if _, duplicate := byStage[assignment.StageID]; duplicate {
			return Decision{}, fmt.Errorf("orchestrator assigned stage %q twice", assignment.StageID)
		}
		byStage[assignment.StageID] = assignment
	}
	normalized := Decision{Notes: strings.TrimSpace(decision.Notes), Assignments: make([]Assignment, 0, len(stages))}
	for _, stage := range stages {
		assignment, ok := byStage[stage.ID]
		if !ok {
			return Decision{}, fmt.Errorf("orchestrator left stage %q unassigned", stage.ID)
		}
		if stage.Agent != "" {
			if assignment.Agent != stage.Agent {
				return Decision{}, fmt.Errorf("stage %q is staffed by the rule with %q, but the orchestrator chose %q", stage.ID, stage.Agent, assignment.Agent)
			}
			if err := validScore(stage, assignment); err != nil {
				return Decision{}, err
			}
			assignment.Skills = append([]string(nil), stage.Skills...)
			sort.Strings(assignment.Skills)
			assignment.Rationale = strings.TrimSpace(assignment.Rationale)
			normalized.Assignments = append(normalized.Assignments, assignment)
			continue
		}
		definition, ok := registry.Get(assignment.Agent)
		if !ok || assignment.Agent == agent.Orchestrator {
			return Decision{}, fmt.Errorf("orchestrator chose unknown agent %q for stage %q", assignment.Agent, stage.ID)
		}
		if stage.Permission == domain.PermissionWorktree && definition.Permission != domain.PermissionWorktree {
			return Decision{}, fmt.Errorf("agent %q is read-only but stage %q needs worktree access", assignment.Agent, stage.ID)
		}
		if definition.Remote() && !definition.Auto {
			return Decision{}, fmt.Errorf("remote agent %q is not marked for automatic staffing, so only a rule can use it (stage %q)", assignment.Agent, stage.ID)
		}
		if err := validScore(stage, assignment); err != nil {
			return Decision{}, err
		}
		skills := make([]string, 0, len(assignment.Skills)+len(stage.Skills))
		seen := make(map[string]bool)
		for _, id := range append(append([]string(nil), stage.Skills...), assignment.Skills...) {
			if !availableSkills[id] {
				return Decision{}, fmt.Errorf("orchestrator selected unavailable skill %q for stage %q", id, stage.ID)
			}
			if !seen[id] {
				seen[id] = true
				skills = append(skills, id)
			}
		}
		sort.Strings(skills)
		assignment.Skills = skills
		assignment.Rationale = strings.TrimSpace(assignment.Rationale)
		normalized.Assignments = append(normalized.Assignments, assignment)
	}
	return normalized, nil
}

// validScore requires a 1 to 10 score with a rationale.
func validScore(stage StageBrief, assignment Assignment) error {
	if assignment.Complexity.Score < 1 || assignment.Complexity.Score > 10 || strings.TrimSpace(assignment.Complexity.Rationale) == "" {
		return fmt.Errorf("stage %q needs a 1-10 complexity score with rationale", stage.ID)
	}
	return nil
}

// Fallback staffs stages deterministically when no model is available: the
// first specialist for the task kind with adequate permission, otherwise the
// first generalist. A stage the rule staffed keeps its agent. Each stage is
// scored by AssessStage.
func Fallback(task domain.TaskSpec, stages []StageBrief, registry agent.Registry, availableSkills map[string]bool) (Decision, error) {
	decision := Decision{Notes: "deterministic fallback: orchestrator unavailable", Assignments: make([]Assignment, 0, len(stages))}
	for _, stage := range stages {
		if stage.Agent != "" {
			decision.Assignments = append(decision.Assignments, Assignment{
				StageID: stage.ID, Agent: stage.Agent, Skills: stage.Skills, Complexity: AssessStage(task, stage),
				Rationale: "the rule names " + stage.Agent,
			})
			continue
		}
		var chosen *agent.Definition
		for _, candidate := range registry.ForKind(task.Kind) {
			if stage.Permission == domain.PermissionWorktree && candidate.Permission != domain.PermissionWorktree {
				continue
			}
			// The fallback is no one's judgment; it never sends work off the
			// machine, even to an agent marked for automatic staffing.
			if candidate.Remote() {
				continue
			}
			current := candidate
			chosen = &current
			break
		}
		if chosen == nil {
			return Decision{}, fmt.Errorf("no agent can serve stage %q with permission %s", stage.ID, stage.Permission)
		}
		// Stage skills are the rule author's requirement and stay strict (Validate
		// rejects unavailable ones); agent-declared skills are optional extras.
		skills := append([]string(nil), stage.Skills...)
		for _, id := range chosen.Skills {
			if availableSkills[id] {
				skills = append(skills, id)
			}
		}
		decision.Assignments = append(decision.Assignments, Assignment{
			StageID: stage.ID, Agent: chosen.Name, Skills: skills, Complexity: AssessStage(task, stage),
			Rationale: fmt.Sprintf("%s is the first %s agent able to run with %s permission", chosen.Name, task.Kind, stage.Permission),
		})
	}
	return Validate(decision, stages, registry, availableSkills)
}

// readOnlyDiscount is how much less the fallback scores a stage that only
// reads than the task. Such a stage, a plan, a review, or research, writes
// nothing the run keeps, so a weaker model or less effort costs less when it
// is wrong; it is a small step, because a plan or review that misses
// something still misleads the stages after it.
const readOnlyDiscount = 2

// AssessStage scores one stage when no model does: the task's score, less
// readOnlyDiscount for a stage that only reads. Only a model can tell the
// work of stages with the same permission apart, such as one that only
// writes a short note from one that implements the task.
func AssessStage(task domain.TaskSpec, stage StageBrief) domain.ComplexityAssessment {
	assessment := AssessComplexity(task)
	if stage.Permission != domain.PermissionReadOnly {
		assessment.Rationale = "deterministic score: the task's, from its kind and declared scope"
		return assessment
	}
	score := max(assessment.Score-readOnlyDiscount, 1)
	assessment.Rationale = fmt.Sprintf("deterministic score: the task scores %d from its kind and declared scope, and this stage only reads", assessment.Score)
	assessment.Score = score
	assessment.Signals = append(assessment.Signals, "read-only stage")
	return assessment
}

// AssessComplexity produces the deterministic initial score of the task,
// used for rule matching and as the start of AssessStage.
func AssessComplexity(task domain.TaskSpec) domain.ComplexityAssessment {
	score := map[domain.TaskKind]int{
		domain.TaskDocumentation:  2,
		domain.TaskResearch:       4,
		domain.TaskReview:         4,
		domain.TaskSkillAuthoring: 5,
		domain.TaskImplementation: 6,
		domain.TaskBugfix:         7,
	}[task.Kind]
	if score == 0 {
		score = 5
	}
	signals := []string{"task kind: " + string(task.Kind)}
	if len(task.AcceptanceCriteria) >= 4 {
		score++
		signals = append(signals, "four or more acceptance criteria")
	}
	if len(task.Constraints) >= 3 {
		score++
		signals = append(signals, "three or more constraints")
	}
	if score > 10 {
		score = 10
	}
	return domain.ComplexityAssessment{Score: score, Rationale: "deterministic initial score based on task kind and declared scope", Signals: signals}
}
