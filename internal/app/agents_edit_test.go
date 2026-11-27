package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestAgentsAreCreatedRenamedOverriddenAndDeletedThroughBindings(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	globalDirectory := filepath.Join(t.TempDir(), "agents")
	service.config.AgentDirectories = []string{globalDirectory}
	encode := func(definition agent.Definition) string {
		data, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	locations, err := service.AgentLocations("project")
	if err != nil || locations.Repository != filepath.Join(repo, ".inkuna", "agents") || locations.Global != globalDirectory {
		t.Fatalf("AgentLocations = %#v, %v", locations, err)
	}

	created, err := service.SaveAgent("project", AgentScopeRepository, "", encode(agent.Definition{
		Name: "security-reviewer", Description: "Reviews for security issues", Permission: domain.PermissionReadOnly,
		TaskKinds: []domain.TaskKind{domain.TaskReview}, Instructions: "Check for injection.",
	}))
	if err != nil || created.Definition.Source != agent.SourceRepository || created.Overrides != "" || created.Card.Name != "security-reviewer" {
		t.Fatalf("SaveAgent = %#v, %v", created, err)
	}

	// Saving under a new name is a rename: the old file goes away.
	renamed := created.Definition
	renamed.Name = "appsec-reviewer"
	if _, err := service.SaveAgent("project", AgentScopeRepository, "security-reviewer", encode(renamed)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(locations.Repository, "security-reviewer.md")); !os.IsNotExist(err) {
		t.Fatalf("the renamed agent's old file should be gone: %v", err)
	}

	// A repository definition named like a built-in overrides it, and deleting
	// the override brings the built-in back.
	override := agent.Definition{Name: "implementer", Description: "House implementer", Permission: domain.PermissionWorktree, Instructions: "Follow house style."}
	overridden, err := service.SaveAgent("project", AgentScopeRepository, "implementer", encode(override))
	if err != nil || overridden.Overrides != agent.SourceBuiltIn || overridden.Definition.Description != "House implementer" {
		t.Fatalf("override = %#v, %v", overridden, err)
	}
	if err := service.DeleteAgent("project", AgentScopeRepository, "implementer"); err != nil {
		t.Fatal(err)
	}
	views, err := service.Agents("project")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]agent.Source{}
	for _, view := range views {
		sources[view.Definition.Name] = view.Definition.Source
	}
	if sources["implementer"] != agent.SourceBuiltIn || sources["appsec-reviewer"] != agent.SourceRepository || sources["security-reviewer"] != "" {
		t.Fatalf("unexpected registry after edits: %#v", sources)
	}

	// The global scope writes to the configured directory, and a repository
	// definition with the same name shadows it.
	shared := agent.Definition{Name: "house-style", Description: "Shared style checker", Permission: domain.PermissionReadOnly, Instructions: "Check style."}
	if saved, err := service.SaveAgent("project", AgentScopeGlobal, "", encode(shared)); err != nil || saved.Definition.Source != agent.SourceGlobal {
		t.Fatalf("global save = %#v, %v", saved, err)
	}
	if _, err := os.Stat(filepath.Join(globalDirectory, "house-style.md")); err != nil {
		t.Fatal(err)
	}
	if shadow, err := service.SaveAgent("project", AgentScopeRepository, "", encode(shared)); err != nil || shadow.Overrides != agent.SourceGlobal {
		t.Fatalf("repository shadow = %#v, %v", shadow, err)
	}

	// Least privilege: undeclared secrets, reserved names, unknown fields, and
	// unknown scopes are refused, and built-ins have no file to delete.
	withSecret := created.Definition
	withSecret.Secrets = []string{"UNDECLARED_TOKEN"}
	if _, err := service.SaveAgent("project", AgentScopeRepository, "", encode(withSecret)); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("an undeclared secret must be rejected: %v", err)
	}
	reserved := created.Definition
	reserved.Name = agent.Human
	if _, err := service.SaveAgent("project", AgentScopeRepository, "", encode(reserved)); err == nil {
		t.Fatal("a reserved name must be rejected")
	}
	// Keys another tool reads come back with the agent and are written back
	// when it is saved, but never set what Inkuna reads.
	explorer := agent.Definition{Name: "code-explorer", Description: "Explores", Permission: domain.PermissionReadOnly, Instructions: "Explore.", Extra: "color: blue\ntools: [Read, Grep]\n"}
	saved, err := service.SaveAgent("project", AgentScopeRepository, "", encode(explorer))
	if err != nil || saved.Definition.Extra != explorer.Extra {
		t.Fatalf("saved = %+v, %v", saved.Definition, err)
	}
	if data, err := os.ReadFile(filepath.Join(locations.Repository, "code-explorer.md")); err != nil || !strings.Contains(string(data), "color: blue\ntools: [Read, Grep]\n---") {
		t.Fatalf("file = %s, %v", data, err)
	}
	escalated := explorer
	escalated.Extra = "permission: worktree-write\n"
	if _, err := service.SaveAgent("project", AgentScopeRepository, "code-explorer", encode(escalated)); err == nil || !strings.Contains(err.Error(), "Inkuna reads itself") {
		t.Fatalf("an extra key that sets the permission was saved: %v", err)
	}
	if _, err := service.SaveAgent("project", AgentScopeRepository, "", `{"name":"x-agent","surprise":true}`); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := service.SaveAgent("project", "elsewhere", "", encode(created.Definition)); err == nil {
		t.Fatal("an unknown scope must be rejected")
	}
	if err := service.DeleteAgent("project", AgentScopeRepository, "reviewer"); err == nil {
		t.Fatal("a built-in agent has no file to delete")
	}

	imported, err := service.ParseAgent("---\nname: migration-writer\ndescription: Writes migrations\npermission: worktree-write\n---\nWrite reversible migrations.\n")
	if err != nil || imported.Name != "migration-writer" || imported.Permission != domain.PermissionWorktree {
		t.Fatalf("ParseAgent = %#v, %v", imported, err)
	}
	if _, err := service.ParseAgent("no frontmatter"); err == nil {
		t.Fatal("ParseAgent must reject a file without frontmatter")
	}
}

func TestSaveAgentNeverReplacesAnotherDefinitionOrHalfRenames(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	directory := filepath.Join(repo, ".inkuna", "agents")
	save := func(originalName string, definition agent.Definition) error {
		data, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.SaveAgent("project", AgentScopeRepository, originalName, string(data))
		return err
	}
	first := agent.Definition{Name: "first-agent", Description: "First", Permission: domain.PermissionReadOnly, Instructions: "Be first."}
	second := agent.Definition{Name: "second-agent", Description: "Second", Permission: domain.PermissionReadOnly, Instructions: "Be second."}
	for _, definition := range []agent.Definition{first, second} {
		if err := save("", definition); err != nil {
			t.Fatal(err)
		}
	}

	// A new agent and a rename are both refused when the name is taken, and
	// neither file changes.
	imposter := first
	imposter.Description = "Imposter"
	if err := save("", imposter); err == nil || !strings.Contains(err.Error(), "already has a definition") {
		t.Fatalf("a new agent must not replace an existing definition: %v", err)
	}
	if err := save("second-agent", imposter); err == nil || !strings.Contains(err.Error(), "already has a definition") {
		t.Fatalf("a rename must not replace an existing definition: %v", err)
	}
	if kept, err := agent.Load(filepath.Join(directory, "first-agent.md"), agent.SourceRepository); err != nil || kept.Description != "First" {
		t.Fatalf("the existing definition changed: %#v, %v", kept, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "second-agent.md")); err != nil {
		t.Fatalf("a refused rename must keep the original: %v", err)
	}

	// Editing in place still replaces the file.
	edited := first
	edited.Description = "First, edited"
	if err := save("first-agent", edited); err != nil {
		t.Fatal(err)
	}

	// An original that cannot be removed fails the rename, and the new file is
	// taken back out so both names are never active. A non-empty directory
	// stands in for a file the OS refuses to delete.
	stuck := filepath.Join(directory, "stuck-agent.md")
	if err := os.MkdirAll(filepath.Join(stuck, "pinned"), 0o755); err != nil {
		t.Fatal(err)
	}
	moved := agent.Definition{Name: "moved-agent", Description: "Moved", Permission: domain.PermissionReadOnly, Instructions: "Move."}
	if err := save("stuck-agent", moved); err == nil || !strings.Contains(err.Error(), "rename agent") {
		t.Fatalf("a failed removal must fail the rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "moved-agent.md")); !os.IsNotExist(err) {
		t.Fatalf("the half-renamed definition should be rolled back: %v", err)
	}
}

func TestGlobalAgentEditsFollowTheDirectoryThatWins(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	base, team := filepath.Join(t.TempDir(), "base"), filepath.Join(t.TempDir(), "team")
	service.config.AgentDirectories = []string{base, team}
	definition := agent.Definition{Name: "house-style", Description: "Base", Permission: domain.PermissionReadOnly, Instructions: "Check style."}
	if _, err := agent.Write(base, definition); err != nil {
		t.Fatal(err)
	}
	definition.Description = "Team"
	if _, err := agent.Write(team, definition); err != nil {
		t.Fatal(err)
	}
	save := func(originalName string, definition agent.Definition) (AgentView, error) {
		data, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		return service.SaveAgent("", AgentScopeGlobal, originalName, string(data))
	}

	// The later directory wins in the registry, so that is the file an edit
	// changes and the view that comes back.
	definition.Description = "Team, edited"
	saved, err := save("house-style", definition)
	if err != nil || saved.Definition.Description != "Team, edited" || saved.Definition.Path != filepath.Join(team, "house-style.md") {
		t.Fatalf("edit = %#v, %v", saved, err)
	}
	if kept, err := agent.Load(filepath.Join(base, "house-style.md"), agent.SourceGlobal); err != nil || kept.Description != "Base" {
		t.Fatalf("the shadowed definition changed: %#v, %v", kept, err)
	}
	// A new agent cannot hide behind the winning definition.
	if _, err := save("", definition); err == nil || !strings.Contains(err.Error(), "already has a definition") {
		t.Fatalf("a new agent must not be saved under a name that already wins: %v", err)
	}
	// A rename is refused when another global directory holds the new name,
	// whether the file would shadow that definition or hide behind it.
	for _, move := range []struct{ from, holder string }{{team, base}, {base, team}} {
		target := agent.Definition{Name: "target-agent", Description: "Target", Permission: domain.PermissionReadOnly, Instructions: "Be the target."}
		targetPath, err := agent.Write(move.holder, target)
		if err != nil {
			t.Fatal(err)
		}
		other := agent.Definition{Name: "other-agent", Description: "Other", Permission: domain.PermissionReadOnly, Instructions: "Be other."}
		otherPath, err := agent.Write(move.from, other)
		if err != nil {
			t.Fatal(err)
		}
		other.Name = "target-agent"
		if _, err := save("other-agent", other); err == nil || !strings.Contains(err.Error(), "already has a definition") {
			t.Fatalf("a rename onto a name held in %s must be refused: %v", move.holder, err)
		}
		if _, err := os.Stat(filepath.Join(move.from, "target-agent.md")); !os.IsNotExist(err) {
			t.Fatalf("a refused rename must not write the new file: %v", err)
		}
		for _, path := range []string{targetPath, otherPath} {
			if err := os.Remove(path); err != nil {
				t.Fatalf("a refused rename must keep both files: %v", err)
			}
		}
	}
	// New names go to the first directory.
	fresh := agent.Definition{Name: "fresh-agent", Description: "Fresh", Permission: domain.PermissionReadOnly, Instructions: "Be fresh."}
	if saved, err := save("", fresh); err != nil || saved.Definition.Path != filepath.Join(base, "fresh-agent.md") {
		t.Fatalf("new agent = %#v, %v", saved, err)
	}
	// Deleting removes the winner and brings the shadowed definition back.
	if err := service.DeleteAgent("", AgentScopeGlobal, "house-style"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(team, "house-style.md")); !os.IsNotExist(err) {
		t.Fatalf("the winning definition should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "house-style.md")); err != nil {
		t.Fatalf("the shadowed definition should remain: %v", err)
	}
}
