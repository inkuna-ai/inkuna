package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestLoadAndRouteUsesMetadata(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "builder")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: builder\ndescription: Builds things\n---\n\nSecret full instructions."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "inkuna.yaml"), []byte("version: 1\ntaskKinds: [implementation]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packages, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(Route(domain.TaskImplementation, packages), []string{"builder"}) {
		t.Fatalf("unexpected route: %#v", packages)
	}
	if len(Route(domain.TaskReview, packages)) != 0 {
		t.Fatal("skill routed for unrelated task")
	}
}

func TestLoadAcceptsCRLFLineEndings(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "builder")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\r\nname: builder\r\ndescription: Builds things\r\n---\r\n\r\nFull instructions.\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := Load(directory)
	if err != nil || pkg.Name != "builder" || pkg.Description != "Builds things" || pkg.Body != "Full instructions." {
		t.Fatalf("CRLF skill parsed incorrectly: %#v, %v", pkg, err)
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("missing SKILL.md must fail")
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("no frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory); err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("bad SKILL.md must name the file: %v", err)
	}
}

func TestManifestChoosesHowASkillIsDelivered(t *testing.T) {
	for delivery, valid := range map[string]bool{"": true, DeliveryInline: true, DeliveryOnDemand: true, "lazy": false} {
		directory := filepath.Join(t.TempDir(), "builder")
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: builder\ndescription: Builds things\n---\nBody."), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "inkuna.yaml"), []byte("version: 1\ndelivery: "+delivery+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(directory)
		if (err == nil) != valid || (valid && loaded.Manifest.Delivery != delivery) {
			t.Fatalf("delivery %q: %+v, err %v", delivery, loaded.Manifest, err)
		}
	}
}

func TestLoadFSChecksASkillAsLoadChecksItsDirectory(t *testing.T) {
	files := fstest.MapFS{
		"SKILL.md":    {Data: []byte("---\nname: builder\ndescription: Builds things\n---\n\nBuild it.\n")},
		"inkuna.yaml": {Data: []byte("version: 1\ndelivery: on-demand\ntaskKinds: [implementation]\n")},
	}
	pkg, err := LoadFS(files, "builder")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ID != "builder" || pkg.Body != "Build it." || pkg.Directory != "" || pkg.Manifest.Delivery != DeliveryOnDemand {
		t.Fatalf("LoadFS = %+v", pkg)
	}
	if _, err := LoadFS(files, "other"); err == nil || !strings.Contains(err.Error(), `must match directory "other"`) {
		t.Fatalf("LoadFS under another name: %v", err)
	}
	files["inkuna.yaml"] = &fstest.MapFile{Data: []byte("version: 1\nactions:\n  run: {interpreter: python3, script: ../escape.py, timeoutSeconds: 5}\n")}
	if _, err := LoadFS(files, "builder"); err == nil || !strings.Contains(err.Error(), "unsafe script path") {
		t.Fatalf("LoadFS with an unsafe script: %v", err)
	}
	if _, err := LoadFS(fstest.MapFS{}, "builder"); err == nil || !strings.Contains(err.Error(), "read SKILL.md") {
		t.Fatalf("LoadFS without SKILL.md: %v", err)
	}
}
