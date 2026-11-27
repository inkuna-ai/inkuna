package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// StrictSchema rewrites an output schema into the strict form OpenAI's
// structured outputs require, which is how Codex sends --output-schema:
// every object closes with additionalProperties false and lists every
// property in required, and a property that was optional becomes required
// but nullable. Go decodes a null as the zero value, the same as a missing
// key, so the decoders read either form. Keys keep their order, since the
// model writes properties in the order the schema lists them.
func StrictSchema(schema []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(schema))
	decoder.UseNumber()
	value, err := decodeOrdered(decoder)
	if err != nil {
		return nil, fmt.Errorf("invalid output schema: %w", err)
	}
	switch _, err := decoder.Token(); {
	case err == nil:
		return nil, errors.New("invalid output schema: more than one JSON value")
	case err != io.EOF:
		return nil, fmt.Errorf("invalid output schema: %w", err)
	}
	root, ok := value.(*orderedObject)
	if !ok {
		return nil, errors.New("invalid output schema: not a JSON object")
	}
	tighten(root)
	return json.Marshal(root)
}

// tighten makes one schema and every schema inside it strict. The
// subschemas are the values under the keywords that hold them, never an
// enum or a default, which only look like schemas.
func tighten(schema *orderedObject) {
	for _, keyword := range []string{"properties", "$defs", "definitions"} {
		if named, ok := schema.values[keyword].(*orderedObject); ok {
			for _, key := range named.keys {
				tightenAny(named.values[key])
			}
		}
	}
	for _, keyword := range []string{"items", "anyOf", "allOf", "oneOf"} {
		tightenAny(schema.values[keyword])
	}
	properties, hasProperties := schema.values["properties"].(*orderedObject)
	if schema.values["type"] != "object" && !hasProperties {
		return
	}
	if properties == nil {
		properties = &orderedObject{values: map[string]any{}}
		schema.set("properties", properties)
	}
	required := map[string]bool{}
	if names, ok := schema.values["required"].([]any); ok {
		for _, name := range names {
			if name, ok := name.(string); ok {
				required[name] = true
			}
		}
	}
	all := make([]any, 0, len(properties.keys))
	for _, key := range properties.keys {
		if !required[key] {
			properties.values[key] = &orderedObject{keys: []string{"anyOf"}, values: map[string]any{
				"anyOf": []any{properties.values[key], &orderedObject{keys: []string{"type"}, values: map[string]any{"type": "null"}}},
			}}
		}
		all = append(all, key)
	}
	schema.set("required", all)
	schema.set("additionalProperties", false)
}

// tightenAny tightens a subschema, or each of a list of them.
func tightenAny(value any) {
	switch value := value.(type) {
	case *orderedObject:
		tighten(value)
	case []any:
		for _, item := range value {
			tightenAny(item)
		}
	}
}

// orderedObject is a decoded JSON object that remembers the order of its
// keys, which a map forgets.
type orderedObject struct {
	keys   []string
	values map[string]any
}

// set replaces a key's value where it stands, or adds the key at the end.
func (object *orderedObject) set(key string, value any) {
	if _, ok := object.values[key]; !ok {
		object.keys = append(object.keys, key)
	}
	object.values[key] = value
}

func (object *orderedObject) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range object.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(object.values[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(name)
		buffer.WriteByte(':')
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// decodeOrdered reads one JSON value, objects as orderedObjects.
func decodeOrdered(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := &orderedObject{values: map[string]any{}}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			value, err := decodeOrdered(decoder)
			if err != nil {
				return nil, err
			}
			object.set(key.(string), value)
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		items := []any{}
		for decoder.More() {
			item, err := decodeOrdered(decoder)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		_, err := decoder.Token()
		return items, err
	default:
		return token, nil
	}
}
