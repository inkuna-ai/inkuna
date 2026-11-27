package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitIgnoresInkunaOutOnce(t *testing.T) {
	isolate(t)
	repository := cloned(t, "")
	initOnce := func() map[string]any {
		t.Helper()
		var output bytes.Buffer
		if err := run([]string{"init", repository}, nil, &output); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("%v: %s", err, output.String())
		}
		return result
	}
	if result := initOnce(); result["added"] != true || result["ignored"] != ".inkuna/out" || !strings.HasSuffix(result["gitignore"].(string), string(filepath.Separator)+".gitignore") {
		t.Fatalf("first init: %v", result)
	}
	data, err := os.ReadFile(filepath.Join(repository, ".gitignore"))
	if err != nil || !strings.HasSuffix(string(data), "\n.inkuna/out/\n") {
		t.Fatalf(".gitignore: %q %v", data, err)
	}
	if result := initOnce(); result["added"] != false {
		t.Fatalf("a second init changes nothing: %v", result)
	}
	if err := run([]string{"init", repository, "extra"}, nil, &bytes.Buffer{}); err == nil || err.Error() != initUsage {
		t.Fatalf("too many arguments: %v", err)
	}
}
