package sources

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/catalog"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// State is what one source gave for a project: a snapshot it gave just now,
// the last one it gave when it could not be reached, or nothing.
type State struct {
	Source string
	// Held is set when there is a snapshot, fresh or cached.
	Held      bool
	Snapshot  extension.Snapshot
	FetchedAt time.Time
	// Cached is set when the snapshot is the last one the source gave,
	// because it could not be reached now.
	Cached bool
	// Unreachable is why the source could not be asked or did not answer;
	// empty when it answered.
	Unreachable string
}

// What this build does with a definition, as a report names it.
const (
	// StatusOffered is a catalog definition offered to add.
	StatusOffered = "offered"
	// StatusFails is a catalog definition that fails the checks its file
	// would, so it is not offered.
	StatusFails = "fails"
	// StatusNotApplied is a shared definition, which this build ignores.
	StatusNotApplied = "not applied"
	// StatusRefuses is an enforced definition, or one of a mode or kind this
	// build does not know, which refuses plans in the project.
	StatusRefuses = "refuses"
)

// Report is one source's snapshot as this build reads it, for the view and
// inkunactl.
type Report struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	// FetchedAt is when the snapshot was given, empty when there is none.
	FetchedAt   string             `json:"fetchedAt,omitempty"`
	Cached      bool               `json:"cached,omitempty"`
	Unreachable string             `json:"unreachable,omitempty"`
	Definitions []DefinitionReport `json:"definitions"`
}

// DefinitionReport is one definition and what this build does with it.
type DefinitionReport struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Scope   string `json:"scope,omitempty"`
	Version string `json:"version,omitempty"`
	// Status is one of the Status constants, and Reason says why for any
	// definition that is not offered.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Catalog is the catalog one source offers.
type Catalog struct {
	Source  string
	Catalog catalog.Catalog
}

// Reading is every source's snapshot for a project, read.
type Reading struct {
	States   []State
	Reports  []Report
	Catalogs []Catalog
	// refusals say, one per definition, why plans in the project are
	// refused.
	refusals []string
}

// knownKinds are the kinds this build reads, as the catalog names them.
var knownKinds = map[extension.DefinitionKind]catalog.Kind{
	extension.KindAgent: catalog.KindAgent,
	extension.KindSkill: catalog.KindSkill,
	extension.KindStage: catalog.KindStage,
	extension.KindGroup: catalog.KindGroup,
	extension.KindRule:  catalog.KindRule,
}

// Read sorts each source's definitions by mode. This build applies only
// catalog definitions, which it checks and offers; it ignores a shared one,
// which loosens nothing, and refuses plans for an enforced one, or one of a
// mode or kind it does not know, since ignoring either would let the
// repository override what was required of it.
func Read(states []State) Reading {
	reading := Reading{States: states}
	for _, state := range states {
		report := Report{Source: state.Source, Cached: state.Cached, Unreachable: state.Unreachable, Definitions: []DefinitionReport{}}
		if state.Held {
			report.Version = state.Snapshot.Version
			report.FetchedAt = state.FetchedAt.UTC().Format(time.RFC3339)
		}
		var offered []catalog.SourceDefinition
		var offeredAt []int
		for _, definition := range state.Snapshot.Definitions {
			entry := DefinitionReport{Kind: string(definition.Kind), Name: definition.Name, Mode: string(definition.Mode), Scope: definition.Scope, Version: definition.Version}
			kind, known := knownKinds[definition.Kind]
			switch {
			case !known:
				entry.Status = StatusRefuses
				entry.Reason = fmt.Sprintf("this build does not know the kind of definition %q, so plans in this project are refused", definition.Kind)
				reading.refusals = append(reading.refusals, fmt.Sprintf("definition source %q shares %q, a kind of definition this build does not know", state.Source, definition.Name))
			case definition.Mode == extension.ModeCatalog:
				entry.Status = StatusOffered
				offered = append(offered, catalog.SourceDefinition{Kind: kind, Name: definition.Name, Scope: definition.Scope, Version: definition.Version, Files: definition.Files})
				offeredAt = append(offeredAt, len(report.Definitions))
			case definition.Mode == extension.ModeShared:
				entry.Status = StatusNotApplied
				entry.Reason = "this build applies only catalog definitions, so it is ignored"
			case definition.Mode == extension.ModeEnforced:
				entry.Status = StatusRefuses
				entry.Reason = "this build does not apply enforced definitions, so plans in this project are refused"
				reading.refusals = append(reading.refusals, fmt.Sprintf("definition source %q enforces %s %q%s, which this build does not apply", state.Source, definition.Kind, definition.Name, scoped(definition.Scope)))
			default:
				entry.Status = StatusRefuses
				entry.Reason = fmt.Sprintf("this build does not know the mode %q, so plans in this project are refused", definition.Mode)
				reading.refusals = append(reading.refusals, fmt.Sprintf("definition source %q shares %s %q%s in the mode %q, which this build does not know", state.Source, definition.Kind, definition.Name, scoped(definition.Scope), definition.Mode))
			}
			report.Definitions = append(report.Definitions, entry)
		}
		if len(offered) > 0 {
			sourceCatalog, problems := catalog.FromSource(state.Source, offered)
			// By the definition's place, not its name: of two that share a
			// name, one may be offered and the other left out.
			for _, problem := range problems {
				at := offeredAt[problem.Definition]
				report.Definitions[at].Status, report.Definitions[at].Reason = StatusFails, problem.Reason
			}
			reading.Catalogs = append(reading.Catalogs, Catalog{Source: state.Source, Catalog: sourceCatalog})
		}
		reading.Reports = append(reading.Reports, report)
	}
	return reading
}

func scoped(scope string) string {
	if scope == "" {
		return ""
	}
	return " for " + scope
}

// Refusal is why plans in the project are refused, or nil when nothing the
// sources share refuses them.
func (reading Reading) Refusal() error {
	if len(reading.refusals) == 0 {
		return nil
	}
	return errors.New(strings.Join(reading.refusals, "; ") + ", so plans in this project are refused until the source changes it or a build that applies it is used")
}

// Snapshots are the snapshots a plan is made with, to record on it.
func (reading Reading) Snapshots() []domain.SourceSnapshot {
	var snapshots []domain.SourceSnapshot
	for _, state := range reading.States {
		if state.Held {
			snapshots = append(snapshots, domain.SourceSnapshot{Source: state.Source, Version: state.Snapshot.Version, FetchedAt: state.FetchedAt.UTC(), Cached: state.Cached})
		}
	}
	return snapshots
}

// Notes say what a plan should know of its sources at now: one it could
// not reach, and how old the snapshot it used instead is, and what a source
// shares that this build does not apply.
func (reading Reading) Notes(now time.Time) []string {
	var notes []string
	for index, state := range reading.States {
		switch {
		case state.Unreachable != "" && state.Held:
			notes = append(notes, fmt.Sprintf("Definition source %q could not be reached (%s), so the plan used its snapshot from %s, %s old.", state.Source, state.Unreachable, state.FetchedAt.UTC().Format("2006-01-02 15:04 UTC"), age(now.Sub(state.FetchedAt))))
		case state.Unreachable != "":
			notes = append(notes, fmt.Sprintf("Definition source %q could not be reached (%s) and has no snapshot of this project kept, so it adds nothing.", state.Source, state.Unreachable))
		}
		var ignored []string
		for _, definition := range reading.Reports[index].Definitions {
			if definition.Status == StatusNotApplied {
				ignored = append(ignored, fmt.Sprintf("%s %q", definition.Kind, definition.Name))
			}
		}
		if len(ignored) > 0 {
			notes = append(notes, fmt.Sprintf("Definition source %q shares %s, which this build does not apply.", state.Source, strings.Join(ignored, ", ")))
		}
	}
	return notes
}

// age is a duration as a person reads it, to the minute.
func age(elapsed time.Duration) string {
	switch {
	case elapsed < time.Minute:
		return "under a minute"
	case elapsed < time.Hour:
		return fmt.Sprintf("%d min", int(elapsed/time.Minute))
	case elapsed < 48*time.Hour:
		return fmt.Sprintf("%dh %dmin", int(elapsed/time.Hour), int(elapsed%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%d days", int(elapsed/(24*time.Hour)))
}
