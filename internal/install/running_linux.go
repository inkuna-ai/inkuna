package install

import (
	"os"
	"path/filepath"
	"strconv"
)

// processExecutables are the /proc/<pid>/exe links, which os.Stat follows to
// each process's executable, even one renamed or replaced since it started.
func processExecutables() ([]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var executables []string
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		executables = append(executables, filepath.Join("/proc", entry.Name(), "exe"))
	}
	return executables, nil
}
