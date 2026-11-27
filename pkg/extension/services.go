package extension

import (
	"context"
	"errors"
)

// ErrSecretNotFound is what SecretStore.Get returns for a name the store
// holds no value for. Inkuna reports the secret as missing, as it does a
// keychain without the item; any other error is shown beside the secret.
var ErrSecretNotFound = errors.New("secret not found")

// SecretStore keeps the values of the secrets inkuna.yaml declares, in place
// of the OS keychain. The process environment still wins: a value there
// overrides the store for one launch, and the store is not asked. Inkuna
// asks only for names it has checked are upper-case environment variable
// names, never sets an empty value, and writes a value nowhere else. Set and
// Delete come from Settings storing or forgetting a value. Deleting a name
// the store holds no value for is not an error. A call that takes longer
// than 30 seconds is abandoned, whether or not it watches its context, and
// fails; one that panics fails too.
type SecretStore interface {
	Get(ctx context.Context, name string) (string, error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
}

// Notifier receives every notification Inkuna asks the user to look at,
// whether or not desktop notifications are on. Inkuna hands a notifier its
// notifications in order from a queue of its own, so a slow one never holds
// up a run: while 64 wait, a new one is dropped and logged. An error is
// logged, never retried. A call that takes longer than 30 seconds is
// abandoned, whether or not it watches its context, and the next
// notification goes, so one that returns late can land after it.
type Notifier interface {
	Notify(context.Context, Notification) error
}

// Notification is one thing Inkuna asks the user to look at, as the desktop
// notification shows it.
type Notification struct {
	Title string `json:"title"`
	// Body is what it is about, such as the run's title or the question a
	// worker asked, so it can hold what a worker or a repository wrote.
	Body string `json:"body"`
	// Kind is "input-required", "review-required", "completed", "failed",
	// or "tracker-failed" for a story update that was not made.
	Kind string `json:"kind"`
	// Reference is the run to open, or for input-required the inbox item.
	Reference string `json:"reference,omitempty"`
}
