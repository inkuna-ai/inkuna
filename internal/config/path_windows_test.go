package config

import (
	"os"
	"testing"
)

func TestEnsureSearchPathLeavesWindowsPathAlone(t *testing.T) {
	t.Setenv("PATH", `C:\Tools;C:\Windows\System32`)
	if got := EnsureSearchPath(); got != `C:\Tools;C:\Windows\System32` || os.Getenv("PATH") != got {
		t.Fatalf("PATH = %q, returned %q; want it unchanged", os.Getenv("PATH"), got)
	}
}
