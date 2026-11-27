// Command embed is the smallest binary that embeds Inkuna from another
// module. It uses only the public packages under pkg/, so CI building it
// proves they suffice: a module outside this one cannot import internal/.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"

	"github.com/inkuna-ai/inkuna/pkg/extension"
	"github.com/inkuna-ai/inkuna/pkg/inkuna"
	"gopkg.in/yaml.v3"
)

// A real embedding binary builds its own frontend bundle and embeds that.
// This page stands in for it, so the example builds without one.
//
//go:embed dist
var assets embed.FS

// greeter is a sample extension. It reports a capability, so the view can
// tell the build has it, logs Inkuna's audit events, logs each decision its
// policy is asked about, and takes its greeting from inkuna.yaml:
//
//	extensions:
//	  example:
//	    greeting: Hello from the example
type greeter struct {
	settings struct {
		Greeting string `yaml:"greeting"`
	}
}

func (*greeter) Name() string { return "example" }

func (*greeter) Register(registrar extension.Registrar) error {
	registrar.Capability(extension.Capability{ID: "example.greeting", Label: "Greeting"})
	registrar.AuditSink(logSink{})
	registrar.Policy(logPolicy{})
	return nil
}

// Configure decodes the extension's section strictly, keeping the default
// greeting when inkuna.yaml has none.
func (g *greeter) Configure(section yaml.Node) error {
	g.settings.Greeting = "Hello"
	if err := extension.DecodeConfig(section, &g.settings); err != nil {
		return err
	}
	log.Printf("greeting: %s", g.settings.Greeting)
	return nil
}

// logPolicy logs each decision Go allowed and allows it too. A policy that
// refused would return Verdict{Refuse: true, Reason: ...}, and Inkuna would
// show the reason.
type logPolicy struct{}

func (logPolicy) Check(_ context.Context, decision extension.Decision) (extension.Verdict, error) {
	log.Printf("policy %s by %s in %q: allowed", decision.Point, decision.Principal.ID, decision.Project)
	return extension.Verdict{}, nil
}

// logSink writes each audit event to the log. Logging the same event twice
// does no harm, which is all a sink's idempotency needs to be here.
type logSink struct{}

func (logSink) Record(_ context.Context, event extension.AuditEvent) error {
	log.Printf("audit %s %s: %s", event.ID, event.Kind, event.Summary)
	return nil
}

func main() {
	frontend, err := fs.Sub(assets, "dist")
	if err != nil {
		log.Fatal(err)
	}
	err = inkuna.Run(inkuna.Options{
		Assets:     frontend,
		Product:    extension.Product{Name: "Inkuna Example", Version: "0.1.0"},
		Extensions: []extension.Extension{&greeter{}},
	})
	if err != nil {
		log.Fatal(err)
	}
}
