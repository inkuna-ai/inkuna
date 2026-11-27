package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// MCPServer is an MCP server as a step is granted it: what the approval
// screen, the Context tab, and the worker's prompt show. It never carries a
// secret's value, only what the server is and which of its tools the worker
// may call (docs/mcp-and-trackers.md).
type MCPServer struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Command is the argv Inkuna's runtime starts, or URL the endpoint it
	// connects to; exactly one is set.
	Command []string `json:"command,omitempty"`
	URL     string   `json:"url,omitempty"`
	// Tools are the server's tools the worker may call; empty means all of
	// them.
	Tools []string `json:"tools,omitempty"`
	// Tracker names the issue tracker the server reaches, whose tools a
	// worker may only read through.
	Tracker string `json:"tracker,omitempty"`
}

// UserMCPServer is one of the user's own MCP servers as a step was given it:
// what its CLI said of it, never its configuration's secrets.
type UserMCPServer struct {
	Name string `json:"name"`
	// Source is where the CLI found it, such as its own configuration, a
	// claude.ai connector, or a plugin.
	Source    string `json:"source,omitempty"`
	Transport string `json:"transport,omitempty"`
	URL       string `json:"url,omitempty"`
	Command   string `json:"command,omitempty"`
	// Status is what the CLI said when the step started: ready, unknown
	// (it says nothing until a call starts it), needs-sign-in, failed,
	// missing, or disabled. Detail says why.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Digest identifies everything the approval screen shows of a server, so an
// approval can tell that a server changed after it was shown.
func (server MCPServer) Digest() string {
	// A struct of strings and string slices always encodes.
	data, _ := json.Marshal(server)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var (
	mcpServerName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	mcpToolName   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// ValidMCPServerName reports whether a name has the form of an MCP server's:
// lower-case letters, digits, and dashes, starting with a letter. The names
// a CLI reserves are the runtime's to refuse (runtime.ValidMCPServerName),
// and whether a server of that name is declared is checked where inkuna.yaml
// is known: planning and starting a run.
func ValidMCPServerName(name string) bool { return mcpServerName.MatchString(name) }

// ValidMCPToolName reports whether a tool name can be put on a runtime's
// allowlist: every runtime takes letters, digits, underscores, dots, and
// dashes, and none of them parentheses, commas, or spaces.
func ValidMCPToolName(name string) bool { return mcpToolName.MatchString(name) }

// MetadataStageMCP is the step metadata key listing, comma separated, the
// MCP servers the step's stage names, as against those its agent brings:
// approval rebuilds the grant from it, and only the stage's are required.
const MetadataStageMCP = "stageMcp"

// StageServers lists the MCP servers a step's stage names.
func StageServers(step WorkflowStep) []string {
	listed := step.Metadata[MetadataStageMCP]
	if listed == "" {
		return nil
	}
	return SortedServerNames(strings.Split(listed, ","))
}

// SortedServerNames returns the names once each, sorted, so a step's grant
// reads the same however its agent and stage listed them.
func SortedServerNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	sorted := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" && !seen[name] {
			seen[name] = true
			sorted = append(sorted, name)
		}
	}
	sort.Strings(sorted)
	return sorted
}
