//go:build bindings

package main

// Wails builds the app with the bindings tag and runs it to generate the
// frontend's bindings, before it builds the frontend, so that binary must
// not insist on one.
func init() { needsFrontend = false }
