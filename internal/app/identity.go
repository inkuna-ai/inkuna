package app

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// identityTimeout bounds how long a decision waits to learn who is making
// it. A provider that takes longer refuses the decision, as one that errs
// does, rather than leaving the person's click hanging. Tests shorten it.
var identityTimeout = 30 * time.Second

// principal returns the person a decision is made for: the registered
// identity provider's answer, or the OS user when there is none. Every
// decision that needs a person asks before it changes anything, and an
// error refuses it with the provider's reason. The principal goes to
// records only; nothing that builds a prompt reads it.
func (service *Service) principal(ctx context.Context) (extension.Principal, error) {
	registry := service.extensions
	if registry == nil || registry.identity == nil {
		return osPrincipal(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, identityTimeout)
	defer cancel()
	type answer struct {
		principal extension.Principal
		err       error
	}
	// The provider runs apart, so one that ignores its context still cannot
	// hold the decision past the deadline.
	answered := make(chan answer, 1)
	go func() {
		principal, err := registry.identity.Current(ctx)
		answered <- answer{principal, err}
	}()
	var current answer
	select {
	case current = <-answered:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return extension.Principal{}, fmt.Errorf("identity provider %q did not say who is acting within %s; try again", registry.identityFrom, identityTimeout)
		}
		return extension.Principal{}, fmt.Errorf("identity provider %q: %w", registry.identityFrom, ctx.Err())
	}
	if current.err != nil {
		return extension.Principal{}, fmt.Errorf("identity provider %q: %w", registry.identityFrom, current.err)
	}
	if strings.TrimSpace(current.principal.ID) == "" {
		return extension.Principal{}, fmt.Errorf("identity provider %q named no one: its principal has no ID", registry.identityFrom)
	}
	// A copy, so a provider that reuses its slice cannot change a principal
	// Inkuna is holding.
	current.principal.Groups = slices.Clone(current.principal.Groups)
	return current.principal, nil
}

// osPrincipal is the principal without an identity provider: the OS user,
// with no groups. A build without one never refuses a decision for want of
// a name, as Inkuna never did; when the OS cannot name its user, the ID is
// empty, as on rows recorded before principals were.
func osPrincipal() extension.Principal {
	current, err := user.Current()
	if err != nil {
		return extension.Principal{}
	}
	return extension.Principal{ID: current.Username, Name: current.Name}
}
