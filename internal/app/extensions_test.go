package app

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/version"
	"github.com/inkuna-ai/inkuna/pkg/extension"
	"gopkg.in/yaml.v3"
)

// fakeExtension registers its capabilities, or fails with err, and keeps the
// registrar it was given so a test can use it after Register returns.
type fakeExtension struct {
	name         string
	capabilities []extension.Capability
	identities   []extension.IdentityProvider
	auditSinks   []extension.AuditSink
	usageSinks   []extension.UsageSink
	policies     []extension.Policy
	sources      []extension.DefinitionSource
	secretStores []extension.SecretStore
	notifiers    []extension.Notifier
	err          error
	kept         extension.Registrar
	// during runs inside Register, after the capabilities.
	during func()
}

func (f *fakeExtension) Name() string { return f.name }

func (f *fakeExtension) Register(registrar extension.Registrar) error {
	f.kept = registrar
	for _, capability := range f.capabilities {
		registrar.Capability(capability)
	}
	for _, provider := range f.identities {
		registrar.Identity(provider)
	}
	for _, sink := range f.auditSinks {
		registrar.AuditSink(sink)
	}
	for _, sink := range f.usageSinks {
		registrar.UsageSink(sink)
	}
	for _, policy := range f.policies {
		registrar.Policy(policy)
	}
	for _, source := range f.sources {
		registrar.DefinitionSource(source)
	}
	for _, store := range f.secretStores {
		registrar.SecretStore(store)
	}
	for _, notifier := range f.notifiers {
		registrar.Notifier(notifier)
	}
	if f.during != nil {
		f.during()
	}
	return f.err
}

func TestNoExtensionsIsInkunaItself(t *testing.T) {
	service := &Service{}
	want := CapabilitiesView{
		Product:      ProductView{Name: "Inkuna", Version: version.Current().Version, Inkuna: true},
		Capabilities: []CapabilityView{},
	}
	if got := service.Capabilities(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() without New = %+v, want %+v", got, want)
	}
	registry, err := registerExtensions(extension.Product{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
	if got := service.Capabilities(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() with no extensions = %+v, want %+v", got, want)
	}
}

func TestCapabilitiesListsEveryExtensionsInOrder(t *testing.T) {
	registry, err := registerExtensions(extension.Product{Name: "Acme Desk", Version: "2.1.0"}, []extension.Extension{
		&fakeExtension{name: "audit", capabilities: []extension.Capability{{ID: "audit.records", Label: "Audit records"}}},
		&fakeExtension{name: "acme-sso", capabilities: []extension.Capability{
			{ID: "acme-sso.sign-in", Label: "Single sign-on"},
			{ID: "acme-sso.groups.sync", Label: "Group sync"},
		}},
		&fakeExtension{name: "quiet"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := (&Service{extensions: registry}).Capabilities()
	want := CapabilitiesView{
		Product: ProductView{Name: "Acme Desk", Version: "2.1.0"},
		Capabilities: []CapabilityView{
			{ID: "audit.records", Label: "Audit records", Extension: "audit"},
			{ID: "acme-sso.sign-in", Label: "Single sign-on", Extension: "acme-sso"},
			{ID: "acme-sso.groups.sync", Label: "Group sync", Extension: "acme-sso"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() = %+v, want %+v", got, want)
	}
}

func TestRegisterExtensionsRefuses(t *testing.T) {
	broken := errors.New("no endpoint configured")
	cases := []struct {
		name       string
		product    extension.Product
		extensions []extension.Extension
		want       string
	}{
		{"a product with no name", extension.Product{Version: "1.0"}, nil, "no name"},
		{"a nil extension", extension.Product{}, []extension.Extension{&fakeExtension{name: "one"}, nil}, "extension 2 is nil"},
		{"an empty name", extension.Product{}, []extension.Extension{&fakeExtension{}}, `extension name "" is not valid`},
		{"an upper-case name", extension.Product{}, []extension.Extension{&fakeExtension{name: "Audit"}}, `"Audit" is not valid`},
		{"Inkuna's own name", extension.Product{}, []extension.Extension{&fakeExtension{name: "inkuna"}}, `"inkuna" is Inkuna's own`},
		{"a duplicate name", extension.Product{}, []extension.Extension{&fakeExtension{name: "audit"}, &fakeExtension{name: "audit"}}, `two extensions are named "audit"`},
		{"a Register error", extension.Product{}, []extension.Extension{&fakeExtension{name: "audit", err: broken}}, `register extension "audit": no endpoint configured`},
		{"a capability outside the namespace", extension.Product{}, []extension.Extension{&fakeExtension{name: "audit", capabilities: []extension.Capability{{ID: "other.records", Label: "Records"}}}}, `capability "other.records" must be "audit."`},
		{"a capability that is only the namespace", extension.Product{}, []extension.Extension{&fakeExtension{name: "audit", capabilities: []extension.Capability{{ID: "audit.", Label: "Records"}}}}, `capability "audit." must be`},
		{"a capability with a malformed suffix", extension.Product{}, []extension.Extension{&fakeExtension{name: "audit", capabilities: []extension.Capability{{ID: "audit.Records..x", Label: "Records"}}}}, `capability "audit.Records..x" must be`},
		{"a capability with no label", extension.Product{}, []extension.Extension{&fakeExtension{name: "audit", capabilities: []extension.Capability{{ID: "audit.records", Label: " "}}}}, `capability "audit.records" has no label`},
		{"a capability registered twice", extension.Product{}, []extension.Extension{&fakeExtension{name: "audit", capabilities: []extension.Capability{{ID: "audit.records", Label: "Records"}, {ID: "audit.records", Label: "Records"}}}}, `capability "audit.records" is registered twice`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registerExtensions(tc.product, tc.extensions)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("registerExtensions: %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// An extension that keeps its registrar and uses it while a later extension
// registers must stop startup, so nothing it declares late is silently lost.
func TestARegistrarUsedAfterRegisterRefusesStartup(t *testing.T) {
	early := &fakeExtension{name: "early"}
	late := &fakeExtension{name: "late", during: func() {
		early.kept.Capability(extension.Capability{ID: "early.records", Label: "Records"})
	}}
	_, err := registerExtensions(extension.Product{}, []extension.Extension{early, late})
	if err == nil || !strings.Contains(err.Error(), `extension "early" used its registrar after Register returned`) {
		t.Fatalf("registerExtensions: %v, want the late registrar refused", err)
	}
}

// Once registration has finished nothing reads the registrar, so a call then
// panics rather than being dropped without a word.
func TestARegistrarUsedAfterStartupPanics(t *testing.T) {
	kept := &fakeExtension{name: "kept"}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{kept})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(recovered.(string), `extension "kept" used its registrar after Register returned`) {
			t.Fatalf("recovered %v, want a panic naming the extension", recovered)
		}
		if len(registry.capabilities) != 0 {
			t.Fatalf("a late capability was registered: %+v", registry.capabilities)
		}
	}()
	kept.kept.Capability(extension.Capability{ID: "kept.records", Label: "Records"})
}

// New refuses a broken build before it reads any configuration.
func TestNewRefusesABrokenExtension(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(config.EnvDataDir, dataDir)
	t.Setenv(config.EnvConfigFile, "")
	_, err := New(dataDir, extension.Product{}, []extension.Extension{&fakeExtension{name: "audit", err: errors.New("no endpoint configured")}})
	if err == nil || !strings.Contains(err.Error(), `register extensions: register extension "audit": no endpoint configured`) {
		t.Fatalf("New: %v, want the Register error", err)
	}
}

// configurableExtension decodes its section into endpoint, or fails with
// err, and keeps the section it was given.
type configurableExtension struct {
	fakeExtension
	section    yaml.Node
	configured int
	endpoint   string
	err        error
}

func (c *configurableExtension) Configure(section yaml.Node) error {
	c.configured++
	c.section = section
	if c.err != nil {
		return c.err
	}
	var settings struct {
		Endpoint string `yaml:"endpoint"`
	}
	if err := extension.DecodeConfig(section, &settings); err != nil {
		return err
	}
	c.endpoint = settings.Endpoint
	return nil
}

// writeConfiguration points the environment at a data directory whose
// inkuna.yaml is content, and returns the file's path.
func writeConfiguration(t *testing.T, content string) (string, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv(config.EnvDataDir, dataDir)
	t.Setenv(config.EnvConfigFile, "")
	path := config.DefaultPath(dataDir)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dataDir, path
}

// Each extension that takes configuration gets its section, or the zero Node
// when the file has none. A section for an extension the build lacks is
// reported, kept through a Settings save, and otherwise ignored.
func TestNewConfiguresEachExtensionFromItsSection(t *testing.T) {
	dataDir, path := writeConfiguration(t, "version: 1\nextensions:\n  example:\n    endpoint: https://records.example.com\n  elsewhere:\n    token: RECORDS_TOKEN\n")
	example := &configurableExtension{fakeExtension: fakeExtension{name: "example"}}
	quiet := &configurableExtension{fakeExtension: fakeExtension{name: "quiet"}}
	service, err := New(dataDir, extension.Product{}, []extension.Extension{example, quiet, &fakeExtension{name: "plain"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	if example.configured != 1 || example.endpoint != "https://records.example.com" {
		t.Fatalf("example configured %d times with endpoint %q", example.configured, example.endpoint)
	}
	if quiet.configured != 1 || quiet.section.Kind != 0 {
		t.Fatalf("an extension with no section is configured once with the zero Node: %d times, kind %v", quiet.configured, quiet.section.Kind)
	}
	check, ok := healthOf(service, "Extension elsewhere")
	if !ok || check.Status != "missing" || !strings.Contains(check.Detail, `this build has no extension named "elsewhere"; the section is kept and ignored`) {
		t.Fatalf("an unknown section must be reported: %+v, %v", check, ok)
	}
	if _, ok := healthOf(service, "Extension example"); ok {
		t.Fatal("a section the build has is not a problem")
	}

	// The view never sees the sections, so its save carries none; Go keeps
	// them as loaded.
	view, err := service.Configuration()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(view.File)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "RECORDS_TOKEN") {
		t.Fatalf("the view must not receive extension sections: %s", encoded)
	}
	if _, err := service.SaveConfiguration(string(encoded)); err != nil {
		t.Fatal(err)
	}
	saved, _, err := config.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if names := slices.Sorted(maps.Keys(saved.Extensions)); !slices.Equal(names, []string{"elsewhere", "example"}) {
		t.Fatalf("a Settings save must keep every section, got %v", names)
	}
}

// A section its extension refuses, or one for an extension that takes no
// configuration, stops startup as an invalid inkuna.yaml does, naming the
// file and the section.
func TestNewRefusesABadExtensionSection(t *testing.T) {
	for _, test := range []struct {
		name      string
		content   string
		extension extension.Extension
		want      string
	}{
		{
			name:      "unknown key",
			content:   "version: 1\nextensions:\n  example:\n    endpont: https://records.example.com\n",
			extension: &configurableExtension{fakeExtension: fakeExtension{name: "example"}},
			want:      "extensions.example: yaml: unmarshal errors:\n  line 4: field endpont not found",
		},
		{
			name:      "refused",
			content:   "version: 1\nextensions:\n  example:\n    endpoint: http://records.example.com\n",
			extension: &configurableExtension{fakeExtension: fakeExtension{name: "example"}, err: errors.New("endpoint must use https")},
			want:      "extensions.example: endpoint must use https",
		},
		{
			name:      "unconfigured refused",
			content:   "version: 1\n",
			extension: &configurableExtension{fakeExtension: fakeExtension{name: "example"}, err: errors.New("set extensions.example.endpoint")},
			want:      "extensions.example: set extensions.example.endpoint",
		},
		{
			name:      "takes no configuration",
			content:   "version: 1\nextensions:\n  example:\n    endpoint: https://records.example.com\n",
			extension: &fakeExtension{name: "example"},
			want:      `extensions.example: extension "example" takes no configuration; remove its section`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir, path := writeConfiguration(t, test.content)
			_, err := New(dataDir, extension.Product{}, []extension.Extension{test.extension})
			if err == nil || !strings.HasPrefix(err.Error(), path+": ") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New: %v, want %q after the path", err, test.want)
			}
		})
	}
}
