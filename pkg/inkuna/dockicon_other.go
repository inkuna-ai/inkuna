//go:build !darwin

package inkuna

// setDockIcon is a no-op outside macOS: Linux and Windows take the window and
// taskbar icon from the packaged build.
func setDockIcon([]byte) {}
