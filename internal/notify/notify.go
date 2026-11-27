// Package notify delivers attention requests to the user outside the app
// window. The desktop implementation uses the operating system's notification
// center; tests and headless runs use the recorder or no-op implementations.
package notify

import (
	"strings"
	"sync"

	"github.com/gen2brain/beeep"
)

// Notification is one attention request.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Kind groups notifications for the UI: input-required, review-required,
	// completed, failed, and tracker-failed for a story update that was not
	// made.
	Kind string `json:"kind"`
	// Reference points at the run or inbox item the user should open.
	Reference string `json:"reference,omitempty"`
}

// Notifier delivers notifications. Implementations must be safe for
// concurrent use and must never block a run on delivery failures.
type Notifier interface {
	Notify(Notification) error
}

// Desktop posts to macOS Notification Center, Windows toasts, or libnotify.
type Desktop struct{ AppName string }

// beeep exposes the application name as a package variable, so the assignment
// and the call are made one critical section.
var desktopMu sync.Mutex

func (desktop Desktop) Notify(notification Notification) error {
	name := desktop.AppName
	if name == "" {
		name = "Inkuna"
	}
	desktopMu.Lock()
	defer desktopMu.Unlock()
	beeep.AppName = name
	title := strings.TrimSpace(notification.Title)
	if title == "" {
		title = name
	}
	return beeep.Notify(title, truncate(notification.Body, 240), "")
}

// Noop discards notifications.
type Noop struct{}

func (Noop) Notify(Notification) error { return nil }

// Recorder stores notifications for assertions.
type Recorder struct {
	mu   sync.Mutex
	sent []Notification
}

func (recorder *Recorder) Notify(notification Notification) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.sent = append(recorder.sent, notification)
	return nil
}

// Sent returns a copy of everything recorded.
func (recorder *Recorder) Sent() []Notification {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]Notification(nil), recorder.sent...)
}

// Multi fans out to several notifiers and returns the first error.
type Multi []Notifier

func (multi Multi) Notify(notification Notification) error {
	var first error
	for _, notifier := range multi {
		if err := notifier.Notify(notification); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
