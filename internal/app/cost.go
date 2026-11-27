package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// TaskCost is what one task cost and where: its planning, each of its runs
// with their steps and each step's calls, and the same calls grouped by
// agent, runtime, model, and step. A call still running is not in it until
// it ends and is stored.
func (service *Service) TaskCost(taskID string) (domain.TaskCost, error) {
	if err := service.ready(); err != nil {
		return domain.TaskCost{}, err
	}
	cost, err := service.store.TaskCost(service.context(), taskID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.TaskCost{}, fmt.Errorf("task %q was not found", taskID)
	}
	if err != nil {
		return domain.TaskCost{}, fmt.Errorf("load task cost: %w", err)
	}
	// A run's view finishes before its record is stored, and the Cost tab
	// reloads when the view's state changes, so a live run's state and end
	// come from its view, as the run header's do. Otherwise a reload in
	// between would keep showing the run as running.
	service.runsMu.RLock()
	for index := range cost.Runs {
		if run := service.runs[cost.Runs[index].ExecutionID]; run != nil {
			record := executionRecord(run.view)
			cost.Runs[index].State, cost.Runs[index].FinishedAt = record.State, record.FinishedAt
		}
	}
	service.runsMu.RUnlock()
	return cost, nil
}

// TaskCosts lists what each task cost over a period, the most expensive
// first, and their sum: a project's tasks, or every project's when
// projectID is empty. from and to are RFC 3339 times; a call counts in the
// period it ended in, from included and to not, and an empty bound leaves
// that side open.
func (service *Service) TaskCosts(projectID, from, to string) (domain.PeriodCost, error) {
	if err := service.ready(); err != nil {
		return domain.PeriodCost{}, err
	}
	start, err := periodBound("from", from)
	if err != nil {
		return domain.PeriodCost{}, err
	}
	end, err := periodBound("to", to)
	if err != nil {
		return domain.PeriodCost{}, err
	}
	if !start.IsZero() && !end.IsZero() && !start.Before(end) {
		return domain.PeriodCost{}, fmt.Errorf("the period must end after it starts: from %s, to %s", from, to)
	}
	if projectID != "" {
		if _, err := service.store.Project(service.context(), projectID); err != nil {
			return domain.PeriodCost{}, fmt.Errorf("load project: %w", err)
		}
	}
	cost, err := service.store.PeriodCost(service.context(), projectID, start, end)
	if err != nil {
		return domain.PeriodCost{}, fmt.Errorf("load task costs: %w", err)
	}
	return cost, nil
}

// SaveCSV asks where to save a CSV export the view built and writes it
// there, replacing any file the user chose to overwrite. It returns the path
// written, or an empty one when the user cancelled.
func (service *Service) SaveCSV(defaultName, content string) (string, error) {
	if service.ctx == nil || service.ctx.Value("frontend") == nil {
		return "", errors.New("saving a file is available in the Inkuna desktop app")
	}
	path, err := wailsruntime.SaveFileDialog(service.ctx, wailsruntime.SaveDialogOptions{
		Title: "Export CSV", DefaultFilename: defaultName, CanCreateDirectories: true,
		Filters: []wailsruntime.FileFilter{{DisplayName: "CSV files (*.csv)", Pattern: "*.csv"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := writeExport(path, []byte(content)); err != nil {
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	return path, nil
}

// writeExport replaces path with data through a temporary file beside it, so
// an export that fails part way leaves the file the user chose as it was.
func writeExport(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".inkuna-export-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file private; an export is the user's own file.
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// attachTaskCosts gives each run what its task has cost so far. A cost is a
// record beside the runs, not part of them, so one that cannot be read
// leaves the runs without it rather than failing the list.
func (service *Service) attachTaskCosts(views []WorkflowRunView) {
	if service.store == nil || len(views) == 0 {
		return
	}
	ids := make([]string, 0, len(views))
	seen := make(map[string]bool, len(views))
	for _, view := range views {
		if !seen[view.TaskID] {
			seen[view.TaskID] = true
			ids = append(ids, view.TaskID)
		}
	}
	totals, err := service.store.TaskTotals(service.context(), ids)
	if err != nil {
		return
	}
	for index := range views {
		if total, ok := totals[views[index].TaskID]; ok {
			views[index].TaskCost = &total
		}
	}
}

func periodBound(name, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	bound, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC 3339 time such as 2026-10-01T00:00:00Z: %w", name, err)
	}
	return bound, nil
}
