package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/docs"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// docReadTracer records which of a project's docs one worker opens. A
// runtime reports the paths its read tools name; the tracer keeps those that
// land, once the step's working directory and symbolic links are resolved,
// on a doc or a directory of docs under the project's docs root.
type docReadTracer struct {
	working string
	root    string
	base    domain.DocRead
}

// docReadTracer is nil when the step has no docs root to trace against.
func (service *Service) docReadTracer(assignment *workerAssignment) *docReadTracer {
	service.runsMu.RLock()
	run := service.runs[assignment.executionID]
	service.runsMu.RUnlock()
	if run == nil {
		return nil
	}
	entry, _ := service.repositoryConfig(run.project)
	declared, err := docs.Dir(assignment.workingDir, entry.Docs)
	if err != nil {
		return nil
	}
	root, err := filepath.EvalSymlinks(declared)
	if err != nil {
		return nil
	}
	working, err := filepath.EvalSymlinks(assignment.workingDir)
	if err != nil {
		working = assignment.workingDir
	}
	return &docReadTracer{working: working, root: root, base: domain.DocRead{
		TaskID: assignment.task.ID, StepID: assignment.step.ID, RunID: assignment.executionID,
		ProjectID: run.project.ID, Agent: assignment.definition.Name, Runtime: assignment.runtimeName,
		DocsDir: docs.RelativeDir(entry.Docs),
	}}
}

// trace records the docs an event's reads opened. A failure to record is
// returned for the caller to report; it never fails the step.
func (tracer *docReadTracer) trace(ctx context.Context, service *Service, event domain.RunEvent) error {
	if tracer == nil {
		return nil
	}
	for _, read := range event.Reads {
		for _, target := range read.Paths {
			docPath, ok := tracer.resolve(target)
			if !ok {
				continue
			}
			record := tracer.base
			record.DocPath, record.Tool, record.OpenedAt = docPath, read.Tool, event.Timestamp
			if err := service.store.RecordDocRead(ctx, record); err != nil {
				return fmt.Errorf("record doc read %s: %w", docPath, err)
			}
		}
	}
	return nil
}

// resolve turns a path an agent named into its path under the docs root:
// a doc as the index lists it, a directory as "dir/", or the root as "/".
// Anything outside the root, missing, hidden, or not Markdown is not a doc.
func (tracer *docReadTracer) resolve(target string) (string, bool) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(tracer.working, target)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(tracer.root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", false
	}
	relative = filepath.ToSlash(relative)
	if info.IsDir() {
		if relative == "." {
			return "/", true
		}
		for _, segment := range strings.Split(relative, "/") {
			if strings.HasPrefix(segment, ".") {
				return "", false
			}
		}
		return relative + "/", true
	}
	if !info.Mode().IsRegular() || docs.ValidatePath(relative) != nil {
		return "", false
	}
	return relative, true
}

// DocReads lists who read one of a project's docs, newest first: the task,
// step, agent, and run of each read, to link back to the run. docPath is
// under the docs root as configured now, so reads made while the root was
// elsewhere, which were of other files, are not listed.
func (service *Service) DocReads(projectID, docPath string) ([]domain.DocRead, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	project, err := service.store.Project(service.context(), projectID)
	if err != nil {
		return nil, fmt.Errorf("load project: %w", err)
	}
	entry, _ := service.repositoryConfig(project)
	reads, err := service.store.DocReadsForDoc(service.context(), projectID, docs.RelativeDir(entry.Docs), docPath, 50)
	if err != nil {
		return nil, fmt.Errorf("load doc reads: %w", err)
	}
	return reads, nil
}

// stepDocReads lists the docs one step of a run read, in order.
func (service *Service) stepDocReads(ctx context.Context, taskID, runID, stepID string) ([]domain.DocRead, error) {
	reads, err := service.store.DocReadsForTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load doc reads: %w", err)
	}
	step := make([]domain.DocRead, 0, len(reads))
	for _, read := range reads {
		if read.RunID == runID && read.StepID == stepID {
			step = append(step, read)
		}
	}
	return step, nil
}
