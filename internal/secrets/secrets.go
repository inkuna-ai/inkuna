// Package secrets resolves named credentials without ever writing them to
// inkuna.yaml or SQLite. Values live in the operating system keychain (macOS
// Keychain, Windows Credential Manager, or a libsecret service on Linux) and
// can be overridden for one launch through the process environment.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound reports that a secret has no stored value.
var ErrNotFound = errors.New("secret not found")

// DefaultService is the keychain service name under which Inkuna stores items.
const DefaultService = "ai.inkuna.inkuna"

var safeName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

// ValidateName accepts conventional environment variable names only, which
// keeps keychain item names predictable and injection-free.
func ValidateName(name string) error {
	if !safeName.MatchString(name) {
		return fmt.Errorf("secret name %q must be an upper-case environment variable name", name)
	}
	return nil
}

// Store is the minimal contract every backing store fulfils.
type Store interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

// Keyring stores secrets through the platform credential manager.
type Keyring struct{ Service string }

func (store Keyring) service() string {
	if store.Service == "" {
		return DefaultService
	}
	return store.Service
}

func (store Keyring) Get(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	value, err := keyring.Get(store.service(), name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read %s from keychain: %w", name, err)
	}
	return value, nil
}

func (store Keyring) Set(name, value string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("secret value is required")
	}
	if err := keyring.Set(store.service(), name, value); err != nil {
		return fmt.Errorf("store %s in keychain: %w", name, err)
	}
	return nil
}

func (store Keyring) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := keyring.Delete(store.service(), name); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete %s from keychain: %w", name, err)
	}
	return nil
}

// Memory keeps secrets in process memory. It backs tests and headless CI
// environments that have no credential manager.
type Memory struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewMemory() *Memory { return &Memory{values: make(map[string]string)} }

func (store *Memory) Get(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	value, ok := store.values[name]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (store *Memory) Set(name, value string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("secret value is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[name] = value
	return nil
}

func (store *Memory) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.values, name)
	return nil
}

// Source names where a resolved value came from.
type Source string

// Labeled is a Store that names itself as the source of the values it
// holds, in place of SourceKeychain, as an extension's store does.
type Labeled interface {
	Source() Source
}

const (
	SourceEnvironment Source = "environment"
	SourceKeychain    Source = "keychain"
	SourceMissing     Source = "missing"
)

// Resolver looks a secret up in the environment first, then the store. The
// environment wins so a one-off launch can override a saved credential.
type Resolver struct {
	Store   Store
	Environ func(string) string
}

func NewResolver(store Store) Resolver { return Resolver{Store: store, Environ: os.Getenv} }

// Resolve returns the value and where it came from. A missing secret is not an
// error; callers decide whether the feature that needs it is optional.
func (resolver Resolver) Resolve(name string) (string, Source, error) {
	if err := ValidateName(name); err != nil {
		return "", SourceMissing, err
	}
	environ := resolver.Environ
	if environ == nil {
		environ = os.Getenv
	}
	if value := strings.TrimSpace(environ(name)); value != "" {
		return value, SourceEnvironment, nil
	}
	if resolver.Store == nil {
		return "", SourceMissing, nil
	}
	value, err := resolver.Store.Get(name)
	if errors.Is(err, ErrNotFound) {
		return "", SourceMissing, nil
	}
	if err != nil {
		return "", SourceMissing, err
	}
	if labeled, ok := resolver.Store.(Labeled); ok {
		return value, labeled.Source(), nil
	}
	return value, SourceKeychain, nil
}

// Status describes one declared secret without revealing its value.
type Status struct {
	Name      string `json:"name"`
	Source    Source `json:"source"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

// Statuses reports every declared secret in stable order.
func (resolver Resolver) Statuses(names []string) []Status {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	result := make([]Status, 0, len(sorted))
	for _, name := range sorted {
		value, source, err := resolver.Resolve(name)
		status := Status{Name: name, Source: source, Available: value != ""}
		if err != nil {
			status.Detail = err.Error()
		}
		result = append(result, status)
	}
	return result
}

// Environment returns KEY=value pairs for every available secret so a worker
// process can be given exactly the credentials it was declared to need.
// A secret whose backend read fails is treated as unavailable so one
// unreadable keychain entry does not block a worker that does not need it;
// an invalid name is still an error because it indicates a configuration bug.
func (resolver Resolver) Environment(names []string) ([]string, error) {
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		if err := ValidateName(name); err != nil {
			return nil, err
		}
		value, _, err := resolver.Resolve(name)
		if err != nil || value == "" {
			continue
		}
		pairs = append(pairs, name+"="+value)
	}
	return pairs, nil
}
