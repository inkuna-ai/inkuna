package main

import (
	"embed"
	"errors"
	"io/fs"
	"log"

	"github.com/inkuna-ai/inkuna/pkg/inkuna"
)

// The built frontend in ui/dist is not committed. Embedding ui rather than
// ui/dist keeps a checkout without a build compiling, since ui/README.md is
// always there; Wails serves the directory that holds index.html.
//
//go:embed all:ui
var assets embed.FS

// frontendBuilt says what to run when the binary was built without the
// frontend, instead of leaving Wails to fail without saying how to fix it.
func frontendBuilt(assets fs.FS) error {
	if _, err := fs.Stat(assets, "ui/dist/index.html"); err != nil {
		return errors.New("this binary was built without the frontend; run `task build` (or `task package` for the desktop app) and build it again")
	}
	return nil
}

// needsFrontend is off only in the binary Wails builds to generate bindings
// (bindings.go): it runs before the frontend is built and opens no window.
var needsFrontend = true

func main() {
	if needsFrontend {
		if err := frontendBuilt(assets); err != nil {
			log.Fatal(err)
		}
	}
	if err := inkuna.Run(inkuna.Options{Assets: assets, Icon: appIcon}); err != nil {
		log.Fatal(err)
	}
}
