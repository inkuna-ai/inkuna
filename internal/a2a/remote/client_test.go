package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/a2a/remote"
	"github.com/inkuna-ai/inkuna/internal/a2a/remote/remotetest"
)

// local is the policy tests use: the fake listens on loopback over http.
func local() remote.Options {
	return remote.WithPollFloor(remote.Options{AllowInsecure: true, PollInterval: time.Millisecond, RequestTimeout: 5 * time.Second}, time.Millisecond)
}

func pinned(t *testing.T, client *remote.Client, fake *remotetest.Server) remote.Pin {
	t.Helper()
	card, err := client.FetchCard(context.Background(), fake.CardURL())
	if err != nil {
		t.Fatal(err)
	}
	pin, err := card.Pin("2026-09-19T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return pin
}

func message(text string) a2a.MessageSendParams {
	return a2a.MessageSendParams{Message: a2a.Message{MessageID: "m1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.TextPart(text)}}}
}

func TestProductionPolicyRefusesPlainHTTPAndInternalAddresses(t *testing.T) {
	fake := remotetest.New(t)
	strict := remote.New(remote.Options{})
	if _, err := strict.FetchCard(context.Background(), fake.CardURL()); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http must be refused: %v", err)
	}
	for _, target := range []string{"https://127.0.0.1/card.json", "https://[::1]/card.json", "https://10.0.0.8/card.json", "https://192.168.1.4/card.json", "https://100.64.0.1/card.json", "https://169.254.169.254/latest/meta-data", "https://0.0.0.0/card.json", "https://localhost/card.json"} {
		if _, err := strict.FetchCard(context.Background(), target); err == nil || !strings.Contains(err.Error(), "public address") {
			t.Fatalf("%s must be refused as an internal address: %v", target, err)
		}
	}
	for _, target := range []string{"ftp://example.com/card.json", "https://user:pass@example.com/card.json", "/relative", "https://"} {
		if _, err := strict.FetchCard(context.Background(), target); err == nil {
			t.Fatalf("%s must be refused", target)
		}
	}
}

func TestCardsArePinnedByContentAndChecked(t *testing.T) {
	fake := remotetest.New(t)
	fake.Set(func(fake *remotetest.Server) { fake.Security, fake.Streaming = "bearer", true })
	client := remote.New(local())
	card, err := client.FetchCard(context.Background(), fake.CardURL())
	if err != nil {
		t.Fatal(err)
	}
	pin, err := card.Pin("now")
	if err != nil || pin.Auth != "bearer" || !pin.Streaming || pin.URL != fake.URL+"/a2a" || !strings.HasPrefix(pin.Digest, "sha256:") || card.Skills[0].ID != "threat-review" {
		t.Fatalf("pin = %#v, %v", pin, err)
	}
	if _, err := client.Verify(context.Background(), fake.CardURL(), pin); err != nil {
		t.Fatalf("an unchanged card must verify: %v", err)
	}
	// Key order and whitespace are not content.
	left, _ := remote.Digest([]byte(`{"a":1, "b":[1,2]}`))
	right, _ := remote.Digest([]byte("{\n\"b\":[1,2],\"a\":1}"))
	if left != right {
		t.Fatal("the digest must ignore key order and whitespace")
	}
	fake.Set(func(fake *remotetest.Server) {
		fake.CardExtra = map[string]any{"description": "Now also deploys to production"}
	})
	if changed, err := client.Verify(context.Background(), fake.CardURL(), pin); !errors.Is(err, remote.ErrCardChanged) || changed.Description != "Now also deploys to production" {
		t.Fatalf("a changed card must be reported with its new content: %#v, %v", changed.Description, err)
	}

	for name, change := range map[string]func(*remotetest.Server){
		"unsupported auth": func(fake *remotetest.Server) { fake.Security = "oauth2" },
		"smuggling header": func(fake *remotetest.Server) { fake.Security = "header:Host" },
		"other origin":     func(fake *remotetest.Server) { fake.CardExtra = map[string]any{"url": "http://elsewhere.example/a2a"} },
		"old protocol":     func(fake *remotetest.Server) { fake.CardExtra = map[string]any{"protocolVersion": "0.2.5"} },
		"grpc only":        func(fake *remotetest.Server) { fake.CardExtra = map[string]any{"preferredTransport": "GRPC"} },
		"nameless":         func(fake *remotetest.Server) { fake.CardExtra = map[string]any{"name": " "} },
		"endpoint with user": func(fake *remotetest.Server) {
			fake.CardExtra = map[string]any{"url": "http://user:pw@" + fake.Listener.Addr().String() + "/a2a"}
		},
	} {
		broken := remotetest.New(t)
		broken.Set(change)
		if _, err := client.FetchCard(context.Background(), broken.CardURL()); err == nil {
			t.Fatalf("%s: the card was accepted", name)
		}
	}
	header := remotetest.New(t)
	header.Set(func(fake *remotetest.Server) { fake.Security = "header:X-API-Key" })
	if pin := pinned(t, client, header); pin.Auth != "header:X-API-Key" {
		t.Fatalf("api key pin = %#v", pin)
	}
}

func TestSendFollowsATaskByPollingAndPresentsTheToken(t *testing.T) {
	for _, reply := range []string{"message", "completed", "working"} {
		fake := remotetest.New(t)
		fake.Set(func(fake *remotetest.Server) { fake.Reply, fake.PollsUntilDone, fake.Security = reply, 3, "bearer" })
		client := remote.New(local())
		session, err := client.Session(pinned(t, client, fake), "s3cret-token")
		if err != nil {
			t.Fatal(err)
		}
		updates := 0
		task, err := session.Run(context.Background(), message("Review the plan"), func(remote.Update) error { updates++; return nil })
		if err != nil {
			t.Fatalf("%s: %v", reply, err)
		}
		if task.Status.State != a2a.StateCompleted || updates == 0 {
			t.Fatalf("%s: task = %#v after %d updates", reply, task.Status, updates)
		}
		text := ""
		if task.Status.Message != nil {
			text = task.Status.Message.Text()
		}
		for _, artifact := range task.Artifacts {
			for _, part := range artifact.Parts {
				text += part.Text
			}
		}
		if !strings.Contains(text, "rate limiting") {
			t.Fatalf("%s: the advisor's answer is missing: %#v", reply, task)
		}
		calls := fake.Calls()
		if calls[0].Method != "message/send" || (reply == "working" && len(calls) != 4) {
			t.Fatalf("%s: calls = %#v", reply, calls)
		}
		for _, call := range calls {
			if call.Authorization != "Bearer s3cret-token" {
				t.Fatalf("%s: %s was sent without the token: %q", reply, call.Method, call.Authorization)
			}
		}
		var sent a2a.MessageSendParams
		if err := json.Unmarshal(calls[0].Params, &sent); err != nil || sent.Message.Text() != "Review the plan" {
			t.Fatalf("%s: sent params = %s, %v", reply, calls[0].Params, err)
		}
	}
}

func TestAPIKeysGoInTheHeaderTheCardNames(t *testing.T) {
	fake := remotetest.New(t)
	fake.Set(func(fake *remotetest.Server) { fake.Security = "header:X-API-Key" })
	client := remote.New(local())
	session, err := client.Session(pinned(t, client, fake), "key-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Run(context.Background(), message("hello"), nil); err != nil {
		t.Fatal(err)
	}
	if call := fake.Calls()[0]; call.Headers.Get("X-API-Key") != "key-123" || call.Authorization != "" {
		t.Fatalf("headers = %#v", call.Headers)
	}
	if _, err := client.Session(pinned(t, client, fake), "bad\r\nInjected: 1"); err == nil {
		t.Fatal("a token with a line break must be refused")
	}
}

func TestStreamingDeliversEventsAndRecoversFromADroppedStream(t *testing.T) {
	events := []map[string]any{
		remotetest.TaskEvent(),
		remotetest.StatusEvent(a2a.StateWorking, "Reading the plan", false),
		remotetest.ArtifactEvent("advice", "Looks safe. ", false),
		remotetest.ArtifactEvent("advice", "Consider rate limiting.", true),
		remotetest.StatusEvent(a2a.StateCompleted, "Done", true),
	}
	for name, script := range map[string]func(*remotetest.Server){
		"complete stream":         func(fake *remotetest.Server) {},
		"dropped, resubscribes":   func(fake *remotetest.Server) { fake.DropAfter, fake.Resubscribe = 3, true },
		"dropped, falls to polls": func(fake *remotetest.Server) { fake.DropAfter, fake.PollsUntilDone = 2, 2 },
	} {
		fake := remotetest.New(t)
		fake.Set(func(fake *remotetest.Server) { fake.Streaming, fake.Events = true, events; script(fake) })
		client := remote.New(local())
		session, err := client.Session(pinned(t, client, fake), "")
		if err != nil {
			t.Fatal(err)
		}
		states := make([]a2a.TaskState, 0)
		task, err := session.Run(context.Background(), message("Review the plan"), func(update remote.Update) error {
			if update.Status != nil {
				states = append(states, update.Status.State)
			}
			return nil
		})
		if err != nil || task.ID != remotetest.TaskID || task.Status.State != a2a.StateCompleted {
			t.Fatalf("%s: task = %#v, %v", name, task, err)
		}
		if len(states) == 0 || states[0] != a2a.StateWorking {
			t.Fatalf("%s: progress was not reported as it arrived: %v", name, states)
		}
		methods := make([]string, 0)
		for _, call := range fake.Calls() {
			methods = append(methods, call.Method)
		}
		switch name {
		case "complete stream":
			if strings.Join(methods, ",") != "message/stream" || task.Artifacts[0].Parts[0].Text+task.Artifacts[0].Parts[1].Text != "Looks safe. Consider rate limiting." {
				t.Fatalf("%s: methods %v, artifacts %#v", name, methods, task.Artifacts)
			}
		case "dropped, resubscribes":
			if strings.Join(methods, ",") != "message/stream,tasks/resubscribe" {
				t.Fatalf("%s: methods %v", name, methods)
			}
		default:
			if strings.Join(methods, ",") != "message/stream,tasks/resubscribe,tasks/get,tasks/get" {
				t.Fatalf("%s: methods %v", name, methods)
			}
		}
	}
}

func TestLimitsRedirectsAndCancellation(t *testing.T) {
	// A response past the cap fails instead of being read.
	fake := remotetest.New(t)
	fake.Set(func(fake *remotetest.Server) { fake.Answer = strings.Repeat("x", 4096) })
	small := local()
	small.MaxResponseBytes = 1024
	capped, err := remote.New(small).Session(pinned(t, remote.New(local()), fake), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capped.Run(context.Background(), message("hi"), nil); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("an oversized response must fail: %v", err)
	}

	// A stream is counted across its events.
	streaming := remotetest.New(t)
	streaming.Set(func(fake *remotetest.Server) {
		fake.Streaming = true
		fake.Events = []map[string]any{remotetest.TaskEvent(), remotetest.ArtifactEvent("a", strings.Repeat("y", 3000), false), remotetest.ArtifactEvent("a", strings.Repeat("y", 3000), true), remotetest.StatusEvent(a2a.StateCompleted, "", true)}
	})
	budget := local()
	budget.MaxTaskBytes = 4096
	budgeted, err := remote.New(budget).Session(pinned(t, remote.New(local()), streaming), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budgeted.Run(context.Background(), message("hi"), nil); err == nil || !strings.Contains(err.Error(), "for one task") {
		t.Fatalf("a stream past the task budget must fail: %v", err)
	}
	for _, call := range streaming.Calls() {
		if call.Method == "tasks/get" {
			t.Fatal("a spent budget must not fall back to polling")
		}
	}

	// A redirect is never followed with the token.
	elsewhere := remotetest.New(t)
	redirecting := remotetest.New(t)
	redirecting.Set(func(fake *remotetest.Server) {
		fake.Handle = func(_ remotetest.Call, writer http.ResponseWriter) bool {
			writer.Header().Set("Location", elsewhere.URL+"/a2a")
			writer.WriteHeader(http.StatusTemporaryRedirect)
			return true
		}
	})
	redirected, err := remote.New(local()).Session(pinned(t, remote.New(local()), redirecting), "token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := redirected.Run(context.Background(), message("hi"), nil); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("a redirect must be refused: %v", err)
	}
	if len(elsewhere.Calls()) != 0 {
		t.Fatal("the request, and its token, followed the redirect")
	}

	// A task that never finishes ends with the context, and can be cancelled.
	slow := remotetest.New(t)
	slow.Set(func(fake *remotetest.Server) { fake.Reply, fake.PollsUntilDone = "working", 1<<30 })
	waiting, err := remote.New(local()).Session(pinned(t, remote.New(local()), slow), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	task, err := waiting.Run(ctx, message("hi"), nil)
	if !errors.Is(err, context.DeadlineExceeded) || task.ID != remotetest.TaskID {
		t.Fatalf("an endless task must end with the context: %#v, %v", task.ID, err)
	}
	if err := waiting.Cancel(context.Background(), task.ID); err != nil || len(slow.Canceled()) != 1 {
		t.Fatalf("cancel = %v, %v", err, slow.Canceled())
	}

	// JSON-RPC errors are reported, not treated as answers.
	failing := remotetest.New(t)
	failing.Set(func(fake *remotetest.Server) {
		fake.Handle = func(_ remotetest.Call, writer http.ResponseWriter) bool {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"quota exceeded"}}`))
			return true
		}
	})
	refused, err := remote.New(local()).Session(pinned(t, remote.New(local()), failing), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refused.Run(context.Background(), message("hi"), nil); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("a JSON-RPC error must be reported: %v", err)
	}
}

// A run cancelled while the agent is answering message/send has still started
// a task there; the reply that names it must arrive, or nothing can cancel it.
func TestACancelledSendStillNamesTheTaskItStarted(t *testing.T) {
	fake := remotetest.New(t)
	received, cancelled := make(chan struct{}), make(chan struct{})
	fake.Set(func(server *remotetest.Server) {
		server.Handle = func(call remotetest.Call, writer http.ResponseWriter) bool {
			if call.Method != "message/send" {
				return false
			}
			close(received)
			<-cancelled
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
				"kind": "task", "id": remotetest.TaskID, "contextId": "ctx", "status": map[string]any{"state": "working"},
			}})
			return true
		}
	})
	session, err := remote.New(local()).Session(pinned(t, remote.New(local()), fake), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-received
		cancel()
		// Reply well after the cancel, but well inside sendGrace: a reply
		// sent at once could beat the transport's abort and hide a request
		// that does not outlive its caller.
		time.Sleep(200 * time.Millisecond)
		close(cancelled)
	}()
	task, err := session.Run(ctx, message("hi"), nil)
	if !errors.Is(err, context.Canceled) || task.ID != remotetest.TaskID {
		t.Fatalf("the cancelled send must still name its task: %q, %v", task.ID, err)
	}

	// A caller that gave up before sending sends nothing, so no task starts.
	idle := remotetest.New(t)
	quiet, err := remote.New(local()).Session(pinned(t, remote.New(local()), idle), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := quiet.Run(ctx, message("hi"), nil); !errors.Is(err, context.Canceled) || len(idle.Calls()) != 0 {
		t.Fatalf("a cancelled caller must send nothing: %v, %d calls", err, len(idle.Calls()))
	}
}

// A proxy resolves the target's name itself, so an address Inkuna looked up
// and accepted says nothing about where the proxy connects: the same name can
// be public here and internal there. Remote-agent traffic therefore never
// goes through one, whatever the environment asks for.
func TestAConfiguredProxyIsNeverHandedTheTarget(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		proxied.Add(1)
		http.Error(writer, "the proxy would resolve and connect on its own", http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY"} {
		t.Setenv(name, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	for _, options := range []remote.Options{{}, local()} {
		if remote.ConsultsProxy(remote.New(options)) {
			t.Fatalf("the transport for %+v consults a proxy", options)
		}
	}
	// net/http never proxies loopback, so only a target elsewhere shows whether
	// the proxy is asked. 203.0.113.0/24 is reserved for documentation and
	// nothing answers on it; the production policy refuses it before dialing,
	// so this runs under the development policy, which skips the address check
	// and must stay just as direct.
	development := local()
	development.RequestTimeout = 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := remote.New(development).FetchCard(ctx, "http://203.0.113.10/.well-known/agent-card.json")
	if err == nil {
		t.Fatal("nothing answers on a documentation address")
	}
	if count := proxied.Load(); count != 0 {
		t.Fatalf("the proxy was handed the request %d time(s): %v", count, err)
	}
	if !strings.Contains(err.Error(), "connects to remote agents directly") {
		t.Fatalf("the failure should say why the proxy was not used: %v", err)
	}
}

func TestOnlyPubliclyRoutableAddressesAreReachable(t *testing.T) {
	refused := map[string][]string{
		"this network":              {"0.0.0.0", "0.1.2.3", "0.255.255.255"},
		"private":                   {"10.0.0.8", "172.16.0.1", "172.31.255.254", "192.168.1.4"},
		"shared address space":      {"100.64.0.1", "100.127.255.254"},
		"loopback":                  {"127.0.0.1", "127.255.255.254", "::1"},
		"link-local and metadata":   {"169.254.169.254", "fe80::1", "fe80::1%en0"},
		"IETF protocol assignments": {"192.0.0.1", "192.0.0.255", "2001::1", "2001:1ff::1"},
		"documentation":             {"192.0.2.1", "198.51.100.1", "203.0.113.1", "2001:db8::1", "3fff::1"},
		"6to4 and its relays":       {"192.88.99.1", "2002:a00:1::1", "2002:808:808::1"},
		"retired 6bone":             {"3ffe::1", "3ffe:831f:a00:1::1", "3ffe:ffff::1"},
		"benchmarking":              {"198.18.0.1", "198.19.255.254"},
		"multicast":                 {"224.0.0.1", "239.255.255.250", "ff02::1"},
		"reserved and broadcast":    {"240.0.0.1", "254.1.2.3", "255.255.255.255"},
		"unspecified":               {"::"},
		"IPv4-compatible":           {"::10.0.0.1", "::8.8.8.8"},
		"IPv4-mapped":               {"::ffff:10.0.0.1", "::ffff:100.64.0.1", "::ffff:240.0.0.1", "::ffff:198.18.0.1"},
		"NAT64 of an internal host": {"64:ff9b::a00:1", "64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "64:ff9b::c612:1", "64:ff9b:1::1"},
		"unique and site local":     {"fc00::1", "fd12:3456::1", "fec0::1"},
		"discard and SRv6":          {"100::1", "5f00::1"},
		"outside global unicast":    {"100:1::1", "200::1", "1000::1", "1fff:ffff::1", "4000::1", "8000::1", "e000::1", "fb00::1"},
	}
	for kind, addresses := range refused {
		for _, address := range addresses {
			for _, form := range []string{address, net.JoinHostPort(address, "443")} {
				if err := remote.CheckAddress(form, false); err == nil || !strings.Contains(err.Error(), "public address") {
					t.Fatalf("%s (%s) must be refused: %v", form, kind, err)
				}
			}
		}
	}
	// The neighbours of every refused range, and ordinary hosts, stay reachable.
	for _, address := range []string{
		"1.1.1.1", "8.8.8.8", "9.255.255.255", "11.0.0.1", "100.63.255.255", "100.128.0.1", "126.255.255.255", "128.0.0.1",
		"169.253.255.255", "169.255.0.1", "172.15.255.255", "172.32.0.1", "191.255.255.255", "192.0.1.1", "192.0.3.1", "192.88.98.1", "192.88.100.1",
		"192.167.255.255", "192.169.0.1", "198.17.255.255", "198.20.0.1", "198.51.99.1", "198.51.101.1", "203.0.112.1", "203.0.114.1", "223.255.255.254",
		"2606:4700:4700::1111", "2001:4860:4860::8888", "2001:200::1", "2003::1", "2a00:1450::1", "2000::1", "3ffd:ffff::1", "3fff:1000::1", "::ffff:8.8.8.8",
		// On an IPv6-only network, DNS64 answers with these for IPv4-only hosts.
		"64:ff9b::808:808", "64:ff9b::101:101",
	} {
		if err := remote.CheckAddress(net.JoinHostPort(address, "443"), false); err != nil {
			t.Fatalf("%s is a public address: %v", address, err)
		}
	}
	if err := remote.CheckAddress("agent.example.com:443", false); err == nil {
		t.Fatal("the dialer is handed addresses, never names")
	}
	if err := remote.CheckAddress("10.0.0.8:443", true); err != nil {
		t.Fatalf("allowInsecure is the development escape hatch: %v", err)
	}
}
