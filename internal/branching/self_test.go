package branching

import (
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// Inkuna's own branching configuration is complete, so its runs never stop
// to propose one, and it names a run's branch the way the conventions say.
func TestInkunaBranchingConfigIsComplete(t *testing.T) {
	loaded, err := Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Complete() {
		t.Fatalf("%s lacks %v", RelativePath, loaded.Missing)
	}
	task := domain.TaskSpec{Kind: domain.TaskImplementation, Objective: "Docs index", Source: domain.TaskSource{Kind: "github", Reference: "https://github.com/inkuna-ai/inkuna/issues/23"}}
	if name, err := loaded.Config.ForTask(task); err != nil || name != "feature/23-docs-index" {
		t.Fatalf("ForTask = %q, %v; want feature/23-docs-index", name, err)
	}
}
