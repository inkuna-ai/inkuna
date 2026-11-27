package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ChannelFile records which channel inkunactl update follows for a data
// directory. It is a file of its own rather than a key of install.json, for
// the reason PreviousRecordFile is: an inkunactl from before the key,
// installed again by a rollback, would refuse the record.
const ChannelFile = "update-channel.json"

// Channel is what inkunactl update follows.
type Channel string

const (
	// ChannelStable follows the newest release, the highest vX.Y.Z tag on
	// main. It is the default.
	ChannelStable Channel = "stable"
	// ChannelEdge follows origin/main, every change as it merges.
	ChannelEdge Channel = "edge"
)

// ParseChannel reads a channel's name, as --channel gives it.
func ParseChannel(name string) (Channel, error) {
	switch channel := Channel(name); channel {
	case ChannelStable, ChannelEdge:
		return channel, nil
	}
	return "", fmt.Errorf("unknown channel %q: use %s or %s", name, ChannelStable, ChannelEdge)
}

// ChannelPath is where the channel is recorded for a data directory.
func ChannelPath(dataDir string) string {
	return filepath.Join(dataDir, ChannelFile)
}

type channelFile struct {
	Channel Channel `json:"channel"`
}

// ReadChannel reads the recorded channel: ChannelStable when none is
// recorded. The file is read strictly, as every file Inkuna reads.
func ReadChannel(path string) (Channel, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ChannelStable, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file channelFile
	if err := decoder.Decode(&file); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("parse %s: it must hold one JSON object", path)
	}
	channel, err := ParseChannel(string(file.Channel))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return channel, nil
}

// WriteChannel records the channel atomically.
func WriteChannel(path string, channel Channel) error {
	if _, err := ParseChannel(string(channel)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(channelFile{Channel: channel}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the update channel: %w", err)
	}
	if err := writeAtomically(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
