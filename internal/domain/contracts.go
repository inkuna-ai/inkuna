package domain

import (
	"strings"
	"time"
)

const ContractVersion = 1

type TaskKind string

const (
	TaskResearch       TaskKind = "research"
	TaskBugfix         TaskKind = "bugfix"
	TaskImplementation TaskKind = "implementation"
	TaskReview         TaskKind = "review"
	TaskDocumentation  TaskKind = "documentation"
	TaskSkillAuthoring TaskKind = "skill-authoring"
)

type Permission string

const (
	PermissionReadOnly Permission = "read-only"
	PermissionWorktree Permission = "worktree-write"
)

// PermissionDenial is a tool call a runtime refused because the step's
// permissions did not cover it. It is provider-neutral: Tool is the runtime's
// tool name, Command is set when the refused call was a shell command, and
// Detail is a short description for the user.
type PermissionDenial struct {
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// PermissionGrant widens one run's permissions after the user approved a
// denial. A command grant allows that exact command line; a tool grant allows
// the named tool. Grants never outlive the run that asked for them.
type PermissionGrant struct {
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
}

type StepKind string

const (
	StepAgent      StepKind = "agent"
	StepScript     StepKind = "script"
	StepEvaluation StepKind = "evaluation"
	StepApproval   StepKind = "approval"
	StepHumanInput StepKind = "human-input"
)

type Runtime string

const (
	RuntimeCodex   Runtime = "codex"
	RuntimeClaude  Runtime = "claude"
	RuntimeCopilot Runtime = "copilot"
	// RuntimeRemote is the reserved runtime of a step staffed by a remote A2A
	// agent: no CLI runs, and the agent picks its own model. It cannot be
	// declared in configuration.
	RuntimeRemote Runtime = "remote"
)

// ModelSourceRemote is the model selection source of a remote agent's step.
const ModelSourceRemote = "remote"

type ComplexityAssessment struct {
	Score     int      `json:"score"`
	Rationale string   `json:"rationale"`
	Signals   []string `json:"signals,omitempty"`
}

type ModelSelection struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	PolicyVersion   int    `json:"policyVersion,omitempty"`
	Source          string `json:"source"`
	Rationale       string `json:"rationale"`
}

type Budget struct {
	WallTimeSeconds int   `json:"wallTimeSeconds" yaml:"wallTimeSeconds"`
	MaxTurns        int   `json:"maxTurns,omitempty" yaml:"maxTurns,omitempty"`
	MaxOutputBytes  int64 `json:"maxOutputBytes,omitempty" yaml:"maxOutputBytes,omitempty"`
	MaxCostCents    int   `json:"maxCostCents,omitempty" yaml:"maxCostCents,omitempty"`
}

type TaskSource struct {
	Kind       string `json:"kind"` // request, github, or linear
	Reference  string `json:"reference,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// TaskSpec is a task as planned. Title names it at a glance, and its runs
// and branch after it; Objective is the details of what to do. A task from
// before titles has none.
type TaskSpec struct {
	Version            int               `json:"version"`
	ID                 string            `json:"id"`
	Kind               TaskKind          `json:"kind"`
	Title              string            `json:"title,omitempty"`
	Objective          string            `json:"objective"`
	Source             TaskSource        `json:"source"`
	AcceptanceCriteria []string          `json:"acceptanceCriteria"`
	Constraints        []string          `json:"constraints,omitempty"`
	Labels             []string          `json:"labels,omitempty"`
	Repository         string            `json:"repository"`
	Priority           int               `json:"priority"`
	Budget             Budget            `json:"budget"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

type WorkflowStep struct {
	ID             string                `json:"id"`
	Title          string                `json:"title"`
	Kind           StepKind              `json:"kind"`
	DependsOn      []string              `json:"dependsOn,omitempty"`
	Runtime        Runtime               `json:"runtime,omitempty"`
	Agent          string                `json:"agent,omitempty"`
	Prompt         string                `json:"prompt,omitempty"`
	Complexity     *ComplexityAssessment `json:"complexity,omitempty"`
	ModelSelection *ModelSelection       `json:"modelSelection,omitempty"`
	Permission     Permission            `json:"permission"`
	SelectedSkills []string              `json:"selectedSkills,omitempty"`
	AllowedPaths   []string              `json:"allowedPaths,omitempty"`
	Commands       [][]string            `json:"commands,omitempty"`
	// ContinueOnFailure runs an evaluation step's remaining commands after
	// one fails.
	ContinueOnFailure bool              `json:"continueOnFailure,omitempty"`
	ExpectedOutputs   []string          `json:"expectedOutputs,omitempty"`
	Budget            Budget            `json:"budget"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	// Review makes an approval step pause for a person to approve or request
	// changes (docs/feedback-loop.md).
	Review bool `json:"review,omitempty"`
	// OnFailure is what an evaluation step's failure does: fail the run, the
	// default, or send the failed commands back as feedback.
	OnFailure FailureAction `json:"onFailure,omitempty"`
	// FeedbackTarget is the writable agent step this approval or evaluation
	// step's feedback goes to; empty at a review gate with none upstream,
	// which can then only be approved.
	FeedbackTarget string `json:"feedbackTarget,omitempty"`
	// MaxRounds caps the automatic rounds an evaluation step's failures may
	// start.
	MaxRounds int `json:"maxRounds,omitempty"`
	// SystemExitCodes are the exit codes with which an evaluation step that
	// sends its failures back says its environment broke rather than the
	// code. When it lists any, a check that exits with one, could not
	// start, or was stopped by a signal pauses the run for a person instead
	// of starting a round (docs/feedback-loop.md).
	SystemExitCodes []int `json:"systemExitCodes,omitempty"`
	// MCPServers names the declared MCP servers an agent step is granted:
	// its stage's and its agent's, sorted.
	MCPServers []string `json:"mcpServers,omitempty"`
	// UserMCP names the user's own MCP servers the step is given with all
	// their tools: those its runtime opts in to (runtimes.<name>.userMcp),
	// as the plan recorded them. Only a local agent step gets them.
	UserMCP []string `json:"userMcp,omitempty"`
	// TrackerKinds are the kinds of tracker change an agent step may ask
	// for: those its stage and its agent ask for that the project allowed
	// when it was planned, in the order settings show them. The run narrows
	// them to what the repository still allows when the step starts.
	TrackerKinds []TrackerKind `json:"trackerKinds,omitempty"`
	// AccessEdits records each change the approval screen made to what the
	// step may do, its permission, commands, or allowed paths, against the
	// plan. Go derives it; an approval cannot set it.
	AccessEdits []AccessEdit `json:"accessEdits,omitempty"`
	// RuleAccess is the access the step's stage gave it when the plan was
	// made, before any approval changed it, kept so a rerun's changes can be
	// carried into the rule only where the rule still says the same. Planning
	// sets it on agent and evaluation steps; an approval cannot change it.
	RuleAccess *StepAccess `json:"ruleAccess,omitempty"`
	// From says where the step came from when its rule used a shared stage
	// or group, as planning expanded it. It is kept with the plan, so a
	// later edit to a library cannot change what an approved plan shows;
	// an approval cannot change it. Nil for a stage written inline in the
	// flow.
	From *StepOrigin `json:"from,omitempty"`
}

// StepOrigin is the shared stage or group a planned step was expanded from.
type StepOrigin struct {
	// Stage is the shared stage the step is a use of; empty for a stage
	// written inline in a group.
	Stage string `json:"stage,omitempty"`
	// Groups are the groups the step came through, outermost first.
	Groups []string `json:"groups,omitempty"`
	// Scope is where the definition lives: builtin, global, or repository.
	Scope string `json:"scope"`
	// File is the file that defines it: the global library file, or the
	// repository's rules file; empty for a built-in one.
	File string `json:"file,omitempty"`
	// Overrides are the stage fields the flow's use set, by their name in
	// the rules file.
	Overrides []string `json:"overrides,omitempty"`
}

// Clone copies the origin so changing the copy never changes it.
func (origin *StepOrigin) Clone() *StepOrigin {
	if origin == nil {
		return nil
	}
	copied := *origin
	if origin.Groups != nil {
		copied.Groups = append([]string(nil), origin.Groups...)
	}
	if origin.Overrides != nil {
		copied.Overrides = append([]string(nil), origin.Overrides...)
	}
	return &copied
}

// StepAccess is what a step may do: its permission, its commands (on an
// agent step those its worker may run without asking, on an evaluation
// step those it runs), and the paths a writable step keeps its writes to.
type StepAccess struct {
	Permission   Permission `json:"permission,omitempty"`
	Commands     [][]string `json:"commands,omitempty"`
	AllowedPaths []string   `json:"allowedPaths,omitempty"`
}

// The fields of a step an approval may change to give it more or less
// access (AccessEdit.Field).
const (
	AccessPermission   = "permission"
	AccessCommands     = "commands"
	AccessAllowedPaths = "allowedPaths"
)

// AccessEdit is one change approval made to what a step may do, kept on the
// step as provenance: the rule said one thing, and the person who approved
// the run chose another.
type AccessEdit struct {
	// Field is AccessPermission, AccessCommands, or AccessAllowedPaths.
	Field string `json:"field"`
	// Note says what changed and what the plan had, for example "Permission
	// set to worktree-write at approval; the plan said read-only."
	Note string `json:"note"`
	// Widens is true when the change lets the step do something the plan
	// did not: write, run a command it did not list, or write under a path
	// outside the plan's. The approval screen warns about it.
	Widens bool `json:"widens,omitempty"`
}

// FailureAction is what an evaluation step's failure does.
type FailureAction string

const (
	FailureFail     FailureAction = "fail"
	FailureFeedback FailureAction = "feedback"
)

// An evaluation step that sends its failures back starts at most
// DefaultFeedbackRounds automatic rounds unless its rule says otherwise,
// and never more than MaxFeedbackRounds.
const (
	DefaultFeedbackRounds = 2
	MaxFeedbackRounds     = 5
)

// MaxExitCode is the highest exit code a process can report, and so the
// highest a rule can name as a system failure.
const MaxExitCode = 255

type WorkflowSpec struct {
	Version              int            `json:"version"`
	ID                   string         `json:"id"`
	TaskID               string         `json:"taskId"`
	DefaultRuntime       Runtime        `json:"defaultRuntime"`
	RoutingPolicyVersion int            `json:"routingPolicyVersion,omitempty"`
	RuleName             string         `json:"ruleName,omitempty"`
	Orchestrated         bool           `json:"orchestrated,omitempty"`
	MaxConcurrency       int            `json:"maxConcurrency"`
	Steps                []WorkflowStep `json:"steps"`
	// Branching is where the run's writable steps work. Planning derives it
	// from the repository's branching configuration; approval cannot edit it.
	Branching *BranchPlan `json:"branching,omitempty"`
	// MCPServerDigests are digests of the MCP servers inkuna.yaml declared
	// when the plan was made: what the approval screen shows of each
	// (MCPServer.Digest), and which secrets it is given, by name. Approval
	// refuses a step granted a server that has changed since, even under the
	// same name.
	MCPServerDigests map[string]string `json:"mcpServerDigests,omitempty"`
	// StageDigests are digests of the shared stages and groups the plan's
	// rule used when the plan was made, by name, wherever each was defined:
	// built in, in a global stage library, or in the repository's rules.
	// Approval refuses the plan once one of them changes.
	StageDigests map[string]string `json:"stageDigests,omitempty"`
	// UserMCP is each runtime's userMcp when the plan was made, the user's
	// own MCP servers a step on it is given. Approval refuses a plan whose
	// runtimes name others by then.
	UserMCP map[Runtime][]string `json:"userMcp,omitempty"`
	// TrackerKinds are the kinds of tracker change the project let agents
	// ask for when the plan was made, which approval derives a restaffed
	// step's kinds from, as planning did.
	TrackerKinds []TrackerKind `json:"trackerKinds,omitempty"`
	// Sources are the snapshots of the project's definition sources the
	// plan was made with, so a run can be traced to what its sources said.
	Sources   []SourceSnapshot `json:"sources,omitempty"`
	CreatedAt time.Time        `json:"createdAt"`
}

// SourceSnapshot is one definition source's snapshot a plan was made with.
type SourceSnapshot struct {
	// Source names the source by its extension and its place among that
	// extension's sources, such as "example/source".
	Source    string    `json:"source"`
	Version   string    `json:"version"`
	FetchedAt time.Time `json:"fetchedAt"`
	// Cached is set when the source could not be reached, so the plan used
	// the last snapshot it gave.
	Cached bool `json:"cached,omitempty"`
}

// Cleanup policies a BranchPlan can carry, plus the marker for a run that
// worked in the project checkout, which is never removed.
const (
	CleanupListener = "listener"
	CleanupPrune    = "prune"
	CleanupMerged   = "merged"
	CleanupManual   = "manual"
	CleanupCheckout = "checkout"
)

// BranchPlan says how a run gets its branch and working directory.
type BranchPlan struct {
	// Worktree false runs writable steps in the project checkout on the
	// branch it has checked out, which may not be Base.
	Worktree bool `json:"worktree"`
	// Branch is the planned name; run start adds -2, -3, ... if it is taken.
	Branch string `json:"branch,omitempty"`
	// Base is the branch new work starts from (origin/<base> when present).
	Base string `json:"base"`
	// Root is the absolute directory the worktree is created under.
	Root      string `json:"root,omitempty"`
	Cleanup   string `json:"cleanup"`
	MaxLength int    `json:"maxLength,omitempty"`
	// Proposed holds values for configuration keys the repository lacked,
	// written to its branching configuration when the plan is approved.
	Proposed map[string]string `json:"proposed,omitempty"`
	Notes    []string          `json:"notes,omitempty"`
}

type ArtifactRef struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	Name      string `json:"name,omitempty"`
	// Kind is what Inkuna stored it as (handoff, output, document, diff,
	// ...), so a link can say what it opens.
	Kind string `json:"kind,omitempty"`
}

// HumanResponse records an answer the user gave to a HumanInputRequest so a
// resumed step can continue with it.
type HumanResponse struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// HumanInputAnswer is one entry of the file an evaluation step's commands
// find at INKUNA_HUMAN_INPUTS: a human-input stage upstream of the step, the
// prompt the user was shown, and the answer they gave, as Inkuna recorded it.
type HumanInputAnswer struct {
	StageID    string    `json:"stageId"`
	Prompt     string    `json:"prompt"`
	Answer     string    `json:"answer"`
	AnsweredAt time.Time `json:"answeredAt"`
}

// PeerMessage is a compact agent-to-agent message delivered through the A2A
// bus. Workers never see each other's transcripts, only these summaries.
type PeerMessage struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject,omitempty"`
	Content string `json:"content"`
}

type ContextEnvelope struct {
	Version            int                   `json:"version"`
	TaskID             string                `json:"taskId"`
	StepID             string                `json:"stepId"`
	Runtime            Runtime               `json:"runtime"`
	Agent              string                `json:"agent,omitempty"`
	AgentInstructions  string                `json:"agentInstructions,omitempty"`
	HumanResponses     []HumanResponse       `json:"humanResponses,omitempty"`
	PeerMessages       []PeerMessage         `json:"peerMessages,omitempty"`
	Complexity         *ComplexityAssessment `json:"complexity,omitempty"`
	ModelSelection     *ModelSelection       `json:"modelSelection,omitempty"`
	Title              string                `json:"title,omitempty"`
	Objective          string                `json:"objective"`
	AcceptanceCriteria []string              `json:"acceptanceCriteria"`
	AllowedPaths       []string              `json:"allowedPaths,omitempty"`
	RelevantDecisions  []string              `json:"relevantDecisions,omitempty"`
	RelevantRisks      []string              `json:"relevantRisks,omitempty"`
	DependencySummary  []string              `json:"dependencySummary,omitempty"`
	Artifacts          []ArtifactRef         `json:"artifacts,omitempty"`
	SkillExcerpts      map[string]string     `json:"skillExcerpts,omitempty"`
	Commands           [][]string            `json:"commands,omitempty"`
	Budget             Budget                `json:"budget"`
	Metadata           map[string]string     `json:"metadata,omitempty"`
	// Docs is the repository's doc index: which docs exist and what their
	// frontmatter says, never their bodies.
	Docs []DocBrief `json:"docs,omitempty"`
	// DependencyOutputs are the files dependency steps declared and Inkuna
	// stored, at the paths they declared them. A step that depends on
	// another runs after it, in the same working directory, so the paths
	// lead to the files.
	DependencyOutputs []ArtifactOutput `json:"dependencyOutputs,omitempty"`
	// SkillIndex lists the selected skills sent by reference instead of in
	// SkillExcerpts, for the worker to open when it needs one.
	SkillIndex []SkillBrief `json:"skillIndex,omitempty"`
	// PreviousTurn is what the worker's last turn on this step left, for a
	// turn that cannot resume that turn's session and so starts without it.
	PreviousTurn *TurnDigest `json:"previousTurn,omitempty"`
	// Feedback is what this step was sent back to fix in a feedback round;
	// only a round's targets get it (docs/feedback-loop.md).
	Feedback *RoundFeedback `json:"feedback,omitempty"`
	// PreviousRound is what the target's own last run of the step left, for
	// a round that cannot resume that run's session.
	PreviousRound *RoundDigest `json:"previousRound,omitempty"`
	// Resumed is what the step's own last attempt left, for a step that runs
	// again because the person resumed the run it stopped in.
	Resumed *ResumeNote `json:"resumed,omitempty"`
	// StageTitle and StagePrompt are the step's stage in the repository's
	// rules: its title, and its prompt, which narrows what this step does
	// within the objective. A peer gets neither, as its objective is the
	// request it answers.
	StageTitle  string `json:"stageTitle,omitempty"`
	StagePrompt string `json:"stagePrompt,omitempty"`
	// Constraints are the limits the user set on the whole task.
	Constraints []string `json:"constraints,omitempty"`
	// MCPServers are the MCP servers the step was given, as inkuna.yaml
	// declared them when it started; a peer is given none.
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
	// UserMCPServers are the user's own MCP servers the step's runtime opts
	// in to, as the CLI reported them when the step started; one that was
	// not usable then says why. A peer is given none.
	UserMCPServers []UserMCPServer `json:"userMcpServers,omitempty"`
	// Tracker is what the step may ask of its repository's tracker in its
	// handoff's trackerChanges, when it may ask for anything; a peer may
	// not.
	Tracker *TrackerGrant `json:"tracker,omitempty"`
}

// MaxDigestChanges is how many changed paths a TurnDigest lists.
const MaxDigestChanges = 50

// TurnDigest is a worker's previous turn on a step, as the next turn needs
// it: what the turn stopped for, its summary, and the paths it changed,
// which are still in the working directory. MoreChanges counts the paths
// past MaxDigestChanges; a path is held to MaxDescriptionBytes, since the
// worker reported it and nothing else bounds it.
type TurnDigest struct {
	Turn        int      `json:"turn"`
	StoppedFor  string   `json:"stoppedFor"`
	Summary     string   `json:"summary,omitempty"`
	Changes     []string `json:"changes,omitempty"`
	MoreChanges int      `json:"moreChanges,omitempty"`
}

// NewTurnDigest takes the digest of a turn from its handoff.
func NewTurnDigest(turn int, stoppedFor string, handoff HandoffResult) *TurnDigest {
	digest := &TurnDigest{Turn: turn, StoppedFor: stoppedFor, Summary: strings.TrimSpace(handoff.Summary)}
	digest.Changes, digest.MoreChanges = digestChanges(handoff)
	return digest
}

// SkillBrief is a skill sent by reference. Path is slash-separated and
// relative to the worker's working directory, where the file holds the body
// Inkuna would otherwise have sent.
type SkillBrief struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
}

// DocBrief is one doc in the envelope's index. Path is slash-separated and
// relative to the repository root, so a worker opens it from its working
// directory. A deprecated doc stays, marked by its status; a doc with
// Problems stays with them, because its other fields may be wrong.
type DocBrief struct {
	Path        string   `json:"path"`
	Title       string   `json:"title,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Status      string   `json:"status,omitempty"`
	LastUpdated string   `json:"lastUpdated,omitempty"`
	Author      string   `json:"author,omitempty"`
	Problems    []string `json:"problems,omitempty"`
}

type Change struct {
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

// Usage is what one model call, or a sum of calls, consumed. Input is split
// the way providers bill it: InputTokens are read fresh, CacheWriteTokens are
// written to the prompt cache, and CacheReadTokens are served from it, so the
// three never overlap and TotalInputTokens is their sum.
// LongCacheWriteTokens are the part of CacheWriteTokens written to a cache
// that lasts an hour, which costs more to write than the five-minute one.
// ReportedCents is the cost the runtime itself reported, in cents, and zero
// when it reported none; PremiumRequests are the Copilot premium requests the
// call used, which Copilot bills instead of tokens.
type Usage struct {
	InputTokens          int     `json:"inputTokens,omitempty"`
	CacheWriteTokens     int     `json:"cacheWriteTokens,omitempty"`
	LongCacheWriteTokens int     `json:"longCacheWriteTokens,omitempty"`
	CacheReadTokens      int     `json:"cacheReadTokens,omitempty"`
	OutputTokens         int     `json:"outputTokens,omitempty"`
	ReportedCents        float64 `json:"reportedCents,omitempty"`
	PremiumRequests      float64 `json:"premiumRequests,omitempty"`
	Turns                int     `json:"turns,omitempty"`
}

// TotalInputTokens is every prompt token the model read, cached or not.
func (usage Usage) TotalInputTokens() int {
	return usage.InputTokens + usage.CacheWriteTokens + usage.CacheReadTokens
}

// Add returns the field-by-field sum of two usages.
func (usage Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens: usage.InputTokens + other.InputTokens, CacheWriteTokens: usage.CacheWriteTokens + other.CacheWriteTokens,
		LongCacheWriteTokens: usage.LongCacheWriteTokens + other.LongCacheWriteTokens,
		CacheReadTokens:      usage.CacheReadTokens + other.CacheReadTokens, OutputTokens: usage.OutputTokens + other.OutputTokens,
		ReportedCents: usage.ReportedCents + other.ReportedCents, PremiumRequests: usage.PremiumRequests + other.PremiumRequests,
		Turns: usage.Turns + other.Turns,
	}
}

// Where a call's cost figure came from. Figures from different sources are
// never summed as if they were one kind.
const (
	// CostReported is the cost the runtime reported.
	CostReported = "reported"
	// CostEstimated is the call's tokens, or its Copilot premium requests,
	// priced by the pricing table.
	CostEstimated = "estimated"
	// CostUnknown is a call nothing priced. It is counted, never valued at
	// zero.
	CostUnknown = "unknown"
)

// How a runtime was signed in when it made a call, where its stream says.
// A subscription login still reports what the call would cost through the
// API, which is not what the subscription bills.
const (
	AuthSubscription = "subscription"
	AuthAPIKey       = "api-key"
)

// Cost is what one model call cost, in cents, and where the figure came
// from: Source is one of CostReported, CostEstimated, and CostUnknown, and
// Cents is zero when it is unknown. EstimatedCents is the pricing table's
// figure for the call, kept beside a reported cost so that a large gap
// between the two shows the table is stale; it is zero when the model has no
// price. PricingVersion is the version of the built-in pricing table in force
// when the call was priced, zero when nothing priced it; prices from
// inkuna.yaml are recorded under the same version.
type Cost struct {
	Cents          float64 `json:"cents"`
	Source         string  `json:"source"`
	EstimatedCents float64 `json:"estimatedCents,omitempty"`
	PricingVersion int     `json:"pricingVersion,omitempty"`
}

// TotalUsage sums what a list of calls consumed.
func TotalUsage(calls []UsageRecord) Usage {
	var total Usage
	for _, call := range calls {
		total = total.Add(call.Usage)
	}
	return total
}

// CostTotal sums calls' costs without mixing their sources: ReportedCents
// adds up the calls whose cost was reported, EstimatedCents those whose cost
// was estimated, and UnknownCalls counts the calls nothing priced.
type CostTotal struct {
	ReportedCents  float64 `json:"reportedCents"`
	EstimatedCents float64 `json:"estimatedCents"`
	UnknownCalls   int     `json:"unknownCalls"`
}

// BestCents is the total of each call's best figure: reported where there
// is one, otherwise estimated. Unknown calls add nothing, so with any of
// them it is a floor, not a total.
func (total CostTotal) BestCents() float64 { return total.ReportedCents + total.EstimatedCents }

// Add counts one call's cost under its source. A call stored before costs
// had sources counts as unknown.
func (total CostTotal) Add(cost Cost) CostTotal {
	switch cost.Source {
	case CostReported:
		total.ReportedCents += cost.Cents
	case CostEstimated:
		total.EstimatedCents += cost.Cents
	default:
		total.UnknownCalls++
	}
	return total
}

// TotalCost sums what a list of calls cost, by source.
func TotalCost(calls []UsageRecord) CostTotal {
	var total CostTotal
	for _, call := range calls {
		total = total.Add(call.Cost)
	}
	return total
}

// Kinds of model call a UsageRecord can describe.
const (
	CallWorker       = "worker"
	CallPeer         = "peer"
	CallOrchestrator = "orchestrator"
)

// Outcomes of a model call. A call that failed, timed out, or was cancelled
// still records the usage its runtime reported before it stopped.
const (
	OutcomeCompleted = "completed"
	OutcomeFailed    = "failed"
	OutcomeTimedOut  = "timed-out"
	OutcomeCancelled = "cancelled"
	// OutcomeRunning marks a call the run view shows before it ends. No
	// stored call has it.
	OutcomeRunning = "running"
)

// Prompt sections a PromptSection can name. A worker prompt is made of the
// schema, the agent's instructions, Inkuna's protocol, and a section for
// each part of the context envelope it carries; SectionEnvelope is the parts
// no other section claims: acceptance criteria, allowed paths, and commands.
// Calls made before Inkuna rendered the envelope as sections measured its
// JSON instead, with the instructions it repeated as an instructions section
// named agentInstructions. An orchestrator prompt uses the
// schema, instructions, and protocol sections and names the rest after the
// fields of its request.
const (
	SectionSchema         = "schema"
	SectionInstructions   = "instructions"
	SectionProtocol       = "protocol"
	SectionSkill          = "skill"
	SectionSkillIndex     = "skillIndex"
	SectionObjective      = "objective"
	SectionStage          = "stage"
	SectionConstraints    = "constraints"
	SectionDependencies   = "dependencies"
	SectionOutputs        = "outputs"
	SectionDecisions      = "decisions"
	SectionRisks          = "risks"
	SectionHumanResponses = "humanResponses"
	SectionPeerMessages   = "peerMessages"
	SectionPreviousTurn   = "previousTurn"
	SectionFeedback       = "feedback"
	SectionPreviousRound  = "previousRound"
	SectionResumed        = "resumed"
	SectionDocs           = "docs"
	SectionEnvelope       = "envelope"
)

// PromptSection is the size in bytes of one part of a prompt as it was sent.
// Name tells apart sections of the same kind, such as one skill from another.
// Bytes are exact where tokens would depend on the model's tokenizer.
type PromptSection struct {
	Section string `json:"section"`
	Name    string `json:"name,omitempty"`
	Bytes   int    `json:"bytes"`
	// Truncated says a budget cut the part this section sends.
	Truncated bool `json:"truncated,omitempty"`
}

// UsageRecord is one model call: a worker's turn, a peer's answer, or an
// orchestrator call made while planning. A planning call happens before the
// run exists, so it has no ExecutionID and belongs to the task. Turn is the
// step's turn the call belongs to; a peer's answer shares the turn of the
// worker that asked. Model is the one Inkuna asked for, empty when the
// runtime chose; ModelUsed is the one the runtime's stream says it used,
// empty when the stream does not say, and AuthMode is how the runtime was
// signed in, where it says. Cost is what the call cost and where that figure
// came from. PromptBytes is the sum of Sections. PromptDigest names the
// stored prompt of a worker or peer call, exactly as its runtime received
// it; it is empty for an orchestrator call and for calls made before Inkuna
// kept prompts.
type UsageRecord struct {
	ID          int64  `json:"id"`
	TaskID      string `json:"taskId"`
	ExecutionID string `json:"executionId,omitempty"`
	StepID      string `json:"stepId"`
	CallID      string `json:"callId"`
	Turn        int    `json:"turn"`
	// Round is the feedback round of the step run that made the call, or
	// zero for a call made outside one, such as while planning.
	Round        int             `json:"round,omitempty"`
	Kind         string          `json:"kind"`
	Agent        string          `json:"agent"`
	Runtime      Runtime         `json:"runtime"`
	Model        string          `json:"model,omitempty"`
	ModelUsed    string          `json:"modelUsed,omitempty"`
	AuthMode     string          `json:"authMode,omitempty"`
	Usage        Usage           `json:"usage"`
	Cost         Cost            `json:"cost"`
	PromptBytes  int             `json:"promptBytes"`
	Sections     []PromptSection `json:"sections,omitempty"`
	PromptDigest string          `json:"promptDigest,omitempty"`
	Outcome      string          `json:"outcome"`
	StartedAt    time.Time       `json:"startedAt"`
	FinishedAt   time.Time       `json:"finishedAt"`
}

// ModelName is the model the call's stream said it used, else the one
// Inkuna asked for: the one that priced it. It is empty when the runtime
// chose and did not say.
func (call UsageRecord) ModelName() string {
	if call.ModelUsed != "" {
		return call.ModelUsed
	}
	return call.Model
}

// Duration is how long the call ran.
func (call UsageRecord) Duration() time.Duration {
	if call.FinishedAt.Before(call.StartedAt) {
		return 0
	}
	return call.FinishedAt.Sub(call.StartedAt)
}

// HumanInputRequest is emitted by a worker when it cannot proceed without a
// decision only the user can make. The run pauses in the A2A input-required
// state until the inbox answers it.
type HumanInputRequest struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"`
}

// ArtifactOutput names a file the worker produced inside its working
// directory. Inkuna ingests it into the content-addressed store so every
// agent, regardless of runtime, can read it later.
type ArtifactOutput struct {
	Path        string `json:"path"`
	Name        string `json:"name,omitempty"`
	MediaType   string `json:"mediaType,omitempty"`
	Description string `json:"description,omitempty"`
	// Digest is the stored copy's, set by Inkuna when it stores the file,
	// so the file can be put back if it is gone before a later step reads
	// it. A worker's own value is never kept.
	Digest string `json:"digest,omitempty"`
}

type HandoffResult struct {
	Version      int      `json:"version"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	Changes      []Change `json:"changes,omitempty"`
	Verification []string `json:"verification,omitempty"`
	Decisions    []string `json:"decisions,omitempty"`
	Risks        []string `json:"risks,omitempty"`
	// Document is the step's plan or report as Markdown, for people and the
	// steps after it to read. Inkuna stores it as the step's document
	// artifact and writes it under .inkuna/out, then clears it here, so a
	// stored handoff never carries it.
	Document   string             `json:"document,omitempty"`
	Usage      Usage              `json:"usage,omitempty"`
	Artifacts  []ArtifactRef      `json:"artifacts,omitempty"`
	Outputs    []ArtifactOutput   `json:"outputs,omitempty"`
	Messages   []PeerMessage      `json:"messages,omitempty"`
	HumanInput *HumanInputRequest `json:"humanInput,omitempty"`
	// TrackerChanges are the changes to the repository's issue tracker the
	// step asks for. Inkuna checks and keeps them when the step ends, then
	// clears them here, so a stored handoff never carries them.
	TrackerChanges []TrackerChange `json:"trackerChanges,omitempty"`
	// Addressed is a round's target's reply on each feedback item it was
	// sent, as Go settled it (SettleAddressed).
	Addressed []Addressed       `json:"addressed,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Handoff statuses shared by every runtime adapter.
const (
	HandoffCompleted     = "completed"
	HandoffFailed        = "failed"
	HandoffInputRequired = "input-required"
)

type EventKind string

const (
	EventLifecycle EventKind = "lifecycle"
	EventMessage   EventKind = "message"
	EventTool      EventKind = "tool"
	EventFile      EventKind = "file-change"
	EventUsage     EventKind = "usage"
	EventWarning   EventKind = "warning"
	EventError     EventKind = "error"
	EventComplete  EventKind = "completion"
)

type RunEvent struct {
	Version   int            `json:"version"`
	RunID     string         `json:"runId"`
	Sequence  int64          `json:"sequence"`
	Timestamp time.Time      `json:"timestamp"`
	Kind      EventKind      `json:"kind"`
	Summary   string         `json:"summary"`
	Payload   map[string]any `json:"payload,omitempty"`
	Artifact  *ArtifactRef   `json:"artifact,omitempty"`
	Usage     *Usage         `json:"usage,omitempty"`
	// Reads are the read-type tool calls this event reports, read from the
	// runtime's own event so every runtime gives the same shape.
	Reads []FileRead `json:"reads,omitempty"`
}

// FileRead is one read-type tool call: the tool, and the paths it named as
// the agent wrote them, absolute or relative to the step's working
// directory. A search names the directory it searched, not the files in it.
type FileRead struct {
	Tool  string   `json:"tool"`
	Paths []string `json:"paths"`
}

// DocRead records that an agent opened one of a project's docs, or searched
// a directory of them, during a task. DocPath is relative to the docs root;
// a searched directory ends in "/", and the docs root itself is "/". DocsDir
// is that root, relative to the repository, as it was configured when the
// step ran, so the read keeps naming the same file after the configuration
// changes. Only the path is kept, never the doc's content.
type DocRead struct {
	TaskID    string    `json:"taskId"`
	StepID    string    `json:"stepId"`
	RunID     string    `json:"runId"`
	ProjectID string    `json:"projectId"`
	Agent     string    `json:"agent"`
	Runtime   Runtime   `json:"runtime"`
	DocsDir   string    `json:"docsDir"`
	DocPath   string    `json:"docPath"`
	Tool      string    `json:"tool"`
	OpenedAt  time.Time `json:"openedAt"`
}
