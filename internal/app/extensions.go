package app

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/inkuna-ai/inkuna/internal/version"
	"github.com/inkuna-ai/inkuna/pkg/extension"
	"gopkg.in/yaml.v3"
)

// inkunaProduct names Inkuna itself, for a binary that passes no product.
const inkunaProduct = "Inkuna"

var (
	extensionName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// capabilitySuffix is what follows "<extension>." in a capability ID.
	capabilitySuffix = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)
)

// extensionRegistry holds what the build's extensions registered. It is
// filled once, by registerExtensions, and read-only after that.
type extensionRegistry struct {
	product extension.Product
	// inkuna is set when the binary passed no product, so product is
	// Inkuna's own.
	inkuna       bool
	capabilities []registeredCapability
	// identity is the one identity provider registered, nil for the OS
	// user, and identityFrom names the extension that registered it.
	identity     extension.IdentityProvider
	identityFrom string
	// sinks are every audit and usage sink, in registration order.
	sinks []registeredSink
	// unknownSections names, in order, each section of inkuna.yaml's
	// extensions that no extension in the build has. They are kept in the
	// file and ignored, and Doctor reports them.
	unknownSections []string
	// policies are every policy, in registration order, which is the order
	// they are asked in.
	policies []registeredPolicy
	// sources are every definition source, in registration order.
	sources []registeredSource
	// secretStore is the one secret store registered, nil for the
	// keychain, and secretStoreFrom names the extension that registered it.
	secretStore     extension.SecretStore
	secretStoreFrom string
	// notifiers are every notifier, in registration order, each behind its
	// own queue.
	notifiers []*extensionNotifier

	mu sync.Mutex
	// sealed is set once registration has finished, after which a registrar
	// call panics: the registry is no longer read for anything it declares.
	sealed bool
	// late names extensions whose registrar was used after their Register
	// returned while registration was still going on.
	late []string
}

type registeredCapability struct {
	extension string
	extension.Capability
}

// registrar is the extension.Registrar one extension's Register receives.
type registrar struct {
	registry     *extensionRegistry
	name         string
	open         bool
	capabilities []extension.Capability
	identities   []extension.IdentityProvider
	auditSinks   []extension.AuditSink
	usageSinks   []extension.UsageSink
	policies     []extension.Policy
	sources      []extension.DefinitionSource
	secretStores []extension.SecretStore
	notifiers    []extension.Notifier
}

// registeredSink is one audit or usage sink; exactly one of audit and usage
// is set. Its name keeps its place in the outbox from one start to the next.
type registeredSink struct {
	name      string
	extension string
	audit     extension.AuditSink
	usage     extension.UsageSink
}

func (r *registrar) Capability(capability extension.Capability) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.capabilities = append(r.capabilities, capability)
}

func (r *registrar) Identity(provider extension.IdentityProvider) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.identities = append(r.identities, provider)
}

func (r *registrar) AuditSink(sink extension.AuditSink) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.auditSinks = append(r.auditSinks, sink)
}

func (r *registrar) UsageSink(sink extension.UsageSink) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.usageSinks = append(r.usageSinks, sink)
}

func (r *registrar) Policy(policy extension.Policy) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.policies = append(r.policies, policy)
}

func (r *registrar) DefinitionSource(source extension.DefinitionSource) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.sources = append(r.sources, source)
}

func (r *registrar) SecretStore(store extension.SecretStore) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.secretStores = append(r.secretStores, store)
}

func (r *registrar) Notifier(notifier extension.Notifier) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()
	if !r.use() {
		return
	}
	r.notifiers = append(r.notifiers, notifier)
}

// use reports whether a call may declare anything, recording or panicking on
// one made after Register returned. The caller holds registry.mu.
func (r *registrar) use() bool {
	if r.open {
		return true
	}
	if r.registry.sealed {
		panic(fmt.Sprintf("extension %q used its registrar after Register returned", r.name))
	}
	if !slices.Contains(r.registry.late, r.name) {
		r.registry.late = append(r.registry.late, r.name)
	}
	return false
}

// registerExtensions calls each extension's Register in order and checks what
// it declared. It refuses the whole build on the first mistake, so an app
// never starts with only some of an extension in place.
func registerExtensions(product extension.Product, extensions []extension.Extension) (*extensionRegistry, error) {
	registry := &extensionRegistry{product: product}
	if product == (extension.Product{}) {
		registry.product = extension.Product{Name: inkunaProduct, Version: version.Current().Version}
		registry.inkuna = true
	} else if strings.TrimSpace(product.Name) == "" {
		return nil, errors.New("the product has a version but no name; set Product.Name, or leave Product empty for Inkuna's own")
	}
	seen := map[string]bool{}
	for index, ext := range extensions {
		if ext == nil {
			return nil, fmt.Errorf("extension %d is nil", index+1)
		}
		name := ext.Name()
		if err := checkExtensionName(name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("two extensions are named %q; each name must be unique in the build", name)
		}
		seen[name] = true
		r := &registrar{registry: registry, name: name, open: true}
		err := ext.Register(r)
		registry.mu.Lock()
		r.open = false
		registry.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
		capabilities, err := checkCapabilities(name, r.capabilities, registry.capabilities)
		if err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
		registry.capabilities = append(registry.capabilities, capabilities...)
		if err := registry.addIdentity(name, r.identities); err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
		if err := registry.addSinks(name, r.auditSinks, r.usageSinks); err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
		if err := registry.addPolicies(name, r.policies); err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
		if err := registry.addSources(name, r.sources); err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
		if err := registry.addSecretStore(name, r.secretStores); err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
		if err := registry.addNotifiers(name, r.notifiers); err != nil {
			return nil, fmt.Errorf("register extension %q: %w", name, err)
		}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.sealed = true
	if len(registry.late) > 0 {
		return nil, fmt.Errorf("extension %q used its registrar after Register returned; declare everything during Register", registry.late[0])
	}
	return registry, nil
}

// configureExtensions hands each extension that takes configuration its
// section of inkuna.yaml, read from path, in registration order. A section
// no extension in the build has is not an error: the same file is read by
// builds without it, such as inkunactl's, which must still open the data
// directory. The caller reports what it returns instead.
func configureExtensions(path string, extensions []extension.Extension, sections map[string]yaml.Node) (unknown []string, err error) {
	names := map[string]bool{}
	for _, ext := range extensions {
		name := ext.Name()
		names[name] = true
		section, configured := sections[name]
		configurable, ok := ext.(extension.Configurable)
		if !ok {
			if configured {
				return nil, fmt.Errorf("%s: extensions.%s: extension %q takes no configuration; remove its section", path, name, name)
			}
			continue
		}
		if err := configurable.Configure(section); err != nil {
			return nil, fmt.Errorf("%s: extensions.%s: %w", path, name, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(sections)) {
		if !names[name] {
			unknown = append(unknown, name)
		}
	}
	return unknown, nil
}

// extensionHealth reports each section of inkuna.yaml for an extension this
// build lacks: a misspelt name, or a file shared with another build.
func (service *Service) extensionHealth() []HealthCheck {
	if service.extensions == nil {
		return nil
	}
	checks := make([]HealthCheck, 0, len(service.extensions.unknownSections))
	for _, name := range service.extensions.unknownSections {
		checks = append(checks, HealthCheck{
			Name: "Extension " + name, Status: "missing",
			Detail: fmt.Sprintf("inkuna.yaml configures extensions.%s, but this build has no extension named %q; the section is kept and ignored", name, name),
		})
	}
	return checks
}

// addIdentity takes the identity provider one extension registered, if any.
// Two providers would leave it unclear who is acting, so a build has one at
// most, whichever extensions register them.
func (registry *extensionRegistry) addIdentity(name string, providers []extension.IdentityProvider) error {
	for _, provider := range providers {
		if provider == nil {
			return errors.New("its identity provider is nil")
		}
		if registry.identity != nil {
			if registry.identityFrom == name {
				return errors.New("it registers more than one identity provider; a build has at most one")
			}
			return fmt.Errorf("extension %q already registers an identity provider; a build has at most one", registry.identityFrom)
		}
		registry.identity, registry.identityFrom = provider, name
	}
	return nil
}

// addSinks takes one extension's sinks. Each is named by its extension, its
// kind, and its place among that extension's sinks of the kind, such as
// "example/audit" and then "example/audit-2", which is how the outbox knows
// what a sink has been given across restarts.
func (registry *extensionRegistry) addSinks(name string, audits []extension.AuditSink, usages []extension.UsageSink) error {
	for index, sink := range audits {
		if sink == nil {
			return errors.New("its audit sink is nil")
		}
		registry.sinks = append(registry.sinks, registeredSink{name: sinkName(name, "audit", index), extension: name, audit: sink})
	}
	for index, sink := range usages {
		if sink == nil {
			return errors.New("its usage sink is nil")
		}
		registry.sinks = append(registry.sinks, registeredSink{name: sinkName(name, "usage", index), extension: name, usage: sink})
	}
	return nil
}

func sinkName(extensionName, kind string, index int) string {
	if index == 0 {
		return extensionName + "/" + kind
	}
	return fmt.Sprintf("%s/%s-%d", extensionName, kind, index+1)
}

func checkExtensionName(name string) error {
	if !extensionName.MatchString(name) {
		return fmt.Errorf("extension name %q is not valid: use a lower-case letter, then lower-case letters, digits, or hyphens", name)
	}
	if name == "inkuna" {
		return errors.New(`extension name "inkuna" is Inkuna's own; choose another`)
	}
	return nil
}

// checkCapabilities checks one extension's capabilities against its namespace
// and against those already registered.
func checkCapabilities(name string, declared []extension.Capability, registered []registeredCapability) ([]registeredCapability, error) {
	ids := map[string]bool{}
	for _, capability := range registered {
		ids[capability.ID] = true
	}
	checked := make([]registeredCapability, 0, len(declared))
	for _, capability := range declared {
		suffix, namespaced := strings.CutPrefix(capability.ID, name+".")
		if !namespaced || !capabilitySuffix.MatchString(suffix) {
			return nil, fmt.Errorf("capability %q must be %q followed by lower-case letters and digits, separated by single dots or hyphens", capability.ID, name+".")
		}
		if strings.TrimSpace(capability.Label) == "" {
			return nil, fmt.Errorf("capability %q has no label", capability.ID)
		}
		if ids[capability.ID] {
			return nil, fmt.Errorf("capability %q is registered twice", capability.ID)
		}
		ids[capability.ID] = true
		checked = append(checked, registeredCapability{extension: name, Capability: capability})
	}
	return checked, nil
}

// CapabilitiesView is what this build is and can do, for the view.
type CapabilitiesView struct {
	Product ProductView `json:"product"`
	// Capabilities are Inkuna's and every extension's, in registration
	// order. Inkuna's own screens are not gated, so with no extensions the
	// list is empty.
	Capabilities []CapabilityView `json:"capabilities"`
}

// ProductView names the app. Inkuna is set when the product is Inkuna itself
// rather than a binary that embeds it under its own name.
type ProductView struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Inkuna  bool   `json:"inkuna"`
}

// CapabilityView is one capability and the extension that registered it.
type CapabilityView struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Extension string `json:"extension"`
}

// Capabilities returns the product and every capability registered in this
// build. The view shows an extension's screen only when its capability is
// listed here.
func (service *Service) Capabilities() CapabilitiesView {
	registry := service.extensions
	if registry == nil {
		// A Service built without New, as tests build one, has Inkuna's own
		// product and nothing registered.
		registry, _ = registerExtensions(extension.Product{}, nil)
	}
	view := CapabilitiesView{
		Product:      ProductView{Name: registry.product.Name, Version: registry.product.Version, Inkuna: registry.inkuna},
		Capabilities: make([]CapabilityView, 0, len(registry.capabilities)),
	}
	for _, capability := range registry.capabilities {
		view.Capabilities = append(view.Capabilities, CapabilityView{ID: capability.ID, Label: capability.Label, Extension: capability.extension})
	}
	return view
}
