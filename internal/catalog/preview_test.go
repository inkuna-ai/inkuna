package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestItemsSayAnAgentsPermissionAndTaskKindsAndARulesKinds(t *testing.T) {
	catalog := load(t)
	writer, _ := catalog.Get("schema-writer")
	if writer.Permission != domain.PermissionWorktree || !slices.Equal(writer.TaskKinds, []domain.TaskKind{domain.TaskImplementation}) {
		t.Fatalf("schema-writer = %q %v", writer.Permission, writer.TaskKinds)
	}
	rule, _ := catalog.Get("schema-change")
	if rule.Permission != "" || !slices.Equal(rule.TaskKinds, []domain.TaskKind{domain.TaskImplementation}) {
		t.Fatalf("schema-change = %q %v", rule.Permission, rule.TaskKinds)
	}
}

func TestPreviewSaysWhatAddWouldDoAndWritesNothing(t *testing.T) {
	target, root := repository(t)
	catalog := load(t)
	preview, err := catalog.Preview("schema-change", target)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Notes) != 1 || !strings.Contains(preview.Notes[0], "will be written with the built-in rules") {
		t.Fatalf("preview notes = %q", preview.Notes)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a preview wrote %v, %v", entries, err)
	}
	added, err := catalog.Add("schema-change", target)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(addedNames(preview), addedNames(added)) {
		t.Fatalf("preview %v, add %v", addedNames(preview), addedNames(added))
	}
	// Once added, a preview refuses it as the add would, naming where it is.
	var exists *ExistsError
	if _, err := catalog.Preview("schema-change", target); !errors.As(err, &exists) || exists.Where != target.RulesPath {
		t.Fatalf("a preview of an added rule: %v", err)
	}
	again, err := catalog.Preview("schema-change", Target{Global: true, AgentDirectories: target.AgentDirectories, StageDirectories: target.StageDirectories})
	if err == nil || !strings.Contains(err.Error(), "rules live in a repository") {
		t.Fatalf("a global preview of a rule = %+v, %v", again, err)
	}
}

func TestPreviewOfAGlobalAddWritesNothing(t *testing.T) {
	target, _ := repository(t)
	target.Global = true
	preview, err := load(t).Preview("review-and-approve", target)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"group:review-and-approve", "stage:schema-review", "agent:schema-reviewer"}; !slices.Equal(addedNames(preview), want) {
		t.Fatalf("preview %v, want %v", addedNames(preview), want)
	}
	for _, directory := range append(slices.Clone(target.AgentDirectories), target.StageDirectories...) {
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a preview made %s: %v", directory, err)
		}
	}
}

func TestPresenceSaysWhereATargetHasEachItem(t *testing.T) {
	target, _ := repository(t)
	catalog := load(t)
	present, err := catalog.Presence(target)
	if err != nil || len(present) != 0 {
		t.Fatalf("an empty repository has %v, %v", present, err)
	}
	if _, err := catalog.Add("schema-change", target); err != nil {
		t.Fatal(err)
	}
	present, err = catalog.Presence(target)
	if err != nil {
		t.Fatal(err)
	}
	if present["schema-change"] != target.RulesPath || present["schema-review"] != target.RulesPath ||
		present["schema-writer"] != filepath.Join(target.AgentDirectory, "schema-writer.md") {
		t.Fatalf("presence = %v", present)
	}
	// The global directories are another target, with nothing in them.
	global := Target{Global: true, AgentDirectories: target.AgentDirectories, StageDirectories: target.StageDirectories}
	if present, err := catalog.Presence(global); err != nil || len(present) != 0 {
		t.Fatalf("global presence = %v, %v", present, err)
	}
	write(t, target.RulesPath, "version: [\n")
	if _, err := catalog.Presence(target); err == nil || !strings.Contains(err.Error(), "does not load") {
		t.Fatalf("presence in rules that do not load: %v", err)
	}
}
