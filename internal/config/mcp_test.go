package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPServerDeclarationsAreChecked(t *testing.T) {
	valid := []MCPServerConfig{
		{Name: "docs-search", Description: "Searches the archive", Command: []string{"npx", "-y", "@example/docs-mcp@2.1.0"}, Env: map[string]string{"DOCS_TOKEN": "DOCS_TOKEN"}, Tools: []string{"search", "read_page"}},
		{Name: "linear", URL: "https://mcp.linear.app/mcp", Bearer: "LINEAR_API_KEY", Tracker: "linear", Tools: []string{"get_issue"}},
		{Name: "local", URL: "http://127.0.0.1:8080/mcp"},
	}
	file := DefaultFile()
	file.Secrets = append(file.Secrets, SecretRef{Name: "DOCS_TOKEN"})
	file.MCPServers = valid
	if problems := file.Problems(); len(problems) != 0 {
		t.Fatalf("valid declarations were refused: %v", problems)
	}
	for _, test := range []struct {
		server MCPServerConfig
		want   string
	}{
		{MCPServerConfig{Name: "Docs", Command: []string{"x"}}, "lower-case letters"},
		{MCPServerConfig{Name: "edit", Command: []string{"x"}}, "runtime's own tool name"},
		{MCPServerConfig{Name: "docs-search", Command: []string{"x"}}, "declared twice"},
		{MCPServerConfig{Name: "both", Command: []string{"x"}, URL: "https://x.example"}, "exactly one of command and url"},
		{MCPServerConfig{Name: "neither"}, "exactly one of command and url"},
		{MCPServerConfig{Name: "blank", Command: []string{" "}}, "needs a program"},
		{MCPServerConfig{Name: "plain", URL: "http://mcp.example/mcp"}, "https, or http only to localhost"},
		{MCPServerConfig{Name: "creds", URL: "https://user:pw@mcp.example/mcp"}, "without credentials"},
		{MCPServerConfig{Name: "undeclared", Command: []string{"x"}, Env: map[string]string{"TOKEN": "NOT_DECLARED"}}, `secret "NOT_DECLARED", which must be declared`},
		{MCPServerConfig{Name: "lowercase-env", Command: []string{"x"}, Env: map[string]string{"token": "DOCS_TOKEN"}}, "upper-case environment variable"},
		{MCPServerConfig{Name: "path", Command: []string{"x"}, Env: map[string]string{"PATH": "DOCS_TOKEN"}}, "every CLI is started with"},
		{MCPServerConfig{Name: "env-url", URL: "https://x.example", Env: map[string]string{"TOKEN": "DOCS_TOKEN"}}, "env is for a command"},
		{MCPServerConfig{Name: "bearer-command", Command: []string{"x"}, Bearer: "DOCS_TOKEN"}, "bearer is for a url"},
		{MCPServerConfig{Name: "bearer-undeclared", URL: "https://x.example", Bearer: "NOT_DECLARED"}, `bearer uses secret "NOT_DECLARED"`},
		{MCPServerConfig{Name: "tracker", URL: "https://x.example", Tracker: "linear"}, "must list its tools"},
		{MCPServerConfig{Name: "jira", URL: "https://x.example", Tracker: "jira", Tools: []string{"get"}}, "must be linear or github"},
		{MCPServerConfig{Name: "tools", Command: []string{"x"}, Tools: []string{"get(issue)"}}, "letters, digits, underscores"},
		{MCPServerConfig{Name: "twice", Command: []string{"x"}, Tools: []string{"get", "get"}}, `tool "get" is listed twice`},
		{MCPServerConfig{Name: "long", Command: []string{"x"}, Description: strings.Repeat("x", MaxMCPDescriptionBytes+1)}, "description is over"},
	} {
		file.MCPServers = append(append([]MCPServerConfig(nil), valid...), test.server)
		problems := file.Problems()
		if len(problems) != 1 || problems[0].Field != "mcpServers" || !strings.Contains(problems[0].Message, test.want) {
			t.Fatalf("%s: want one mcpServers problem containing %q, got %v", test.server.Name, test.want, problems)
		}
	}
}

func TestAnMCPServersUnknownKeyIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	content := "version: 1\nmcpServers:\n  - name: docs\n    command: [docs-mcp]\n    token: abc\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("an unknown key in a server must be rejected so a secret never lands in YAML: %v", err)
	}
}

func TestABriefKeepsWhatTheServerIsAndNotItsSecrets(t *testing.T) {
	entry := MCPServerConfig{Name: "docs", Command: []string{"docs-mcp"}, Env: map[string]string{"DOCS_TOKEN": "DOCS_TOKEN"}, Tools: []string{"search"}}
	brief := entry.Brief()
	if brief.Name != "docs" || len(brief.Command) != 1 || len(brief.Tools) != 1 {
		t.Fatalf("brief = %+v", brief)
	}
}
