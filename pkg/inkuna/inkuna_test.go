package inkuna

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// Without assets Run must refuse before it touches the data directory or
// opens a window, so an embedding binary that forgot them fails at once and
// says what is missing.
func TestRunRefusesWithoutAssets(t *testing.T) {
	t.Setenv("INKUNA_DATA_DIR", t.TempDir())
	err := Run(Options{})
	if err == nil || !strings.Contains(err.Error(), "Options.Assets") {
		t.Fatalf("Run without assets: %v", err)
	}
}

// TestThePageCannotCallTheLifecycleMethods reads the bindings Wails
// generated, which CI regenerates and compares, so they are what the page
// gets. Wails leaves out a method passed to it as a lifecycle callback, and
// Run passes Startup and Shutdown so: from the page, Shutdown would end every
// run and close the store with the window still open.
func TestThePageCannotCallTheLifecycleMethods(t *testing.T) {
	bindings, err := os.ReadFile("../../frontend/wailsjs/go/app/Service.d.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"Startup", "Shutdown"} {
		if strings.Contains(string(bindings), "export function "+method+"(") {
			t.Errorf("the page can call %s", method)
		}
	}
}

func TestSetDockIconIgnoresNoIcon(t *testing.T) {
	setDockIcon(nil)
	setDockIcon([]byte{})
}

type named string

func (n named) Name() string                     { return string(n) }
func (named) Register(extension.Registrar) error { return nil }

// A build whose extensions Inkuna refuses must not open a window, and the
// error must say which extension to fix.
func TestRunRefusesDuplicateExtensionNames(t *testing.T) {
	t.Setenv("INKUNA_DATA_DIR", t.TempDir())
	err := Run(Options{
		Assets:     fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
		Extensions: []extension.Extension{named("audit"), named("audit")},
	})
	if err == nil || !strings.Contains(err.Error(), `run inkuna: register extensions: two extensions are named "audit"`) {
		t.Fatalf("Run with duplicate extensions: %v", err)
	}
}
