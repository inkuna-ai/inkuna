package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/frontmatter"
	"gopkg.in/yaml.v3"
)

// Deliveries a skill's manifest can choose. Inline, the default, sends the
// body in every envelope that selects the skill; on-demand sends its name,
// description, and path for the worker to open when it needs it, wherever
// the worker can read the file.
const (
	DeliveryInline   = "inline"
	DeliveryOnDemand = "on-demand"
)

type Manifest struct {
	Version              int               `yaml:"version" json:"version"`
	Delivery             string            `yaml:"delivery,omitempty" json:"delivery,omitempty"`
	TaskKinds            []domain.TaskKind `yaml:"taskKinds" json:"taskKinds"`
	RequiredCapabilities []string          `yaml:"requiredCapabilities" json:"requiredCapabilities"`
	Compatibility        []string          `yaml:"compatibility" json:"compatibility"`
	Actions              map[string]Action `yaml:"actions" json:"actions"`
}

type Action struct {
	Interpreter  string `yaml:"interpreter" json:"interpreter"`
	Script       string `yaml:"script" json:"script"`
	Timeout      int    `yaml:"timeoutSeconds" json:"timeoutSeconds"`
	InputSchema  string `yaml:"inputSchema" json:"inputSchema"`
	OutputSchema string `yaml:"outputSchema" json:"outputSchema"`
}

type Package struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Directory   string   `json:"directory"`
	Manifest    Manifest `json:"manifest"`
}

type header struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Load reads and checks the skill in directory, whose name is the skill's.
func Load(directory string) (Package, error) {
	pkg, err := LoadFS(os.DirFS(directory), filepath.Base(filepath.Clean(directory)))
	if err != nil {
		return Package{}, err
	}
	pkg.Directory = directory
	return pkg, nil
}

// LoadFS reads and checks a skill whose files fsys holds as its directory
// does, SKILL.md at the root, by the same checks Load makes of a directory
// named id. The package it returns has no Directory: a skill read this way
// has none on disk.
func LoadFS(fsys fs.FS, id string) (Package, error) {
	data, err := fs.ReadFile(fsys, "SKILL.md")
	if err != nil {
		return Package{}, fmt.Errorf("read SKILL.md: %w", err)
	}
	metadata, body, err := frontmatter.Split(string(data))
	if err != nil {
		return Package{}, fmt.Errorf("SKILL.md: %w", err)
	}
	body = strings.TrimSpace(body)
	var header header
	if err := yaml.Unmarshal([]byte(metadata), &header); err != nil {
		return Package{}, fmt.Errorf("parse SKILL.md frontmatter: %w", err)
	}
	if header.Name == "" || header.Description == "" {
		return Package{}, errors.New("SKILL.md requires name and description")
	}
	if header.Name != id {
		return Package{}, fmt.Errorf("skill name %q must match directory %q", header.Name, id)
	}
	manifest := Manifest{Version: 1, Actions: map[string]Action{}}
	if manifestData, readErr := fs.ReadFile(fsys, "inkuna.yaml"); readErr == nil {
		if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
			return Package{}, fmt.Errorf("parse inkuna.yaml: %w", err)
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return Package{}, fmt.Errorf("read inkuna.yaml: %w", readErr)
	}
	if manifest.Version != 1 {
		return Package{}, fmt.Errorf("unsupported skill manifest version %d", manifest.Version)
	}
	if manifest.Delivery != "" && manifest.Delivery != DeliveryInline && manifest.Delivery != DeliveryOnDemand {
		return Package{}, fmt.Errorf("skill delivery %q must be %s or %s", manifest.Delivery, DeliveryInline, DeliveryOnDemand)
	}
	for name, action := range manifest.Actions {
		if name == "" || action.Interpreter == "" || action.Script == "" || action.Timeout <= 0 {
			return Package{}, fmt.Errorf("action %q requires interpreter, script, and positive timeout", name)
		}
		clean := filepath.Clean(action.Script)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return Package{}, fmt.Errorf("action %q has unsafe script path", name)
		}
		if !supportsPlatform(manifest.Compatibility) {
			return Package{}, fmt.Errorf("skill is incompatible with %s", runtime.GOOS)
		}
	}
	return Package{ID: id, Name: header.Name, Description: header.Description, Body: body, Manifest: manifest}, nil
}

func Discover(root string) ([]Package, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	packages := make([]Package, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkg, loadErr := Load(filepath.Join(root, entry.Name()))
		if loadErr != nil {
			return nil, fmt.Errorf("load skill %s: %w", entry.Name(), loadErr)
		}
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ID < packages[j].ID })
	return packages, nil
}

func Route(kind domain.TaskKind, packages []Package) []string {
	selected := make([]string, 0)
	for _, pkg := range packages {
		for _, supported := range pkg.Manifest.TaskKinds {
			if supported == kind {
				selected = append(selected, pkg.ID)
				break
			}
		}
	}
	sort.Strings(selected)
	return selected
}

func supportsPlatform(platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, platform := range platforms {
		if platform == runtime.GOOS || platform == "all" {
			return true
		}
	}
	return false
}
