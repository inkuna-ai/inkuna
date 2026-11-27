package runtime

import (
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/proc"
)

// TestEveryProcessARuntimeStartsCarriesItsInstance builds each preset with
// an instance and without one. A worker, Copilot's server listing, and a
// user MCP server's sign-in all start from environment, so each carries
// the instance's mark, and a runtime without one marks nothing.
func TestEveryProcessARuntimeStartsCarriesItsInstance(t *testing.T) {
	for _, preset := range []domain.Runtime{domain.RuntimeCodex, domain.RuntimeClaude, domain.RuntimeCopilot} {
		marked, err := New(Spec{Name: preset, Preset: preset, Binary: string(preset), Instance: "inkuna-0123456789abcdef"})
		if err != nil {
			t.Fatal(err)
		}
		if !contains(marked.(*commandRuntime).environment(RunRequest{}), proc.Mark("inkuna-0123456789abcdef")) {
			t.Fatalf("a %s worker lacks its Inkuna's mark", preset)
		}
		unmarked, err := New(Spec{Name: preset, Preset: preset, Binary: string(preset)})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range unmarked.(*commandRuntime).environment(RunRequest{}) {
			if strings.HasPrefix(entry, proc.InstanceVariable+"=") {
				t.Fatalf("a %s runtime without an instance marks its workers: %s", preset, entry)
			}
		}
	}
}
