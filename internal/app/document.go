package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// DocumentArtifact is the artifact kind of a step's plan or report: the
// Markdown its handoff carried in document.
const DocumentArtifact = "document"

// maxDocumentBytes bounds a step's document. It is the preview limit, so a
// stored document always opens whole.
const maxDocumentBytes = maxArtifactPreview

// documentName names a step's document artifact; a later round's names its
// round, so each round's document stays apart from the first.
func documentName(stepID string, round int) string {
	name := safeArtifactName(stepID)
	if round > 1 {
		return fmt.Sprintf("%s-round%d.md", name, round)
	}
	return name + ".md"
}

// keepDocument stores the document a step's handoff carries as its
// document artifact and, for a step of the run rather than a peer, writes
// it to .inkuna/out/<step>.md in the step's working directory. The steps
// after it find it there among their dependency outputs. The handoff keeps
// only the references, so the stored handoff and the step's record never
// hold the text twice.
func (service *Service) keepDocument(ctx context.Context, assignment *workerAssignment, round int, handoff *domain.HandoffResult) {
	document := handoff.Document
	handoff.Document = ""
	if strings.TrimSpace(document) == "" {
		return
	}
	warn := func(text string) {
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, text))
	}
	// A document cut short would be approved as if it were whole, so one
	// over the limit is not kept at all and the step's summary stands.
	if len(document) > maxDocumentBytes {
		warn(fmt.Sprintf("The step's document was %d bytes, over the %d-byte limit, so it was not kept.", len(document), maxDocumentBytes))
		return
	}
	name := documentName(assignment.step.ID, round)
	ref, err := service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, assignment.definition.Name, DocumentArtifact, name, "text/markdown", strings.NewReader(document))
	if err != nil {
		warn("The step's document was not stored: " + err.Error())
		return
	}
	handoff.Artifacts = append(handoff.Artifacts, ref)
	if assignment.depth > 0 || assignment.workingDir == "" {
		return
	}
	file := path.Join(worktree.OutDir, safeArtifactName(assignment.step.ID)+".md")
	if err := writeOut(assignment.workingDir, file, []byte(document)); err != nil {
		warn("The step's document was stored but not written to " + file + ", so the steps after it will not find it there: " + err.Error())
		return
	}
	handoff.Outputs = append(handoff.Outputs, domain.ArtifactOutput{
		Path: file, Name: name, MediaType: "text/markdown", Description: "The document " + assignment.step.ID + " wrote", Digest: ref.Digest,
	})
	service.checkOutIgnored(ctx, assignment)
}

// checkOutIgnored warns, once per run, when the repository does not ignore
// .inkuna/out: Inkuna's commits leave it out, but the user's git sees it.
func (service *Service) checkOutIgnored(ctx context.Context, assignment *workerAssignment) {
	service.runsMu.Lock()
	run := service.runs[assignment.executionID]
	first := run != nil && !run.outChecked
	if first {
		run.outChecked = true
	}
	service.runsMu.Unlock()
	if !first {
		return
	}
	if ignored, err := worktree.OutIgnored(ctx, assignment.workingDir); err == nil && !ignored {
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning,
			worktree.OutDir+" is not ignored in this repository, so git shows the files Inkuna writes there; Inkuna's commits leave them out. Run `inkunactl init` in the repository to ignore it."))
	}
}

// restoreOutputs puts back the files under .inkuna/out that the steps
// before this one declared and Inkuna stored, when they are gone. A
// completed run's cleanup empties the directory, and another run's can
// empty a checkout's, yet a later round still lists the files to its
// steps. A file that is there is left as it is.
func (service *Service) restoreOutputs(executionID, stepRunID, workingDir string, dependencies []domain.HandoffResult) {
	for _, dependency := range dependencies {
		for _, output := range dependency.Outputs {
			if output.Digest == "" || !strings.HasPrefix(output.Path, worktree.OutDir+"/") {
				continue
			}
			if err := service.restoreOutput(workingDir, output); err != nil {
				_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "Could not put back "+output.Path+" from the step that wrote it: "+err.Error()))
			}
		}
	}
}

func (service *Service) restoreOutput(workingDir string, output domain.ArtifactOutput) error {
	root, err := os.OpenRoot(workingDir)
	if err != nil {
		return err
	}
	_, err = root.Lstat(output.Path)
	_ = root.Close()
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if service.artifacts == nil {
		return errors.New("artifact store is not initialized")
	}
	reader, err := service.artifacts.Open(output.Digest)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, maxOutputArtifact+1))
	if err != nil {
		return err
	}
	if len(data) > maxOutputArtifact {
		return fmt.Errorf("it is over %d bytes", maxOutputArtifact)
	}
	return writeOut(workingDir, output.Path, data)
}

// writeOut writes a file under .inkuna/out of a working directory, through
// a temporary file renamed over it, so a step never reads half of it. The
// root keeps every path inside the working directory, symbolic links
// included.
func writeOut(workingDir, file string, data []byte) error {
	if !strings.HasPrefix(file, worktree.OutDir+"/") || path.Clean(file) != file {
		return fmt.Errorf("%s is not a file under %s", file, worktree.OutDir)
	}
	root, err := os.OpenRoot(workingDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(path.Dir(file), 0o755); err != nil {
		return err
	}
	temporary := path.Join(path.Dir(file), "."+path.Base(file)+"."+newID("tmp"))
	if err := root.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	if err := root.Rename(temporary, file); err != nil {
		_ = root.Remove(temporary)
		return err
	}
	return nil
}
