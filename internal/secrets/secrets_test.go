package secrets

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	store := NewMemory()
	if _, err := store.Get("LINEAR_API_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := store.Set("LINEAR_API_KEY", "lin_api_test"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get("LINEAR_API_KEY")
	if err != nil || value != "lin_api_test" {
		t.Fatalf("unexpected value %q err %v", value, err)
	}
	if err := store.Delete("LINEAR_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("LINEAR_API_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deletion, got %v", err)
	}
}

func TestNamesMustLookLikeEnvironmentVariables(t *testing.T) {
	store := NewMemory()
	for _, name := range []string{"", "lower", "HAS SPACE", "1START", "HAS-DASH"} {
		if err := store.Set(name, "value"); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
	if err := store.Set("OK_NAME", ""); err == nil {
		t.Fatal("expected empty value to be rejected")
	}
}

func TestResolverPrefersEnvironmentOverStore(t *testing.T) {
	store := NewMemory()
	_ = store.Set("ANTHROPIC_API_KEY", "from-keychain")
	resolver := Resolver{Store: store, Environ: func(name string) string {
		if name == "ANTHROPIC_API_KEY" {
			return "from-env"
		}
		return ""
	}}
	value, source, err := resolver.Resolve("ANTHROPIC_API_KEY")
	if err != nil || value != "from-env" || source != SourceEnvironment {
		t.Fatalf("unexpected %q %s %v", value, source, err)
	}
	resolver.Environ = func(string) string { return "" }
	value, source, err = resolver.Resolve("ANTHROPIC_API_KEY")
	if err != nil || value != "from-keychain" || source != SourceKeychain {
		t.Fatalf("unexpected %q %s %v", value, source, err)
	}
	value, source, err = resolver.Resolve("OPENAI_API_KEY")
	if err != nil || value != "" || source != SourceMissing {
		t.Fatalf("expected missing, got %q %s %v", value, source, err)
	}
}

func TestStatusesAndEnvironmentNeverLeakValues(t *testing.T) {
	store := NewMemory()
	_ = store.Set("B_KEY", "secret-b")
	resolver := Resolver{Store: store, Environ: func(string) string { return "" }}
	statuses := resolver.Statuses([]string{"B_KEY", "A_KEY"})
	if len(statuses) != 2 || statuses[0].Name != "A_KEY" || statuses[0].Available || !statuses[1].Available {
		t.Fatalf("unexpected statuses %+v", statuses)
	}
	environment, err := resolver.Environment([]string{"A_KEY", "B_KEY"})
	if err != nil || len(environment) != 1 || environment[0] != "B_KEY=secret-b" {
		t.Fatalf("unexpected environment %v %v", environment, err)
	}
	failing := Resolver{Store: failingStore{}, Environ: func(string) string { return "" }}
	environment, err = failing.Environment([]string{"B_KEY"})
	if err != nil || len(environment) != 0 {
		t.Fatalf("backend failures must yield a missing secret, got %v %v", environment, err)
	}
	if _, err := failing.Environment([]string{"bad name"}); err == nil {
		t.Fatal("invalid names must still be rejected")
	}
}

type failingStore struct{}

func (failingStore) Get(string) (string, error) { return "", errors.New("keychain locked") }
func (failingStore) Set(string, string) error   { return errors.New("keychain locked") }
func (failingStore) Delete(string) error        { return errors.New("keychain locked") }

func TestKeyringRoundTripStaysInItsService(t *testing.T) {
	keyring.MockInit()
	store := Keyring{}
	if err := store.Set("LINEAR_API_KEY", "lin_api_inkuna"); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get("LINEAR_API_KEY"); err != nil || value != "lin_api_inkuna" {
		t.Fatalf("stored secret: %q %v", value, err)
	}
	if stored, err := keyring.Get(DefaultService, "LINEAR_API_KEY"); err != nil || stored != "lin_api_inkuna" {
		t.Fatalf("the default store must use Inkuna's service: %q %v", stored, err)
	}
	// A store with a service of its own never reads another service's items.
	if _, err := (Keyring{Service: "test.service"}).Get("LINEAR_API_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a custom service read the default service's item: %v", err)
	}
	if err := store.Delete("LINEAR_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("LINEAR_API_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a deleted secret came back: %v", err)
	}
	// Deleting what was never stored is not an error.
	if err := store.Delete("LINEAR_API_KEY"); err != nil {
		t.Fatalf("delete of a missing secret: %v", err)
	}
}

// labeledStore is a store that names itself as its values' source.
type labeledStore struct{ *Memory }

func (labeledStore) Source() Source { return "vault" }

func TestALabeledStoreNamesItsOwnSource(t *testing.T) {
	store := labeledStore{NewMemory()}
	_ = store.Set("VAULT_TOKEN", "from-vault")
	resolver := Resolver{Store: store, Environ: func(string) string { return "" }}
	if value, source, err := resolver.Resolve("VAULT_TOKEN"); err != nil || value != "from-vault" || source != "vault" {
		t.Fatalf("Resolve = %q from %s (%v), want the store's own source", value, source, err)
	}
	// The environment still wins, and a missing value is still missing.
	resolver.Environ = func(string) string { return "from-env" }
	if _, source, _ := resolver.Resolve("VAULT_TOKEN"); source != SourceEnvironment {
		t.Fatalf("the environment must win over a labeled store, got %s", source)
	}
	resolver.Environ = func(string) string { return "" }
	if _, source, _ := resolver.Resolve("OTHER_TOKEN"); source != SourceMissing {
		t.Fatalf("a name the store lacks must be missing, got %s", source)
	}
}
