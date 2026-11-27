//go:build darwin

package inkuna

// Wails' macOS code uses UniformTypeIdentifiers without linking it. The flag
// lives here rather than in a main package, so every binary built through
// Run links it.

/*
#cgo LDFLAGS: -framework UniformTypeIdentifiers
*/
import "C"
