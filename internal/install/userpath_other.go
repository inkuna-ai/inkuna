//go:build !windows

package install

// SystemUserPath is nil outside Windows, where a shell startup file puts a
// directory on PATH instead (ShellProfile).
func SystemUserPath() UserPath { return nil }
