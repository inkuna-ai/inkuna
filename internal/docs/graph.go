package docs

import (
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
)

// GraphSource is one registered project's docs as the doc graph reads them.
type GraphSource struct {
	// Project is the project's ID, Name what the graph labels it.
	Project string
	Name    string
	// Remote names the project's origin as host/owner/repo, lowercase, or
	// is empty when it has none; GitHub links to it land on this project.
	Remote string
	// DocsDir is the docs root relative to the repository, slash-separated.
	DocsDir string
	Index   []Doc
}

// NodeKind says what a graph node stands for.
type NodeKind string

const (
	// NodeDoc is a doc of a registered project.
	NodeDoc NodeKind = "doc"
	// NodeProject is a registered project's repository, for links into it
	// that do not land on a doc.
	NodeProject NodeKind = "project"
	// NodeExternal is a repository on the GitHub host that no registered
	// project's origin names.
	NodeExternal NodeKind = "external"
)

// GraphNode is one node of the doc graph. Project is set on doc and project
// nodes, Path and Status on doc nodes, URL on external ones.
type GraphNode struct {
	ID       string   `json:"id"`
	Kind     NodeKind `json:"kind"`
	Label    string   `json:"label"`
	Project  string   `json:"project,omitempty"`
	Path     string   `json:"path,omitempty"`
	Status   Status   `json:"status,omitempty"`
	Problems int      `json:"problems,omitempty"`
	URL      string   `json:"url,omitempty"`
}

// GraphEdge is every link from one doc to one target, counted.
type GraphEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// Graph is the doc graph: nodes and edges sorted by ID.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// DocNodeID, ProjectNodeID, and ExternalNodeID name graph nodes, so the
// view can find a doc's node.
func DocNodeID(project, docPath string) string { return "doc:" + project + ":" + docPath }

// ProjectNodeID names a registered project's repository node.
func ProjectNodeID(project string) string { return "project:" + project }

// ExternalNodeID names an unregistered repository's node.
func ExternalNodeID(repository string) string { return "external:" + repository }

// BuildGraph links docs within and across registered projects. With focus
// set to a project ID, it keeps that project's docs and every edge that
// starts or ends in that project; with focus empty, every project's docs and
// edges. githubHost is the host whose repository URLs are graphed; any other
// URL is not. Self-links are dropped.
func BuildGraph(sources []GraphSource, focus, githubHost string) Graph {
	graph := graphBuilder{
		host:     strings.ToLower(githubHost),
		nodes:    map[string]GraphNode{},
		counts:   map[[2]string]int{},
		byRemote: map[string]*GraphSource{},
		docs:     map[string]map[string]Doc{},
	}
	for index := range sources {
		source := &sources[index]
		if source.Remote != "" {
			graph.byRemote[source.Remote] = source
		}
		graph.docs[source.Project] = make(map[string]Doc, len(source.Index))
		for _, doc := range source.Index {
			graph.docs[source.Project][doc.Path] = doc
		}
	}
	inFocus := func(node GraphNode) bool { return focus == "" || node.Project == focus }
	for index := range sources {
		source := &sources[index]
		for _, doc := range source.Index {
			from := docNode(*source, doc)
			if inFocus(from) {
				graph.nodes[from.ID] = from
			}
			for _, link := range doc.Links {
				to, ok := graph.target(*source, link)
				if !ok || to.ID == from.ID || (!inFocus(from) && !inFocus(to)) {
					continue
				}
				graph.nodes[from.ID] = from
				if _, seen := graph.nodes[to.ID]; !seen {
					graph.nodes[to.ID] = to
				}
				graph.counts[[2]string{from.ID, to.ID}]++
			}
		}
	}
	return graph.sorted()
}

type graphBuilder struct {
	host     string
	nodes    map[string]GraphNode
	counts   map[[2]string]int
	byRemote map[string]*GraphSource
	docs     map[string]map[string]Doc
}

func docNode(source GraphSource, doc Doc) GraphNode {
	label := doc.Title
	if label == "" {
		label = pathpkg.Base(doc.Path)
	}
	return GraphNode{
		ID: DocNodeID(source.Project, doc.Path), Kind: NodeDoc, Label: label,
		Project: source.Project, Path: doc.Path, Status: doc.Status, Problems: len(doc.Problems),
	}
}

func projectNode(source GraphSource) GraphNode {
	return GraphNode{ID: ProjectNodeID(source.Project), Kind: NodeProject, Label: source.Name, Project: source.Project}
}

// target is the node a link lands on, if the graph shows it.
func (graph graphBuilder) target(source GraphSource, link Link) (GraphNode, bool) {
	switch link.Kind {
	case LinkDoc:
		return graph.doc(source, link.Path), true
	case LinkFile:
		return projectNode(source), true
	case LinkURL:
		return graph.url(link.URL)
	}
	return GraphNode{}, false
}

// doc is a doc node by path, labelled from the project's index.
func (graph graphBuilder) doc(source GraphSource, docPath string) GraphNode {
	if doc, ok := graph.docs[source.Project][docPath]; ok {
		return docNode(source, doc)
	}
	return GraphNode{ID: DocNodeID(source.Project, docPath), Kind: NodeDoc, Label: pathpkg.Base(docPath), Project: source.Project, Path: docPath}
}

// url resolves a link to https://<host>/<owner>/<repo>[/blob|tree/<ref>/<path>].
// A registered project's URL lands on its doc when the path is one,
// otherwise on the project; an unregistered repository is an external node.
// The ref can itself contain slashes, so each split is tried in turn.
func (graph graphBuilder) url(raw string) (GraphNode, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || strings.ToLower(parsed.Hostname()) != graph.host {
		return GraphNode{}, false
	}
	segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	if len(segments) < 2 {
		return GraphNode{}, false
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	remote := strings.ToLower(graph.host + "/" + owner + "/" + repo)
	source, registered := graph.byRemote[remote]
	if !registered {
		return GraphNode{
			ID: ExternalNodeID(remote), Kind: NodeExternal, Label: owner + "/" + repo,
			URL: "https://" + graph.host + "/" + owner + "/" + repo,
		}, true
	}
	rest := segments[2:]
	if len(rest) >= 3 && (rest[0] == "blob" || rest[0] == "tree") {
		for split := 2; split < len(rest); split++ {
			repoPath := strings.Join(rest[split:], "/")
			if docPath, ok := underDir(repoPath, source.DocsDir); ok && graph.listed(source.Project, docPath) {
				return graph.doc(*source, docPath), true
			}
		}
	}
	return projectNode(*source), true
}

func (graph graphBuilder) listed(project, docPath string) bool {
	_, ok := graph.docs[project][docPath]
	return ok
}

func (graph graphBuilder) sorted() Graph {
	result := Graph{Nodes: make([]GraphNode, 0, len(graph.nodes)), Edges: make([]GraphEdge, 0, len(graph.counts))}
	for _, node := range graph.nodes {
		result.Nodes = append(result.Nodes, node)
	}
	for pair, count := range graph.counts {
		result.Edges = append(result.Edges, GraphEdge{From: pair[0], To: pair[1], Count: count})
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].From != result.Edges[j].From {
			return result.Edges[i].From < result.Edges[j].From
		}
		return result.Edges[i].To < result.Edges[j].To
	})
	return result
}
