package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// extensionCallTimeout bounds each call to an extension's secret store or
// notifier, so one that hangs cannot hold up a worker's start, a Settings
// save, or the notifications after it for longer. A variable so tests can
// shorten it.
var extensionCallTimeout = 30 * time.Second

// notifierBacklog is how many notifications wait for an extension's
// notifier before a new one is dropped.
const notifierBacklog = 64

// addSecretStore takes the secret store one extension registered, if any.
// Two stores would leave it unclear where a value is kept, so a build has
// one at most, whichever extensions register them.
func (registry *extensionRegistry) addSecretStore(name string, stores []extension.SecretStore) error {
	for _, store := range stores {
		if store == nil {
			return errors.New("its secret store is nil")
		}
		if registry.secretStore != nil {
			if registry.secretStoreFrom == name {
				return errors.New("it registers more than one secret store; a build has at most one")
			}
			return fmt.Errorf("extension %q already registers a secret store; a build has at most one", registry.secretStoreFrom)
		}
		registry.secretStore, registry.secretStoreFrom = store, name
	}
	return nil
}

func (registry *extensionRegistry) addNotifiers(name string, notifiers []extension.Notifier) error {
	for _, notifier := range notifiers {
		if notifier == nil {
			return errors.New("its notifier is nil")
		}
		registry.notifiers = append(registry.notifiers, &extensionNotifier{extension: name, notifier: notifier, queue: make(chan notify.Notification, notifierBacklog)})
	}
	return nil
}

// secretStoreOr is the registered secret store, or fallback when none is.
func (registry *extensionRegistry) secretStoreOr(fallback secrets.Store) secrets.Store {
	if registry == nil || registry.secretStore == nil {
		return fallback
	}
	return extensionSecrets{extension: registry.secretStoreFrom, store: registry.secretStore}
}

// notifierWith adds the registered notifiers to Inkuna's own, which is the
// desktop or nothing, so each receives the same notifications it does.
func (registry *extensionRegistry) notifierWith(own notify.Notifier) notify.Notifier {
	if registry == nil || len(registry.notifiers) == 0 {
		return own
	}
	multi := notify.Multi{own}
	for _, notifier := range registry.notifiers {
		multi = append(multi, notifier)
	}
	return multi
}

// extensionSecrets is an extension's secret store as Inkuna's own. Names are
// checked and empty values refused before the extension sees them, as the
// keychain store does, and the extension's name is the source the view
// shows for a value it holds.
type extensionSecrets struct {
	extension string
	store     extension.SecretStore
}

func (store extensionSecrets) Get(name string) (string, error) {
	if err := secrets.ValidateName(name); err != nil {
		return "", err
	}
	value, err := callExtension(store.extension, "secret store", func(ctx context.Context) (string, error) {
		value, err := store.store.Get(ctx, name)
		if errors.Is(err, extension.ErrSecretNotFound) {
			return "", secrets.ErrNotFound
		}
		return value, err
	})
	if errors.Is(err, secrets.ErrNotFound) {
		return "", err
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	// An empty value is no value, as it is in the environment.
	if strings.TrimSpace(value) == "" {
		return "", secrets.ErrNotFound
	}
	return value, nil
}

func (store extensionSecrets) Set(name, value string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("secret value is required")
	}
	_, err := callExtension(store.extension, "secret store", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.store.Set(ctx, name, value)
	})
	if err != nil {
		return fmt.Errorf("store %s: %w", name, err)
	}
	return nil
}

func (store extensionSecrets) Delete(name string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}
	_, err := callExtension(store.extension, "secret store", func(ctx context.Context) (struct{}, error) {
		if err := store.store.Delete(ctx, name); !errors.Is(err, extension.ErrSecretNotFound) {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	if err != nil {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}

// Source names the extension as where a value came from, in place of the
// keychain.
func (store extensionSecrets) Source() secrets.Source { return secrets.Source(store.extension) }

// extensionNotifier hands an extension's notifier its notifications from a
// queue, in order, so a notifier that is slow or down never holds up the
// run that notified. Its goroutine starts with the first notification and
// lives as long as the process.
type extensionNotifier struct {
	extension string
	notifier  extension.Notifier
	queue     chan notify.Notification
	start     sync.Once
}

// Notify queues the notification, or drops it when the queue is full. It
// never fails: what the notifier returns is logged as it is delivered.
func (notifier *extensionNotifier) Notify(notification notify.Notification) error {
	notifier.start.Do(func() { go notifier.deliver() })
	select {
	case notifier.queue <- notification:
	default:
		log.Printf("inkuna: extension %q's notifier is %d notifications behind; dropped a %s notification", notifier.extension, notifierBacklog, notification.Kind)
	}
	return nil
}

// deliver hands over each queued notification. One the notifier has not
// taken within the deadline is abandoned and the next one goes, so a
// notifier that ignores its context costs each notification the deadline,
// never the queue.
func (notifier *extensionNotifier) deliver() {
	for notification := range notifier.queue {
		_, err := callExtension(notifier.extension, "notifier", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, notifier.notifier.Notify(ctx, extension.Notification{Title: notification.Title, Body: notification.Body, Kind: notification.Kind, Reference: notification.Reference})
		})
		if err != nil {
			log.Printf("inkuna: a %s notification was not delivered: %v", notification.Kind, err)
		}
	}
}

// callExtension calls an extension's secret store or notifier on a
// goroutine of its own and waits at most extensionCallTimeout, so one that
// ignores its context cannot hold up its caller past the deadline, and one
// that panics cannot take the app down. The error it returns names the
// extension. A call it stops waiting for keeps its goroutine until it
// returns; its answer is dropped. An error's message is the extension's, so
// it is rendered on that goroutine too, where one that blocks meets the
// deadline and one that panics is recovered; only Inkuna's own
// secrets.ErrNotFound is passed on as itself.
func callExtension[T any](name, point string, call func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), extensionCallTimeout)
	defer cancel()
	type answer struct {
		value T
		err   error
	}
	answered := make(chan answer, 1)
	go func() {
		// A failure until call returns, so one that panics, or ends its
		// goroutine with runtime.Goexit, which recover cannot see, still
		// answers.
		result := answer{err: fmt.Errorf("extension %q's %s stopped without answering", name, point)}
		defer func() {
			if recovered := recover(); recovered != nil {
				// What a panic says may hold anything, so it goes to the log
				// rather than to the person.
				log.Printf("inkuna: extension %q's %s panicked: %v", name, point, recovered)
				result = answer{err: fmt.Errorf("extension %q's %s failed; see the log", name, point)}
			}
			answered <- result
		}()
		value, err := call(ctx)
		switch {
		case err == nil:
			result = answer{value: value}
		case errors.Is(err, secrets.ErrNotFound):
			result = answer{err: secrets.ErrNotFound}
		default:
			result = answer{err: fmt.Errorf("extension %q: %s", name, err.Error())}
		}
	}()
	select {
	case result := <-answered:
		return result.value, result.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("extension %q's %s did not answer within %s", name, point, extensionCallTimeout)
	}
}
