package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// temporaryPattern names in-progress saves. It does not end in .md, so
// Discover never mistakes one for a definition.
const temporaryPattern = ".agent-*.tmp"

// Marshal renders a definition as the Markdown-with-frontmatter file Load
// reads back. It refuses a definition that fails validation.
func Marshal(definition Definition) ([]byte, error) {
	definition.Instructions = strings.TrimSpace(definition.Instructions)
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	header, err := yaml.Marshal(definition)
	if err != nil {
		return nil, err
	}
	// The keys another tool reads follow Inkuna's, as the file had them.
	extra, err := extraYAML(definition.Extra)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	buffer.WriteString("---\n")
	buffer.Write(header)
	buffer.WriteString(extra)
	buffer.WriteString("---\n\n")
	buffer.WriteString(definition.Instructions)
	buffer.WriteString("\n")
	return buffer.Bytes(), nil
}

// Write validates the definition and atomically replaces <directory>/<name>.md.
// The file name always equals the agent name, which Load requires.
func Write(directory string, definition Definition) (string, error) {
	data, err := Marshal(definition)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(directory, definition.Name+".md")
	// A unique name in the same directory keeps the rename atomic and lets two
	// saves run at once without sharing a half-written file.
	temporary, err := os.CreateTemp(directory, temporaryPattern)
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(temporary.Name())
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return "", err
	}
	// CreateTemp makes the file owner-only; definitions are ordinary source files.
	if err := temporary.Chmod(0o644); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return "", err
	}
	committed = true
	return path, nil
}

// Exists reports whether <directory>/<name>.md is present. A name that fails
// the agent name pattern can have no definition file.
func Exists(directory, name string) (bool, error) {
	if !safeName.MatchString(name) {
		return false, nil
	}
	_, err := os.Stat(filepath.Join(directory, name+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Remove deletes <directory>/<name>.md. The name is checked against the
// agent name pattern first, so it can never address a file outside directory.
func Remove(directory, name string) error {
	if !safeName.MatchString(name) {
		return fmt.Errorf("agent name %q must match %s", name, safeName.String())
	}
	err := os.Remove(filepath.Join(directory, name+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agent %q has no definition file in %s", name, directory)
	}
	return err
}
