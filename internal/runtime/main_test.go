package runtime

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// The fake CLIs ignore Claude Code's sandbox, so a machine without one,
	// such as a Linux runner without bubblewrap, still runs the tests that
	// give Claude Code a server's secrets. The check itself has its own tests.
	claudeSandboxMissing = func() string { return "" }
	// A Claude Code call given servers reads the user's settings file, so the
	// tests get an empty configuration directory instead of the developer's.
	directory, err := os.MkdirTemp("", "inkuna-claude-config-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("CLAUDE_CONFIG_DIR", directory)
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}
