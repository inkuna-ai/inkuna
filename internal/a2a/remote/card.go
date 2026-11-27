// Package remote is Inkuna's outbound A2A 0.3 client. It connects external
// agents, by Agent Card URL, as read-only advisors: it fetches and pins a
// card, sends one message per stage, and follows the resulting task by polling
// or Server-Sent Events. It never listens on the network, never follows a
// redirect with a credential, and bounds everything it reads. The trust model
// is in docs/remote-a2a.md.
//
// The package knows nothing about Inkuna's service, storage, or UI; callers
// decide what to send and what to do with what comes back.
package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/a2a"
)

// SecurityScheme is the part of an OpenAPI-style security scheme the client
// understands.
type SecurityScheme struct {
	Type   string `json:"type"`
	Scheme string `json:"scheme,omitempty"`
	In     string `json:"in,omitempty"`
	Name   string `json:"name,omitempty"`
}

// Card is a fetched Agent Card: the fields Inkuna's bus already models plus
// the transport and security declarations a network client needs.
type Card struct {
	a2a.AgentCard
	PreferredTransport string                    `json:"preferredTransport,omitempty"`
	SecuritySchemes    map[string]SecurityScheme `json:"securitySchemes,omitempty"`
	Security           []map[string][]string     `json:"security,omitempty"`
	// Digest identifies the card's content; see Digest.
	Digest string `json:"digest"`
}

// Pin is what Inkuna remembers about a card the user reviewed. A later fetch
// must produce the same digest, or the agent stays unused until it is
// reviewed again.
type Pin struct {
	Digest          string `yaml:"digest" json:"digest"`
	URL             string `yaml:"url" json:"url"`
	ProtocolVersion string `yaml:"protocolVersion" json:"protocolVersion"`
	Streaming       bool   `yaml:"streaming,omitempty" json:"streaming,omitempty"`
	// Auth is how the token is presented: "bearer", "header:<name>", or ""
	// when the card asks for no credential.
	Auth      string `yaml:"auth,omitempty" json:"auth,omitempty"`
	FetchedAt string `yaml:"fetchedAt,omitempty" json:"fetchedAt,omitempty"`
}

// Pin returns the record to store for this card.
func (card Card) Pin(fetchedAt string) (Pin, error) {
	auth, err := card.auth()
	if err != nil {
		return Pin{}, err
	}
	return Pin{Digest: card.Digest, URL: card.URL, ProtocolVersion: card.ProtocolVersion, Streaming: card.Capabilities.Streaming, Auth: auth, FetchedAt: fetchedAt}, nil
}

// Digest is the SHA-256 of the card's canonical JSON: decoded and encoded
// again, which sorts object keys and drops insignificant whitespace, so only a
// change in content changes it.
func Digest(raw []byte) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("agent card is not valid JSON: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// parseCard decodes and checks a card fetched from cardURL.
func parseCard(raw []byte, cardURL *url.URL, allowInsecure bool) (Card, error) {
	var card Card
	if err := json.Unmarshal(raw, &card); err != nil {
		return Card{}, fmt.Errorf("agent card is not valid JSON: %w", err)
	}
	digest, err := Digest(raw)
	if err != nil {
		return Card{}, err
	}
	card.Digest = digest
	if strings.TrimSpace(card.Name) == "" {
		return Card{}, errors.New("agent card has no name")
	}
	if !strings.HasPrefix(card.ProtocolVersion, "0.3") {
		return Card{}, fmt.Errorf("agent card speaks A2A %q; Inkuna supports 0.3", card.ProtocolVersion)
	}
	if transport := strings.ToUpper(strings.TrimSpace(card.PreferredTransport)); transport != "" && transport != "JSONRPC" {
		return Card{}, fmt.Errorf("agent card prefers the %s transport; Inkuna speaks JSON-RPC", card.PreferredTransport)
	}
	endpoint, err := checkURL(card.URL, allowInsecure)
	if err != nil {
		return Card{}, fmt.Errorf("agent card endpoint: %w", err)
	}
	// The token the user grants for this card must not be sendable anywhere
	// but where the card came from.
	if !SameOrigin(endpoint, cardURL) {
		return Card{}, fmt.Errorf("agent card endpoint %s is on a different origin than the card (%s)", endpoint.Host, cardURL.Host)
	}
	if _, err := card.auth(); err != nil {
		return Card{}, err
	}
	return card, nil
}

// auth picks how to present a token from what the card requires. Cards list
// alternatives; the first one Inkuna supports wins. A card with requirements
// Inkuna supports none of is refused rather than called without credentials.
func (card Card) auth() (string, error) {
	required := make([]string, 0)
	for _, requirement := range card.Security {
		for name := range requirement {
			required = append(required, name)
		}
	}
	if len(required) == 0 {
		// Nothing is required. A scheme that is merely described still tells
		// us how to present an optional token.
		for name := range card.SecuritySchemes {
			required = append(required, name)
		}
		sort.Strings(required)
		for _, name := range required {
			if auth := supported(card.SecuritySchemes[name]); auth != "" {
				return auth, nil
			}
		}
		return "", nil
	}
	sort.Strings(required)
	for _, name := range required {
		if auth := supported(card.SecuritySchemes[name]); auth != "" {
			return auth, nil
		}
	}
	return "", fmt.Errorf("agent card requires authentication Inkuna does not support (%s); bearer tokens and API keys in a header are supported", strings.Join(required, ", "))
}

func supported(scheme SecurityScheme) string {
	switch {
	case strings.EqualFold(scheme.Type, "http") && strings.EqualFold(scheme.Scheme, "bearer"):
		return "bearer"
	case strings.EqualFold(scheme.Type, "apiKey") && strings.EqualFold(scheme.In, "header") && validHeaderName(scheme.Name):
		return "header:" + scheme.Name
	default:
		return ""
	}
}

// validHeaderName keeps a card from naming a header that would smuggle the
// token somewhere it does not belong.
func validHeaderName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, character := range name {
		switch {
		case character == '-', character == '_', character >= '0' && character <= '9', character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z':
		default:
			return false
		}
	}
	switch strings.ToLower(name) {
	case "host", "content-length", "content-type", "cookie", "transfer-encoding", "connection", "accept":
		return false
	}
	return true
}

// checkURL parses an absolute URL and applies the scheme policy.
func checkURL(raw string, allowInsecure bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%q is not a URL", raw)
	}
	if parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("%q must be an absolute URL without credentials", raw)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowInsecure {
			return nil, fmt.Errorf("%q must use https", raw)
		}
	default:
		return nil, fmt.Errorf("%q must use https", raw)
	}
	return parsed, nil
}

// SameOrigin reports whether two URLs share scheme, host, and port, with a
// default port equal to the same port written out.
func SameOrigin(left, right *url.URL) bool { return origin(left) == origin(right) }
