package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Limits on the doc index one envelope carries, so a large documentation
// tree cannot crowd the task out of a worker's prompt. The byte limit is on
// the compact JSON of the index.
const (
	MaxDocBriefs     = 400
	MaxDocBriefBytes = 64 << 10
)

// DocStatusDeprecated is the doc status the index drops first when it is
// over its limits.
const DocStatusDeprecated = "deprecated"

// MetadataDocsTruncated is the envelope metadata key set to "true" when the
// doc index was cut to fit its limits.
const MetadataDocsTruncated = "docsTruncated"

// MetadataPermission is the envelope metadata key holding the permission the
// worker runs with, which can be narrower than its agent's: a rule's stage
// or a peer request makes it read-only.
const MetadataPermission = "permission"

// BuildContext constructs a bounded downstream handoff. RunEvent transcripts are
// deliberately not accepted by this API. Every agent step gets the same doc
// index, cut to its limits. The step's stage title and prompt and the task's
// constraints go with the objective; the task's labels do not, as they
// select rules rather than say what to do.
func BuildContext(task TaskSpec, step WorkflowStep, dependencies []HandoffResult, skills map[string]string, index []DocBrief) (ContextEnvelope, error) {
	if err := ValidateTask(task); err != nil {
		return ContextEnvelope{}, err
	}
	summaries := make([]string, 0, len(dependencies))
	var outputs []ArtifactOutput
	artifacts := make([]ArtifactRef, 0)
	decisions := make([]string, 0)
	var risks []string
	for _, result := range dependencies {
		if result.Summary == "" {
			return ContextEnvelope{}, fmt.Errorf("dependency handoff is missing its summary")
		}
		summaries = append(summaries, result.Summary)
		outputs = append(outputs, result.Outputs...)
		artifacts = append(artifacts, result.Artifacts...)
		decisions = append(decisions, result.Decisions...)
		risks = append(risks, result.Risks...)
	}
	selected := make(map[string]string, len(step.SelectedSkills))
	for _, id := range step.SelectedSkills {
		body, ok := skills[id]
		if !ok {
			return ContextEnvelope{}, fmt.Errorf("selected skill %q is unavailable", id)
		}
		selected[id] = body
	}
	envelope := ContextEnvelope{
		Version: ContractVersion, TaskID: task.ID, StepID: step.ID,
		Runtime: step.Runtime, Agent: step.Agent, Complexity: step.Complexity, ModelSelection: step.ModelSelection,
		Title: task.Title, Objective: task.Objective, StageTitle: step.Title, StagePrompt: step.Prompt,
		AcceptanceCriteria: task.AcceptanceCriteria, Constraints: task.Constraints, AllowedPaths: step.AllowedPaths, RelevantDecisions: decisions, RelevantRisks: risks,
		DependencySummary: summaries, DependencyOutputs: outputs, Artifacts: artifacts, SkillExcerpts: selected,
		Commands: step.Commands, Budget: step.Budget,
	}
	var truncated bool
	envelope.Docs, truncated = boundDocs(index)
	if truncated {
		envelope.Metadata = map[string]string{MetadataDocsTruncated: "true"}
	}
	return envelope, nil
}

// IndexSkills sends the given skills by reference: each leaves SkillExcerpts
// for SkillIndex, so the envelope carries its id, description, and path, and
// the worker opens the body when it needs it. Skills the envelope did not
// select are ignored. The caller's map is never modified.
func IndexSkills(envelope ContextEnvelope, briefs []SkillBrief) ContextEnvelope {
	if len(briefs) == 0 {
		return envelope
	}
	excerpts := make(map[string]string, len(envelope.SkillExcerpts))
	for id, body := range envelope.SkillExcerpts {
		excerpts[id] = body
	}
	index := append([]SkillBrief(nil), envelope.SkillIndex...)
	for _, brief := range briefs {
		if _, selected := excerpts[brief.ID]; selected {
			delete(excerpts, brief.ID)
			index = append(index, brief)
		}
	}
	envelope.SkillExcerpts, envelope.SkillIndex = excerpts, index
	return envelope
}

// boundDocs cuts the doc index to MaxDocBriefs entries and MaxDocBriefBytes
// and reports whether anything was dropped. Deprecated docs go first, then
// the oldest lastUpdated, where a date that does not parse counts as oldest;
// ties go by path. Kept entries stay in index order.
func boundDocs(index []DocBrief) ([]DocBrief, bool) {
	return boundDocsTo(index, MaxDocBriefs, MaxDocBriefBytes)
}

// boundDocsTo cuts the index as boundDocs does, to the given limits.
func boundDocsTo(index []DocBrief, maxEntries, maxBytes int) ([]DocBrief, bool) {
	if len(index) == 0 {
		return index, false
	}
	sizes := make([]int, len(index))
	// The enclosing brackets, and a comma between entries.
	total := 2 + max(len(index)-1, 0)
	for i, doc := range index {
		// A DocBrief holds only strings, so encoding it cannot fail.
		data, _ := json.Marshal(doc)
		sizes[i] = len(data)
		total += len(data)
	}
	count := len(index)
	if count <= maxEntries && total <= maxBytes {
		return index, false
	}
	order := make([]int, len(index))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return dropsBefore(index[order[a]], index[order[b]]) })
	dropped := make([]bool, len(index))
	for _, i := range order {
		if count <= maxEntries && total <= maxBytes {
			break
		}
		dropped[i] = true
		total -= sizes[i]
		if count > 1 {
			total--
		}
		count--
	}
	kept := make([]DocBrief, 0, count)
	for i, doc := range index {
		if !dropped[i] {
			kept = append(kept, doc)
		}
	}
	return kept, true
}

func dropsBefore(a, b DocBrief) bool {
	if deprecatedA, deprecatedB := a.Status == DocStatusDeprecated, b.Status == DocStatusDeprecated; deprecatedA != deprecatedB {
		return deprecatedA
	}
	if dateA, dateB := docDate(a), docDate(b); !dateA.Equal(dateB) {
		return dateA.Before(dateB)
	}
	return a.Path < b.Path
}

func docDate(doc DocBrief) time.Time {
	date, err := time.Parse("2006-01-02", doc.LastUpdated)
	if err != nil {
		return time.Time{}
	}
	return date
}

// FieldBytes is how many bytes value takes as a top-level field's value in
// an object encoded by json.MarshalIndent(object, "", "  "). Nested lines of
// such a value carry one level of indent, so encoding it alone with that
// prefix reproduces it byte for byte. A value that cannot be encoded counts
// as nothing, as it could not have been sent either.
func FieldBytes(value any) int {
	data, err := json.MarshalIndent(value, "  ", "  ")
	if err != nil {
		return 0
	}
	return len(data)
}

// AdvisorHandoff is everything a remote advisor is told about a stage. It is
// its own type, not a filtered ContextEnvelope, so that what leaves the machine
// is decided by what this struct has fields for: no agent instructions, skill
// bodies, peer messages, allowed paths, commands, repository, budget, model,
// or transcript can be sent, because there is nowhere to put them.
type AdvisorHandoff struct {
	Version            int             `json:"version"`
	Title              string          `json:"title,omitempty"`
	Objective          string          `json:"objective"`
	AcceptanceCriteria []string        `json:"acceptanceCriteria,omitempty"`
	Constraints        []string        `json:"constraints,omitempty"`
	Stage              AdvisorStage    `json:"stage"`
	DependencySummary  []string        `json:"dependencySummary,omitempty"`
	RelevantDecisions  []string        `json:"relevantDecisions,omitempty"`
	Artifacts          []ArtifactRef   `json:"artifacts,omitempty"`
	ArtifactBodies     []ArtifactBody  `json:"artifactBodies,omitempty"`
	HumanResponses     []HumanResponse `json:"humanResponses,omitempty"`
}

// AdvisorStage identifies the rule stage the advisor is asked about.
type AdvisorStage struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Prompt string `json:"prompt,omitempty"`
}

// ArtifactBody is the text of an upstream artifact, sent only to a remote
// agent the user allowed to receive artifact bodies.
type ArtifactBody struct {
	Name      string `json:"name,omitempty"`
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// BuildAdvisorHandoff constructs the handoff for a remote advisor from the
// same inputs as BuildContext. Artifact bodies are the caller's to add.
func BuildAdvisorHandoff(task TaskSpec, step WorkflowStep, dependencies []HandoffResult, responses []HumanResponse) (AdvisorHandoff, error) {
	if err := ValidateTask(task); err != nil {
		return AdvisorHandoff{}, err
	}
	if step.Permission != PermissionReadOnly {
		return AdvisorHandoff{}, fmt.Errorf("step %q is not read-only and cannot go to a remote advisor", step.ID)
	}
	handoff := AdvisorHandoff{
		Version: ContractVersion, Title: task.Title, Objective: task.Objective, AcceptanceCriteria: task.AcceptanceCriteria, Constraints: task.Constraints,
		Stage: AdvisorStage{ID: step.ID, Title: step.Title, Prompt: step.Prompt}, HumanResponses: responses,
	}
	for _, result := range dependencies {
		if result.Summary == "" {
			return AdvisorHandoff{}, fmt.Errorf("dependency handoff is missing its summary")
		}
		handoff.DependencySummary = append(handoff.DependencySummary, result.Summary)
		handoff.RelevantDecisions = append(handoff.RelevantDecisions, result.Decisions...)
		handoff.Artifacts = append(handoff.Artifacts, result.Artifacts...)
	}
	return handoff, nil
}
