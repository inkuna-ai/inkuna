//go:build !windows && !linux && !darwin

package proc

// markedProcesses finds nothing where Inkuna does not know how to read
// another process's environment.
func markedProcesses(string) ([]marked, error) {
	return nil, nil
}

func environmentOf(int) ([][]byte, bool) {
	return nil, false
}
