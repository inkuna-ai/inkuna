package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// numberedLines is "line 1" to "line n", one a line, with changes put in
// place of some of them.
func numberedLines(n int, changes map[int]string) string {
	var text strings.Builder
	for number := 1; number <= n; number++ {
		if change, ok := changes[number]; ok {
			text.WriteString(change + "\n")
			continue
		}
		fmt.Fprintf(&text, "line %d\n", number)
	}
	return text.String()
}

// commentedRun runs an implementation under targetRules whose implement
// step writes name.go: ten lines in round 1, and in round 2 a line on top
// and line 3 changed. It waits for round 1 to complete.
func commentedRun(t *testing.T, reply func(request agentruntime.RunRequest) agentruntime.RunResult) (*Service, *workflowRuntime, WorkflowRunView) {
	t.Helper()
	repository := branchingRepository(t, roundsBranching)
	if err := os.WriteFile(filepath.Join(repository, ".inkuna", "rules.yaml"), []byte(targetRules), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "Target rules"}} {
		if err := runGit(repository, args...); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &workflowRuntime{handle: func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		content := ""
		switch {
		case strings.HasSuffix(request.RunID, "-implement"):
			content = numberedLines(10, nil)
		case strings.HasSuffix(request.RunID, ".r2.implement"):
			content = "top\n" + numberedLines(10, map[int]string{3: "line three"})
		}
		if content != "" {
			if err := os.WriteFile(filepath.Join(request.WorkingDir, "name.go"), []byte(content), 0o644); err != nil {
				return agentruntime.RunResult{}, err
			}
		}
		return reply(request), nil
	}}
	service := workflowTestService(t, repository, runtime)
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Validate names", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	return service, runtime, waitForWorkflowRun(t, service, run.ID, "completed")
}

func TestLineCommentsGoBackAsFeedbackAndTheirRepliesComeBackToThem(t *testing.T) {
	service, runtime, run := commentedRun(t, func(request agentruntime.RunRequest) agentruntime.RunResult {
		switch {
		case strings.HasSuffix(request.RunID, ".r2.implement"):
			return agentruntime.RunResult{Output: handoffOutput(t, domain.HandoffResult{Summary: "Handled empty names", Changes: []domain.Change{{Path: "name.go"}}, Addressed: []domain.Addressed{
				{Item: "c1", Status: "fixed", Note: "Line 3 now handles it."}, {Item: "c2", Status: "declined", Note: "Callers rely on them."},
			}})}
		case strings.HasSuffix(request.RunID, ".r3.implement"):
			return agentruntime.RunResult{Output: handoffOutput(t, domain.HandoffResult{Summary: "Checked the callers", Addressed: []domain.Addressed{{Item: "c1", Status: "fixed", Note: "Removed the last caller."}}})}
		case strings.HasSuffix(request.RunID, "implement"):
			return agentruntime.RunResult{Output: handoffOutput(t, domain.HandoffResult{Summary: "Wrote name.go", Changes: []domain.Change{{Path: "name.go"}}})}
		default:
			return agentruntime.RunResult{Output: handoffOutput(t, domain.HandoffResult{Summary: "Reviewed " + request.RunID})}
		}
	})
	view, err := service.RunComments(run.ID, "")
	if err != nil || view.Refusal != "" || len(view.Targets) != 1 || view.Targets[0].ID != "implement" || len(view.To) != 40 {
		t.Fatalf("an ended run takes comments: %+v %v", view, err)
	}
	tip := view.To
	// The stage that wrote a line is the one a comment on it goes to.
	if target, err := service.CommentTarget(run.ID, "name.go", domain.SideNew, 3); err != nil || target != "implement" {
		t.Fatalf("the default target %q %v", target, err)
	}
	for name, draft := range map[string]CommentDraft{
		"a stage that does not write": {Path: "name.go", Side: domain.SideNew, Line: 3, Commit: tip, Body: "No.", Target: "review"},
		"a line the diff lacks":       {Path: "name.go", Side: domain.SideNew, Line: 30, Commit: tip, Body: "No.", Target: "implement"},
		"an older version":            {Path: "name.go", Side: domain.SideNew, Line: 3, Commit: strings.Repeat("a", 40), Body: "No.", Target: "implement"},
		"a removed line never there":  {Path: "name.go", Side: domain.SideOld, Line: 3, Commit: tip, Body: "No.", Target: "implement"},
		"nothing written":             {Path: "name.go", Side: domain.SideNew, Line: 3, Commit: tip, Body: "  ", Target: "implement"},
	} {
		if err := service.SaveComment(run.ID, draft); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	if err := service.SaveComment(run.ID, CommentDraft{Path: "name.go", Side: domain.SideNew, Line: 3, Commit: tip, Body: "Handle an empty name here.", Target: "implement"}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveComment(run.ID, CommentDraft{Path: "name.go", Side: domain.SideNew, Line: 6, EndLine: 7, Commit: tip, Body: "Keep these as they are.", Target: "implement"}); err != nil {
		t.Fatal(err)
	}
	if review, err := service.RunReview(run.ID); err != nil || review.Drafts != 2 {
		t.Fatalf("the review bar counts the drafts it sends: %+v %v", review, err)
	}

	// Request changes with no text sends the drafts.
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1}); err != nil {
		t.Fatal(err)
	}
	done := waitForWorkflowRun(t, service, run.ID, "completed")
	if done.Round != 2 {
		t.Fatalf("round %d", done.Round)
	}
	target, err := service.RunContext(run.ID, "implement")
	if err != nil || target.Envelope.Feedback == nil {
		t.Fatalf("the target's context %+v %v", target, err)
	}
	items := target.Envelope.Feedback.Items
	if len(items) != 2 || items[0].ID != "c1" || items[1].ID != "c2" || items[0].Comment == nil || items[1].Comment == nil {
		t.Fatalf("round 2's items %+v", items)
	}
	first, second := *items[0].Comment, *items[1].Comment
	if first.Path != "name.go" || first.Line != 3 || first.EndLine != 3 || first.Side != domain.SideNew || !strings.Contains(first.Excerpt, "> ") || !strings.Contains(first.Excerpt, "line 3") {
		t.Fatalf("c1 %+v", first)
	}
	if second.Line != 6 || second.EndLine != 7 || !strings.Contains(second.Excerpt, "line 6") || !strings.Contains(second.Excerpt, "line 9") || strings.Contains(second.Excerpt, "line 1\n") {
		t.Fatalf("c2 %+v", second)
	}
	prompt := string(requestOf(t, runtime, run.ID+".r2.implement").Prompt)
	for _, part := range []string{"- [c1] On `name.go` line 3 (the branch's line): Handle an empty name here.\n", "- [c2] On `name.go` lines 6–7 (the branch's lines): Keep these as they are.\n", "The diff there, > marking the commented lines:\n"} {
		if !strings.Contains(prompt, part) {
			t.Errorf("the target's prompt lacks %q:\n%s", part, prompt)
		}
	}

	if review := string(requestOf(t, runtime, run.ID+".r2.review").Prompt); strings.Contains(review, "Handle an empty name here.") || strings.Contains(review, "# Feedback") {
		t.Fatalf("a stage downstream of the target saw the comments:\n%s", review)
	}

	// c1's fix resolves its thread, and its line changed, so it is outdated
	// after round 2; c2 was declined and stays open, moved down one line by
	// the line added on top.
	view, err = service.RunComments(run.ID, "")
	if err != nil || len(view.Threads) != 2 {
		t.Fatalf("threads after round 2 %+v %v", view, err)
	}
	fixed, declined := view.Threads[0], view.Threads[1]
	if fixed.State != storage.ThreadResolved || fixed.ResolvedBy != storage.ResolvedByAgent || !fixed.Outdated || fixed.OutdatedRound != 2 ||
		fixed.Comments[0].Status != domain.AddressedFixed || fixed.Comments[0].Note != "Line 3 now handles it." || fixed.Comments[0].Item != "c1" || fixed.Comments[0].Round != 2 {
		t.Fatalf("the fixed thread %+v", fixed)
	}
	if declined.State != storage.ThreadOpen || declined.Outdated || declined.Line != 7 || declined.EndLine != 8 ||
		declined.Comments[0].Status != domain.AddressedDeclined || declined.Comments[0].Note != "Callers rely on them." {
		t.Fatalf("the declined thread %+v", declined)
	}
	stored, err := service.store.CommentThreads(context.Background(), run.ID)
	if err != nil || stored[1].Commit != view.To || stored[1].Line != 7 || stored[0].Commit != tip {
		t.Fatalf("the threads were not followed to the round's commit: %+v %v", stored, err)
	}

	// A reply goes back with the thread so far; a thread takes one draft at
	// a time, and an outdated one none.
	reply := CommentDraft{ThreadID: declined.ID, Body: "Nothing calls them any more.", Target: "implement"}
	if err := service.SaveComment(run.ID, reply); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveComment(run.ID, reply); !errors.Is(err, storage.ErrDraftExists) {
		t.Fatalf("a second draft in a thread: %v", err)
	}
	if err := service.SaveComment(run.ID, CommentDraft{ThreadID: fixed.ID, Body: "Again.", Target: "implement"}); err == nil {
		t.Fatal("a resolved thread took a reply")
	}
	if err := service.ResolveThread(run.ID, fixed.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveComment(run.ID, CommentDraft{ThreadID: fixed.ID, Body: "Again.", Target: "implement"}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("an outdated thread took a reply: %v", err)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 2, Items: []FeedbackRequestItem{{Target: "implement", Text: "Check the callers."}}}); err != nil {
		t.Fatal(err)
	}
	done = waitForWorkflowRun(t, service, run.ID, "completed")
	if done.Round != 3 {
		t.Fatalf("round %d", done.Round)
	}
	prompt = string(requestOf(t, runtime, run.ID+".r3.implement").Prompt)
	for _, part := range []string{"- [f1] Check the callers.\n", "- [c1] On `name.go` lines 7–8 (the branch's lines): Nothing calls them any more.\n",
		"Earlier on these lines:\n  c2 declined: \"Keep these as they are.\" → \"Callers rely on them.\" (round 2)\n"} {
		if !strings.Contains(prompt, part) {
			t.Errorf("round 3's prompt lacks %q:\n%s", part, prompt)
		}
	}
	view, err = service.RunComments(run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if thread := view.Threads[1]; thread.State != storage.ThreadResolved || len(thread.Comments) != 2 || thread.Comments[1].Item != "c1" || thread.Comments[1].Round != 3 || thread.Comments[1].Status != domain.AddressedFixed {
		t.Fatalf("the replied thread after round 3 %+v", thread)
	}
}

func TestDraftCommentsAreKeptUntilSentAndCanBeEditedOrDeleted(t *testing.T) {
	service, _, run := commentedRun(t, func(request agentruntime.RunRequest) agentruntime.RunResult {
		return agentruntime.RunResult{Output: handoffOutput(t, domain.HandoffResult{Summary: "Done " + request.RunID})}
	})
	view, err := service.RunComments(run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveComment(run.ID, CommentDraft{Path: "name.go", Side: domain.SideNew, Line: 2, Commit: view.To, Body: "First.", Target: "implement"}); err != nil {
		t.Fatal(err)
	}
	view, err = service.RunComments(run.ID, "")
	if err != nil || len(view.Threads) != 1 || view.Threads[0].Comments[0].State != storage.CommentDraft {
		t.Fatalf("the draft %+v %v", view, err)
	}
	draft := view.Threads[0].Comments[0]
	if err := service.SaveComment(run.ID, CommentDraft{ID: draft.ID, Body: "Second.", Target: "implement"}); err != nil {
		t.Fatal(err)
	}
	view, _ = service.RunComments(run.ID, "")
	if view.Threads[0].Comments[0].Body != "Second." {
		t.Fatalf("the edit %+v", view.Threads[0])
	}
	if err := service.DeleteComment(run.ID, draft.ID); err != nil {
		t.Fatal(err)
	}
	if view, _ = service.RunComments(run.ID, ""); len(view.Threads) != 0 {
		t.Fatalf("deleting a thread's only draft leaves %+v", view.Threads)
	}
	if err := service.DeleteComment(run.ID, draft.ID); err == nil {
		t.Fatal("a deleted draft was deleted again")
	}
	// With nothing written and no drafts there is nothing to send.
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1}); err == nil || !strings.Contains(err.Error(), "say what to change") {
		t.Fatalf("an empty request: %v", err)
	}
}
