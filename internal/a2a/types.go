// Package a2a implements the data model of the Agent2Agent (A2A) protocol as
// Inkuna's in-process message bus. Headless CLIs cannot speak A2A over the
// network, so Inkuna brokers: every specialized agent publishes an Agent Card,
// every unit of work is a Task with a lifecycle, and all inter-agent traffic
// is Messages made of Parts. Artifacts are the durable outputs of a Task.
//
// The shapes follow the A2A specification (version 0.3) so a future network
// transport (JSON-RPC over HTTP with Server-Sent Events) can expose the same
// objects without translation. See docs/a2a.md for the protocol walkthrough.
package a2a

import "time"

// ProtocolVersion is the A2A specification version these types follow.
const ProtocolVersion = "0.3.0"

// TaskState enumerates the A2A task lifecycle.
type TaskState string

const (
	StateSubmitted     TaskState = "submitted"
	StateWorking       TaskState = "working"
	StateInputRequired TaskState = "input-required"
	StateCompleted     TaskState = "completed"
	StateCanceled      TaskState = "canceled"
	StateFailed        TaskState = "failed"
	StateRejected      TaskState = "rejected"
	StateAuthRequired  TaskState = "auth-required"
	StateUnknown       TaskState = "unknown"
)

// Terminal reports whether no further transitions are possible.
func (state TaskState) Terminal() bool {
	switch state {
	case StateCompleted, StateCanceled, StateFailed, StateRejected:
		return true
	default:
		return false
	}
}

// Interrupted reports whether the task is paused waiting on the client.
func (state TaskState) Interrupted() bool {
	return state == StateInputRequired || state == StateAuthRequired
}

// AgentProvider identifies the organization behind an agent.
type AgentProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

// AgentCapabilities advertises optional protocol features.
type AgentCapabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

// AgentSkill describes one capability an agent offers. Inkuna maps its skill
// packages and task kinds onto this structure.
type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

// AgentCard is the discovery document for an agent. In network A2A it is
// served at /.well-known/agent-card.json; Inkuna keeps cards in the registry.
type AgentCard struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	URL                string            `json:"url"`
	Version            string            `json:"version"`
	ProtocolVersion    string            `json:"protocolVersion"`
	Provider           *AgentProvider    `json:"provider,omitempty"`
	Capabilities       AgentCapabilities `json:"capabilities"`
	DefaultInputModes  []string          `json:"defaultInputModes"`
	DefaultOutputModes []string          `json:"defaultOutputModes"`
	Skills             []AgentSkill      `json:"skills"`
	Metadata           map[string]any    `json:"metadata,omitempty"`
}

// Role identifies which side of the conversation authored a message.
type Role string

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

// PartKind discriminates the Part union.
type PartKind string

const (
	PartText PartKind = "text"
	PartFile PartKind = "file"
	PartData PartKind = "data"
)

// FileContent carries a file either inline (Bytes, base64) or by reference
// (URI). Inkuna always uses URIs of the form inkuna://artifact/<digest>.
type FileContent struct {
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
}

// Part is one unit of content inside a Message or Artifact.
type Part struct {
	Kind     PartKind       `json:"kind"`
	Text     string         `json:"text,omitempty"`
	File     *FileContent   `json:"file,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func TextPart(text string) Part { return Part{Kind: PartText, Text: text} }

func DataPart(data map[string]any) Part { return Part{Kind: PartData, Data: data} }

func FilePart(name, mimeType, uri string) Part {
	return Part{Kind: PartFile, File: &FileContent{Name: name, MimeType: mimeType, URI: uri}}
}

// Message is a single turn between a client and an agent.
type Message struct {
	MessageID        string         `json:"messageId"`
	Role             Role           `json:"role"`
	Parts            []Part         `json:"parts"`
	TaskID           string         `json:"taskId,omitempty"`
	ContextID        string         `json:"contextId,omitempty"`
	ReferenceTaskIDs []string       `json:"referenceTaskIds,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	Timestamp        time.Time      `json:"timestamp"`
}

// Text concatenates the message's text parts.
func (message Message) Text() string {
	text := ""
	for _, part := range message.Parts {
		if part.Kind == PartText {
			if text != "" {
				text += "\n"
			}
			text += part.Text
		}
	}
	return text
}

// Artifact is a durable output of a task, such as a diff, report, or file.
type Artifact struct {
	ArtifactID  string         `json:"artifactId"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parts       []Part         `json:"parts"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// TaskStatus is the current state plus an optional explanatory message.
type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Task is the unit of work exchanged between agents.
type Task struct {
	ID        string         `json:"id"`
	ContextID string         `json:"contextId"`
	Status    TaskStatus     `json:"status"`
	History   []Message      `json:"history,omitempty"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	// Agent is an Inkuna extension recording which card the task was sent to.
	Agent string `json:"agent"`
}

// SendConfiguration mirrors MessageSendConfiguration from the specification.
type SendConfiguration struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`
	HistoryLength       int      `json:"historyLength,omitempty"`
	Blocking            bool     `json:"blocking,omitempty"`
}

// MessageSendParams is the payload of message/send and message/stream.
type MessageSendParams struct {
	Message       Message            `json:"message"`
	Configuration *SendConfiguration `json:"configuration,omitempty"`
	Metadata      map[string]any     `json:"metadata,omitempty"`
}

// EventKind discriminates streaming events.
type EventKind string

const (
	EventStatusUpdate   EventKind = "status-update"
	EventArtifactUpdate EventKind = "artifact-update"
)

// Event is what message/stream and tasks/resubscribe deliver over SSE. Inkuna
// delivers the same events over Go channels.
type Event struct {
	Kind      EventKind   `json:"kind"`
	TaskID    string      `json:"taskId"`
	ContextID string      `json:"contextId"`
	Status    *TaskStatus `json:"status,omitempty"`
	Artifact  *Artifact   `json:"artifact,omitempty"`
	Append    bool        `json:"append,omitempty"`
	LastChunk bool        `json:"lastChunk,omitempty"`
	Final     bool        `json:"final"`
	Timestamp time.Time   `json:"timestamp"`
}
