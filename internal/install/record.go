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

// RecordFile is the name of the install record in the data directory.
const RecordFile = "install.json"

// PreviousRecordFile records the build kept as *.previous, which
// inkunactl update --rollback swaps back in. It is a file of its own rather
// than a key of install.json, which is read strictly: an inkunactl from
// before the key, installed again by a rollback, would refuse the record.
const PreviousRecordFile = "install.previous.json"

// Record is what the last install put in place and where it was built
// from. The next install reads it to find its checkout and to see whether
// that build is already installed, and inkunactl update to see what an
// update would change.
type Record struct {
	// Source is the checkout the build came from.
	Source string `json:"source"`
	// Version and Commit are the checkout's `git describe --tags --always
	// --dirty` and `git rev-parse HEAD` when it was built, the values the
	// build stamped (internal/version).
	Version string `json:"version"`
	Commit  string `json:"commit"`
	// App and CLI are where the app and inkunactl were put.
	App string `json:"app"`
	CLI string `json:"cli"`
}

// RecordPath is where the install record lives for a data directory.
func RecordPath(dataDir string) string {
	return filepath.Join(dataDir, RecordFile)
}

// PreviousRecordPath is where the record of the previous build lives for a
// data directory.
func PreviousRecordPath(dataDir string) string {
	return filepath.Join(dataDir, PreviousRecordFile)
}

// ReadRecord reads the install record. A missing record is not an error:
// it reports exists=false, as before the first install. An unknown key is
// an error, as in every file Inkuna reads.
func ReadRecord(path string) (record Record, exists bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, true, fmt.Errorf("parse %s: %w", path, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Record{}, true, fmt.Errorf("parse %s: it must hold one JSON object", path)
	}
	return record, true, nil
}

// WriteRecord replaces an install record atomically.
func WriteRecord(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the install record: %w", err)
	}
	if err := writeAtomically(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
