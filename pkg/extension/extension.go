// Package extension defines what another module plugs into Inkuna when it
// embeds it through pkg/inkuna. It is public API, so it names no type from
// internal/, and Inkuna converts between these types and its own at each call
// (docs/extensions.md).
//
// Inkuna implements Registrar, so it can gain a method whenever an extension
// point is built. An extension implements Extension and each point's own
// interface, which stay small and never gain a method.
package extension

import "context"

// Extension is one unit an embedding binary plugs into Inkuna. Inkuna calls
// Register once, in the order the binary lists its extensions, before the app
// starts.
type Extension interface {
	// Name identifies the extension in errors and namespaces what it
	// registers. It is lower case: a letter, then letters, digits, or
	// hyphens. It is unique in the build, and "inkuna" is Inkuna's own.
	Name() string
	// Register declares what the extension provides. An error stops the app
	// from starting, naming the extension. The registrar is valid only until
	// Register returns.
	Register(Registrar) error
}

// Registrar is how an extension declares what it provides, during Register
// and only then. Inkuna implements it and checks each declaration once
// Register returns: one it refuses stops the app from starting. A call after
// Register returns also stops startup when it happens while extensions are
// still being registered, and panics once the app has started, since nothing
// it declares could take effect by then.
type Registrar interface {
	// Capability reports a feature this build has to the view, which shows
	// a screen an extension adds only when its capability is reported.
	Capability(Capability)
	// Identity names who Inkuna acts for in place of the OS user. A build
	// has at most one identity provider: a second one, from any extension,
	// stops the app from starting.
	Identity(IdentityProvider)
	// AuditSink receives the audit events Inkuna records while the build
	// has an audit sink, in the order it recorded them, starting with any
	// still queued in the data directory.
	AuditSink(AuditSink)
	// UsageSink receives every model call Inkuna records, starting with
	// those already stored the first time a data directory has one.
	UsageSink(UsageSink)
	// Policy is asked at each decision point once Go has allowed the
	// decision, and can refuse it. A build may have any number, asked in
	// the order they were registered.
	Policy(Policy)
	// DefinitionSource shares agents, skills, shared stages, groups, and
	// rules from outside the machine. A build may have any number.
	DefinitionSource(DefinitionSource)
	// SecretStore keeps secret values in place of the OS keychain. A build
	// has at most one: a second one, from any extension, stops the app from
	// starting.
	SecretStore(SecretStore)
	// Notifier receives every notification beside the desktop one. A build
	// may have any number.
	Notifier(Notifier)
}

// Principal is the person Inkuna is acting for.
type Principal struct {
	// ID is stable for the person: Inkuna stores it on the tasks they create
	// and the runs they approve. A provider must set it. By default it is
	// the OS user name.
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	// Groups are the person's groups as the identity provider names them,
	// such as "platform-team". There are none by default.
	Groups []string `json:"groups,omitempty"`
}

// IdentityProvider says who is acting. Inkuna asks it whenever a person
// creates a task, approves a plan, resumes a run, approves a review or sends
// changes, or answers input. An error refuses that decision, and its message
// is shown as the reason, so it should say what the person can do, such as
// "Sign in to continue". Runs already started go on under the principal they
// recorded. The principal goes only to Inkuna's records, never into a
// model's prompt.
type IdentityProvider interface {
	Current(context.Context) (Principal, error)
}

// Capability is a feature this build has, reported to the view.
type Capability struct {
	// ID is namespaced by the extension that registers it: its name, a dot,
	// then lower-case letters and digits, which may be separated by single
	// dots or hyphens, such as "example.audit". It is unique in the build.
	ID string
	// Label is what the view shows for the capability. It is required.
	Label string
}

// Product is what the view names the app: an embedding binary's own name and
// version. The zero Product is Inkuna's own, with the version its build was
// stamped with.
type Product struct {
	// Name is required whenever Version is set.
	Name    string
	Version string
}
