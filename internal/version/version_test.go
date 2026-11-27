package version

import (
	"encoding/json"
	"testing"
)

// The test binary is built without stamps, as `go run` is.
func TestCurrentWithoutStampsIsDev(t *testing.T) {
	got := Current()
	if got != (Info{Version: Dev, Commit: Dev}) {
		t.Fatalf("Current() = %+v, want dev for both", got)
	}
}

func TestInfoKeepsStamps(t *testing.T) {
	got := info("v1.2.0-3-gabc1234-dirty", "abc1234def")
	if got.Version != "v1.2.0-3-gabc1234-dirty" || got.Commit != "abc1234def" {
		t.Fatalf("info() = %+v, want the stamped values", got)
	}
	if got := info("abc1234", ""); got.Commit != Dev {
		t.Fatalf("an unstamped commit = %q, want %q", got.Commit, Dev)
	}
}

// inkunactl version --json and the update command read this shape.
func TestInfoJSON(t *testing.T) {
	data, err := json.Marshal(info("v1.2.0", "abc1234def"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"version":"v1.2.0","commit":"abc1234def"}`; string(data) != want {
		t.Fatalf("JSON = %s, want %s", data, want)
	}
}

func TestString(t *testing.T) {
	if got, want := info("v1.2.0", "abc1234def").String(), "v1.2.0 (commit abc1234def)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
