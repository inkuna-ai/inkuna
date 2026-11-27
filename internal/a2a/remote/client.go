package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
)

// Defaults bound what a remote agent can make Inkuna read and how often it is
// asked. They apply to polling and streaming alike.
const (
	DefaultMaxResponseBytes = 1 << 20
	DefaultMaxTaskBytes     = 4 << 20
	DefaultRequestTimeout   = 30 * time.Second
	minPollInterval         = 250 * time.Millisecond
	maxPollInterval         = 5 * time.Second
	maxCardRedirects        = 3
	// sendGrace is how long message/send keeps waiting for its reply after
	// the caller gives up, to learn which task to cancel.
	sendGrace = 5 * time.Second
)

// ErrCardChanged reports a card whose content no longer matches its pin.
var ErrCardChanged = errors.New("the agent card changed since it was reviewed")

// errBudget marks a task that read more than it may. No fallback continues
// past it.
var errBudget = errors.New("remote task byte budget exceeded")

// Options configure a Client. The zero value is the production policy.
type Options struct {
	// AllowInsecure permits plain http and private or loopback addresses. It
	// exists for local development and is set by hand in inkuna.yaml.
	AllowInsecure    bool
	MaxResponseBytes int64
	MaxTaskBytes     int64
	RequestTimeout   time.Duration
	// PollInterval is the first delay between tasks/get calls; it backs off to
	// maxPollInterval. Values under the floor are raised to it, except in
	// tests, which set pollFloor.
	PollInterval time.Duration
	pollFloor    time.Duration
}

// Client talks to one remote agent's origin under one policy.
type Client struct {
	options Options
	// direct checks the address it actually connects to. There is no proxied
	// client: a proxy resolves the name and connects by itself, so the address
	// policy would not bind it, whatever a lookup made here had returned.
	direct *http.Client
}

type redirectPolicy struct{}

// New builds a client for one policy.
func New(options Options) *Client {
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if options.MaxTaskBytes <= 0 {
		options.MaxTaskBytes = DefaultMaxTaskBytes
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = DefaultRequestTimeout
	}
	if options.pollFloor <= 0 {
		options.pollFloor = minPollInterval
	}
	if options.PollInterval < options.pollFloor {
		options.PollInterval = options.pollFloor
	}
	client := &Client{options: options}
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		return checkAddress(address, options.AllowInsecure)
	}}
	redirects := func(request *http.Request, via []*http.Request) error {
		// Only the card fetch, which carries no credential, may follow a
		// redirect, and only within the origin the user typed.
		if request.Context().Value(redirectPolicy{}) == nil || len(via) > maxCardRedirects || !SameOrigin(request.URL, via[0].URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	client.direct = &http.Client{CheckRedirect: redirects, Transport: &http.Transport{
		Proxy: nil, DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: options.RequestTimeout, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second,
	}}
	return client
}

// nonPublic lists the special-purpose ranges that are not publicly routable
// hosts, from the IANA IPv4 and IPv6 special-purpose address registries.
// net/netip's own predicates cover only some of them. Two entries are about
// what an address carries rather than where it is: 6to4 and Teredo addresses
// embed an IPv4 address, which would let an internal one through in disguise.
var nonPublic = func() []netip.Prefix {
	ranges := []string{
		"0.0.0.0/8",       // "this network"
		"10.0.0.0/8",      // private
		"100.64.0.0/10",   // shared address space (carrier-grade NAT), RFC 6598
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local, including cloud metadata services
		"172.16.0.0/12",   // private
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"192.88.99.0/24",  // deprecated 6to4 relay anycast
		"192.168.0.0/16",  // private
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, including limited broadcast
		"::/96",           // unspecified, loopback, and IPv4-compatible
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard-only
		"2001::/23",       // IETF protocol assignments, including Teredo
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4
		"3ffe::/16",       // retired 6bone, reserved; old Teredo stacks used part of it
		"3fff::/20",       // documentation
		"5f00::/16",       // segment routing identifiers
		"fc00::/7",        // unique local
		"fe80::/10",       // link-local
		"fec0::/10",       // deprecated site-local
		"ff00::/8",        // multicast
	}
	prefixes := make([]netip.Prefix, 0, len(ranges))
	for _, value := range ranges {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}()

// globalIPv6 is the only IPv6 space delegated for global unicast. An IPv6
// address outside it is not a public host whether or not a registry entry
// names it, so the entries of nonPublic beyond this prefix say why an address
// is refused rather than decide it.
var globalIPv6 = netip.MustParsePrefix("2000::/3")

// nat64 is the well-known NAT64 prefix. On an IPv6-only network every
// IPv4-only host resolves into it, so it is judged by the IPv4 address in its
// last four bytes rather than refused.
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// checkAddress lets a connection go only to a publicly routable address:
// everything a card URL has no business reaching (loopback, private, shared,
// link-local and the cloud metadata services on it, multicast, reserved,
// documentation, and unspecified ranges) is refused.
func checkAddress(address string, allowInsecure bool) error {
	if allowInsecure {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return fmt.Errorf("refusing to connect to %q: not an IP address", host)
	}
	if !publicAddress(ip) {
		return fmt.Errorf("refusing to connect to %s: remote agents must be on a public address", ip.Unmap())
	}
	return nil
}

func publicAddress(ip netip.Addr) bool {
	// A prefix never contains a zoned address, and only link-local ones have
	// a zone; without it the address is judged like any other.
	ip = ip.WithZone("").Unmap()
	if nat64.Contains(ip) {
		bytes := ip.As16()
		return publicAddress(netip.AddrFrom4([4]byte(bytes[12:])))
	}
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublic {
		if prefix.Contains(ip) {
			return false
		}
	}
	return ip.Is4() || globalIPv6.Contains(ip)
}

// do sends one request under the policy, always directly: HTTPS_PROXY and its
// relatives are ignored for remote agents. The lookup here only makes the
// refusal of an internal host early and readable; what enforces the policy is
// the dialer's check of the address actually connected to.
func (client *Client) do(request *http.Request) (*http.Response, error) {
	if _, err := checkURL(request.URL.String(), client.options.AllowInsecure); err != nil {
		return nil, err
	}
	if !client.options.AllowInsecure {
		addresses, err := net.DefaultResolver.LookupNetIP(request.Context(), "ip", request.URL.Hostname())
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", request.URL.Hostname(), err)
		}
		for _, address := range addresses {
			if err := checkAddress(address.String(), false); err != nil {
				return nil, err
			}
		}
	}
	response, err := client.direct.Do(request)
	// A blocked direct connection usually ends as a timeout, so only a
	// cancellation by the caller goes without the explanation.
	if err != nil && !errors.Is(request.Context().Err(), context.Canceled) && proxyConfigured() {
		return nil, fmt.Errorf("%w (a proxy is configured in the environment, but Inkuna connects to remote agents directly: a proxy resolves names itself, which would bypass the address policy)", err)
	}
	return response, err
}

// proxyConfigured reports whether the environment asks for a proxy. It reads
// the variables itself because net/http caches its own reading of them.
func proxyConfigured() bool {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

// FetchCard downloads and validates the card at cardURL. No credential is
// sent: a card is a public discovery document.
func (client *Client) FetchCard(ctx context.Context, cardURL string) (Card, error) {
	parsed, err := checkURL(cardURL, client.options.AllowInsecure)
	if err != nil {
		return Card{}, err
	}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, redirectPolicy{}, true), client.options.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Card{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.do(request)
	if err != nil {
		return Card{}, fmt.Errorf("fetch agent card: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Card{}, fmt.Errorf("fetch agent card: %s answered %s", parsed.Host, response.Status)
	}
	raw, err := readCapped(response.Body, client.options.MaxResponseBytes)
	if err != nil {
		return Card{}, fmt.Errorf("fetch agent card: %w", err)
	}
	// A followed redirect stays within the origin, so the final URL is the one
	// the endpoint is compared with.
	return parseCard(raw, response.Request.URL, client.options.AllowInsecure)
}

// Verify fetches the card again and compares it with the pin.
func (client *Client) Verify(ctx context.Context, cardURL string, pin Pin) (Card, error) {
	card, err := client.FetchCard(ctx, cardURL)
	if err != nil {
		return Card{}, err
	}
	if card.Digest != pin.Digest {
		return card, ErrCardChanged
	}
	return card, nil
}

func readCapped(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	return data, nil
}

// Session is one task's conversation with a pinned agent.
type Session struct {
	client *Client
	pin    Pin
	token  string
	read   atomic.Int64
	nextID atomic.Int64
}

// Session starts a conversation. The token is kept in memory for the
// session's lifetime only and sent nowhere but the pinned endpoint.
func (client *Client) Session(pin Pin, token string) (*Session, error) {
	if _, err := checkURL(pin.URL, client.options.AllowInsecure); err != nil {
		return nil, fmt.Errorf("pinned endpoint: %w", err)
	}
	if strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("the agent's token contains a line break")
	}
	return &Session{client: client, pin: pin, token: token}, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (session *Session) request(ctx context.Context, method string, params any, accept string) (*http.Response, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: session.nextID.Add(1), Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, session.pin.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", accept)
	if session.token != "" {
		switch {
		case strings.HasPrefix(session.pin.Auth, "header:"):
			request.Header.Set(strings.TrimPrefix(session.pin.Auth, "header:"), session.token)
		default:
			request.Header.Set("Authorization", "Bearer "+session.token)
		}
	}
	response, err := session.client.do(request)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return nil, fmt.Errorf("%s: the agent answered with a redirect (%s), which is not followed", method, response.Status)
		}
		return nil, fmt.Errorf("%s: the agent answered %s", method, response.Status)
	}
	return response, nil
}

// spend counts bytes against the task's budget.
func (session *Session) spend(size int) error {
	if session.read.Add(int64(size)) > session.client.options.MaxTaskBytes {
		return fmt.Errorf("the agent sent more than %d bytes for one task: %w", session.client.options.MaxTaskBytes, errBudget)
	}
	return nil
}

// send posts message/send, and lets the request outlive a cancelled ctx by
// sendGrace. An agent that accepted the message has already started a task,
// and only this reply names it: without the name the caller cannot cancel
// the task, and the agent keeps working for a run that has ended.
func (session *Session) send(ctx context.Context, params a2a.MessageSendParams) (json.RawMessage, error) {
	// A caller that has already given up sends nothing, so no task starts.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("message/send: %w", err)
	}
	detached, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stop := context.AfterFunc(ctx, func() { time.AfterFunc(sendGrace, cancel) })
	defer stop()
	return session.call(detached, "message/send", params)
}

func (session *Session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, session.client.options.RequestTimeout)
	defer cancel()
	response, err := session.request(ctx, method, params, "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := readCapped(response.Body, session.client.options.MaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if err := session.spend(len(raw)); err != nil {
		return nil, err
	}
	return decodeResult(method, raw)
}

func decodeResult(method string, raw []byte) (json.RawMessage, error) {
	var envelope rpcResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%s: the agent's answer is not JSON-RPC: %w", method, err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("%s: the agent reported error %d: %s", method, envelope.Error.Code, truncate(envelope.Error.Message, 300))
	}
	if len(envelope.Result) == 0 {
		return nil, fmt.Errorf("%s: the agent's answer has no result", method)
	}
	return envelope.Result, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// Update is one thing a remote agent said about a task: the whole task, a
// direct message, a status change, or an artifact (chunk).
type Update struct {
	Task     *a2a.Task
	Message  *a2a.Message
	Status   *a2a.TaskStatus
	Artifact *a2a.Artifact
	Append   bool
	Final    bool
}

// decodeUpdate reads a message/send result or a stream event by its kind.
func decodeUpdate(raw json.RawMessage) (Update, error) {
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return Update{}, fmt.Errorf("the agent's answer is not an object: %w", err)
	}
	switch header.Kind {
	case "task":
		var task a2a.Task
		if err := json.Unmarshal(raw, &task); err != nil {
			return Update{}, err
		}
		return Update{Task: &task, Final: task.Status.State.Terminal() || task.Status.State.Interrupted()}, nil
	case "message":
		var message a2a.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			return Update{}, err
		}
		return Update{Message: &message, Final: true}, nil
	case string(a2a.EventStatusUpdate):
		var event a2a.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return Update{}, err
		}
		if event.Status == nil {
			return Update{}, errors.New("the agent sent a status update without a status")
		}
		return Update{Status: event.Status, Final: event.Final || event.Status.State.Terminal() || event.Status.State.Interrupted()}, nil
	case string(a2a.EventArtifactUpdate):
		var event a2a.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return Update{}, err
		}
		if event.Artifact == nil {
			return Update{}, errors.New("the agent sent an artifact update without an artifact")
		}
		return Update{Artifact: event.Artifact, Append: event.Append}, nil
	default:
		return Update{}, fmt.Errorf("the agent sent an unknown object kind %q", header.Kind)
	}
}

// stream runs one SSE request and reports each event. It returns whether a
// final event arrived. A stream that ends or breaks after it started has not
// finished the task and is not an error: the caller carries on another way.
// Errors are the things no fallback should paper over: a refused request, a
// malformed event, a spent budget, or the caller's own failure.
func (session *Session) stream(ctx context.Context, method string, params any, onUpdate func(Update) error) (bool, error) {
	response, err := session.request(ctx, method, params, "text/event-stream")
	if err != nil {
		return false, err
	}
	defer func() { _ = response.Body.Close() }()
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		return false, fmt.Errorf("%s: the agent answered %q instead of an event stream", method, contentType)
	}
	// The reader itself stops at what is left of the task's budget, so one
	// endless line cannot be buffered before it is counted.
	remaining := session.client.options.MaxTaskBytes - session.read.Load()
	reader := bufio.NewReaderSize(io.LimitReader(response.Body, remaining+1), 64<<10)
	var data bytes.Buffer
	for {
		line, err := reader.ReadBytes('\n')
		if spendErr := session.spend(len(line)); spendErr != nil {
			return false, spendErr
		}
		trimmed := bytes.TrimRight(line, "\r\n")
		switch {
		case len(trimmed) == 0 && data.Len() > 0:
			result, decodeErr := decodeResult(method, data.Bytes())
			data.Reset()
			if decodeErr != nil {
				return false, decodeErr
			}
			update, decodeErr := decodeUpdate(result)
			if decodeErr != nil {
				return false, decodeErr
			}
			if err := onUpdate(update); err != nil {
				return false, err
			}
			if update.Final {
				return true, nil
			}
		case bytes.HasPrefix(trimmed, []byte("data:")):
			if int64(data.Len()+len(trimmed)) > session.client.options.MaxResponseBytes {
				return false, fmt.Errorf("%s: one event is larger than %d bytes", method, session.client.options.MaxResponseBytes)
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.Write(bytes.TrimPrefix(bytes.TrimPrefix(trimmed, []byte("data:")), []byte(" ")))
		}
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		}
	}
}

// Run sends the message and follows the task to a terminal or interrupted
// state, within ctx. It streams when the pinned card says the agent can;
// a stream that drops resubscribes once and then falls back to polling, which
// is also what an agent that cannot stream gets. Every change is reported to
// onUpdate as it arrives, and the accumulated task is returned.
func (session *Session) Run(ctx context.Context, params a2a.MessageSendParams, onUpdate func(Update) error) (a2a.Task, error) {
	var task a2a.Task
	apply := func(update Update) error {
		switch {
		case update.Task != nil:
			task = *update.Task
		case update.Message != nil:
			// A direct reply is a finished exchange without a task object.
			task.Status = a2a.TaskStatus{State: a2a.StateCompleted, Message: update.Message, Timestamp: time.Now().UTC()}
		case update.Status != nil:
			task.Status = *update.Status
		case update.Artifact != nil:
			task.Artifacts = mergeArtifact(task.Artifacts, *update.Artifact, update.Append)
		}
		if onUpdate == nil {
			return nil
		}
		return onUpdate(update)
	}

	finished := false
	if session.pin.Streaming {
		var err error
		if finished, err = session.stream(ctx, "message/stream", params, apply); err != nil {
			return task, err
		}
		if !finished && task.ID != "" {
			// Not every agent that streams can resubscribe; polling covers it.
			if finished, err = session.stream(ctx, "tasks/resubscribe", map[string]any{"id": task.ID}, apply); errors.Is(err, errBudget) || ctx.Err() != nil {
				return task, err
			}
		}
	} else {
		result, err := session.send(ctx, params)
		if err != nil {
			return task, err
		}
		update, err := decodeUpdate(result)
		if err != nil {
			return task, fmt.Errorf("message/send: %w", err)
		}
		if ctx.Err() != nil {
			// Cancelled while the reply was on its way: return the task it
			// names, so the caller can cancel it, and go no further.
			if update.Task != nil {
				task = *update.Task
			}
			return task, ctx.Err()
		}
		if err := apply(update); err != nil {
			return task, err
		}
		finished = update.Final
	}
	if finished {
		return task, nil
	}
	if task.ID == "" {
		return task, errors.New("the agent ended the exchange without a task or a reply")
	}
	return session.poll(ctx, task, apply)
}

func (session *Session) poll(ctx context.Context, task a2a.Task, apply func(Update) error) (a2a.Task, error) {
	delay := session.client.options.PollInterval
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return task, ctx.Err()
		case <-timer.C:
		}
		result, err := session.call(ctx, "tasks/get", map[string]any{"id": task.ID})
		if err != nil {
			return task, err
		}
		var current a2a.Task
		if err := json.Unmarshal(result, &current); err != nil {
			return task, fmt.Errorf("tasks/get: %w", err)
		}
		if current.ID != task.ID {
			return task, fmt.Errorf("tasks/get: asked for task %s and got %s", task.ID, current.ID)
		}
		if err := apply(Update{Task: &current}); err != nil {
			return current, err
		}
		task = current
		if task.Status.State.Terminal() || task.Status.State.Interrupted() {
			return task, nil
		}
		if delay *= 2; delay > maxPollInterval {
			delay = maxPollInterval
		}
		timer.Reset(delay)
	}
}

// Cancel asks the agent to stop a task. It is best effort: the caller has
// already stopped waiting.
func (session *Session) Cancel(ctx context.Context, taskID string) error {
	if taskID == "" {
		return nil
	}
	_, err := session.call(ctx, "tasks/cancel", map[string]any{"id": taskID})
	return err
}

func mergeArtifact(artifacts []a2a.Artifact, artifact a2a.Artifact, appendParts bool) []a2a.Artifact {
	for index := range artifacts {
		if artifacts[index].ArtifactID != artifact.ArtifactID {
			continue
		}
		if appendParts {
			artifacts[index].Parts = append(artifacts[index].Parts, artifact.Parts...)
		} else {
			artifacts[index] = artifact
		}
		return artifacts
	}
	return append(artifacts, artifact)
}

// origin normalizes a URL to scheme, lower-case host, and explicit port.
func origin(value *url.URL) string {
	port := value.Port()
	if port == "" {
		if value.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return value.Scheme + "://" + strings.ToLower(value.Hostname()) + ":" + port
}
