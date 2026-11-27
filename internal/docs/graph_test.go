package docs

import (
	"reflect"
	"testing"
)

func graphSources() []GraphSource {
	url := func(raw string) Link { return Link{Kind: LinkURL, URL: raw} }
	return []GraphSource{
		{
			Project: "bakery", Name: "Bakery", Remote: "github.com/acme/bakery", DocsDir: "docs",
			Index: []Doc{
				{Path: "a.md", Title: "A", Status: StatusCurrent, Links: []Link{
					{Kind: LinkDoc, Path: "b.md"},
					{Kind: LinkDoc, Path: "b.md"},
					{Kind: LinkDoc, Path: "a.md"},
					{Kind: LinkFile, Path: "README.md"},
					url("https://github.com/inkuna-ai/inkuna/blob/main/docs/guide.md"),
					url("https://GitHub.com/Inkuna-AI/Inkuna.git/tree/feature/x/docs/guide.md"),
					url("https://github.com/inkuna-ai/inkuna/blob/main/internal/app.go"),
					url("https://github.com/other/lib/blob/main/README.md"),
					url("https://github.com/acme"),
					url("https://example.com/docs/a.md"),
				}},
				{Path: "b.md", Title: "B", Status: StatusDraft},
				{Path: "orphan.md", Problems: []string{ProblemMissing}},
			},
		},
		{
			Project: "inkuna", Name: "Inkuna", Remote: "github.com/inkuna-ai/inkuna", DocsDir: "docs",
			Index: []Doc{
				{Path: "guide.md", Title: "Guide", Status: StatusCurrent, Links: []Link{
					url("https://github.com/acme/bakery/blob/main/docs/b.md"),
				}},
				{Path: "other.md", Title: "Other"},
			},
		},
		{
			Project: "notes", Name: "Notes", DocsDir: ".",
			Index: []Doc{
				{Path: "x.md", Title: "X", Links: []Link{{Kind: LinkDoc, Path: "y.md"}}},
				{Path: "y.md", Title: "Y"},
			},
		},
	}
}

func ids(nodes []GraphNode) []string {
	result := []string{}
	for _, node := range nodes {
		result = append(result, node.ID)
	}
	return result
}

func TestBuildGraphFocusedOnAProject(t *testing.T) {
	graph := BuildGraph(graphSources(), "bakery", "github.com")
	wantNodes := []string{
		"doc:bakery:a.md",
		"doc:bakery:b.md",
		"doc:bakery:orphan.md",
		"doc:inkuna:guide.md",
		"external:github.com/other/lib",
		"project:bakery",
		"project:inkuna",
	}
	if got := ids(graph.Nodes); !reflect.DeepEqual(got, wantNodes) {
		t.Fatalf("nodes = %v", got)
	}
	wantEdges := []GraphEdge{
		{From: "doc:bakery:a.md", To: "doc:bakery:b.md", Count: 2},
		{From: "doc:bakery:a.md", To: "doc:inkuna:guide.md", Count: 2},
		{From: "doc:bakery:a.md", To: "external:github.com/other/lib", Count: 1},
		{From: "doc:bakery:a.md", To: "project:bakery", Count: 1},
		{From: "doc:bakery:a.md", To: "project:inkuna", Count: 1},
		{From: "doc:inkuna:guide.md", To: "doc:bakery:b.md", Count: 1},
	}
	if !reflect.DeepEqual(graph.Edges, wantEdges) {
		t.Fatalf("edges = %#v", graph.Edges)
	}
	byID := map[string]GraphNode{}
	for _, node := range graph.Nodes {
		byID[node.ID] = node
	}
	if guide := byID["doc:inkuna:guide.md"]; guide.Label != "Guide" || guide.Project != "inkuna" || guide.Path != "guide.md" || guide.Status != StatusCurrent {
		t.Errorf("cross-project doc node = %#v", guide)
	}
	if orphan := byID["doc:bakery:orphan.md"]; orphan.Label != "orphan.md" || orphan.Problems != 1 {
		t.Errorf("a doc without links or a title = %#v", orphan)
	}
	if external := byID["external:github.com/other/lib"]; external.Label != "other/lib" || external.URL != "https://github.com/other/lib" || external.Project != "" {
		t.Errorf("external node = %#v", external)
	}
	if project := byID["project:inkuna"]; project.Kind != NodeProject || project.Label != "Inkuna" {
		t.Errorf("project node = %#v", project)
	}
}

func TestBuildGraphAcrossAllProjects(t *testing.T) {
	graph := BuildGraph(graphSources(), "", "github.com")
	wantNodes := []string{
		"doc:bakery:a.md",
		"doc:bakery:b.md",
		"doc:bakery:orphan.md",
		"doc:inkuna:guide.md",
		"doc:inkuna:other.md",
		"doc:notes:x.md",
		"doc:notes:y.md",
		"external:github.com/other/lib",
		"project:bakery",
		"project:inkuna",
	}
	if got := ids(graph.Nodes); !reflect.DeepEqual(got, wantNodes) {
		t.Fatalf("nodes = %v", got)
	}
	if len(graph.Edges) != 7 || graph.Edges[len(graph.Edges)-1] != (GraphEdge{From: "doc:notes:x.md", To: "doc:notes:y.md", Count: 1}) {
		t.Fatalf("edges = %#v", graph.Edges)
	}
}

func TestBuildGraphOnlyFollowsTheConfiguredGitHubHost(t *testing.T) {
	sources := graphSources()
	sources[1].Remote = "ghe.example.com/inkuna-ai/inkuna"
	graph := BuildGraph(sources, "bakery", "ghe.example.com")
	for _, edge := range graph.Edges {
		if edge.From == "doc:bakery:a.md" && edge.To != "doc:bakery:b.md" && edge.To != "project:bakery" {
			t.Errorf("github.com link graphed under another host: %#v", edge)
		}
	}
	if empty := BuildGraph(nil, "", "github.com"); empty.Nodes == nil || empty.Edges == nil {
		t.Fatalf("an empty graph must encode lists, not null: %#v", empty)
	}
}
