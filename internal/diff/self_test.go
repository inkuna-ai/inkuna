package diff

import "testing"

// Inkuna's own review config is valid, so its Changes view never fails on
// it, and it collapses the stylesheet this package generates.
func TestInkunaReviewConfigIsValid(t *testing.T) {
	review, err := LoadReview("../..")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := newGenerated(review)
	if err != nil {
		t.Fatal(err)
	}
	if !rules.flags(StylesheetPath) {
		t.Fatalf("%s should mark %s generated", ReviewPath, StylesheetPath)
	}
}
