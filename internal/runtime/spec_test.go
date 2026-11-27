package runtime

import (
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func customSpec() Spec {
	return Spec{
		Name: "gemini", Binary: "gemini", Stdin: StdinNone, Output: OutputText,
		Args:         []string{"--prompt", "{prompt}", "--model", "{model}", "--effort={reasoning_effort}", "--schema-file", "{schema_path}"},
		ReadOnlyArgs: []string{"--approval-mode", "plan"},
	}
}

func TestSpecValidationKeepsLeastPrivilege(t *testing.T) {
	if err := customSpec().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Spec){
		"bad name":            func(spec *Spec) { spec.Name = "Gemini CLI" },
		"no binary":           func(spec *Spec) { spec.Binary = " " },
		"unknown output":      func(spec *Spec) { spec.Output = "xml" },
		"unknown stdin":       func(spec *Spec) { spec.Stdin = "file" },
		"unknown placeholder": func(spec *Spec) { spec.Args = append(spec.Args, "{home}") },
		"empty argument":      func(spec *Spec) { spec.Args = append(spec.Args, " ") },
		"no prompt":           func(spec *Spec) { spec.Args = []string{"--model", "{model}"} },
		"prompt only on write": func(spec *Spec) {
			spec.Args, spec.WriteArgs = nil, []string{"{prompt}"}
		},
		"no read-only arguments": func(spec *Spec) { spec.ReadOnlyArgs = nil },
		"prompt only on read-only": func(spec *Spec) {
			spec.Args, spec.ReadOnlyArgs, spec.WriteArgs = nil, []string{"{prompt}"}, []string{"--write"}
		},
		"bypass flag":               func(spec *Spec) { spec.Args = append(spec.Args, "--yolo") },
		"bypass flag cased":         func(spec *Spec) { spec.WriteArgs = []string{"--Dangerously-Skip-Permissions"} },
		"bypass flag valued":        func(spec *Spec) { spec.WriteArgs = []string{"--allow-all-tools=true"} },
		"bypass value":              func(spec *Spec) { spec.WriteArgs = []string{"--permission-mode", "bypassPermissions"} },
		"bypass inline value":       func(spec *Spec) { spec.WriteArgs = []string{"--sandbox=danger-full-access"} },
		"bypass in probe":           func(spec *Spec) { spec.VersionArgs = []string{"--yolo"} },
		"placeholder in probe":      func(spec *Spec) { spec.VersionArgs = []string{"{prompt}"} },
		"unknown preset":            func(spec *Spec) { spec.Preset = "gemini" },
		"preset with template":      func(spec *Spec) { spec.Preset = domain.RuntimeClaude },
		"user config on a template": func(spec *Spec) { spec.UserConfig = true },
	} {
		spec := customSpec()
		mutate(&spec)
		if err := spec.Validate(); err == nil {
			t.Fatalf("%s: the declaration was accepted: %#v", name, spec)
		}
	}
	if err := (Spec{Name: domain.RuntimeCodex, Preset: domain.RuntimeClaude}).Validate(); err == nil {
		t.Fatal("a built-in name must not borrow another preset")
	}
	// A preset under another name is a second copy of a built-in CLI.
	wrapper := Spec{Name: "claude-work", Preset: domain.RuntimeClaude, Binary: "/opt/claude-work"}
	if err := wrapper.Validate(); err != nil || !wrapper.CanWrite() {
		t.Fatalf("preset copy = %v, can write %v", err, wrapper.CanWrite())
	}
	for _, spec := range []Spec{{Name: domain.RuntimeCodex, UserConfig: true}, {Name: "claude-work", Preset: domain.RuntimeClaude, UserConfig: true}} {
		if err := spec.Validate(); err != nil {
			t.Fatalf("an adapter may load the user's own setup: %v", err)
		}
	}
}

func TestTemplateExpansionDropsUnsetValuesAndNeverSplitsThem(t *testing.T) {
	built, err := New(customSpec())
	if err != nil {
		t.Fatal(err)
	}
	runtime := built.(*commandRuntime)
	prompt := "review this; rm -rf / --yolo\nsecond line"
	got := runtime.arguments(RunRequest{Prompt: []byte(prompt), Permission: domain.PermissionReadOnly}, "/tmp/schema.json")
	want := []string{"--prompt", prompt, "--schema-file", "/tmp/schema.json", "--approval-mode", "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v\nwant %#v", got, want)
	}
	got = runtime.arguments(RunRequest{Prompt: []byte("p"), Model: "pro", ReasoningEffort: "high", Permission: domain.PermissionReadOnly}, "/s")
	if joined := strings.Join(got, " "); !strings.Contains(joined, "--model pro --effort=high") {
		t.Fatalf("set values must be passed: %v", got)
	}
	if runtime.promptOnStdin() || runtime.Capabilities().WorktreeWrite || runtime.Capabilities().MCPDisabled {
		t.Fatalf("capabilities = %#v", runtime.Capabilities())
	}
	if _, err := built.Start(t.Context(), RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("p"), Permission: domain.PermissionWorktree, Budget: domain.Budget{WallTimeSeconds: 5}}, func(domain.RunEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a runtime without write arguments must refuse a write step: %v", err)
	}

	writable := customSpec()
	writable.WriteArgs = []string{"--approval-mode", "auto-edit"}
	built, err = New(writable)
	if err != nil {
		t.Fatal(err)
	}
	got = built.(*commandRuntime).arguments(RunRequest{Prompt: []byte("p"), Permission: domain.PermissionWorktree}, "/s")
	if joined := strings.Join(got, " "); !strings.HasSuffix(joined, "--approval-mode auto-edit") || strings.Contains(joined, "plan") {
		t.Fatalf("write steps get the write arguments only: %v", got)
	}
}

func TestLastJSONObjectSkipsProseAndKeepsTheFinalAnswer(t *testing.T) {
	for text, want := range map[string]string{
		"thinking {not json}\n{\"draft\":1}\nFinal:\n```json\n{\"summary\":\"ok\",\"nested\":{\"a\":[1,2]}}\n```\nbye": `{"nested":{"a":[1,2]},"summary":"ok"}`,
		`{"only":true}`:         `{"only":true}`,
		"no objects [1,2] here": "",
		"":                      "",
	} {
		got, ok := lastJSONObject(text)
		if string(got) != want || ok != (want != "") {
			t.Fatalf("lastJSONObject(%q) = %s, %v; want %s", text, got, ok, want)
		}
	}
}
