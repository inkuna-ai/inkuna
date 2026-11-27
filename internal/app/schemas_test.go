package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// TestEverySchemaInkunaSendsHasAStrictFormOpenAIAccepts checks the rules
// OpenAI's strict structured outputs enforce, which refused all three
// schemas before Codex was sent their strict form: the root is an object,
// and every object closes and requires every one of its properties.
func TestEverySchemaInkunaSendsHasAStrictFormOpenAIAccepts(t *testing.T) {
	asking := handoffSchemaFor(&domain.TrackerGrant{Tracker: "linear", Kinds: domain.TrackerKinds()})
	for name, schema := range map[string]string{"handoff": handoffSchema, "handoff with tracker changes": asking, "staffing": orchestrator.Schema, "branching": orchestrator.BranchingSchema} {
		strict, err := agentruntime.StrictSchema([]byte(schema))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var root map[string]any
		if err := json.Unmarshal(strict, &root); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if root["type"] != "object" || root["anyOf"] != nil {
			t.Fatalf("%s: the root must be an object: %s", name, strict)
		}
		checkStrict(t, name, root)
	}
}

func checkStrict(t *testing.T, path string, schema map[string]any) {
	t.Helper()
	properties, _ := schema["properties"].(map[string]any)
	if schema["type"] == "object" || properties != nil {
		if schema["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties must be false", path)
		}
		required := map[string]bool{}
		names, _ := schema["required"].([]any)
		for _, name := range names {
			required[name.(string)] = true
		}
		for key := range properties {
			if !required[key] {
				t.Errorf("%s: required must include %q", path, key)
			}
		}
	}
	for key, property := range properties {
		checkStrict(t, path+"."+key, property.(map[string]any))
	}
	if items, ok := schema["items"].(map[string]any); ok {
		checkStrict(t, path+"[]", items)
	}
	alternatives, _ := schema["anyOf"].([]any)
	for _, alternative := range alternatives {
		checkStrict(t, path, alternative.(map[string]any))
	}
}

// A model held to the strict schema sends every field of a change, null
// where it sets none, which reads as a change that leaves them out.
func TestAStrictTrackerChangeReadsLikeOneWithoutItsNulls(t *testing.T) {
	strict := `{"version":1,"status":"completed","summary":"done","trackerChanges":[{"kind":"comment","issue":"story","body":"Done","status":null,"title":null,"description":null,"addLabels":null,"removeLabels":null,"estimate":null,"priority":null,"assignee":null,"cycle":null,"milestone":null,"parent":null}]}`
	handoff := parseHandoff(json.RawMessage(strict))
	if len(handoff.TrackerChanges) != 1 {
		t.Fatalf("changes = %+v", handoff.TrackerChanges)
	}
	if change := handoff.TrackerChanges[0].Normalized(); change.Problem() != "" || !reflect.DeepEqual(change, domain.TrackerChange{Kind: domain.TrackerComment, Issue: "story", Body: "Done"}) {
		t.Fatalf("change = %+v (%s)", change, change.Problem())
	}
}

func TestAHandoffWithNullsReadsLikeOneWithoutThem(t *testing.T) {
	strict := `{"version":1,"status":"completed","summary":"done","changes":[{"path":"a.go","summary":null}],"verification":null,"decisions":null,"risks":null,"outputs":null,"messages":null,"humanInput":null}`
	open := `{"version":1,"status":"completed","summary":"done","changes":[{"path":"a.go"}]}`
	if got, want := parseHandoff(json.RawMessage(strict)), parseHandoff(json.RawMessage(open)); !reflect.DeepEqual(got, want) {
		t.Fatalf("strict handoff %+v, want %+v", got, want)
	}
}
