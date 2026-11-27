package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheChannelIsStableUntilOneIsRecorded(t *testing.T) {
	path := ChannelPath(t.TempDir())
	if channel, err := ReadChannel(path); err != nil || channel != ChannelStable {
		t.Fatalf("channel %q, err %v", channel, err)
	}
	if err := WriteChannel(path, ChannelEdge); err != nil {
		t.Fatal(err)
	}
	if channel, err := ReadChannel(path); err != nil || channel != ChannelEdge {
		t.Fatalf("channel %q, err %v", channel, err)
	}
	if filepath.Base(path) != ChannelFile {
		t.Fatalf("path %s", path)
	}
}

func TestAChannelFileIsReadStrictly(t *testing.T) {
	for name, content := range map[string]string{
		"an unknown key":     `{"channel":"edge","pinned":"v1.0.0"}`,
		"an unknown channel": `{"channel":"nightly"}`,
		"two objects":        `{"channel":"edge"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ChannelFile)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadChannel(path); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("err %v", err)
			}
		})
	}
	if err := WriteChannel(filepath.Join(t.TempDir(), ChannelFile), "nightly"); err == nil {
		t.Fatal("an unknown channel is written")
	}
}
