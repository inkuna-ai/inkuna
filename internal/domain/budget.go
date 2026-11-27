package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Budgets caps, in bytes, the text a context envelope carries, so that no
// single input (a pasted issue, a long skill, a chatty peer) can crowd the
// rest out of a worker's prompt or past what a command line holds. Budgets
// count the text itself, not the markdown it is rendered in, and include
// the note that says a field was cut. A zero field takes its default.
type Budgets struct {
	Objective int `yaml:"objective,omitempty" json:"objective,omitempty"`
	// StagePrompt is the prompt of the step's stage in the repository's
	// rules.
	StagePrompt int `yaml:"stagePrompt,omitempty" json:"stagePrompt,omitempty"`
	// AcceptanceCriteria, Constraints, Decisions, and Risks are totals;
	// DependencySummary is per summary.
	AcceptanceCriteria int `yaml:"acceptanceCriteria,omitempty" json:"acceptanceCriteria,omitempty"`
	Constraints        int `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	DependencySummary  int `yaml:"dependencySummary,omitempty" json:"dependencySummary,omitempty"`
	Decisions          int `yaml:"decisions,omitempty" json:"decisions,omitempty"`
	Risks              int `yaml:"risks,omitempty" json:"risks,omitempty"`
	// Skill is per skill body.
	Skill          int `yaml:"skill,omitempty" json:"skill,omitempty"`
	HumanResponses int `yaml:"humanResponses,omitempty" json:"humanResponses,omitempty"`
	PeerMessages   int `yaml:"peerMessages,omitempty" json:"peerMessages,omitempty"`
	// PreviousTurn is the summary of a worker's previous turn, sent when a
	// turn cannot resume that turn's session.
	PreviousTurn int `yaml:"previousTurn,omitempty" json:"previousTurn,omitempty"`
	// Feedback is the total of what a round's target was sent back to fix:
	// each item's text and each failed check's output.
	Feedback int `yaml:"feedback,omitempty" json:"feedback,omitempty"`
	// PreviousRound is the summary of a target's own last run of its step,
	// sent when a round cannot resume that run's session.
	PreviousRound int `yaml:"previousRound,omitempty" json:"previousRound,omitempty"`
	// Envelope is the whole envelope, its doc index counted as compact JSON
	// the way the index's own limit counts it.
	Envelope int `yaml:"envelope,omitempty" json:"envelope,omitempty"`
}

// MinBudget is the smallest budget a field can be given or cut down to: room
// for the note that says what was cut, and some of the text.
const MinBudget = 1 << 10

// MinFeedbackText is the least a budget cuts one feedback item's text, or
// its check's output, down to: an item is cut, never dropped, so each keeps
// room for its note and the start of what it says.
const MinFeedbackText = 256

// MaxDescriptionBytes bounds the description of a dependency output or of a
// skill sent by reference. Either describes a file the worker can open, so a
// long one is shortened with an ellipsis rather than a note.
const MaxDescriptionBytes = 512

// PartAcceptanceCriteria names the acceptance criteria in MetadataTruncated.
// The prompt sends them with the parts no section of their own claims.
const PartAcceptanceCriteria = "acceptanceCriteria"

// ErrOverBudget is returned when an envelope cannot fit its budget even with
// every part cut as far as it can be.
var ErrOverBudget = errors.New("the context envelope does not fit its budget")

// MetadataTruncated is the envelope metadata key listing, comma-separated,
// the parts a budget cut, named as usage accounting names prompt sections:
// objective, stage, acceptanceCriteria, constraints, dependencies,
// decisions, risks, humanResponses, peerMessages, previousTurn, feedback,
// previousRound, docs, and skill:<id>.
const MetadataTruncated = "truncated"

// DefaultBudgets is what an envelope gets unless inkuna.yaml says otherwise.
func DefaultBudgets() Budgets {
	return Budgets{
		Objective: 16 << 10, StagePrompt: 8 << 10, AcceptanceCriteria: 8 << 10, Constraints: 8 << 10, DependencySummary: 4 << 10, Decisions: 8 << 10, Risks: 8 << 10,
		Skill: 12 << 10, HumanResponses: 8 << 10, PeerMessages: 16 << 10, PreviousTurn: 8 << 10, Feedback: 16 << 10, PreviousRound: 8 << 10,
		Envelope: 96 << 10,
	}
}

// WithDefaults fills every zero budget with its default.
func (budgets Budgets) WithDefaults() Budgets {
	defaults := DefaultBudgets()
	for _, field := range budgets.fields() {
		if *field.value == 0 {
			*field.value = *defaults.field(field.name)
		}
	}
	return budgets
}

// Invalid names the budgets that are neither zero, for the default, nor at
// least MinBudget, in field order.
func (budgets Budgets) Invalid() []string {
	invalid := make([]string, 0)
	for _, field := range budgets.fields() {
		if *field.value != 0 && *field.value < MinBudget {
			invalid = append(invalid, field.name)
		}
	}
	return invalid
}

type budgetField struct {
	name  string
	value *int
}

// fields lists the budgets by their names in inkuna.yaml. The receiver is a
// copy, so the pointers reach only that copy.
func (budgets *Budgets) fields() []budgetField {
	return []budgetField{
		{"objective", &budgets.Objective}, {"stagePrompt", &budgets.StagePrompt}, {"acceptanceCriteria", &budgets.AcceptanceCriteria}, {"constraints", &budgets.Constraints},
		{"dependencySummary", &budgets.DependencySummary}, {"decisions", &budgets.Decisions},
		{"risks", &budgets.Risks}, {"skill", &budgets.Skill}, {"humanResponses", &budgets.HumanResponses},
		{"peerMessages", &budgets.PeerMessages}, {"previousTurn", &budgets.PreviousTurn}, {"feedback", &budgets.Feedback},
		{"previousRound", &budgets.PreviousRound}, {"envelope", &budgets.Envelope},
	}
}

func (budgets *Budgets) field(name string) *int {
	for _, field := range budgets.fields() {
		if field.name == name {
			return field.value
		}
	}
	return nil
}

// FullText says where the whole of a part that is about to be cut can be
// read, given the part's name (as in MetadataTruncated) and its text: a path
// relative to the working directory, or an artifact URI. An empty answer
// leaves the cut note without a place.
type FullText func(part, text string) string

// ApplyBudgets cuts every part of the envelope over its budget, then, while
// the envelope is over its own budget, cuts further in this order: the doc
// index (deprecated and the oldest docs first), skills (the largest down to a
// common size), the previous turn's summary, the previous round's, peer
// messages, human responses, risks, decisions, dependency summaries (as
// skills), acceptance criteria, constraints, feedback (as skills), the stage
// prompt, and the objective, none below MinBudget, and no feedback text below
// MinFeedbackText. What cannot be cut (allowed paths, commands, output paths,
// skill paths, feedback ids, places and commands, the branch's changed paths,
// a previous round's reply and earlier lines) is sent whole, and descriptions
// and the stage title are held to MaxDescriptionBytes, as is each path the
// previous turn or round changed (at most MaxDigestChanges of them); an
// envelope that still does not fit is
// refused with ErrOverBudget rather than sent over its budget. Each part is
// cut once, at a line boundary where that keeps most of the room, and ends
// with a note saying how many bytes were left out and where the full text is.
// Human responses and peer messages keep their newest entries, one cut short
// ending with its note and those left out stood for by one entry; feedback
// keeps every item, none below MinFeedbackText, so many items can take it
// past its own budget, though never past the envelope's; a check's output
// keeps its end, after the note; the others keep their first. A part within its budget is left as it was, so applying
// the same budgets again changes nothing; a note counts from the text it was
// given, so callers apply budgets to the full envelope each time rather than
// to one already cut. The caller's slices and maps are never modified.
func ApplyBudgets(envelope ContextEnvelope, budgets Budgets, fullText FullText) (ContextEnvelope, error) {
	budgets = budgets.WithDefaults()
	plan := planBudgets(envelope, budgets)
	if need := plan.total(); need > budgets.Envelope {
		return envelope, fmt.Errorf("%w: it needs %d bytes even with every part cut as far as it can be, and budgets.envelope is %d; raise budgets.envelope in inkuna.yaml, or give the step fewer skills, dependencies, or acceptance criteria", ErrOverBudget, need, budgets.Envelope)
	}
	cutter := cutter{fullText: fullText}
	envelope.Objective = cutter.text(SectionObjective, envelope.Objective, plan.objective)
	envelope.StageTitle = shorten(envelope.StageTitle)
	envelope.StagePrompt = cutter.text(SectionStage, envelope.StagePrompt, plan.stagePrompt)
	envelope.AcceptanceCriteria = cutter.list(PartAcceptanceCriteria, envelope.AcceptanceCriteria, plan.criteria)
	envelope.Constraints = cutter.list(SectionConstraints, envelope.Constraints, plan.constraints)
	envelope.DependencyOutputs = shortenDescriptions(envelope.DependencyOutputs,
		func(output ArtifactOutput) string { return output.Description },
		func(output ArtifactOutput, description string) ArtifactOutput {
			output.Description = description
			return output
		})
	envelope.SkillIndex = shortenDescriptions(envelope.SkillIndex,
		func(brief SkillBrief) string { return brief.Description },
		func(brief SkillBrief, description string) SkillBrief { brief.Description = description; return brief })
	if len(envelope.DependencySummary) > 0 {
		summaries := make([]string, len(envelope.DependencySummary))
		for index, summary := range envelope.DependencySummary {
			summaries[index] = cutter.text(SectionDependencies, summary, plan.summaries[index])
		}
		envelope.DependencySummary = summaries
	}
	envelope.RelevantDecisions = cutter.list(SectionDecisions, envelope.RelevantDecisions, plan.decisions)
	envelope.RelevantRisks = cutter.list(SectionRisks, envelope.RelevantRisks, plan.risks)
	envelope.HumanResponses = cutter.humanResponses(envelope.HumanResponses, plan.humanResponses)
	envelope.PeerMessages = cutter.peerMessages(envelope.PeerMessages, plan.peerMessages)
	if envelope.PreviousTurn != nil {
		digest := *envelope.PreviousTurn
		digest.Summary = cutter.text(SectionPreviousTurn, digest.Summary, plan.previousTurn)
		envelope.PreviousTurn = &digest
	}
	if envelope.PreviousRound != nil {
		digest := *envelope.PreviousRound
		digest.Summary = cutter.text(SectionPreviousRound, digest.Summary, plan.previousRound)
		envelope.PreviousRound = &digest
	}
	if envelope.Feedback != nil {
		feedback := *envelope.Feedback
		feedback.Items = make([]FeedbackItem, len(envelope.Feedback.Items))
		piece := 0
		for index, item := range envelope.Feedback.Items {
			item.Text = cutter.text(SectionFeedback, item.Text, plan.feedback[piece])
			piece++
			if item.Comment != nil {
				comment := *item.Comment
				comment.Excerpt = cutter.text(SectionFeedback, comment.Excerpt, plan.feedback[piece])
				comment.Thread = cutter.text(SectionFeedback, comment.Thread, plan.feedback[piece+1])
				piece += 2
				item.Comment = &comment
			}
			if item.Check != nil {
				check := *item.Check
				check.OutputTail = cutter.tail(SectionFeedback, check.OutputTail, plan.feedback[piece])
				piece++
				item.Check = &check
			}
			feedback.Items[index] = item
		}
		envelope.Feedback = &feedback
	}
	if len(envelope.SkillExcerpts) > 0 {
		ids := make([]string, 0, len(envelope.SkillExcerpts))
		for id := range envelope.SkillExcerpts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		skills := make(map[string]string, len(ids))
		for _, id := range ids {
			skills[id] = cutter.text(SectionSkill+":"+id, envelope.SkillExcerpts[id], plan.skills[id])
		}
		envelope.SkillExcerpts = skills
	}
	docsTruncated := envelope.Metadata[MetadataDocsTruncated] == "true"
	if docs, cut := boundDocsTo(envelope.Docs, MaxDocBriefs, plan.docs); cut {
		envelope.Docs, docsTruncated = docs, true
	}
	if docsTruncated {
		cutter.cut = append(cutter.cut, SectionDocs)
	}
	if len(cutter.cut) == 0 {
		return envelope, nil
	}
	metadata := make(map[string]string, len(envelope.Metadata)+2)
	for key, value := range envelope.Metadata {
		metadata[key] = value
	}
	if docsTruncated {
		metadata[MetadataDocsTruncated] = "true"
	}
	parts := cutter.cut
	if previous := metadata[MetadataTruncated]; previous != "" {
		parts = append(strings.Split(previous, ","), parts...)
	}
	metadata[MetadataTruncated] = strings.Join(unique(parts), ",")
	envelope.Metadata = metadata
	return envelope, nil
}

// shorten holds text to MaxDescriptionBytes, ending it with an ellipsis when
// it is cut.
func shorten(text string) string {
	if len(text) <= MaxDescriptionBytes {
		return text
	}
	return prefix(text, MaxDescriptionBytes-len("…")) + "…"
}

// shortenDescriptions holds each item's description to MaxDescriptionBytes,
// returning the items themselves when none is longer.
func shortenDescriptions[T any](items []T, description func(T) string, with func(T, string) T) []T {
	var shortened []T
	for index, item := range items {
		if text := description(item); len(text) > MaxDescriptionBytes {
			if shortened == nil {
				shortened = append([]T(nil), items...)
			}
			shortened[index] = with(item, shorten(text))
		}
	}
	if shortened == nil {
		return items
	}
	return shortened
}

// EnvelopeBytes is what an envelope weighs against its budget: the bytes of
// its objective, stage title and prompt, acceptance criteria, constraints,
// allowed paths, commands, dependency
// summaries and outputs, decisions, risks, human responses, peer messages,
// the previous turn and round, feedback, skills and skill index, and the
// compact JSON of its doc index, with descriptions counted as ApplyBudgets
// sends them.
func EnvelopeBytes(envelope ContextEnvelope) int {
	plan := planBudgets(envelope, Budgets{Envelope: 1 << 62}.unlimited())
	return plan.total()
}

// unlimited sets every unset budget so high that nothing is cut.
func (budgets Budgets) unlimited() Budgets {
	for _, field := range budgets.fields() {
		if *field.value == 0 {
			*field.value = 1 << 62
		}
	}
	return budgets
}

// budgetPlan is the size each part will have once cut: its budget, or its
// size when that is smaller.
type budgetPlan struct {
	fixed, objective, stagePrompt, criteria, constraints, decisions, risks, humanResponses, peerMessages, previousTurn, previousRound, docs int
	// summaries are per dependency summary, and feedback per text that
	// feedbackTexts lists.
	summaries, feedback []int
	skills              map[string]int
}

func (plan budgetPlan) total() int {
	total := plan.fixed + plan.objective + plan.stagePrompt + plan.criteria + plan.constraints + plan.decisions + plan.risks + plan.humanResponses + plan.peerMessages + plan.previousTurn + plan.previousRound + plan.docs
	for _, size := range plan.summaries {
		total += size
	}
	for _, size := range plan.feedback {
		total += size
	}
	for _, size := range plan.skills {
		total += size
	}
	return total
}

func planBudgets(envelope ContextEnvelope, budgets Budgets) budgetPlan {
	plan := budgetPlan{
		objective:      min(len(envelope.Objective), budgets.Objective),
		stagePrompt:    min(len(envelope.StagePrompt), budgets.StagePrompt),
		criteria:       min(listBytes(envelope.AcceptanceCriteria), budgets.AcceptanceCriteria),
		constraints:    min(listBytes(envelope.Constraints), budgets.Constraints),
		decisions:      min(listBytes(envelope.RelevantDecisions), budgets.Decisions),
		risks:          min(listBytes(envelope.RelevantRisks), budgets.Risks),
		humanResponses: min(humanResponseBytes(envelope.HumanResponses), budgets.HumanResponses),
		peerMessages:   min(peerMessageBytes(envelope.PeerMessages), budgets.PeerMessages),
		docs:           docsBytes(envelope.Docs),
		skills:         make(map[string]int, len(envelope.SkillExcerpts)),
	}
	// What no budget cuts: the title, paths, and commands are sent whole,
	// and descriptions and the stage title are held to MaxDescriptionBytes.
	plan.fixed = len(envelope.Title) + min(len(envelope.StageTitle), MaxDescriptionBytes) + listBytes(envelope.AllowedPaths)
	for _, command := range envelope.Commands {
		plan.fixed += listBytes(command)
	}
	for _, output := range envelope.DependencyOutputs {
		plan.fixed += len(output.Path) + len(output.Name) + min(len(output.Description), MaxDescriptionBytes)
	}
	for _, brief := range envelope.SkillIndex {
		plan.fixed += len(brief.ID) + min(len(brief.Description), MaxDescriptionBytes) + len(brief.Path)
	}
	for _, summary := range envelope.DependencySummary {
		plan.summaries = append(plan.summaries, min(len(summary), budgets.DependencySummary))
	}
	for id, body := range envelope.SkillExcerpts {
		plan.skills[id] = min(len(body), budgets.Skill)
	}
	if digest := envelope.PreviousTurn; digest != nil {
		plan.previousTurn = min(len(digest.Summary), budgets.PreviousTurn)
		plan.fixed += len(digest.StoppedFor) + listBytes(digest.Changes)
	}
	if digest := envelope.PreviousRound; digest != nil {
		plan.previousRound = min(len(digest.Summary), budgets.PreviousRound)
		plan.fixed += listBytes(digest.Changes) + listBytes(digest.Replies) + listBytes(digest.Earlier)
	}
	if feedback := envelope.Feedback; feedback != nil {
		plan.fixed += feedbackFixedBytes(*feedback)
		texts := feedbackTexts(*feedback)
		plan.feedback = make([]int, len(texts))
		for index, text := range texts {
			plan.feedback[index] = len(text)
		}
		// An item is cut to its share of the budget, never dropped.
		levelDown(plan.feedback, listBytes(texts)-budgets.Feedback, MinFeedbackText)
	}
	// Whatever the envelope weighs over its budget comes off the parts in
	// order, each down to MinBudget at least (the doc index down to none).
	shrink := func(size *int, floor int) {
		if over := plan.total() - budgets.Envelope; over > 0 && *size > floor {
			*size = max(floor, *size-over)
		}
	}
	over := func() int { return plan.total() - budgets.Envelope }
	shrink(&plan.docs, 0)
	ids := make([]string, 0, len(plan.skills))
	for id := range plan.skills {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	skills := make([]int, len(ids))
	for index, id := range ids {
		skills[index] = plan.skills[id]
	}
	levelDown(skills, over(), MinBudget)
	for index, id := range ids {
		plan.skills[id] = skills[index]
	}
	shrink(&plan.previousTurn, MinBudget)
	shrink(&plan.previousRound, MinBudget)
	shrink(&plan.peerMessages, MinBudget)
	shrink(&plan.humanResponses, MinBudget)
	shrink(&plan.risks, MinBudget)
	shrink(&plan.decisions, MinBudget)
	levelDown(plan.summaries, over(), MinBudget)
	shrink(&plan.criteria, MinBudget)
	shrink(&plan.constraints, MinBudget)
	levelDown(plan.feedback, over(), MinFeedbackText)
	shrink(&plan.stagePrompt, MinBudget)
	shrink(&plan.objective, MinBudget)
	return plan
}

// feedbackTexts lists the texts of a round's feedback that budgets cut:
// each item's text, then its comment's excerpt and thread, or its check's
// output, in the items' order.
func feedbackTexts(feedback RoundFeedback) []string {
	texts := make([]string, 0, len(feedback.Items))
	for _, item := range feedback.Items {
		texts = append(texts, item.Text)
		if item.Comment != nil {
			texts = append(texts, item.Comment.Excerpt, item.Comment.Thread)
		}
		if item.Check != nil {
			texts = append(texts, item.Check.OutputTail)
		}
	}
	return texts
}

// feedbackFixedBytes is what of a round's feedback no budget cuts: each
// item's id and author, a comment's place, a check's command, and the
// branch's range and changed paths.
func feedbackFixedBytes(feedback RoundFeedback) int {
	total := 0
	for _, item := range feedback.Items {
		total += len(item.ID) + len(item.From)
		if comment := item.Comment; comment != nil {
			total += len(comment.Path) + len(comment.Side)
		}
		if check := item.Check; check != nil {
			total += listBytes(check.Command)
		}
	}
	if changes := feedback.Changes; changes != nil {
		total += len(changes.Base) + len(changes.Tip)
		for _, file := range changes.Files {
			total += len(file.Path)
		}
	}
	return total
}

// levelDown sheds bytes from the largest sizes first by bringing them down
// to one common level, none below floor, so that parts of a size are cut
// alike: shed bytes, or as many as the floor allows.
func levelDown(sizes []int, shed, floor int) {
	shedAt := func(level int) int {
		total := 0
		for _, size := range sizes {
			total += max(0, size-level)
		}
		return total
	}
	top := 0
	for _, size := range sizes {
		top = max(top, size)
	}
	if shed <= 0 || top <= floor {
		return
	}
	level := floor
	if shedAt(floor) > shed {
		// The highest level that still sheds enough: shedAt(low) >= shed
		// and shedAt(high) < shed throughout.
		low, high := floor, top
		for high-low > 1 {
			if middle := (low + high) / 2; shedAt(middle) >= shed {
				low = middle
			} else {
				high = middle
			}
		}
		level = low
	}
	for index, size := range sizes {
		sizes[index] = min(size, level)
	}
}

// cutter cuts parts to their planned size and remembers which it cut.
type cutter struct {
	fullText FullText
	cut      []string
}

// note is the line that ends a cut part.
func (cutter *cutter) note(part, text string) func(omitted int) string {
	location := ""
	if cutter.fullText != nil {
		location = cutter.fullText(part, text)
	}
	cutter.cut = append(cutter.cut, part)
	return func(omitted int) string {
		note := fmt.Sprintf("[truncated: %d bytes omitted", omitted)
		if location != "" {
			note += "; full text in " + location
		}
		return note + "]"
	}
}

func (cutter *cutter) text(part, text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	note := cutter.note(part, text)
	// A note for the whole text is at least as long as the one written.
	kept := prefix(text, budget-len(note(len(text)))-1)
	if kept == "" {
		return note(len(text))
	}
	return kept + "\n" + note(len(text)-len(kept))
}

// tail keeps the end of text, after the note, as a log says most in its
// last lines.
func (cutter *cutter) tail(part, text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	note := cutter.note(part, text)
	kept := suffix(text, budget-len(note(len(text)))-1)
	if kept == "" {
		return note(len(text))
	}
	return note(len(text)-len(kept)) + "\n" + kept
}

// list keeps entries from the first while they fit, the last of them cut
// when that keeps at least a quarter of the room (a sliver of an entry says
// less than its absence), and ends with the note as an entry of its own.
func (cutter *cutter) list(part string, entries []string, budget int) []string {
	total := listBytes(entries)
	if total <= budget {
		return entries
	}
	note := cutter.note(part, "- "+strings.Join(entries, "\n- "))
	room := budget - len(note(total))
	kept := make([]string, 0, len(entries))
	used := 0
	for _, entry := range entries {
		if used+len(entry) > room {
			if rest := prefix(entry, room-used); len(rest) >= room/4 && rest != "" {
				kept = append(kept, rest)
				used += len(rest)
			}
			break
		}
		kept = append(kept, entry)
		used += len(entry)
	}
	return append(kept, note(total-used))
}

// humanResponses keeps the newest answers. An answer cut short ends with
// the note; answers left out entirely are stood for by one entry before the
// rest, with a note of its own.
func (cutter *cutter) humanResponses(responses []HumanResponse, budget int) []HumanResponse {
	total := humanResponseBytes(responses)
	if total <= budget {
		return responses
	}
	lines := make([]string, 0, len(responses))
	for _, response := range responses {
		lines = append(lines, "Q: "+response.Question+"\nA: "+response.Answer)
	}
	const earlier = "Earlier answers, left out"
	note := cutter.note(SectionHumanResponses, strings.Join(lines, "\n\n"))
	// Room for both notes, each no longer than one for the whole part.
	room := budget - len(earlier) - 2*len(note(total)) - 1
	kept, shortened, cut, dropped := keepNewest(responses, room,
		func(response HumanResponse) int { return len(response.Question) + len(response.Answer) },
		func(response HumanResponse, room int) HumanResponse {
			// The question gives way too, so the answer always has room.
			response.Question = prefix(response.Question, max(room/2, room-len(response.Answer)))
			response.Answer = prefix(response.Answer, room-len(response.Question))
			return response
		})
	if shortened {
		kept[0].Answer = strings.TrimLeft(kept[0].Answer+"\n"+note(cut), "\n")
	}
	if dropped > 0 {
		kept = append([]HumanResponse{{Question: earlier, Answer: note(dropped)}}, kept...)
	}
	return kept
}

// peerMessages keeps the newest messages, as humanResponses keeps answers.
func (cutter *cutter) peerMessages(messages []PeerMessage, budget int) []PeerMessage {
	total := peerMessageBytes(messages)
	if total <= budget {
		return messages
	}
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		lines = append(lines, "From "+message.From+" ("+message.Subject+"):\n"+message.Content)
	}
	const from, earlier = "inkuna", "Earlier messages, left out"
	note := cutter.note(SectionPeerMessages, strings.Join(lines, "\n\n"))
	room := budget - len(from) - len(earlier) - 2*len(note(total)) - 1
	kept, shortened, cut, dropped := keepNewest(messages, room,
		func(message PeerMessage) int { return len(message.From) + len(message.Subject) + len(message.Content) },
		func(message PeerMessage, room int) PeerMessage {
			message.Subject = prefix(message.Subject, room/4)
			message.Content = prefix(message.Content, room-len(message.From)-len(message.Subject))
			return message
		})
	if shortened {
		kept[0].Content = strings.TrimLeft(kept[0].Content+"\n"+note(cut), "\n")
	}
	if dropped > 0 {
		kept = append([]PeerMessage{{From: from, Subject: earlier, Content: note(dropped)}}, kept...)
	}
	return kept
}

// keepNewest keeps whole items from the newest back while they fit in room,
// then the next older one shortened, when that keeps at least a quarter of
// the room; anything older is left out. It returns the kept items in their
// order (a shortened one first), whether one was shortened, the bytes cut
// from it, and the bytes of the items left out.
func keepNewest[T any](items []T, room int, size func(T) int, shorten func(T, int) T) (kept []T, shortened bool, cut, dropped int) {
	kept = make([]T, 0, len(items))
	used := 0
	index := len(items) - 1
	for ; index >= 0; index-- {
		if used+size(items[index]) > room {
			break
		}
		kept = append(kept, items[index])
		used += size(items[index])
	}
	if index >= 0 {
		if rest := room - used; rest >= room/4 && rest > 0 {
			if short := shorten(items[index], rest); size(short) > 0 && size(short) <= rest {
				kept = append(kept, short)
				shortened, cut = true, size(items[index])-size(short)
				index--
			}
		}
		for ; index >= 0; index-- {
			dropped += size(items[index])
		}
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept, shortened, cut, dropped
}

// prefix is at most n bytes from the start of text, ending at a line
// boundary when that keeps at least half of n, otherwise at a character
// boundary, without trailing space.
func prefix(text string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(text) <= n {
		return text
	}
	end := n
	if line := strings.LastIndexByte(text[:n], '\n'); line >= n/2 {
		end = line
	}
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return strings.TrimRight(text[:end], " \t\r\n")
}

// suffix is at most n bytes from the end of text, starting at a line start
// when that keeps at least half of n, otherwise at a character boundary,
// without leading line breaks. Indentation is kept: in a log it can matter.
func suffix(text string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(text) <= n {
		return text
	}
	start := len(text) - n
	if line := strings.IndexByte(text[start:], '\n'); line >= 0 && line <= n/2 {
		start += line + 1
	}
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return strings.TrimLeft(text[start:], "\r\n")
}

func listBytes(entries []string) int {
	total := 0
	for _, entry := range entries {
		total += len(entry)
	}
	return total
}

func humanResponseBytes(responses []HumanResponse) int {
	total := 0
	for _, response := range responses {
		total += len(response.Question) + len(response.Answer)
	}
	return total
}

func peerMessageBytes(messages []PeerMessage) int {
	total := 0
	for _, message := range messages {
		total += len(message.From) + len(message.Subject) + len(message.Content)
	}
	return total
}

// docsBytes is the compact JSON of the index, as boundDocs counts it.
func docsBytes(docs []DocBrief) int {
	if len(docs) == 0 {
		return 0
	}
	// A DocBrief holds only strings, so encoding it cannot fail.
	data, _ := json.Marshal(docs)
	return len(data)
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			kept = append(kept, value)
		}
	}
	return kept
}
