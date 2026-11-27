// Package sources reads what definition sources share: it keeps the last
// snapshot each source gave for each repository, and sorts each definition
// by what this build does with its mode. The app asks the sources; inkunactl,
// which registers no extensions, reads the snapshots the app kept, so both
// offer and refuse the same definitions.
package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// Entry is one source's snapshot for one repository, as it was last given,
// kept in <data dir>/sources/.
type Entry struct {
	// Source names the source as Inkuna does: its extension and its place
	// among that extension's sources, such as "example/source".
	Source string `json:"source"`
	// Repository is the repository's directory, which names the project to
	// the app and to inkunactl alike.
	Repository string             `json:"repository"`
	FetchedAt  time.Time          `json:"fetchedAt"`
	Snapshot   extension.Snapshot `json:"snapshot"`
}

// Directory is where the snapshots of the data directory are kept.
func Directory(dataDir string) string { return filepath.Join(dataDir, "sources") }

// Save keeps entry as the last snapshot of its source for its repository,
// replacing the file atomically so a reader never sees half of one.
func Save(dataDir string, entry Entry) error {
	entry.Repository = filepath.Clean(entry.Repository)
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode the snapshot of %s: %w", entry.Source, err)
	}
	directory := Directory(dataDir)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", directory, err)
	}
	temporary, err := os.CreateTemp(directory, ".snapshot-*.json")
	if err != nil {
		return fmt.Errorf("save the snapshot of %s: %w", entry.Source, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("save the snapshot of %s: %w", entry.Source, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("save the snapshot of %s: %w", entry.Source, err)
	}
	if err := os.Rename(name, filepath.Join(directory, fileName(entry.Source, entry.Repository))); err != nil {
		return fmt.Errorf("save the snapshot of %s: %w", entry.Source, err)
	}
	return nil
}

// Load returns the last snapshot source gave for repository, and false
// when there is none.
func Load(dataDir, source, repository string) (Entry, bool, error) {
	path := filepath.Join(Directory(dataDir), fileName(source, filepath.Clean(repository)))
	entry, err := read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	// The file name is a digest of both, so this guards only against a file
	// copied in under another's name.
	if entry.Source != source || entry.Repository != filepath.Clean(repository) {
		return Entry{}, false, nil
	}
	return entry, true, nil
}

// ForRepository returns every source's last snapshot for repository, in
// source order: what inkunactl offers there, whichever build cached it.
func ForRepository(dataDir, repository string) ([]Entry, error) {
	directory := Directory(dataDir)
	files, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", directory, err)
	}
	repository = filepath.Clean(repository)
	var entries []Entry
	for _, file := range files {
		if file.IsDir() || strings.HasPrefix(file.Name(), ".") || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		entry, err := read(filepath.Join(directory, file.Name()))
		if err != nil {
			return nil, err
		}
		if entry.Repository == repository && file.Name() == fileName(entry.Source, entry.Repository) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Source < entries[j].Source })
	return entries, nil
}

func read(path string) (Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("read the cached snapshot %s: %w", path, err)
	}
	return entry, nil
}

// fileName keeps a source's names and a repository's path, which hold
// slashes and characters a file name may not, out of the file name.
func fileName(source, repository string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + repository))
	return hex.EncodeToString(sum[:16]) + ".json"
}
