//go:build !darwin

package main

// appIcon is empty outside macOS: Linux and Windows take the window and
// taskbar icon from the packaged build.
var appIcon []byte
