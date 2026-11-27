package remote

import (
	"net/http"
	"time"
)

// ConsultsProxy reports whether the client's transport would ask the
// environment, or anything else, for a proxy.
func ConsultsProxy(client *Client) bool {
	transport, ok := client.direct.Transport.(*http.Transport)
	return !ok || transport.Proxy != nil
}

// CheckAddress exposes the connect-time address policy.
var CheckAddress = checkAddress

// WithPollFloor lets tests poll faster than production allows.
func WithPollFloor(options Options, floor time.Duration) Options {
	options.pollFloor = floor
	return options
}
