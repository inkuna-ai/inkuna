package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	inkunagithub "github.com/inkuna-ai/inkuna/internal/github"
	"github.com/inkuna-ai/inkuna/internal/linear"
)

// trackerRepository is a checkout whose stories are GitHub issues of
// acme/app, with the tracker settings given.
func trackerRepository(t *testing.T, trackerYAML string) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:Acme/App.git"}} {
		if err := runGit(root, args...); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"branching.yaml": "version: 1\ntracker: github\nbranch: { base: main, pattern: \"{type}/{story}-{slug}\" }\n"}
	if trackerYAML != "" {
		files["tracker.yaml"] = trackerYAML
	}
	if err := os.MkdirAll(filepath.Join(root, ".inkuna"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, ".inkuna", name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// signedInGitHub answers gh auth status as octocat, signed in to
// github.com, and every other command with output, keeping those calls.
type signedInGitHub struct {
	output []byte
	calls  [][]string
}

func (runner *signedInGitHub) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) > 1 && args[0] == "auth" && args[1] == "status" {
		return []byte(`{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat"}]}}`), nil
	}
	runner.calls = append(runner.calls, args)
	return runner.output, nil
}

// trackerRun is a service with a live run whose step run can hold events.
func trackerRun(t *testing.T, root string) *Service {
	t.Helper()
	service := workflowTestService(t, root, &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	if err := service.store.StartRun(context.Background(), "step-run", "", spec.ID, spec.Steps[0].ID, 1, domain.RuntimeCodex, ""); err != nil {
		t.Fatal(err)
	}
	// Finished, so shutting the service down has no step to cancel.
	service.runs["execution"] = &liveExecution{view: WorkflowRunView{ID: "execution", State: "completed"}}
	return service
}

func TestARunsTrackerWritesAreOnItsTimeline(t *testing.T) {
	root := trackerRepository(t, "version: 1\nwrites: [comment, status]\ngithub: { project: { owner: acme, number: 3 } }\n")
	service := trackerRun(t, root)
	gh := &signedInGitHub{output: []byte("https://github.com/acme/app/issues/12#issuecomment-1\n")}
	service.github = gh
	// This machine keeps comments and turns status writes off.
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "app", Path: root, Tracker: &config.TrackerOverride{Writes: &[]domain.TrackerKind{domain.TrackerComment}}}}
	ctx := context.Background()
	guard, err := service.storyTracker(ctx, root, "12", "execution", "step-run")
	if err != nil {
		t.Fatal(err)
	}
	url, err := guard.Comment(ctx, "12", "Looks good", "")
	if err != nil || url != "https://github.com/acme/app/issues/12#issuecomment-1" {
		t.Fatalf("Comment = %q, %v", url, err)
	}
	if want := []string{"issue", "comment", "12", "--repo", "github.com/acme/app", "--body", "Looks good"}; len(gh.calls) != 1 || !reflect.DeepEqual(gh.calls[0], want) {
		t.Fatalf("gh calls = %v", gh.calls)
	}
	var refusal domain.TrackerRefusal
	if err := guard.SetStatus(ctx, "12", "Done"); !errors.As(err, &refusal) {
		t.Fatalf("SetStatus = %v", err)
	}
	if len(gh.calls) != 1 {
		t.Fatalf("a status the machine turned off reached gh: %v", gh.calls)
	}
	view, err := service.WorkflowRun("execution")
	if err != nil {
		t.Fatal(err)
	}
	if !eventSaid(view, "Wrote to GitHub issue 12: comment: Looks good") || !eventSaid(view, "Did not write to GitHub issue 12 (status → Done): inkuna.yaml turns status off") {
		t.Fatalf("timeline = %+v", view.Events)
	}
	// The run view leaves payloads out; the stored events keep the write.
	rows, err := service.store.DB().QueryContext(ctx, `SELECT event_json FROM events WHERE run_id = ? ORDER BY sequence`, "step-run")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	outcomes := make([]string, 0, 2)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		var event struct {
			Payload struct {
				TrackerWrite domain.TrackerWrite `json:"trackerWrite"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatal(err)
		}
		if event.Payload.TrackerWrite.Issue != "12" {
			t.Fatalf("a stored event does not carry the write: %s", data)
		}
		outcomes = append(outcomes, event.Payload.TrackerWrite.Outcome)
	}
	if !reflect.DeepEqual(outcomes, []string{domain.TrackerApplied, domain.TrackerRefused}) {
		t.Fatalf("stored outcomes = %v", outcomes)
	}
	if last, ok := service.lastTrackerWrite("github"); !ok || last.Outcome != domain.TrackerRefused {
		t.Fatalf("last write = %+v", last)
	}
}

func TestARepositoryWithoutTrackerSettingsWritesNothing(t *testing.T) {
	root := trackerRepository(t, "")
	service := trackerRun(t, root)
	gh := &recordingGitHub{}
	service.github = gh
	ctx := context.Background()
	guard, err := service.storyTracker(ctx, root, "12", "execution", "step-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Report(ctx, "Completed", ""); err == nil {
		t.Fatal("a repository without tracker.yaml was written to")
	}
	view, _ := service.WorkflowRun("execution")
	if len(gh.calls) != 0 || !eventSaid(view, "the repository has no .inkuna/tracker.yaml, so nothing is written") {
		t.Fatalf("gh calls = %v, timeline = %+v", gh.calls, view.Events)
	}
	broken := trackerRepository(t, "version: 1\nwrite: [comment]\n")
	if _, err := service.storyTracker(ctx, broken, "12", "execution", "step-run"); err == nil || !strings.Contains(err.Error(), "field write not found") {
		t.Fatalf("an invalid tracker.yaml = %v", err)
	}
}

func TestConfigurationShowsEachRepositorysTrackerSettings(t *testing.T) {
	root := trackerRepository(t, "version: 1\nwrites: [comment, create]\nupdates: { start: true, result: true }\ngithub: { project: { owner: acme, number: 3 } }\n")
	off := false
	service := &Service{config: config.Config{File: config.DefaultFile()}}
	service.config.File.Repositories = []config.RepositoryConfig{
		{Name: "app", Path: root, Tracker: &config.TrackerOverride{Updates: config.TrackerUpdatesOverride{Start: &off}}},
		{Name: "bare", Path: t.TempDir()},
	}
	views := service.configurationView().Trackers
	if len(views) != 2 {
		t.Fatalf("trackers = %+v", views)
	}
	app := views[0]
	if app.Tracker != "github" || !app.Exists || app.Project != "acme/3" || !app.Ceiling.AllowsUpdate(domain.UpdateStart) || app.Effective.AllowsUpdate(domain.UpdateStart) ||
		!app.Effective.AllowsUpdate(domain.UpdateResult) || !reflect.DeepEqual(app.Effective.Writes, []domain.TrackerKind{"comment", "create"}) {
		t.Fatalf("app = %+v", app)
	}
	if bare := views[1]; bare.Exists || !strings.Contains(bare.Note, "names no tracker") || bare.Effective.Writes == nil {
		t.Fatalf("bare = %+v", bare)
	}
}

func TestGitHubWriteAccessReadsTheTokensScopes(t *testing.T) {
	root := trackerRepository(t, "version: 1\nupdates: { start: true }\ngithub: { project: { owner: acme, number: 3 } }\n")
	service := &Service{config: config.Config{File: config.DefaultFile()}}
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "app", Path: root}}
	for _, test := range []struct {
		scopes []string
		status string
		detail string
	}{
		{[]string{"repo", "project", "read:org"}, writeAccessFull, "set a GitHub Project's Status"},
		{[]string{"repo", "read:org"}, writeAccessPartial, "no project scope, which app need"},
		{[]string{"public_repo", "project"}, writeAccessPartial, "public repositories only"},
		{[]string{"read:org"}, writeAccessReadOnly, "gh auth refresh -s repo"},
		{nil, writeAccessUnknown, "lists no scopes"},
	} {
		view := service.githubWriteAccess(inkunagithub.AuthStatus{Authenticated: true, Scopes: test.scopes})
		if view == nil || view.Status != test.status || !strings.Contains(view.Detail, test.detail) {
			t.Errorf("scopes %v: %+v", test.scopes, view)
		}
	}
	if view := service.githubWriteAccess(inkunagithub.AuthStatus{}); view != nil {
		t.Fatalf("a signed-out gh was given write access: %+v", view)
	}
	// Without a repository that sets a Project's Status, the project scope
	// is only mentioned.
	service.config.File.Repositories = nil
	if view := service.githubWriteAccess(inkunagithub.AuthStatus{Authenticated: true, Scopes: []string{"repo"}}); view.Status != writeAccessFull {
		t.Fatalf("view = %+v", view)
	}
}

func TestDescribeLinearAccess(t *testing.T) {
	scope := map[string]string{"update": "Linear API: Invalid scope: write required", "create": "Linear API: Invalid scope: write required"}
	for _, test := range []struct {
		access linear.WriteAccess
		err    error
		status string
		detail string
	}{
		{linear.WriteAccess{Update: linear.AccessAllowed, Comment: linear.AccessAllowed, Create: linear.AccessAllowed}, nil, writeAccessFull, "can change issues, comment, and create issues"},
		{linear.WriteAccess{Update: linear.AccessDenied, Comment: linear.AccessDenied, Create: linear.AccessDenied, Reasons: scope}, nil, writeAccessReadOnly, "can only read"},
		{linear.WriteAccess{Update: linear.AccessDenied, Comment: linear.AccessAllowed, Create: linear.AccessDenied, Reasons: scope}, nil, writeAccessPartial, "can comment, but not change issues or create issues. Linear said: Linear API: Invalid scope: write required."},
		{linear.WriteAccess{Update: linear.AccessUnknown, Comment: linear.AccessAllowed, Create: linear.AccessAllowed}, nil, writeAccessUnknown, "did not say whether the key can change issues"},
		{linear.WriteAccess{}, errors.New("linear returned HTTP 503"), writeAccessUnknown, "HTTP 503"},
	} {
		view := describeLinearAccess(test.access, test.err)
		if view.Status != test.status || !strings.Contains(view.Detail, test.detail) {
			t.Errorf("%+v: %+v", test.access, view)
		}
	}
}

// GitHub writes need gh's signed-in account, to tell what Inkuna wrote from
// what others did; signed out, every write is refused, saying why.
func TestGitHubWritesNeedGHsAccount(t *testing.T) {
	root := trackerRepository(t, "version: 1\nwrites: [comment]\n")
	service := trackerRun(t, root)
	service.github = serviceGitHubRunner{output: []byte(`{"hosts":{"github.com":[]}}`)}
	ctx := context.Background()
	guard, err := service.storyTracker(ctx, root, "12", "execution", "step-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Comment(ctx, "12", "Looks good", ""); err == nil || !strings.Contains(err.Error(), "gh is not signed in to github.com") {
		t.Fatalf("Comment = %v", err)
	}
}

// An unclear answer, such as a rate limit, is asked again next time; a
// definite one is kept until the key changes.
func TestOnlyADefiniteLinearAnswerIsKept(t *testing.T) {
	answer := `{"errors":[{"message":"Rate limit exceeded","extensions":{"code":"RATELIMITED"}}]}`
	requests := 0
	service := &Service{
		config: config.Config{LinearEndpoint: "https://linear.test/graphql", LinearTimeout: time.Second},
		http: &http.Client{Transport: serviceRoundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(answer))}, nil
		})},
	}
	client, err := service.linearClient("lin_api_test")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if view := service.linearWriteAccess(client, 0); view == nil || view.Status != writeAccessUnknown {
			t.Fatalf("view = %+v", view)
		}
	}
	if requests != 6 {
		t.Fatalf("an unclear answer was kept: %d requests", requests)
	}
	answer = `{"errors":[{"message":"Entity not found: Issue","extensions":{"code":"INVALID_INPUT"}}]}`
	for range 2 {
		if view := service.linearWriteAccess(client, 0); view == nil || view.Status != writeAccessFull {
			t.Fatalf("view = %+v", view)
		}
	}
	if requests != 9 {
		t.Fatalf("a definite answer was not kept: %d requests", requests)
	}
}
