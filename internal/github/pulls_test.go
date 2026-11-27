package github

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const branchPulls = `[{"number":14,"title":"Retry the import","url":"https://github.com/acme/app/pull/14","state":"OPEN","mergedAt":null},
{"number":12,"title":"Fix login","url":"https://github.com/acme/app/pull/12","state":"MERGED","mergedAt":"2026-11-01T10:00:00Z"},
{"number":9,"title":"First try","url":"https://github.com/acme/app/pull/9","state":"CLOSED","mergedAt":null}]`

func TestPullRequestsFromABranch(t *testing.T) {
	stub := &stubGH{t: t, answers: map[string]func([]string) (string, error){"pr list": reply(branchPulls)}}
	pulls, err := PullRequestsFrom(context.Background(), stub, "github.com/acme/app", "feature/12-login")
	if err != nil || len(pulls) != 3 {
		t.Fatalf("PullRequestsFrom = %+v, %v", pulls, err)
	}
	if want := []string{"pr", "list", "--repo", "github.com/acme/app", "--head", "feature/12-login", "--state", "all", "--limit", "20", "--json", "number,title,url,state,mergedAt"}; !reflect.DeepEqual(stub.calls[0], want) {
		t.Fatalf("gh = %v", stub.calls[0])
	}
	if merged, ok := MergedPull(pulls); !ok || merged.Number != 12 {
		t.Fatalf("MergedPull = %+v, %v", merged, ok)
	}
	if current, ok := CurrentPull(pulls); !ok || current.Number != 14 {
		t.Fatalf("CurrentPull = %+v, %v", current, ok)
	}
	closed := pulls[2:]
	if _, ok := MergedPull(closed); ok {
		t.Fatal("a closed pull request counted as merged")
	}
	if current, ok := CurrentPull(closed); !ok || current.Number != 9 {
		t.Fatalf("without an open one, CurrentPull = %+v, %v", current, ok)
	}
	if _, ok := CurrentPull(nil); ok {
		t.Fatal("a branch without pull requests has one")
	}
}

func TestPullRequestsFromCarriesGHsReason(t *testing.T) {
	stub := &stubGH{t: t, answers: map[string]func([]string) (string, error){
		"pr list": func([]string) (string, error) { return "HTTP 401: Bad credentials", errors.New("exit status 1") },
	}}
	if _, err := PullRequestsFrom(context.Background(), stub, "github.com/acme/app", "feature/12-login"); err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("PullRequestsFrom = %v", err)
	}
	if _, err := PullRequestsFrom(context.Background(), stub, "github.com/acme/app", ""); err == nil {
		t.Fatal("a pull request was looked for without a branch")
	}
}

func TestRecentPullRequestsNameTheirBranches(t *testing.T) {
	stub := &stubGH{t: t, answers: map[string]func([]string) (string, error){"pr list": reply(`[
{"number":15,"title":"Fork fix","url":"https://github.com/acme/app/pull/15","state":"OPEN","headRefName":"main","isCrossRepository":true},
{"number":12,"title":"Fix login","url":"https://github.com/acme/app/pull/12","state":"MERGED","mergedAt":"2026-11-01T10:00:00Z","headRefName":"feature/12-login","isCrossRepository":false}]`)}}
	pulls, err := RecentPullRequests(context.Background(), stub, "github.com/acme/app", 100)
	if err != nil || len(pulls) != 2 {
		t.Fatalf("RecentPullRequests = %+v, %v", pulls, err)
	}
	if want := []string{"pr", "list", "--repo", "github.com/acme/app", "--state", "all", "--search", "sort:updated-desc", "--limit", "100", "--json", "number,title,url,state,mergedAt,headRefName,isCrossRepository"}; !reflect.DeepEqual(stub.calls[0], want) {
		t.Fatalf("gh = %v", stub.calls[0])
	}
	if !pulls[0].IsCrossRepository || pulls[1].HeadRefName != "feature/12-login" || !pulls[1].Merged() {
		t.Fatalf("pulls = %+v", pulls)
	}
	if _, err := RecentPullRequests(context.Background(), stub, "", 100); err == nil {
		t.Fatal("pull requests were listed without a repository")
	}
}
