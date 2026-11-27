package app

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// vaultStore is an extension's secret store kept in memory, counting what it
// is asked.
type vaultStore struct {
	mu     sync.Mutex
	values map[string]string
	gets   int
	err    error
}

func (store *vaultStore) Get(_ context.Context, name string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.gets++
	if store.err != nil {
		return "", store.err
	}
	value, ok := store.values[name]
	if !ok {
		return "", extension.ErrSecretNotFound
	}
	return value, nil
}

func (store *vaultStore) Set(_ context.Context, name, value string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[name] = value
	return nil
}

func (store *vaultStore) Delete(_ context.Context, name string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.values[name]; !ok {
		return extension.ErrSecretNotFound
	}
	delete(store.values, name)
	return nil
}

func (store *vaultStore) value(name string) (string, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.values[name]
	return value, ok
}

// heldNotifier records what it is given, after waiting for release when it
// has one.
type heldNotifier struct {
	mu       sync.Mutex
	received []extension.Notification
	release  chan struct{}
}

func (notifier *heldNotifier) Notify(_ context.Context, notification extension.Notification) error {
	if notifier.release != nil {
		<-notifier.release
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	notifier.received = append(notifier.received, notification)
	return nil
}

// waitFor returns what the notifier received once it holds count.
func (notifier *heldNotifier) waitFor(t *testing.T, count int) []extension.Notification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		notifier.mu.Lock()
		received := append([]extension.Notification(nil), notifier.received...)
		notifier.mu.Unlock()
		if len(received) >= count || time.Now().After(deadline) {
			return received
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestARegisteredSecretStoreKeepsTheValues(t *testing.T) {
	dataDir, path := writeConfiguration(t, "version: 1\nsecrets:\n  - name: VAULT_TOKEN\n")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_OTHER", "")
	vault := &vaultStore{values: map[string]string{"VAULT_TOKEN": "from-vault"}}
	service, err := New(dataDir, extension.Product{}, []extension.Extension{&fakeExtension{name: "vault", secretStores: []extension.SecretStore{vault}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	value, source, err := service.secrets.Resolve("VAULT_TOKEN")
	if err != nil || value != "from-vault" || source != "vault" {
		t.Fatalf("Resolve = %q from %s (%v), want the vault's value from vault", value, source, err)
	}
	if value, source, err := service.secrets.Resolve("VAULT_OTHER"); err != nil || value != "" || source != secrets.SourceMissing {
		t.Fatalf("a name the store lacks resolves to %q from %s (%v), want missing", value, source, err)
	}

	// Settings stores and forgets values in the extension's store, which a
	// configuration save keeps.
	if _, err := service.SaveSecret("VAULT_OTHER", "saved"); err != nil {
		t.Fatal(err)
	}
	if value, ok := vault.value("VAULT_OTHER"); !ok || value != "saved" {
		t.Fatalf("SaveSecret stored %q (%v) in the extension's store", value, ok)
	}
	if file, _, err := config.ReadFile(path); err != nil || len(file.Secrets) != 2 {
		t.Fatalf("SaveSecret must declare the new name: %+v (%v)", file.Secrets, err)
	}
	if value, source, err := service.secrets.Resolve("VAULT_OTHER"); err != nil || value != "saved" || source != "vault" {
		t.Fatalf("after a configuration save, Resolve = %q from %s (%v)", value, source, err)
	}
	if _, err := service.DeleteSecret("VAULT_OTHER"); err != nil {
		t.Fatal(err)
	}
	if _, ok := vault.value("VAULT_OTHER"); ok {
		t.Fatal("DeleteSecret left the value in the extension's store")
	}
	// Forgetting what was never stored is not an error, as with the keychain.
	if _, err := service.DeleteSecret("VAULT_OTHER"); err != nil {
		t.Fatalf("deleting a missing secret: %v", err)
	}
	if err := service.secretStore.Set("lower", "value"); err == nil {
		t.Fatal("a name that is not an environment variable name reached the extension")
	}
	if err := service.secretStore.Set("VAULT_OTHER", " "); err == nil {
		t.Fatal("an empty value reached the extension")
	}

	// A store that fails is shown beside the secret, never as its value.
	vault.err = errors.New("vault sealed")
	statuses := service.secrets.Statuses([]string{"VAULT_TOKEN"})
	if len(statuses) != 1 || statuses[0].Available || !strings.Contains(statuses[0].Detail, `extension "vault": vault sealed`) {
		t.Fatalf("a failing store's status = %+v", statuses)
	}
}

func TestTheEnvironmentWinsOverARegisteredSecretStore(t *testing.T) {
	dataDir, _ := writeConfiguration(t, "version: 1\n")
	t.Setenv("VAULT_TOKEN", "from-environment")
	vault := &vaultStore{values: map[string]string{"VAULT_TOKEN": "from-vault"}}
	service, err := New(dataDir, extension.Product{}, []extension.Extension{&fakeExtension{name: "vault", secretStores: []extension.SecretStore{vault}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	value, source, err := service.secrets.Resolve("VAULT_TOKEN")
	if err != nil || value != "from-environment" || source != secrets.SourceEnvironment {
		t.Fatalf("Resolve = %q from %s (%v), want the environment's", value, source, err)
	}
	environment, err := service.secrets.Environment([]string{"VAULT_TOKEN"})
	if err != nil || !reflect.DeepEqual(environment, []string{"VAULT_TOKEN=from-environment"}) {
		t.Fatalf("a worker's environment = %v (%v)", environment, err)
	}
	if vault.gets != 0 {
		t.Fatalf("the store was asked %d times for a value the environment holds", vault.gets)
	}
}

func TestABuildHasAtMostOneSecretStore(t *testing.T) {
	store := func() extension.SecretStore { return &vaultStore{values: map[string]string{}} }
	cases := []struct {
		name       string
		extensions []extension.Extension
		want       string
	}{
		{"a nil store", []extension.Extension{&fakeExtension{name: "vault", secretStores: []extension.SecretStore{nil}}}, "its secret store is nil"},
		{"two from one extension", []extension.Extension{&fakeExtension{name: "vault", secretStores: []extension.SecretStore{store(), store()}}}, "more than one secret store"},
		{"one each from two", []extension.Extension{&fakeExtension{name: "vault", secretStores: []extension.SecretStore{store()}}, &fakeExtension{name: "other", secretStores: []extension.SecretStore{store()}}}, `extension "vault" already registers a secret store`},
		{"a nil notifier", []extension.Extension{&fakeExtension{name: "chat", notifiers: []extension.Notifier{nil}}}, "its notifier is nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registerExtensions(extension.Product{}, tc.extensions)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("registerExtensions: %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// With none registered, the keychain keeps the values.
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.secretStoreOr(secrets.Keyring{}).(secrets.Keyring); !ok {
		t.Fatal("a build with no secret store must use the keychain")
	}
}

func TestANotifierReceivesWhatTheDesktopDoes(t *testing.T) {
	first, second := &heldNotifier{}, &heldNotifier{}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{
		&fakeExtension{name: "chat", notifiers: []extension.Notifier{first}}, &fakeExtension{name: "pager", notifiers: []extension.Notifier{second}},
	})
	if err != nil {
		t.Fatal(err)
	}
	desktop := &notify.Recorder{}
	notifier := registry.notifierWith(desktop)
	sent := []notify.Notification{
		{Title: "Inkuna needs your input", Body: "Which database?", Kind: "input-required", Reference: "input-1"},
		{Title: "Review needed", Body: "Add a cost view", Kind: "review-required", Reference: "run-1"},
		{Title: "A tracker change failed", Body: "Comment on MAD-12: refused", Kind: "tracker-failed", Reference: "run-1"},
		{Title: "Task completed", Body: "Add a cost view", Kind: "completed", Reference: "run-1"},
		{Title: "Task failed", Body: "Fix the tasks list", Kind: "failed", Reference: "run-2"},
	}
	for _, notification := range sent {
		if err := notifier.Notify(notification); err != nil {
			t.Fatal(err)
		}
	}
	want := make([]extension.Notification, 0, len(sent))
	for _, notification := range desktop.Sent() {
		want = append(want, extension.Notification{Title: notification.Title, Body: notification.Body, Kind: notification.Kind, Reference: notification.Reference})
	}
	if len(want) != len(sent) {
		t.Fatalf("the desktop received %d notifications, want %d", len(want), len(sent))
	}
	for name, extensionNotifier := range map[string]*heldNotifier{"chat": first, "pager": second} {
		if got := extensionNotifier.waitFor(t, len(want)); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s received %+v, want what the desktop did: %+v", name, got, want)
		}
	}
}

// With desktop notifications off, an extension's notifier still receives
// every notification.
func TestANotifierIsNotifiedWithDesktopNotificationsOff(t *testing.T) {
	dataDir, _ := writeConfiguration(t, "version: 1\n")
	t.Setenv(config.EnvDesktopNotifications, "false")
	chat := &heldNotifier{}
	service, err := New(dataDir, extension.Product{}, []extension.Extension{&fakeExtension{name: "chat", notifiers: []extension.Notifier{chat}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	if service.config.DesktopNotifications {
		t.Fatal("the test needs desktop notifications off")
	}
	if err := service.notifier.Notify(notify.Notification{Title: "Review needed", Kind: "review-required", Reference: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if got := chat.waitFor(t, 1); len(got) != 1 || got[0].Kind != "review-required" {
		t.Fatalf("the notifier received %+v", got)
	}
}

func TestASlowNotifierNeverHoldsUpARun(t *testing.T) {
	slow := &heldNotifier{release: make(chan struct{})}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "chat", notifiers: []extension.Notifier{slow}}})
	if err != nil {
		t.Fatal(err)
	}
	notifier := registry.notifierWith(notify.Noop{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := range 2 * notifierBacklog {
			_ = notifier.Notify(notify.Notification{Title: strconv.Itoa(index), Kind: "completed", Reference: "run-1"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a notifier that does not answer held up Notify")
	}
	close(slow.release)
	// The first notifications fill the queue, so they all arrive. The
	// goroutine may take its first only after the queue filled, freeing one
	// place for a later notification; every other one was dropped. Whatever
	// arrives comes in the order it was sent.
	slow.waitFor(t, notifierBacklog)
	time.Sleep(50 * time.Millisecond)
	received := slow.waitFor(t, 0)
	if len(received) < notifierBacklog || len(received) > notifierBacklog+1 {
		t.Fatalf("received %d notifications, want the %d queued and at most one more", len(received), notifierBacklog)
	}
	previous := -1
	for index, notification := range received {
		sent, err := strconv.Atoi(notification.Title)
		if err != nil || sent <= previous || (index < notifierBacklog && sent != index) {
			t.Fatalf("notification %d is %q after %d: the queued ones must arrive first, in order", index, notification.Title, previous)
		}
		previous = sent
	}
}

// stuckStore ignores its context: Get blocks until release, or panics.
type stuckStore struct {
	release chan struct{}
	panics  bool
}

func (store stuckStore) Get(context.Context, string) (string, error) {
	if store.panics {
		panic("the store's own bug")
	}
	<-store.release
	return "late", nil
}

func (store stuckStore) Set(context.Context, string, string) error {
	<-store.release
	return nil
}

func (store stuckStore) Delete(context.Context, string) error {
	<-store.release
	return nil
}

func shortExtensionCalls(t *testing.T) {
	t.Helper()
	previous := extensionCallTimeout
	extensionCallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { extensionCallTimeout = previous })
}

// Go enforces the deadline, so a store that ignores its context, or panics,
// still answers in time and never takes the app down.
func TestASecretStoreThatIgnoresItsContextStillAnswersInTime(t *testing.T) {
	shortExtensionCalls(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	stuck := extensionSecrets{extension: "vault", store: stuckStore{release: release}}
	for name, call := range map[string]func() error{
		"Get":    func() error { _, err := stuck.Get("VAULT_TOKEN"); return err },
		"Set":    func() error { return stuck.Set("VAULT_TOKEN", "value") },
		"Delete": func() error { return stuck.Delete("VAULT_TOKEN") },
	} {
		started := time.Now()
		err := call()
		if err == nil || !strings.Contains(err.Error(), `extension "vault"'s secret store did not answer within`) {
			t.Fatalf("%s of a stuck store = %v", name, err)
		}
		if waited := time.Since(started); waited > 2*time.Second {
			t.Fatalf("%s waited %s for a store that ignores its context", name, waited)
		}
	}
	resolver := secrets.Resolver{Store: stuck, Environ: func(string) string { return "" }}
	if value, source, err := resolver.Resolve("VAULT_TOKEN"); err == nil || value != "" || source != secrets.SourceMissing {
		t.Fatalf("a stuck store resolves to %q from %s (%v)", value, source, err)
	}
	panicking := extensionSecrets{extension: "vault", store: stuckStore{panics: true}}
	if _, err := panicking.Get("VAULT_TOKEN"); err == nil || !strings.Contains(err.Error(), `extension "vault"'s secret store failed; see the log`) || strings.Contains(err.Error(), "own bug") {
		t.Fatalf("a panicking store = %v; want a failure that keeps what it said out of the message", err)
	}
}

// stuckNotifier hangs on its first notification, ignoring its context, and
// takes the rest.
type stuckNotifier struct {
	heldNotifier
	first sync.Once
	hang  chan struct{}
}

func (notifier *stuckNotifier) Notify(ctx context.Context, notification extension.Notification) error {
	hung := false
	notifier.first.Do(func() { hung = true })
	if hung {
		<-notifier.hang
		return nil
	}
	return notifier.heldNotifier.Notify(ctx, notification)
}

func TestANotifierThatIgnoresItsContextDoesNotHoldUpTheNext(t *testing.T) {
	shortExtensionCalls(t)
	stuck := &stuckNotifier{hang: make(chan struct{})}
	t.Cleanup(func() { close(stuck.hang) })
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "chat", notifiers: []extension.Notifier{stuck}}})
	if err != nil {
		t.Fatal(err)
	}
	notifier := registry.notifierWith(notify.Noop{})
	for _, kind := range []string{"review-required", "completed"} {
		_ = notifier.Notify(notify.Notification{Kind: kind, Reference: "run-1"})
	}
	if got := stuck.waitFor(t, 1); len(got) != 1 || got[0].Kind != "completed" {
		t.Fatalf("after a notification the notifier never took, it received %+v", got)
	}
}
