package tracker

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An adapter writes to a tracker with no check and no record, so only this
// package builds one, behind a Guard. A call to linear.NewTracker or
// github.NewTracker anywhere else in Inkuna's code fails the build.
func TestOnlyTheGuardBuildsATrackerAdapter(t *testing.T) {
	root := filepath.Join("..", "..")
	constructor := regexp.MustCompile(`\b\w+\.NewTracker\(`)
	allowed := map[string]bool{
		filepath.Join("internal", "tracker"): true,
		filepath.Join("internal", "linear"):  true,
		filepath.Join("internal", "github"):  true,
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && relative != "." || entry.Name() == "node_modules" || entry.Name() == "frontend" || allowed[relative] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if constructor.Match(data) {
			t.Errorf("%s builds a tracker adapter; open one with tracker.OpenGuard instead", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
