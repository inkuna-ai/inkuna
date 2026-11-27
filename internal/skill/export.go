package skill

import (
	"fmt"
	"os"
	"path/filepath"
)

func Export(packages []Package, repository, target string) error {
	var roots []string
	switch target {
	case "codex":
		roots = []string{filepath.Join(repository, ".agents", "skills")}
	case "claude":
		roots = []string{filepath.Join(repository, ".claude", "skills")}
	case "both":
		roots = []string{filepath.Join(repository, ".agents", "skills"), filepath.Join(repository, ".claude", "skills")}
	default:
		return fmt.Errorf("unknown export target %q", target)
	}
	for _, root := range roots {
		for _, pkg := range packages {
			destination := filepath.Join(root, pkg.ID)
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", pkg.Name, pkg.Description, pkg.Body)
			if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte(content), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
