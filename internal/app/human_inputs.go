package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// humanInputsEnv names the variable that gives an evaluation step's commands
// the path of the file holding the answers to the human-input stages
// upstream of it.
const humanInputsEnv = "INKUNA_HUMAN_INPUTS"

// humanInputsFile is one evaluation step's answers file: where it goes and
// what it holds.
type humanInputsFile struct {
	path string
	data []byte
}

// humanInputs gathers, for one evaluation step, the answers the user gave to
// the human-input stages upstream of it in this run, oldest answer first.
// The answers come from Inkuna's own store, and the file goes in the run's
// data directory, outside any worktree, so a check can trust what a human
// approved rather than what an agent recorded about it.
func (service *Service) humanInputs(ctx context.Context, executionID string, spec domain.WorkflowSpec, step domain.WorkflowStep) (humanInputsFile, error) {
	upstream := upstreamHumanInputs(spec, step.ID)
	answers := []domain.HumanInputAnswer{}
	if len(upstream) > 0 {
		answered, err := service.store.AnsweredInputs(ctx, executionID)
		if err != nil {
			return humanInputsFile{}, fmt.Errorf("load human input answers: %w", err)
		}
		for _, input := range answered {
			if upstream[input.StepID] {
				answers = append(answers, domain.HumanInputAnswer{StageID: input.StepID, Prompt: input.Question, Answer: input.Answer, AnsweredAt: input.ResolvedAt})
			}
		}
	}
	// The store lists questions in the order they were asked, and stages
	// asked together can be answered in either order.
	sort.SliceStable(answers, func(i, j int) bool { return answers[i].AnsweredAt.Before(answers[j].AnsweredAt) })
	data, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		return humanInputsFile{}, fmt.Errorf("encode human input answers: %w", err)
	}
	name, err := humanInputsName(spec, step.ID)
	if err != nil {
		return humanInputsFile{}, err
	}
	// The commands run in the repository or a worktree, where a path under a
	// relative data directory would point somewhere else.
	directory, err := filepath.Abs(filepath.Join(service.dataDir, "runs", executionID, "human-inputs"))
	if err != nil {
		return humanInputsFile{}, fmt.Errorf("locate human input answers: %w", err)
	}
	return humanInputsFile{path: filepath.Join(directory, name), data: append(data, '\n')}, nil
}

// humanInputsName names a step's answers file. Step IDs are free text, so
// the name starts with the step's position in the run, which no other step
// shares, and keeps only the ID's letters, digits, '-', and '_' for whoever
// reads the directory: safe on every file system, and never a name Windows
// reserves.
func humanInputsName(spec domain.WorkflowSpec, stepID string) (string, error) {
	for index, step := range spec.Steps {
		if step.ID != stepID {
			continue
		}
		readable := strings.Map(func(character rune) rune {
			if character < 128 && (character == '-' || character == '_' || character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z') {
				return character
			}
			return '_'
		}, stepID)
		if len(readable) > 64 {
			readable = readable[:64]
		}
		return fmt.Sprintf("%02d-%s.json", index+1, readable), nil
	}
	return "", fmt.Errorf("step %q is not in the workflow", stepID)
}

// write puts the answers in place as a read-only file, replacing whatever
// is there. The evaluation step writes it before each command, so a command
// that changes or removes it cannot change what the next command reads.
func (file humanInputsFile) write() error {
	directory := filepath.Dir(file.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".incoming-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(file.data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o400); err != nil {
		return err
	}
	// Windows refuses to replace a read-only file.
	if err := os.Chmod(file.path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Rename(name, file.path)
}

// upstreamHumanInputs lists the human-input steps that stepID depends on,
// directly or through other steps.
func upstreamHumanInputs(spec domain.WorkflowSpec, stepID string) map[string]bool {
	upstream := ancestors(spec, stepID)
	found := map[string]bool{}
	for _, step := range spec.Steps {
		if step.Kind == domain.StepHumanInput && upstream[step.ID] {
			found[step.ID] = true
		}
	}
	return found
}

// confirmAnswered records a human-input step's answer as answered once the
// step has received it. AnswerInput claims the answer before sending it and
// confirms it after; the step can finish in between, and the steps after it
// must find the answer recorded.
func (service *Service) confirmAnswered(ctx context.Context, a2aTaskID string) error {
	inputs, err := service.store.TaskInputs(ctx, a2aTaskID)
	if err != nil {
		return fmt.Errorf("load human input: %w", err)
	}
	for _, input := range inputs {
		if input.State != storage.InputDelivering {
			continue
		}
		if _, err := service.store.FinishDelivery(ctx, input.ID, true); err != nil {
			return fmt.Errorf("record human input answer: %w", err)
		}
	}
	return nil
}
