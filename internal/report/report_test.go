package report

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/artifact"
	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

var update = flag.Bool("update", false, "rewrite testdata/report.golden.html")

// fixture is a stored run on a repository whose commits have fixed dates
// and authors, so its hashes, and the page, come out the same every time.
type fixture struct {
	store     *storage.Store
	artifacts *artifact.Store
	base      string
	commits   []string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Ada Lovelace", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_AUTHOR_DATE=2026-10-26T09:00:00Z",
		"GIT_COMMITTER_NAME=Ada Lovelace", "GIT_COMMITTER_EMAIL=ada@example.com", "GIT_COMMITTER_DATE=2026-10-26T09:00:00Z",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const appBefore = `package app

// Greet says hello.
func Greet(name string) string {
	return "Hello, " + name
}
`

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q")
	write(t, repo, map[string]string{"src/app.go": appBefore, "README.md": "# Demo\n"})
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "Start")
	base := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "switch", "-q", "-c", "feature/greet")
	write(t, repo, map[string]string{
		"src/app.go":    strings.Replace(appBefore, `"Hello, "`, `"Hello there, "`, 1),
		"docs/guide.md": "# Guide\n\nCall Greet.",
		"go.sum":        "example.com/x v1.0.0 h1:abc=\n",
		"src/evil.txt":  "</script><script>alert(1)</script>\n<img src=x onerror=alert(1)>\n",
	})
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "inkuna(implement): Greet warmly")
	first := git(t, repo, "rev-parse", "HEAD")
	write(t, repo, map[string]string{"README.md": "# Demo\n\nNow greets warmly.\n"})
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "Mention it by hand")
	second := git(t, repo, "rev-parse", "HEAD")

	store, err := storage.Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	artifacts, err := artifact.New(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	project := storage.Project{ID: "project", Name: "Demo", Repository: repo, DefaultRuntime: domain.RuntimeClaude}
	if err := store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.TaskSpec{Version: 1, ID: "task", Kind: domain.TaskImplementation, Objective: "Greet warmly", Repository: repo, Budget: domain.Budget{WallTimeSeconds: 60}}
	if err := store.SaveTask(ctx, project.ID, "approved", task); err != nil {
		t.Fatal(err)
	}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{
		{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Agent: "implementer", Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}, Budget: domain.Budget{WallTimeSeconds: 10}},
		{ID: "verify", Title: "Run the checks", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}},
	}}
	if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveExecution(ctx, storage.Execution{ID: "run-1", WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "completed", Title: "Greet warmly", Branch: "feature/greet", BaseCommit: base}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCommit(ctx, storage.CommitRecord{ExecutionID: "run-1", StepID: "implement", Agent: "implementer", Hash: first, Subject: "inkuna(implement): Greet warmly"}); err != nil {
		t.Fatal(err)
	}
	log, err := artifacts.Put("verify-check-2.log", "text/plain", strings.NewReader("=== RUN TestGreet\n\x1b[31m--- FAIL: TestGreet\x1b[0m\n<b>not bold</b>\nFAIL\n"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 26, 9, 5, 0, 0, time.UTC)
	passed, failed := 0, 1
	for _, check := range []storage.CheckRecord{
		{ID: "run-1-verify-0", Position: 0, Argv: []string{"go", "vet", "./..."}, Status: storage.CheckPassed, ExitCode: &passed, StartedAt: started, FinishedAt: started.Add(1500 * time.Millisecond), LogDigest: "a", LogBytes: 0},
		{ID: "run-1-verify-1", Position: 1, Argv: []string{"go", "test", "./..."}, Status: storage.CheckFailed, ExitCode: &failed, StartedAt: started.Add(2 * time.Second), FinishedAt: started.Add(14 * time.Second), LogDigest: log.Digest, LogBytes: log.Size},
	} {
		check.ExecutionID, check.StepID, check.StepRunID = "run-1", "verify", "run-1-verify"
		if err := store.SaveCheck(ctx, check); err != nil {
			t.Fatal(err)
		}
	}
	return fixture{store: store, artifacts: artifacts, base: base, commits: []string{first, second}}
}

// withComments sends comments on the run's lines in round 2 and settles
// their replies: one declined on the changed line of src/app.go, one on the
// line it removed, left without a reply, and one outdated since, resolved
// by its fix. A last one is still a draft.
func withComments(t *testing.T, fixture fixture) {
	t.Helper()
	ctx := context.Background()
	tip := fixture.commits[1]
	threads := []struct {
		id, side, body string
		line           int
	}{
		{"thread-1", domain.SideNew, "Say hello <b>warmly</b>.", 5},
		{"thread-2", domain.SideOld, "Keep the comma?", 5},
		{"thread-3", domain.SideNew, "Name it Hello.", 4},
		{"thread-4", domain.SideNew, "Still a draft.", 3},
	}
	var items []storage.FeedbackItem
	for index, thread := range threads {
		record := storage.CommentThread{ExecutionID: "run-1", ID: thread.id, Path: "src/app.go", Side: thread.side, Line: thread.line, EndLine: thread.line, Base: fixture.base, Commit: tip, Round: 1}
		if err := fixture.store.StartThread(ctx, record, storage.Comment{ID: thread.id + "-comment", Body: thread.body, Target: "implement"}); err != nil {
			t.Fatal(err)
		}
		if index < 3 {
			items = append(items, storage.FeedbackItem{ID: fmt.Sprintf("c%d", index+1), Target: "implement", From: storage.FeedbackFromHuman, Text: thread.body,
				CommentID: thread.id + "-comment", Path: "src/app.go", Side: thread.side, Line: thread.line, EndLine: thread.line, Commit: tip, Excerpt: fmt.Sprintf("> %d %d + line %d", thread.line, thread.line, thread.line)})
		}
	}
	if started, err := fixture.store.StartRound(ctx, "run-1", 1, items); err != nil || !started {
		t.Fatalf("start round 2: %v %v", started, err)
	}
	if _, err := fixture.store.SettleFeedback(ctx, "run-1", 2, "implement", []domain.Addressed{
		{Item: "c1", Status: domain.AddressedDeclined, Note: "Warm enough."}, {Item: "c3", Status: domain.AddressedFixed, Note: "Renamed."},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.OutdateThread(ctx, "run-1", "thread-3", tip, 2); err != nil {
		t.Fatal(err)
	}
}

func render(t *testing.T, fixture fixture, options Options) string {
	t.Helper()
	page, err := Build(context.Background(), fixture.store, fixture.artifacts, "run-1", options)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Render(&out, page); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestThePageMatchesItsGoldenFile(t *testing.T) {
	fixture := newFixture(t)
	withComments(t, fixture)
	got := render(t, fixture, Options{Generated: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)})
	golden := filepath.Join("testdata", "report.golden.html")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/report -update to write it)", err)
	}
	if got != string(want) {
		t.Fatalf("the page differs from %s; run go test ./internal/report -update and review the change", golden)
	}
	if again := render(t, fixture, Options{Generated: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)}); again != got {
		t.Fatal("the same run must render the same page")
	}
	if strings.Contains(render(t, fixture, Options{}), "Made by inkunactl diff at") {
		t.Fatal("a page with no time given holds none")
	}
}

func TestThePageShowsTheRunsFilesCommitsAndChecks(t *testing.T) {
	fixture := newFixture(t)
	got := render(t, fixture, Options{})
	for _, want := range []string{
		"<strong>5 files changed</strong>",
		"1 of 2 checks passed",
		`<span class="tok-string changed"> there</span>`,
		"<span class=\"chip warn\">external</span>",
		"step <b>implement</b> · implementer",
		`<span class="ansi-fg-1">--- FAIL: TestGreet</span>`,
		"&lt;b&gt;not bold&lt;/b&gt;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	// guide.md ends without a newline, marked in both layouts.
	if count := strings.Count(got, `<abbr class="no-newline" title="No newline at end of file">⊘</abbr>`); count != 2 {
		t.Errorf("the missing final newline is marked %d times, want 2", count)
	}
	// go.sum is generated, so it starts collapsed; the others start open.
	if !regexp.MustCompile(`id="file-\d+" data-path="go.sum" data-blob="[0-9a-f]+">\n<details>`).MatchString(got) {
		t.Error("a generated file must start collapsed")
	}
	split := render(t, fixture, Options{Split: true, Scope: diff.Scope{Kind: diff.ScopeCommit, Commit: fixture.commits[1]}})
	if !strings.Contains(split, `<body class="layout-split"`) || !strings.Contains(split, "<strong>1 file changed</strong>") || !strings.Contains(split, "Mention it by hand") {
		t.Fatal("the commit scope opens split on that commit's files")
	}
}

func TestThePageShowsSentCommentsAtTheirLinesAndOutdatedOnesApart(t *testing.T) {
	fixture := newFixture(t)
	withComments(t, fixture)
	got := render(t, fixture, Options{})
	// html/template writes + as &#43;, which reads back the same.
	section := html.UnescapeString(regexp.MustCompile(`(?s)<section class="comments".*?</section>`).FindString(got))
	files := regexp.MustCompile(`(?s)<div class="files">.*</main>`).FindString(got)
	// Each thread at its line shows in both layouts.
	for _, want := range []string{
		`<span class="place">src/app.go line 5</span>`,
		"Say hello &lt;b&gt;warmly&lt;/b&gt;.",
		`<p class="reply r-declined"><b>implement</b> declined: Warm enough.</p>`,
		`<span class="place">src/app.go removed line 5</span>`,
		`<p class="reply r-no-reply"><b>implement</b> did not reply</p>`,
	} {
		if strings.Count(files, want) != 2 {
			t.Errorf("the diff shows %q %d times, want once a layout", want, strings.Count(files, want))
		}
	}
	// The outdated thread is listed apart, with the diff it was sent with.
	for _, want := range []string{
		`<span class="place">src/app.go line 4</span><span class="chip ok">resolved by implement</span><span class="chip">outdated after round 2</span>`,
		`<pre class="excerpt">> 4 4 + line 4</pre>`,
		`<p class="reply r-fixed"><b>implement</b> fixed it: Renamed.</p>`,
	} {
		if !strings.Contains(section, want) || strings.Contains(files, want) {
			t.Errorf("the comments section, not the diff, should show %q", want)
		}
	}
	if strings.Contains(got, "Still a draft.") {
		t.Error("a draft reached the page")
	}
	// On a commit's changes every thread is listed apart.
	commit := render(t, fixture, Options{Scope: diff.Scope{Kind: diff.ScopeCommit, Commit: fixture.commits[0]}})
	listed := regexp.MustCompile(`(?s)<section class="comments".*?</section>`).FindString(commit)
	if strings.Count(listed, `<div class="thread">`) != 3 || strings.Contains(commit, `class="thread-row"`) {
		t.Errorf("a commit's page lists every sent thread apart:\n%s", listed)
	}
}

func TestAThreadUnderItsLinesNamesThemWhereTheBranchMovedThem(t *testing.T) {
	fixture := newFixture(t)
	withComments(t, fixture)
	execution, err := fixture.store.Execution(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	project, err := fixture.store.Project(context.Background(), execution.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	// A commit by hand puts two lines above the commented ones after the
	// threads were last followed.
	repo := project.Repository
	write(t, repo, map[string]string{"src/app.go": "// one\n// two\n" + strings.Replace(appBefore, `"Hello, "`, `"Hello there, "`, 1)})
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "Add two lines on top")
	got := render(t, fixture, Options{})
	files := regexp.MustCompile(`(?s)<div class="files">.*</main>`).FindString(got)
	if count := strings.Count(files, `<span class="place">src/app.go line 7</span>`); count != 2 {
		t.Errorf("the moved thread is named at line 7 %d times, want once a layout", count)
	}
	if strings.Contains(files, `<span class="place">src/app.go line 5</span>`) {
		t.Error("the moved thread still names the line it was on")
	}
	// Removed lines are numbered in the base, so that thread keeps its name.
	if count := strings.Count(files, `<span class="place">src/app.go removed line 5</span>`); count != 2 {
		t.Errorf("the removed-line thread is named %d times, want once a layout", count)
	}
}

func TestAFailedCheckWhoseLogIsGoneStillShows(t *testing.T) {
	fixture := newFixture(t)
	failed := 2
	started := time.Date(2026, 10, 26, 9, 6, 0, 0, time.UTC)
	check := storage.CheckRecord{ID: "run-1-lint-0", ExecutionID: "run-1", StepID: "lint", StepRunID: "run-1-lint", Argv: []string{"golangci-lint", "run"}, Status: storage.CheckFailed, ExitCode: &failed, StartedAt: started, FinishedAt: started.Add(time.Second), LogDigest: strings.Repeat("ab", 32), LogBytes: 10}
	if err := fixture.store.SaveCheck(context.Background(), check); err != nil {
		t.Fatal(err)
	}
	got := render(t, fixture, Options{})
	if !strings.Contains(got, `<code>golangci-lint run</code><span class="muted">lint · failed</span><span class="exit">exit 2</span><span class="exit">its log could not be read</span>`) || !strings.Contains(got, "1 of 3 checks passed") {
		t.Fatal("a check whose log cannot be read keeps its status and exit code, and says so")
	}
	if strings.Contains(got, check.LogDigest) || strings.Contains(got, "no such file") {
		t.Fatal("the note must not name the artifact or its path")
	}
}

func TestLayoutKeepsAMissingFinalNewline(t *testing.T) {
	// The newer version only adds the final newline, so both lines read
	// the same; the marker is the difference.
	unified, split := layout([]diff.Hunk{{Header: "@@ -1 +1 @@", Lines: []diff.Line{
		{Kind: diff.LineDeleted, Old: 1, Text: "end", NoNewline: true},
		{Kind: diff.LineAdded, New: 1, Text: "end"},
	}}})
	if len(unified) != 3 || !unified[1].NoNewline || unified[2].NoNewline {
		t.Fatalf("unified rows %+v", unified)
	}
	if len(split) != 2 || !split[1].Left.NoNewline || split[1].Right.NoNewline {
		t.Fatalf("split rows %+v", split)
	}
}

func TestEveryLogColourHasAStyle(t *testing.T) {
	classes := []string{"ansi-bold"}
	for colour := range 16 {
		classes = append(classes, fmt.Sprintf("ansi-fg-%d", colour), fmt.Sprintf("ansi-bg-%d", colour))
	}
	for _, class := range classes {
		if !regexp.MustCompile(`\.` + class + `[,\s{]`).MatchString(pageStyle) {
			t.Errorf("logLines can write %s, which has no style", class)
		}
	}
}

func TestEverythingFromTheRunIsEscaped(t *testing.T) {
	got := render(t, newFixture(t), Options{})
	script := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllStringSubmatch(got, -1)
	if len(script) != 1 || script[0][1] != pageScript {
		t.Fatalf("the page must hold exactly its own script, found %d", len(script))
	}
	for _, raw := range []string{"<img src=x", "<script>alert", "</script><script>", "<b>not bold"} {
		if strings.Contains(got, raw) {
			t.Errorf("%q reached the page unescaped", raw)
		}
	}
	if !strings.Contains(got, "&lt;img src=x onerror=alert(1)&gt;") || !strings.Contains(got, "&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("the file's markup should show as text")
	}
}

func TestThePageLoadsNothingFromTheNetwork(t *testing.T) {
	got := render(t, newFixture(t), Options{})
	// The template escapes the attribute (' and + among others), and the
	// browser reads it back as the policy.
	content := regexp.MustCompile(`http-equiv="Content-Security-Policy" content="([^"]*)"`).FindStringSubmatch(got)
	want := "default-src 'none'; style-src '" + sha256Source(style) + "'; script-src '" + sha256Source(pageScript) + "'"
	if content == nil || !strings.HasPrefix(html.UnescapeString(content[1]), want) {
		t.Fatalf("the page's policy must allow only its own style and script, by hash: %v", content)
	}
	// The hash holds only if the page carries those exact bytes.
	styles := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindAllStringSubmatch(got, -1)
	if len(styles) != 1 || styles[0][1] != style {
		t.Fatalf("the page must hold exactly its own stylesheet, byte for byte, found %d", len(styles))
	}
	// Escaped markup from the run starts with &lt;, so only real tags match.
	if tag := regexp.MustCompile(`<[a-zA-Z][^>]*\ssrc\s*=`).FindString(got); tag != "" {
		t.Fatalf("no element may have a src: %s", tag)
	}
	for _, match := range regexp.MustCompile(`<[a-z]+[^>]*\shref="([^"]*)"`).FindAllStringSubmatch(got, -1) {
		if !strings.HasPrefix(match[1], "#") {
			t.Errorf("link %q leaves the page", match[1])
		}
	}
	for _, request := range []string{"@import", "url(", "fetch(", "XMLHttpRequest", "WebSocket", "<link", "<iframe", "<object", "<embed"} {
		if strings.Contains(style, request) || strings.Contains(pageScript, request) || strings.Contains(strings.ReplaceAll(got, pageScript, ""), request) {
			t.Errorf("the page holds %q", request)
		}
	}
}

func TestATooLargeFileSaysHowToIncludeIt(t *testing.T) {
	fixture := newFixture(t)
	execution, err := fixture.store.Execution(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	project, err := fixture.store.Project(context.Background(), execution.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	repo := project.Repository
	write(t, repo, map[string]string{"data/big.txt": strings.Repeat("row\n", diff.SoftLines+10)})
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "Add a big file")
	got := render(t, fixture, Options{})
	if !strings.Contains(got, "Run inkunactl diff again with --include-large to include it.") {
		t.Fatal("a too-large file should point at --include-large")
	}
	if large := render(t, fixture, Options{IncludeLarge: true}); strings.Contains(large, "--include-large") || !strings.Contains(large, `<td class="num">20010</td>`) {
		t.Fatal("--include-large should load the file")
	}
}
