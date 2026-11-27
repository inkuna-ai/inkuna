package extension

import (
	"context"
	"time"
)

// AuditSink receives Inkuna's audit events. Inkuna stores each event before
// handing it on and gives a sink its events one at a time, in the order they
// were recorded, calling Record again with the same event until it returns
// nil. A slow or failing sink never holds up a run, and an event is never
// lost to a crash, so Record must be idempotent on the event's ID: it may
// see an event again after it accepted it. A call that takes longer than a
// minute is abandoned and retried.
type AuditSink interface {
	Record(context.Context, AuditEvent) error
}

// AuditEvent is one thing that happened in Inkuna, built from what Inkuna
// had already stored, in the redacted form it stored it. It never carries a
// secret value, a worker's transcript, or file content. A receiver accepts
// fields it does not know, since later versions add them.
type AuditEvent struct {
	// ID is unique across machines and the same each time the event is
	// delivered: the installation, "/audit/", and a sequence number.
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
	// Kind is what happened: "run.event", "tracker.write", "a2a.task",
	// "approval", "config.save", "rules.save", "library.save",
	// "agent.save", "agent.delete", or "history.delete". Later versions
	// add kinds.
	Kind string `json:"kind"`
	// Installation is the data directory that recorded the event.
	Installation string `json:"installation"`
	Project      string `json:"project,omitempty"`
	Task         string `json:"task,omitempty"`
	Run          string `json:"run,omitempty"`
	// Actor is "user", an agent's name, or "inkuna".
	Actor string `json:"actor,omitempty"`
	// Principal is the Principal.ID of the person the event happened for:
	// the person who decided, or the run's approver.
	Principal string `json:"principal,omitempty"`
	// Summary is Inkuna's own wording, never text from a run, which can hold
	// what a worker, a command, or a person wrote. A run event or tracker
	// write has none: its facts are in Fields.
	Summary string `json:"summary,omitempty"`
	// Fields are the kind's facts, such as a run event's step or a saved
	// file's digest. Saves carry the new content's digest, never the
	// content.
	Fields map[string]string `json:"fields,omitempty"`
}

// UsageSink receives every model call Inkuna records, delivered as audit
// events are: stored first, in order, retried until Record returns nil, so
// Record must be idempotent on the record's ID. The first time a data
// directory starts with a usage sink, every call already stored is queued
// once, oldest first. Deleting history never takes back a record that is
// queued.
type UsageSink interface {
	Record(context.Context, UsageRecord) error
}

// UsageRecord is one model call and what it cost, as Inkuna stored it. It
// carries counts and figures only, never a prompt or what it held. A
// receiver accepts fields it does not know, since later versions add them.
type UsageRecord struct {
	// ID is unique across machines and the same each time the record is
	// delivered: the installation, "/", and the call's usage row.
	ID string `json:"id"`
	// Installation is random and made once per data directory.
	Installation string `json:"installation"`
	// Principal is the Principal.ID of who started the work: the run's
	// approver, or for a planning call, the task's creator. It is empty for
	// work from before principals were recorded.
	Principal string `json:"principal,omitempty"`
	Project   string `json:"project,omitempty"`
	Task      string `json:"task"`
	// Run is empty for a call made while planning, before there was one.
	Run  string `json:"run,omitempty"`
	Step string `json:"step"`
	Call string `json:"call"`
	Turn int    `json:"turn"`
	// Kind is "worker", "peer", or "orchestrator".
	Kind    string `json:"kind"`
	Agent   string `json:"agent"`
	Runtime string `json:"runtime"`
	// Model is the model Inkuna asked for, and ModelUsed the one the
	// runtime's stream named; either may be empty.
	Model     string `json:"model,omitempty"`
	ModelUsed string `json:"modelUsed,omitempty"`
	// AuthMode is "api-key", "subscription", or empty when not known.
	AuthMode string `json:"authMode,omitempty"`
	// Outcome is "completed", "failed", "timed-out", or "cancelled".
	Outcome    string    `json:"outcome"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Tokens     Tokens    `json:"tokens"`
	// Premium is the Copilot premium requests the call used.
	Premium float64 `json:"premium,omitempty"`
	Cost    Cost    `json:"cost"`
}

// Tokens are the tokens a call read and wrote.
type Tokens struct {
	Input int `json:"input"`
	// CacheWrite includes LongCacheWrite, the part written to the one-hour
	// cache.
	CacheWrite     int `json:"cacheWrite"`
	LongCacheWrite int `json:"longCacheWrite"`
	CacheRead      int `json:"cacheRead"`
	Output         int `json:"output"`
}

// Cost is what a call cost and where the figure came from, so reported and
// estimated sums can be kept apart and calls nothing priced counted.
type Cost struct {
	// Source is "reported" (the runtime said), "estimated" (Inkuna priced
	// the tokens), or "unknown" (neither could).
	Source string  `json:"source"`
	Cents  float64 `json:"cents"`
	// EstimatedCents is Inkuna's own price for the tokens, set beside a
	// reported cost when the pricing table knew the model.
	EstimatedCents float64 `json:"estimatedCents,omitempty"`
	// PricingVersion is the pricing table that estimated it, so estimates
	// can be repriced when a table is found wrong.
	PricingVersion int `json:"pricingVersion,omitempty"`
}
