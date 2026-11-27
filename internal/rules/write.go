package rules

import (
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"
)

// temporaryPattern names in-progress saves; the dot keeps them out of listings.
const temporaryPattern = ".rules-*.tmp"

const fileHeader = "# Inkuna rules. The first rule whose `when` matches a task decides its flow;\n# `default` applies when none match. Every flow must end with an approval stage.\n"

// Marshal renders a document as the YAML Inkuna reads back. It refuses a
// document that fails validation, so an invalid flow is never produced.
// Uses of shared stages and groups are written as they were written, never
// expanded.
func Marshal(document Document) ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	if document.Rules == nil {
		document.Rules = []Rule{}
	}
	data, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	return append([]byte(fileHeader), data...), nil
}

// plainStage is a Stage without its MarshalYAML, so a plain stage is
// written field by field.
type plainStage Stage

// MarshalYAML writes a use as it was written: use, id, and dependsOn, then
// each field it overrides, even one set to false or empty, and never the
// fields it takes from its definition. Any other stage is written field by
// field.
func (stage Stage) MarshalYAML() (any, error) {
	if stage.Use == "" {
		return plainStage(stage), nil
	}
	node := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, value any) error {
		var encoded yaml.Node
		if err := encoded.Encode(value); err != nil {
			return err
		}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &encoded)
		return nil
	}
	if err := add("use", stage.Use); err != nil {
		return nil, err
	}
	if stage.ID != "" {
		if err := add("id", stage.ID); err != nil {
			return nil, err
		}
	}
	if len(stage.DependsOn) > 0 {
		if err := add("dependsOn", stage.DependsOn); err != nil {
			return nil, err
		}
	}
	value := reflect.ValueOf(stage)
	for _, name := range stage.overridden() {
		if err := add(name, value.Field(stageFields[name]).Interface()); err != nil {
			return nil, err
		}
	}
	return node, nil
}

// Write validates the document and replaces the rules file atomically. The
// file lives in the repository and is meant to be committed, so it is
// written with ordinary source-file permissions.
func Write(path string, document Document) error {
	data, err := Marshal(document)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// writeFile replaces the file at path with data atomically, creating its
// directory when needed.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// A unique name in the same directory keeps the rename atomic and lets two
	// saves run at once without sharing a half-written file.
	temporary, err := os.CreateTemp(filepath.Dir(path), temporaryPattern)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(temporary.Name())
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	// CreateTemp makes the file owner-only; rules and library files are
	// ordinary source files.
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	committed = true
	return nil
}
