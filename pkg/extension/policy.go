package extension

import "context"

// The points at which Inkuna asks its policies, as Decision.Point names
// them. Later versions add points, and a policy allows a point it does not
// know unless it means to refuse everything it does not understand.
const (
	// PointPlanApprove is a person approving a plan, which starts a run.
	PointPlanApprove = "plan.approve"
	// PointReviewApprove is a person approving a run paused at review.
	PointReviewApprove = "review.approve"
	// PointTrackerWrite is a write to the repository's tracker: a change an
	// agent asked for that a person approved, or one of Inkuna's own
	// updates to the run's story.
	PointTrackerWrite = "tracker.write"
	// PointMCPGrant is a step starting with MCP servers, before its CLI
	// starts.
	PointMCPGrant = "mcp.grant"
	// PointPermissionGrant is a person allowing what a worker was denied.
	PointPermissionGrant = "permission.grant"
	// PointRulesSave is a save of a project's rules.
	PointRulesSave = "rules.save"
	// PointAgentSave is a save of an agent definition.
	PointAgentSave = "agent.save"
	// PointConfigSave is a save of inkuna.yaml.
	PointConfigSave = "config.save"
)

// Policy can refuse something Go has already decided to allow. Inkuna asks
// its policies only once every check of its own has passed, so a policy
// never sees, and can never allow, what Go refused. It asks them in the
// order they were registered and stops at the first refusal.
//
// A policy fails closed: an error, a panic, or no answer within ten seconds
// refuses, as a refusal does. Check is called while a person waits for the
// answer, and some points hold a save or a run until it comes, so it should
// answer quickly and honour its context.
type Policy interface {
	Check(context.Context, Decision) (Verdict, error)
}

// Decision is something Go has already decided to allow, as a policy is
// asked about it. A policy gets its own copy, so changing it changes
// nothing.
type Decision struct {
	// Point is where the decision is made: one of the Point constants.
	Point string `json:"point"`
	// Project is the project's ID, empty for a decision that belongs to no
	// project, such as saving inkuna.yaml or a global agent.
	Project string `json:"project,omitempty"`
	// Actor is "user" for a person's decision, the step's agent for a grant
	// to a step, or "inkuna" for Inkuna's own updates to a tracker.
	Actor string `json:"actor"`
	// Principal is the person the decision is made for, with their groups,
	// as the identity provider names them when the policy is asked: the
	// person deciding, or the one at this machine for a decision a run
	// makes. When the provider cannot name them, nothing is asked and the
	// decision is refused.
	Principal Principal `json:"principal"`
	// Details are the point's facts, such as the run, the agents, the
	// servers, or a saved file's digest. A list is comma-separated and
	// sorted. A key with no value is left out. docs/extensions.md lists
	// each point's keys; later versions add keys.
	Details map[string]string `json:"details,omitempty"`
}

// Verdict is a policy's answer.
type Verdict struct {
	// Refuse refuses the decision. The zero Verdict allows it.
	Refuse bool `json:"refuse"`
	// Reason is shown to the person as Go's own reasons are, after the
	// policy's name. It should say what the person can do.
	Reason string `json:"reason,omitempty"`
}
