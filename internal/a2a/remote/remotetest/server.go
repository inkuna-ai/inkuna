// Package remotetest is an in-process fake A2A 0.3 agent for tests. It serves
// an Agent Card and a JSON-RPC endpoint whose behaviour each test scripts.
package remotetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
)

// Call is one JSON-RPC request the fake received.
type Call struct {
	Method        string
	Params        json.RawMessage
	Authorization string
	Headers       http.Header
}

// Server is the fake agent. Fields may be changed between requests; they are
// read under the server's lock.
type Server struct {
	*httptest.Server
	mu    sync.Mutex
	calls []Call

	// Card fields.
	Name, Description string
	Streaming         bool
	// Security is "bearer", "header:<name>", "oauth2", or "" for none.
	Security string
	// CardExtra is merged into the card, which changes its digest.
	CardExtra map[string]any

	// Reply scripts message/send: "message", "completed", or "working", which
	// completes after PollsUntilDone tasks/get calls.
	Reply          string
	PollsUntilDone int
	// Events are sent on message/stream. DropAfter > 0 ends the stream after
	// that many events without a final one.
	Events      []map[string]any
	DropAfter   int
	Resubscribe bool
	// Answer is the text of the advisor's reply.
	Answer string
	// Handle, when set, may take over a request; returning true means it did.
	Handle func(call Call, writer http.ResponseWriter) bool

	polls    int
	canceled []string
}

// New starts a fake agent that stops with the test.
func New(t testing.TB) *Server {
	t.Helper()
	fake := &Server{Name: "Security Advisor", Description: "Reviews plans for security issues", Reply: "completed", Answer: "Looks safe. Consider rate limiting."}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", fake.serveCard)
	mux.HandleFunc("/a2a", fake.serveRPC)
	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	return fake
}

// CardURL is where the fake serves its card.
func (fake *Server) CardURL() string { return fake.URL + "/.well-known/agent-card.json" }

// Calls returns the JSON-RPC requests received so far.
func (fake *Server) Calls() []Call {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]Call(nil), fake.calls...)
}

// Canceled lists the task ids tasks/cancel was called for.
func (fake *Server) Canceled() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.canceled...)
}

// Set changes the fake's behaviour under its lock.
func (fake *Server) Set(change func(*Server)) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	change(fake)
}

func (fake *Server) serveCard(writer http.ResponseWriter, _ *http.Request) {
	fake.mu.Lock()
	card := map[string]any{
		"name": fake.Name, "description": fake.Description, "url": fake.URL + "/a2a", "version": "1.0.0", "protocolVersion": "0.3.0",
		"preferredTransport": "JSONRPC", "capabilities": map[string]any{"streaming": fake.Streaming},
		"defaultInputModes": []string{"text/plain", "application/json"}, "defaultOutputModes": []string{"text/plain"},
		"skills": []map[string]any{{"id": "threat-review", "name": "Threat review", "description": "Finds security risks in a plan", "tags": []string{"security"}}},
	}
	switch {
	case fake.Security == "bearer":
		card["securitySchemes"] = map[string]any{"token": map[string]any{"type": "http", "scheme": "bearer"}}
		card["security"] = []map[string][]string{{"token": {}}}
	case len(fake.Security) > 7 && fake.Security[:7] == "header:":
		card["securitySchemes"] = map[string]any{"key": map[string]any{"type": "apiKey", "in": "header", "name": fake.Security[7:]}}
		card["security"] = []map[string][]string{{"key": {}}}
	case fake.Security == "oauth2":
		card["securitySchemes"] = map[string]any{"login": map[string]any{"type": "oauth2"}}
		card["security"] = []map[string][]string{{"login": {}}}
	}
	for key, value := range fake.CardExtra {
		card[key] = value
	}
	fake.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(card)
}

func (fake *Server) serveRPC(writer http.ResponseWriter, request *http.Request) {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}
	call := Call{Method: envelope.Method, Params: envelope.Params, Authorization: request.Header.Get("Authorization"), Headers: request.Header.Clone()}
	fake.mu.Lock()
	fake.calls = append(fake.calls, call)
	handle := fake.Handle
	fake.mu.Unlock()
	if handle != nil && handle(call, writer) {
		return
	}
	switch envelope.Method {
	case "message/send":
		fake.mu.Lock()
		reply := fake.Reply
		fake.mu.Unlock()
		switch reply {
		case "message":
			writeResult(writer, envelope.ID, map[string]any{"kind": "message", "messageId": "reply-1", "role": "agent", "parts": []map[string]any{{"kind": "text", "text": fake.answer()}}})
		case "working":
			writeResult(writer, envelope.ID, fake.task(a2a.StateWorking))
		default:
			writeResult(writer, envelope.ID, fake.task(a2a.StateCompleted))
		}
	case "tasks/get":
		fake.mu.Lock()
		fake.polls++
		done := fake.polls >= fake.PollsUntilDone
		fake.mu.Unlock()
		state := a2a.StateWorking
		if done {
			state = a2a.StateCompleted
		}
		writeResult(writer, envelope.ID, fake.task(state))
	case "tasks/cancel":
		var params struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(envelope.Params, &params)
		fake.mu.Lock()
		fake.canceled = append(fake.canceled, params.ID)
		fake.mu.Unlock()
		writeResult(writer, envelope.ID, fake.task(a2a.StateCanceled))
	case "message/stream":
		fake.mu.Lock()
		events, dropAfter := append([]map[string]any(nil), fake.Events...), fake.DropAfter
		fake.mu.Unlock()
		fake.streamEvents(writer, envelope.ID, events, dropAfter)
	case "tasks/resubscribe":
		fake.mu.Lock()
		supported := fake.Resubscribe
		fake.mu.Unlock()
		if !supported {
			writeError(writer, envelope.ID, -32004, "resubscribe is not supported")
			return
		}
		fake.streamEvents(writer, envelope.ID, []map[string]any{StatusEvent(a2a.StateCompleted, fake.answer(), true)}, 0)
	default:
		writeError(writer, envelope.ID, -32601, "method not found")
	}
}

func (fake *Server) answer() string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.Answer
}

// TaskID is the id of the one task the fake ever creates.
const TaskID = "remote-task-1"

func (fake *Server) task(state a2a.TaskState) map[string]any {
	task := map[string]any{
		"kind": "task", "id": TaskID, "contextId": "remote-context-1",
		"status": map[string]any{"state": state, "timestamp": time.Now().UTC().Format(time.RFC3339)},
	}
	if state == a2a.StateCompleted {
		task["artifacts"] = []map[string]any{{"artifactId": "advice", "name": "advice.md", "parts": []map[string]any{{"kind": "text", "text": fake.answer()}}}}
	}
	return task
}

// TaskEvent is the first event of a stream: the task itself, still working.
func TaskEvent() map[string]any {
	return map[string]any{"kind": "task", "id": TaskID, "contextId": "remote-context-1", "status": map[string]any{"state": a2a.StateWorking, "timestamp": time.Now().UTC().Format(time.RFC3339)}}
}

// StatusEvent is a status-update stream event.
func StatusEvent(state a2a.TaskState, text string, final bool) map[string]any {
	status := map[string]any{"state": state, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	if text != "" {
		status["message"] = map[string]any{"kind": "message", "messageId": "status-" + string(state), "role": "agent", "parts": []map[string]any{{"kind": "text", "text": text}}}
	}
	return map[string]any{"kind": "status-update", "taskId": TaskID, "contextId": "remote-context-1", "status": status, "final": final}
}

// ArtifactEvent is an artifact-update stream event.
func ArtifactEvent(id, text string, appendParts bool) map[string]any {
	return map[string]any{"kind": "artifact-update", "taskId": TaskID, "contextId": "remote-context-1", "append": appendParts,
		"artifact": map[string]any{"artifactId": id, "name": id + ".md", "parts": []map[string]any{{"kind": "text", "text": text}}}}
}

func (fake *Server) streamEvents(writer http.ResponseWriter, id json.RawMessage, events []map[string]any, dropAfter int) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	flusher, _ := writer.(http.Flusher)
	for index, event := range events {
		if dropAfter > 0 && index >= dropAfter {
			return
		}
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": event})
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func writeResult(writer http.ResponseWriter, id json.RawMessage, result any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeError(writer http.ResponseWriter, id json.RawMessage, code int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}
