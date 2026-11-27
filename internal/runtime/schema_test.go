package runtime

import "testing"

func TestStrictSchemaClosesEveryObjectAndMakesOptionalKeysNullable(t *testing.T) {
	schema := `{
  "type": "object",
  "required": ["status"],
  "properties": {
    "status": {"type": "string", "enum": ["done", "failed"]},
    "changes": {"type": "array", "items": {"type": "object", "properties": {"path": {"type": "string"}, "summary": {"type": "string"}}}},
    "score": {"type": "integer", "minimum": 1, "maximum": 10},
    "target": {"anyOf": [{"$ref": "#/$defs/file"}, {"type": "string"}]},
    "example": {"type": "object", "default": {"kept": "as written"}, "properties": {"note": {"type": "string"}}, "required": ["note"]}
  },
  "$defs": {"file": {"type": "object", "properties": {"name": {"type": "string"}}}},
  "additionalProperties": true
}`
	strict, err := StrictSchema([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	// The keys stay in the order they were written, and a default, which is
	// a value rather than a schema, is left alone.
	want := `{"type":"object","required":["status","changes","score","target","example"],"properties":{` +
		`"status":{"type":"string","enum":["done","failed"]},` +
		`"changes":{"anyOf":[{"type":"array","items":{"type":"object","properties":{` +
		`"path":{"anyOf":[{"type":"string"},{"type":"null"}]},"summary":{"anyOf":[{"type":"string"},{"type":"null"}]}},` +
		`"required":["path","summary"],"additionalProperties":false}},{"type":"null"}]},` +
		`"score":{"anyOf":[{"type":"integer","minimum":1,"maximum":10},{"type":"null"}]},` +
		`"target":{"anyOf":[{"anyOf":[{"$ref":"#/$defs/file"},{"type":"string"}]},{"type":"null"}]},` +
		`"example":{"anyOf":[{"type":"object","default":{"kept":"as written"},"properties":{"note":{"type":"string"}},"required":["note"],"additionalProperties":false},{"type":"null"}]}},` +
		`"$defs":{"file":{"type":"object","properties":{"name":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["name"],"additionalProperties":false}},` +
		`"additionalProperties":false}`
	if string(strict) != want {
		t.Fatalf("strict schema\n got %s\nwant %s", strict, want)
	}
	again, err := StrictSchema(strict)
	if err != nil || string(again) != want {
		t.Fatalf("a strict schema must come back unchanged: %s, %v", again, err)
	}
}

func TestStrictSchemaRefusesWhatIsNotOneObject(t *testing.T) {
	for _, schema := range []string{`not json`, `[{"type":"object"}]`, `{"type":"object"} {}`, `{"type":"object"} garbage`, `{"type":"object"}}`, `{"type":`} {
		if strict, err := StrictSchema([]byte(schema)); err == nil {
			t.Errorf("%s: got %s, want an error", schema, strict)
		}
	}
	if _, err := StrictSchema([]byte("{\"type\":\"object\"}\n")); err != nil {
		t.Errorf("trailing whitespace is not an error: %v", err)
	}
}
