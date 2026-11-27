// Package inkuna runs the Inkuna desktop app. It is the composition root for
// every binary that embeds Inkuna: Inkuna's own main package calls Run with
// its built frontend, and another module calls it with its own. It is public
// API, so it names no type from internal/ (docs/extensions.md).
package inkuna

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync/atomic"

	"github.com/inkuna-ai/inkuna/internal/app"
	"github.com/inkuna-ai/inkuna/pkg/extension"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Options is what a binary that embeds Inkuna passes to Run.
type Options struct {
	// Assets is the built frontend Wails serves, which it finds by its
	// index.html. It is required: this package cannot embed Inkuna's ui
	// directory, which belongs to the root package, so every binary passes
	// the bundle it was built with.
	Assets fs.FS
	// Icon is a PNG shown in the macOS Dock when the binary runs outside an
	// app bundle, as `wails dev` does. Nil leaves the default; a packaged
	// app takes its icon from the bundle either way.
	Icon []byte
	// Bind lists Go objects the frontend may call beside Inkuna's own
	// service. Wails generates TypeScript for each, so a screen an embedding
	// binary adds calls its own service rather than a method added to
	// Inkuna's.
	Bind []any
	// DataDir is the data directory. Empty discovers it as Inkuna always
	// has: INKUNA_DATA_DIR, then inkuna.yaml's dataDir, then the default.
	DataDir string
	// Product is the name and version the app shows, as the window title
	// and in Settings. The zero Product is Inkuna's own.
	Product extension.Product
	// Extensions are registered in order before the app starts. Run
	// refuses to start when two share a name, one's Register returns an
	// error, or Inkuna refuses what one registered.
	Extensions []extension.Extension
}

// Run starts the desktop app and returns when it quits. It registers the
// extensions, builds the service, and runs the window with the service bound.
func Run(opts Options) error {
	if opts.Assets == nil {
		return errors.New("run inkuna: no frontend assets: pass the built frontend as Options.Assets")
	}
	service, err := app.New(opts.DataDir, opts.Product, opts.Extensions)
	if err != nil {
		return fmt.Errorf("run inkuna: %w", err)
	}
	bind := append([]any{service}, opts.Bind...)
	// The window's context, set once its page loaded: a second launch is
	// reported on another goroutine.
	var window atomic.Pointer[context.Context]
	err = wails.Run(&options.App{
		Title: service.Capabilities().Product.Name, Width: 1360, Height: 860, MinWidth: 960, MinHeight: 640,
		AssetServer:      &assetserver.Options{Assets: opts.Assets},
		BackgroundColour: &options.RGBA{R: 13, G: 18, B: 21, A: 1},
		// A second launch on the same data directory exits and brings this
		// window forward. The service's own lock on the directory refuses
		// one this misses, such as on Linux without a session bus.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: app.InstanceID(service),
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if ctx := window.Load(); ctx != nil {
					wailsruntime.WindowUnminimise(*ctx)
					wailsruntime.WindowShow(*ctx)
				}
			},
		},
		// The lifecycle callbacks are the methods themselves, which Wails
		// then leaves out of the bindings: a closure would let the page call
		// Startup or Shutdown.
		OnStartup: service.Startup,
		OnDomReady: func(ctx context.Context) {
			window.Store(&ctx)
			setDockIcon(opts.Icon)
		},
		OnShutdown: service.Shutdown,
		Bind:       bind,
	})
	if err != nil {
		return fmt.Errorf("run inkuna: %w", err)
	}
	return nil
}
