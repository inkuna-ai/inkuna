package planner

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// Access is what the approval screen may change about what a step may do:
// on an agent step, its permission, the commands its worker may run without
// asking, and the paths it is told to keep its writes to; on an evaluation
// step, the commands it runs.
type Access struct {
	Permission   domain.Permission
	Commands     [][]string
	AllowedPaths []string
}

// applyAccess gives a planned agent step the access the approval screen
// shows, checked as a rule's stage is, and records each change against the
// plan in its AccessEdits, keeping the edits it already carried for the
// other fields. definition is the agent that will staff it, so a
// read-only or remote agent is never left on a step that writes. A
// read-only step keeps no allowed paths, and a writable one given none gets
// the whole repository, as a rule's stage does.
func applyAccess(step *domain.WorkflowStep, access Access, definition agent.Definition) error {
	permission := access.Permission
	switch permission {
	case domain.PermissionReadOnly, domain.PermissionWorktree:
	default:
		return fmt.Errorf("step %q needs permission read-only or worktree-write, not %q", step.ID, permission)
	}
	if permission == domain.PermissionWorktree {
		switch {
		case definition.Remote():
			return fmt.Errorf("remote agent %q is a read-only advisor, so step %q must stay read-only", definition.Name, step.ID)
		case definition.Permission != domain.PermissionWorktree:
			return fmt.Errorf("agent %q is read-only, so step %q cannot be worktree-write; choose a writable agent or keep the step read-only", definition.Name, step.ID)
		}
	}
	commands, err := checkedCommands(step.ID, access.Commands, agentruntime.CheckAllowedCommand)
	if err != nil {
		return err
	}
	var paths []string
	if permission == domain.PermissionWorktree {
		for _, allowed := range access.AllowedPaths {
			allowed = strings.TrimSpace(allowed)
			if err := domain.CheckAllowedPath(allowed); err != nil {
				return fmt.Errorf("step %q: %w", step.ID, err)
			}
			paths = append(paths, allowed)
		}
		if len(paths) == 0 {
			paths = []string{"."}
		}
	}
	var edits []domain.AccessEdit
	if permission != step.Permission {
		edits = append(edits, domain.AccessEdit{
			Field: domain.AccessPermission, Widens: permission == domain.PermissionWorktree,
			Note: fmt.Sprintf("Permission set to %s at approval; the plan said %s.", permission, step.Permission),
		})
	}
	if edit, changed := commandsEdit(step.Commands, commands); changed {
		edits = append(edits, edit)
	}
	// A read-only step has no paths to keep its writes to, so narrowing one
	// says nothing about them.
	if permission == domain.PermissionWorktree && !slices.Equal(step.AllowedPaths, paths) {
		edits = append(edits, domain.AccessEdit{
			Field: domain.AccessAllowedPaths, Widens: !covered(paths, step.AllowedPaths),
			Note: fmt.Sprintf("Allowed paths changed at approval; the plan had %s.", listOrNone(step.AllowedPaths)),
		})
	}
	step.Permission, step.Commands, step.AllowedPaths, step.AccessEdits = permission, commands, paths, mergeEdits(step.AccessEdits, edits)
	return nil
}

// EditCommands gives a planned evaluation step the commands the approval
// screen shows, checked as a rule's evaluation stage's are, and records the
// change against the plan in its AccessEdits. Any other step is left alone.
func EditCommands(step *domain.WorkflowStep, commands [][]string) error {
	if step.Kind != domain.StepEvaluation {
		return nil
	}
	checked, err := checkedCommands(step.ID, commands, agentruntime.CheckCommand)
	if err != nil {
		return err
	}
	var edits []domain.AccessEdit
	if edit, changed := commandsEdit(step.Commands, checked); changed {
		edits = append(edits, edit)
	}
	step.Commands, step.AccessEdits = checked, mergeEdits(step.AccessEdits, edits)
	return nil
}

// mergeEdits is a step's access edits after an approval: those it carried
// for the fields this approval leaves alone, which a rerun's plan keeps from
// the approval before it, then this approval's, which replace any earlier
// edit of the same field. A rerun's edit is made against the plan the run
// was approved with, not the rule, so commands or allowed paths an earlier
// approval widened can still be wider than the rule after a rerun reorders
// or narrows them: their edit stays marked as widening. A permission is one
// value, so its latest edit alone says whether the step writes beyond its
// rule.
func mergeEdits(earlier, edits []domain.AccessEdit) []domain.AccessEdit {
	var merged []domain.AccessEdit
	for _, edit := range earlier {
		if !slices.ContainsFunc(edits, func(current domain.AccessEdit) bool { return current.Field == edit.Field }) {
			merged = append(merged, edit)
		}
	}
	for _, edit := range edits {
		if edit.Field != domain.AccessPermission && slices.ContainsFunc(earlier, func(before domain.AccessEdit) bool { return before.Field == edit.Field && before.Widens }) {
			edit.Widens = true
		}
		merged = append(merged, edit)
	}
	return merged
}

// checkedCommands copies commands that pass check, or names the step and the
// first one that does not.
func checkedCommands(stepID string, commands [][]string, check func([]string) error) ([][]string, error) {
	if len(commands) == 0 {
		return nil, nil
	}
	checked := make([][]string, 0, len(commands))
	for _, command := range commands {
		if err := check(command); err != nil {
			return nil, fmt.Errorf("step %q: %w", stepID, err)
		}
		checked = append(checked, slices.Clone(command))
	}
	return checked, nil
}

// commandsEdit describes a change to a step's commands against the plan's.
// Adding a command widens what the step does; removing or reordering them
// does not.
func commandsEdit(planned, approved [][]string) (domain.AccessEdit, bool) {
	same := slices.EqualFunc(planned, approved, slices.Equal[[]string])
	if same {
		return domain.AccessEdit{}, false
	}
	widens := false
	for _, command := range approved {
		if !slices.ContainsFunc(planned, func(listed []string) bool { return slices.Equal(listed, command) }) {
			widens = true
		}
	}
	lines := make([]string, len(planned))
	for index, command := range planned {
		lines[index] = strings.Join(command, " ")
	}
	return domain.AccessEdit{
		Field: domain.AccessCommands, Widens: widens,
		Note: fmt.Sprintf("Commands changed at approval; the plan had %s.", listOrNone(lines)),
	}, true
}

// covered reports whether every path lies within one of the plan's: the
// same directory or one inside it, read lexically, as allowed paths are.
func covered(paths, planned []string) bool {
	for _, allowed := range paths {
		inside := false
		for _, outer := range planned {
			inner, outer := slashPath(allowed), slashPath(outer)
			inside = inside || outer == "." || inner == outer || strings.HasPrefix(inner, outer+"/")
		}
		if !inside {
			return false
		}
	}
	return true
}

func slashPath(value string) string {
	return path.Clean(strings.ReplaceAll(value, `\`, "/"))
}

// listOrNone quotes each value, so a path such as . or a command ending in
// ./... still reads as one item at the end of a sentence.
func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return `"` + strings.Join(values, `", "`) + `"`
}
