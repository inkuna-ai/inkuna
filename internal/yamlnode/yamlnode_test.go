package yamlnode

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parse(t *testing.T, content string) *yaml.Node {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		t.Fatal(err)
	}
	return document.Content[0]
}

// A resolved section encodes to YAML that parses on its own, whatever its
// aliases referred to.
func TestResolveExpandsAliasesAndDropsAnchors(t *testing.T) {
	root := parse(t, "base: &base {endpoint: https://records.example.com} # shared\nexample:\n  <<: *base\n  retries: 3\n")
	resolved, err := Resolve(root.Content[3], false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(data), "&*") || !strings.Contains(string(data), "# shared\n") {
		t.Fatalf("resolved section:\n%s", data)
	}
	var settings struct {
		Endpoint string `yaml:"endpoint"`
		Retries  int    `yaml:"retries"`
	}
	if err := yaml.Unmarshal(data, &settings); err != nil || settings.Endpoint != "https://records.example.com" || settings.Retries != 3 {
		t.Fatalf("decoded %+v, %v", settings, err)
	}
	if root.Content[1].Anchor != "base" {
		t.Fatal("Resolve must not change the node it copies")
	}
	stripped, err := Resolve(root.Content[3], true)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := yaml.Marshal(stripped); strings.Contains(string(data), "#") {
		t.Fatalf("comments kept:\n%s", data)
	}
}

func TestResolveRefusesAnAliasInsideItsOwnTarget(t *testing.T) {
	if _, err := Resolve(parse(t, "loop: &loop [*loop]\n"), false); err == nil || !strings.Contains(err.Error(), "alias *loop refers to a node that contains it") {
		t.Fatalf("a self-referencing alias: %v", err)
	}
}

// Aliases of aliases multiply; past MaxNodes the copy is refused.
func TestResolveCapsWhatAliasesExpandTo(t *testing.T) {
	content := "a: &a [x, x, x, x, x, x, x, x, x, x]\n"
	previous := "a"
	for _, name := range []string{"b", "c", "d", "e", "f"} {
		content += name + ": &" + name + " [" + strings.Repeat("*"+previous+", ", 9) + "*" + previous + "]\n"
		previous = name
	}
	if _, err := Resolve(parse(t, content), false); err == nil || !strings.Contains(err.Error(), "too many nodes") {
		t.Fatalf("an alias bomb: %v", err)
	}
}
