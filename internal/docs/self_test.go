package docs

import "testing"

// Inkuna's own docs follow the contract they document.
func TestInkunaDocsFollowTheContract(t *testing.T) {
	index, err := Discover("../..", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index) == 0 {
		t.Fatal("expected Inkuna's docs to be indexed")
	}
	for _, doc := range index {
		if len(doc.Problems) > 0 {
			t.Errorf("%s: %v", doc.Path, doc.Problems)
		}
	}
}
