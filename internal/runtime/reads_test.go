package runtime

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// recordedReads replays a recorded event stream through ExtractReads. The
// fixtures are real output of each CLI run with Inkuna's flags against a
// repository at /work/bakery, asked to Read docs/guide.md, cat
// docs/notes/setup.md, search docs for TODO, and read src/app.go.
func recordedReads(t *testing.T, kind domain.Runtime) []domain.FileRead {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "reads", string(kind)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var reads []domain.FileRead
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		var payload map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		reads = append(reads, ExtractReads(kind, payload)...)
	}
	return reads
}

func TestExtractReadsFromRecordedStreams(t *testing.T) {
	read := func(tool, path string) domain.FileRead { return domain.FileRead{Tool: tool, Paths: []string{path}} }
	for kind, want := range map[domain.Runtime][]domain.FileRead{
		// Claude's Read and Grep tools, and a Bash cat.
		domain.RuntimeClaude: {
			read("Read", "/work/bakery/docs/guide.md"),
			read("shell:cat", "docs/notes/setup.md"),
			read("Grep", "docs"),
			read("Read", "/work/bakery/src/app.go"),
		},
		// Codex reads through zsh -lc, reported once each command completes.
		domain.RuntimeCodex: {
			read("shell:cat", "docs/guide.md"),
			read("shell:cat", "docs/notes/setup.md"),
			read("shell:rg", "docs"),
			read("shell:cat", "src/app.go"),
		},
		// Copilot's view and grep tools, and a bash cat.
		domain.RuntimeCopilot: {
			read("view", "/work/bakery/docs/guide.md"),
			read("shell:cat", "docs/notes/setup.md"),
			read("grep", "docs"),
			read("view", "/work/bakery/src/app.go"),
		},
	} {
		if got := recordedReads(t, kind); !reflect.DeepEqual(got, want) {
			t.Errorf("%s reads = %#v\nwant %#v", kind, got, want)
		}
	}
}

func TestExtractReadsIgnoresOtherRuntimesAndEvents(t *testing.T) {
	for kind, payload := range map[domain.Runtime]string{
		domain.RuntimeClaude:  `{"type":"user","message":{"content":[{"type":"tool_result","content":"# Guide"}]}}`,
		domain.RuntimeCodex:   `{"type":"item.started","item":{"type":"command_execution","command":"/bin/zsh -lc 'cat docs/guide.md'","exit_code":null}}`,
		domain.RuntimeCopilot: `{"type":"tool.execution_complete","data":{"toolCallId":"1","success":true}}`,
		"aider":               `{"type":"tool","name":"Read","file_path":"docs/guide.md"}`,
	} {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatal(err)
		}
		if reads := ExtractReads(kind, decoded); len(reads) != 0 {
			t.Errorf("%s %s read %#v", kind, payload, reads)
		}
	}
	failed := map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "command": "cat docs/missing.md", "exit_code": float64(1)}}
	if reads := ExtractReads(domain.RuntimeCodex, failed); len(reads) != 0 {
		t.Errorf("a failed command read %#v", reads)
	}
	argv := map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "command": []any{"bash", "-lc", "head -n 5 docs/a.md"}, "exit_code": float64(0)}}
	if reads := ExtractReads(domain.RuntimeCodex, argv); !reflect.DeepEqual(reads, []domain.FileRead{{Tool: "shell:head", Paths: []string{"docs/a.md"}}}) {
		t.Errorf("an argv command read %#v", reads)
	}
}

func TestShellReads(t *testing.T) {
	type reads = []domain.FileRead
	for command, want := range map[string]reads{
		"cat docs/a.md docs/b.md":                     {{Tool: "shell:cat", Paths: []string{"docs/a.md", "docs/b.md"}}},
		`sed -n '1,200p' docs/a.md`:                   {{Tool: "shell:sed", Paths: []string{"docs/a.md"}}},
		`sed -e 's/a/b/' docs/a.md`:                   {{Tool: "shell:sed", Paths: []string{"docs/a.md"}}},
		`sed -i 's/a/b/' docs/a.md`:                   nil,
		"head -n 20 docs/a.md":                        {{Tool: "shell:head", Paths: []string{"docs/a.md"}}},
		"tail -50 docs/a.md":                          {{Tool: "shell:tail", Paths: []string{"docs/a.md"}}},
		`rg -n "TODO" docs`:                           {{Tool: "shell:rg", Paths: []string{"docs"}}},
		`rg -g '*.md' -e TODO docs/a.md`:              {{Tool: "shell:rg", Paths: []string{"docs/a.md"}}},
		"rg --files docs":                             {{Tool: "shell:rg", Paths: []string{"docs"}}},
		"grep -rn pattern docs/ notes.md":             {{Tool: "shell:grep", Paths: []string{"docs/", "notes.md"}}},
		"grep -rn pattern":                            nil,
		"less -- -odd.md":                             {{Tool: "shell:less", Paths: []string{"-odd.md"}}},
		"cd docs && cat a.md":                         {{Tool: "shell:cat", Paths: []string{"docs/a.md"}}},
		"cd /work/bakery/docs; nl a.md":               {{Tool: "shell:nl", Paths: []string{"/work/bakery/docs/a.md"}}},
		"cd $HOME && cat a.md":                        nil,
		`bash -lc 'cat "docs/my notes.md" | head -5'`: {{Tool: "shell:cat", Paths: []string{"docs/my notes.md"}}},
		"wc -l < docs/a.md":                           {{Tool: "shell:wc", Paths: []string{"docs/a.md"}}},
		"cat docs/a.md 2>/dev/null > out.txt":         {{Tool: "shell:cat", Paths: []string{"docs/a.md"}}},
		"FOO=1 cat docs/a.md":                         {{Tool: "shell:cat", Paths: []string{"docs/a.md"}}},
		"cat docs/*.md":                               nil,
		"cat $FILE":                                   nil,
		"echo hi > docs/a.md":                         nil,
		"go test ./...":                               nil,
		"":                                            nil,
	} {
		if got := shellReads(command, ""); !reflect.DeepEqual(got, want) {
			t.Errorf("shellReads(%q) = %#v, want %#v", command, got, want)
		}
	}
}

// TestShellReadsFollowsTheDirectory checks that a cd counts only inside the
// shell it ran in: a nested shell starts in its parent's directory, and a
// subshell's cd does not reach the commands after it.
func TestShellReadsFollowsTheDirectory(t *testing.T) {
	type reads = []domain.FileRead
	cat := func(paths ...string) domain.FileRead { return domain.FileRead{Tool: "shell:cat", Paths: paths} }
	for command, want := range map[string]reads{
		"cd docs && sh -c 'cat guide.md'":                {cat("docs/guide.md")},
		`cd docs && bash -lc "cd drafts && cat a.md"`:    {cat("docs/drafts/a.md")},
		"cd docs && bash -c 'cd drafts' && cat a.md":     {cat("docs/a.md")},
		"cd $HOME && sh -c 'cat guide.md'":               nil,
		"(cd docs; cat a.md); cat b.md":                  {cat("docs/a.md"), cat("b.md")},
		"cd docs && (cd drafts && cat a.md) && cat b.md": {cat("docs/drafts/a.md"), cat("docs/b.md")},
		"echo $(cd docs; pwd); cat b.md":                 {cat("b.md")},
		"cd docs | cat a.md; cat b.md":                   {cat("a.md"), cat("b.md")},
		"cd docs & cat a.md":                             {cat("a.md")},
		"cd docs && cat a.md | head -5":                  {cat("docs/a.md")},
		// zsh keeps the last command of a pipeline in the current shell and
		// bash does not, so where cat runs is unknown.
		"true | cd docs; cat a.md": nil,
	} {
		if got := shellReads(command, ""); !reflect.DeepEqual(got, want) {
			t.Errorf("shellReads(%q) = %#v, want %#v", command, got, want)
		}
	}
	if got := shellReads("cat guide.md", "docs"); !reflect.DeepEqual(got, reads{cat("docs/guide.md")}) {
		t.Errorf("a shell started in docs read %#v", got)
	}
}
