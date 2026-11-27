package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// plannedRules has a read-only stage plan the approach, then a writable
// stage build it, then a review gate.
const plannedRules = `version: 1
rules:
  - name: planned
    when:
      kind: [implementation]
    then:
      stages:
        - id: plan
          title: Plan the approach
          kind: agent
          agent: auto
          permission: read-only
        - id: implement
          title: Implement
          kind: agent
          agent: auto
          permission: worktree-write
          dependsOn: [plan]
        - id: approval
          kind: approval
          review: true
          dependsOn: [implement]
`

const planDocument = "# Plan\n\n1. Change the readme.\n2. Say why.\n"

// planSeen is what the implement stage found when it ran.
type planSeen struct {
	mu        sync.Mutex
	documents []string
	prompts   []string
}

// plannedRun starts a task under plannedRules with a runtime whose plan
// stage returns planDocument and whose implement stage records the plan
// file it finds, writes a change, and leaves a scratch file in .inkuna/out.
// It waits for the run to pause at its gate.
func plannedRun(t *testing.T) (*Service, *workflowRuntime, WorkflowRunView, *planSeen) {
	t.Helper()
	repository := branchingRepository(t, roundsBranching)
	if err := os.WriteFile(filepath.Join(repository, ".inkuna", "rules.yaml"), []byte(plannedRules), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repository, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repository, "commit", "-q", "-m", "Planned rules"); err != nil {
		t.Fatal(err)
	}
	seen := &planSeen{}
	runtime := &workflowRuntime{handle: func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if strings.HasSuffix(request.RunID, "plan") {
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Planned the change", Document: planDocument})}, nil
		}
		data, err := os.ReadFile(filepath.Join(request.WorkingDir, ".inkuna", "out", "plan.md"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return agentruntime.RunResult{}, err
		}
		seen.mu.Lock()
		seen.documents = append(seen.documents, string(data))
		seen.prompts = append(seen.prompts, string(request.Prompt))
		seen.mu.Unlock()
		if err := os.WriteFile(filepath.Join(request.WorkingDir, "CHANGE-"+filepath.Base(request.RunID)+".md"), []byte("change"), 0o644); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDir, ".inkuna", "out", "scratch.txt"), []byte("notes the implementer kept"), 0o644); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Changed the readme"})}, nil
	}}
	service := workflowTestService(t, repository, runtime)
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Tidy the readme", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, storage.Reviewing)
	if err := service.settle(run.ID); err != nil {
		t.Fatal(err)
	}
	return service, runtime, run, seen
}

func TestAPlanStagesDocumentIsAMarkdownArtifactItsMessageLinksAndTheNextStepReads(t *testing.T) {
	service, _, run, seen := plannedRun(t)
	ctx := context.Background()
	repository := projectRepository(t, service)

	records, err := service.RunArtifacts(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var document ArtifactView
	for _, record := range records {
		if record.Kind == DocumentArtifact {
			document = record
		}
	}
	if document.Name != "plan.md" || document.MediaType != "text/markdown" || document.StepID != "plan" || document.Size != int64(len(planDocument)) {
		t.Fatalf("the plan is stored as a Markdown document of its step: %+v", records)
	}
	opened, err := service.ReadArtifact(document.Digest)
	if err != nil || opened.Content != planDocument || opened.Truncated {
		t.Fatalf("the document opens whole: %+v %v", opened, err)
	}

	// The plan stage's message carries the document as a link: a file part
	// with what the link shows before it is opened.
	tasks, err := service.RunMessages(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var linked *a2a.Part
	for _, view := range tasks {
		if view.Task.Metadata["stepId"] != "plan" {
			continue
		}
		for _, artifact := range view.Task.Artifacts {
			for index, part := range artifact.Parts {
				if part.Kind == a2a.PartFile && part.File.URI == "inkuna://artifact/"+document.Digest {
					linked = &artifact.Parts[index]
				}
			}
		}
	}
	if linked == nil {
		t.Fatalf("the plan's message does not carry its document: %+v", tasks)
	}
	if linked.File.Name != "plan.md" || linked.File.MimeType != "text/markdown" || linked.Metadata["kind"] != DocumentArtifact || linked.Metadata["round"] != 1 {
		t.Fatalf("the link says what it opens: %+v %+v", linked.File, linked.Metadata)
	}
	if size, _ := linked.Metadata["size"].(int64); size != int64(len(planDocument)) {
		t.Fatalf("the link gives the document's size: %#v", linked.Metadata["size"])
	}

	// The stored handoff keeps the reference, never the text.
	results, err := service.store.StepResults(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan := results["plan"]; plan.Document != "" || len(plan.Outputs) != 1 || plan.Outputs[0].Path != ".inkuna/out/plan.md" || plan.Outputs[0].Digest != document.Digest {
		t.Fatalf("the plan's result lists its document as an output: %+v", plan)
	}

	// The read-only plan ran in the checkout, before the worktree existed;
	// the implementer found the document in the worktree all the same.
	seen.mu.Lock()
	documents, prompts := seen.documents, seen.prompts
	seen.mu.Unlock()
	if len(documents) != 1 || documents[0] != planDocument {
		t.Fatalf("the implementer read %q", documents)
	}
	if !strings.Contains(prompts[0], "\n# Dependency outputs\n- `.inkuna/out/plan.md`: The document plan wrote\n") {
		t.Fatalf("the implementer is told where the plan is:\n%s", prompts[0])
	}
	if data, err := os.ReadFile(filepath.Join(repository, ".inkuna", "out", "plan.md")); err != nil || string(data) != planDocument {
		t.Fatalf("the plan was written where it ran: %q %v", data, err)
	}
	warned := false
	for _, event := range run.Events {
		if event.Kind == domain.EventWarning && strings.Contains(event.Summary, ".inkuna/out is not ignored") && strings.Contains(event.Summary, "inkunactl init") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("the run warns that .inkuna/out is not ignored: %+v", run.Events)
	}

	// The commit leaves .inkuna/out out, ignored or not.
	commits, err := service.store.Commits(ctx, run.ID)
	if err != nil || len(commits) != 1 {
		t.Fatalf("one commit: %+v %v", commits, err)
	}
	if strings.Contains(commits[0].Stat, ".inkuna/out") || !strings.Contains(commits[0].Stat, "CHANGE-") {
		t.Fatalf("the commit took .inkuna/out: %s", commits[0].Stat)
	}

	// The gate links the plan before Approve; the implementer left nothing
	// to read, so it is not listed.
	steps, err := service.GateArtifacts(run.ID, "approval")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].StepID != "plan" || steps[0].Round != 1 || len(steps[0].Artifacts) != 1 || steps[0].Artifacts[0].Digest != document.Digest || steps[0].Artifacts[0].Kind != DocumentArtifact {
		t.Fatalf("the gate links the plan: %+v", steps)
	}
	if _, err := service.GateArtifacts(run.ID, "nope"); err == nil {
		t.Fatal("a gate the run does not have is refused")
	}
}

func TestACompletedRunArchivesWhatIsLeftInInkunaOutAndALaterRoundPutsThePlanBack(t *testing.T) {
	service, _, run, seen := plannedRun(t)
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForWorkflowRun(t, service, run.ID, "completed")
	for _, directory := range []string{projectRepository(t, service), done.Worktree} {
		if _, err := os.Stat(filepath.Join(directory, ".inkuna", "out")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s/.inkuna/out is emptied: %v", directory, err)
		}
	}
	// Only the scratch file was not already an artifact, so only it is in
	// the worktree's archive; the checkout held just the plan.
	records, err := service.RunArtifacts(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var archives []ArtifactView
	for _, record := range records {
		if record.Kind == OutArchiveArtifact {
			archives = append(archives, record)
		}
	}
	if len(archives) != 1 || archives[0].Name != "inkuna-out-"+run.ID+"-worktree.tar.gz" || archives[0].MediaType != "application/gzip" {
		t.Fatalf("one archive of the worktree's leftovers: %+v", archives)
	}
	if files := archived(t, service, archives[0].Digest); len(files) != 1 || files["scratch.txt"] != "notes the implementer kept" {
		t.Fatalf("the archive holds the scratch file only: %v", files)
	}
	notes := strings.Join(done.WorkspaceNotes, "\n")
	if !strings.Contains(notes, "Archived 1 file(s) left in .inkuna/out of the worktree") || !strings.Contains(notes, "Emptied .inkuna/out of the checkout; the run's artifacts already held everything in it") {
		t.Fatalf("the run says what it did with .inkuna/out: %s", notes)
	}

	// Sent back, the implementer runs again with the plan kept from round
	// 1, and finds its document put back.
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "implement", Text: "Say more."}}}); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, storage.Reviewing)
	seen.mu.Lock()
	documents := append([]string(nil), seen.documents...)
	seen.mu.Unlock()
	if len(documents) != 2 || documents[1] != planDocument {
		t.Fatalf("round 2's implementer read %q", documents)
	}
}

func projectRepository(t *testing.T, service *Service) string {
	t.Helper()
	project, err := service.store.Project(context.Background(), "project")
	if err != nil {
		t.Fatal(err)
	}
	return project.Repository
}

// archived reads a .tar.gz artifact into its files' contents by name.
func archived(t *testing.T, service *Service, digest string) map[string]string {
	t.Helper()
	reader, err := service.artifacts.Open(digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	unzipped, err := gzip.NewReader(reader)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	archive := tar.NewReader(unzipped)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(data)
	}
}

func TestOutCleanupKeepsRemovesOrArchives(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	ctx := context.Background()
	if err := service.store.SaveTask(ctx, "project", "planned", domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task", Kind: domain.TaskResearch, Objective: "Look",
		Source: domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}); err != nil {
		t.Fatal(err)
	}
	// A link in .inkuna/out to a file outside it, which emptying must leave.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	fill := func() string {
		directory := t.TempDir()
		writeDocs(t, directory, map[string]string{".inkuna/out/notes/a.md": "a", ".inkuna/out/b.txt": "b"})
		if err := os.Symlink(outside, filepath.Join(directory, ".inkuna", "out", "link")); err != nil {
			t.Fatal(err)
		}
		return directory
	}

	kept := fill()
	if note, err := service.emptyOut(ctx, "task", "run", kept, "", branching.OutKeep); err != nil || note != "" {
		t.Fatalf("keep: %q %v", note, err)
	}
	if _, err := os.Stat(filepath.Join(kept, ".inkuna", "out", "b.txt")); err != nil {
		t.Fatal("keep leaves the files")
	}

	removed := fill()
	if note, err := service.emptyOut(ctx, "task", "run", removed, "", branching.OutRemove); err != nil || note != "Emptied .inkuna/out" {
		t.Fatalf("remove: %q %v", note, err)
	}
	if _, err := os.Stat(filepath.Join(removed, ".inkuna", "out")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("remove empties .inkuna/out")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("a link's target is never removed")
	}

	packed := fill()
	note, err := service.emptyOut(ctx, "task", "run", packed, "", branching.OutArchive)
	if err != nil || note != "Archived 2 file(s) left in .inkuna/out as inkuna-out-run.tar.gz, then emptied it" {
		t.Fatalf("archive: %q %v", note, err)
	}
	records, err := service.store.TaskArtifacts(ctx, "task")
	if err != nil || len(records) != 1 {
		t.Fatalf("one archive: %+v %v", records, err)
	}
	if files := archived(t, service, records[0].Digest); len(files) != 2 || files["notes/a.md"] != "a" || files["b.txt"] != "b" {
		t.Fatalf("the archive holds the regular files, links left out: %v", files)
	}

	// A directory without .inkuna/out has nothing to do.
	if note, err := service.emptyOut(ctx, "task", "run", t.TempDir(), "", branching.OutArchive); err != nil || note != "" {
		t.Fatalf("nothing to clean: %q %v", note, err)
	}
}

func TestArchivingRefusesAFileOverTheLimitByName(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	ctx := context.Background()
	directory := t.TempDir()
	writeDocs(t, directory, map[string]string{".inkuna/out/notes.md": "kept", ".inkuna/out/big.bin": ""})
	// Sparse, so the test writes no data: only its size matters.
	if err := os.Truncate(filepath.Join(directory, ".inkuna", "out", "big.bin"), maxOutputArtifact+1); err != nil {
		t.Fatal(err)
	}

	_, err := service.emptyOut(ctx, "task", "run", directory, "", branching.OutArchive)
	if err == nil || !strings.Contains(err.Error(), "big.bin in it is over") {
		t.Fatalf("an oversized file is refused by name before it is read: %v", err)
	}
	if records, err := service.store.TaskArtifacts(ctx, "task"); err != nil || len(records) != 0 {
		t.Fatalf("nothing is archived: %+v %v", records, err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".inkuna", "out", "notes.md")); err != nil {
		t.Fatal("a refused archive leaves .inkuna/out as it was")
	}
}

func TestADocumentOverTheLimitIsNotKept(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	workingDir := t.TempDir()
	assignment := &workerAssignment{
		executionID: "run", stepRunID: "run-plan", step: domain.WorkflowStep{ID: "plan"}, task: domain.TaskSpec{ID: "task"}, workingDir: workingDir,
	}
	handoff := domain.HandoffResult{Summary: "Planned", Document: strings.Repeat("x", maxDocumentBytes+1)}
	service.keepDocument(context.Background(), assignment, 1, &handoff)
	if handoff.Document != "" || len(handoff.Artifacts) != 0 || len(handoff.Outputs) != 0 {
		t.Fatalf("an oversized document is dropped whole: %d artifacts, %d outputs", len(handoff.Artifacts), len(handoff.Outputs))
	}
	if _, err := os.Stat(filepath.Join(workingDir, ".inkuna", "out", "plan.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("an oversized document is not written")
	}
}

func TestAWorkerCannotNameArtifactsInItsHandoff(t *testing.T) {
	handoff := parseHandoff(json.RawMessage(`{"version":1,"status":"completed","summary":"done","artifacts":[{"digest":"` + strings.Repeat("a", 64) + `","name":"secrets"}]}`))
	if len(handoff.Artifacts) != 0 {
		t.Fatalf("artifacts are Inkuna's, never the worker's: %+v", handoff.Artifacts)
	}
}

func TestDocumentNamesSayTheirRound(t *testing.T) {
	if got := documentName("plan", 1); got != "plan.md" {
		t.Fatal(got)
	}
	if got := documentName("plan", 3); got != "plan-round3.md" {
		t.Fatal(got)
	}
	if got := documentName("../plan x", 1); got != "plan-x.md" {
		t.Fatal(got)
	}
}
