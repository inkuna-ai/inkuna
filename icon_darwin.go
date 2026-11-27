//go:build darwin

package main

import _ "embed"

// appIcon is the Dock image for a binary run outside an app bundle. Only
// macOS shows it, so only macOS builds carry it.
//
//go:embed build/appicon.png
var appIcon []byte
