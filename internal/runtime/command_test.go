package runtime

import (
	"strings"
	"testing"
)

func TestCheckCommandRunsProgramsDirectlyAndNeverAShell(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "test", "./..."},
		{"task", "ci"},
		{"go", "test", "-run", "TestA|TestB", "./..."},
		{"git", "commit", "-m", "a message with spaces"},
		{"grep", "-r", "bash", "."},
		{"./scripts/check.sh"},
		{"npm", "test", "--", "--run"},
	} {
		if err := CheckCommand(argv); err != nil {
			t.Errorf("%q was refused: %v", argv, err)
		}
	}
	for _, refused := range []struct {
		argv []string
		says string
	}{
		{nil, "needs a program"},
		{[]string{""}, "needs a program"},
		{[]string{"go test ./..."}, "whole command line"},
		{[]string{"sh", "-c", "go test ./..."}, "runs a shell"},
		{[]string{"/bin/bash", "scripts/check.sh"}, "runs a shell"},
		{[]string{"zsh", "-lc", "make"}, "runs a shell"},
		{[]string{"cmd.exe", "/c", "npm test"}, "runs a shell"},
		{[]string{"pwsh", "-Command", "Invoke-Build"}, "runs a shell"},
		{[]string{"env", "FOO=1", "sh", "-c", "make"}, "runs a shell"},
		{[]string{"timeout", "60", "bash", "-ec", "make"}, "runs a shell"},
		{[]string{"powershell", "-e", "SQBuAHYAbwBrAGUA"}, "runs a shell"},
		{[]string{"go", "test", "nul\x00byte"}, "NUL"},
		{[]string{"claude", "--dangerously-skip-permissions", "-p", "fix it"}, "permission checks off"},
		{[]string{"codex", "exec", "--SANDBOX=danger-full-access"}, "permission checks off"},
		{[]string{"claude", "--permission-mode", "bypassPermissions"}, "permission checks off"},
	} {
		if err := CheckCommand(refused.argv); err == nil || !strings.Contains(err.Error(), refused.says) {
			t.Errorf("%q: got %v, want an error saying %q", refused.argv, err, refused.says)
		}
	}
}

func TestCheckAllowedCommandNeedsPlainWords(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "test", "./..."},
		{"gofmt", "-w", "."},
		{"git", "diff", "HEAD~1"},
		{"go", "test", "-tags=integration,slow", "./internal/app/..."},
		{"npm", "run", "test:unit"},
	} {
		if err := CheckAllowedCommand(argv); err != nil {
			t.Errorf("%q was refused: %v", argv, err)
		}
	}
	for _, argv := range [][]string{
		{"git", "commit", "-m", "two words"},
		{"go", "test", "-run", "TestA|TestB"},
		{"rm", "-rf", "*"},
		{"echo", "$HOME"},
		{"make", "&&", "make", "install"},
		{"go", "test", ""},
		{"cat", "~/.ssh/id_ed25519"},
		{"echo", `"quoted"`},
	} {
		if err := CheckAllowedCommand(argv); err == nil || !strings.Contains(err.Error(), "plain word") {
			t.Errorf("%q: got %v, want it refused as not plain words", argv, err)
		}
	}
	if err := CheckAllowedCommand([]string{"bash", "-c", "true"}); err == nil || !strings.Contains(err.Error(), "runs a shell") {
		t.Errorf("an allowed command gets CheckCommand's checks first, got %v", err)
	}
}

func TestARefusedCommandLineIsAllowedOnlyAsItWasWritten(t *testing.T) {
	if argv, ok := AllowedCommandFor("  go test ./...  "); !ok || strings.Join(argv, " ") != "go test ./..." {
		t.Fatalf("a plain command line becomes its argv: %q %v", argv, ok)
	}
	for _, line := range []string{"", "go test ./... | tee out.txt", `git commit -m "a message"`, "bash -c make", "cd web && npm test"} {
		if argv, ok := AllowedCommandFor(line); ok {
			t.Errorf("%q cannot be allowed as one command, got %q", line, argv)
		}
	}
	if !EditTool("Write") || !EditTool("NotebookEdit") || EditTool("Bash") || EditTool("Read") {
		t.Fatal("EditTool names the tools that change files")
	}
}
