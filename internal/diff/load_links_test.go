package diff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// git writes a type change as two sections for one path, the old version
// deleted and the new one added; the patch has both.
func TestAFileReplacedByASymlinkKeepsBothHalvesOfItsPatch(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("file", map[string]string{"link": "one\ntwo\n"})
	if err := os.Remove(filepath.Join(repo.path, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(repo.path, "link")); err != nil {
		t.Fatal(err)
	}
	tip := repo.commit("symlink", nil)
	patch, err := Patch(context.Background(), repo.path, Range{From: base, To: tip}, Review{}, "link", false)
	if err != nil {
		t.Fatal(err)
	}
	if patch.File.Status != StatusModified || patch.File.OldMode != "100644" || patch.File.NewMode != "120000" {
		t.Fatalf("type change %+v", patch.File)
	}
	var kinds []LineKind
	for _, hunk := range patch.Hunks {
		for _, line := range hunk.Lines {
			kinds = append(kinds, line.Kind)
		}
	}
	if len(patch.Hunks) != 2 || len(kinds) != 3 || kinds[0] != LineDeleted || kinds[2] != LineAdded || patch.File.Additions != 1 || patch.File.Deletions != 2 {
		t.Fatalf("hunks %+v, file %+v", patch.Hunks, patch.File)
	}
}
