package extension

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type sampleConfig struct {
	Endpoint string `yaml:"endpoint"`
	Retries  int    `yaml:"retries"`
}

// section parses document, an inkuna.yaml, and returns extensions.example as
// Inkuna would hand it to Configure.
func section(t *testing.T, document string) yaml.Node {
	t.Helper()
	var file struct {
		Extensions map[string]yaml.Node `yaml:"extensions"`
		Defaults   yaml.Node            `yaml:"defaults"`
	}
	if err := yaml.Unmarshal([]byte(document), &file); err != nil {
		t.Fatal(err)
	}
	return file.Extensions["example"]
}

func TestDecodeConfigDecodesASection(t *testing.T) {
	got := sampleConfig{Retries: 3}
	node := section(t, "version: 1\nextensions:\n  example:\n    # where records go\n    endpoint: https://records.example.com # the collector\n")
	if err := DecodeConfig(node, &got); err != nil {
		t.Fatal(err)
	}
	if want := (sampleConfig{Endpoint: "https://records.example.com", Retries: 3}); got != want {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
}

// No section, or an empty one, keeps the defaults out already holds.
func TestDecodeConfigLeavesDefaultsWithoutASection(t *testing.T) {
	for name, node := range map[string]yaml.Node{
		"absent": {},
		"empty":  section(t, "extensions:\n  example:\n"),
	} {
		got := sampleConfig{Retries: 3}
		if err := DecodeConfig(node, &got); err != nil || got != (sampleConfig{Retries: 3}) {
			t.Fatalf("%s section: %+v, %v; want the defaults kept", name, got, err)
		}
	}
}

// A key the extension has no field for is refused, at its line in the file,
// even with comments and blank lines above it that re-encoding drops.
func TestDecodeConfigRefusesUnknownKeysAtTheirLine(t *testing.T) {
	node := section(t, "version: 1\nextensions:\n  example:\n    # where records go\n    endpoint: https://records.example.com\n\n\n    retries: many\n    endpont: typo\n")
	err := DecodeConfig(node, &sampleConfig{})
	if err == nil {
		t.Fatal("an unknown key must be refused")
	}
	for _, want := range []string{"line 8: cannot unmarshal !!str `many` into int", "line 9: field endpont not found in type extension.sampleConfig"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestDecodeConfigRefusesASectionOfTheWrongShape(t *testing.T) {
	err := DecodeConfig(section(t, "extensions:\n  example: [1, 2]\n"), &sampleConfig{})
	if err == nil || !strings.Contains(err.Error(), "line 2: cannot unmarshal !!seq into extension.sampleConfig") {
		t.Fatalf("a list where a mapping belongs: %v", err)
	}
}

// A duplicate key names both its lines in inkuna.yaml.
func TestDecodeConfigRefusesADuplicateKeyAtBothLines(t *testing.T) {
	node := section(t, "version: 1\nextensions:\n  example:\n    # where records go\n\n    endpoint: https://a.example.com\n\n    endpoint: https://b.example.com\n")
	err := DecodeConfig(node, &sampleConfig{})
	if err == nil || !strings.Contains(err.Error(), `line 8: mapping key "endpoint" already defined at line 6`) {
		t.Fatalf("a duplicate key: %v", err)
	}
}

// An alias decodes as what it refers to, even when its anchor is outside the
// section.
func TestDecodeConfigFollowsAliases(t *testing.T) {
	node := section(t, "version: 1\ndefaults: &defaults {endpoint: https://records.example.com}\nextensions:\n  example:\n    <<: *defaults\n    retries: 5\n")
	var got sampleConfig
	if err := DecodeConfig(node, &got); err != nil || got != (sampleConfig{Endpoint: "https://records.example.com", Retries: 5}) {
		t.Fatalf("decoded %+v, %v", got, err)
	}
}
