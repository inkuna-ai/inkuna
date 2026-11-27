package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/proc"
)

type Capabilities struct {
	Runtime          domain.Runtime `json:"runtime"`
	Available        bool           `json:"available"`
	Version          string         `json:"version,omitempty"`
	ReadOnly         bool           `json:"readOnly"`
	WorktreeWrite    bool           `json:"worktreeWrite"`
	StructuredOutput bool           `json:"structuredOutput"`
	MCPDisabled      bool           `json:"mcpDisabled"`
	// Resume reports that a later call can continue an earlier call's
	// session (RunRequest.Session) instead of being sent everything again.
	Resume bool `json:"resume"`
	// MCPServers reports that a call can be given the MCP servers its step
	// is granted (RunRequest.MCPServers): every built-in CLI, never a
	// template.
	MCPServers bool `json:"mcpServers"`
	// UserMCP names the user's own MCP servers every step on the runtime
	// is given (Spec.UserMCP); MCPDisabled is false when it names any.
	UserMCP []string `json:"userMcp,omitempty"`
}

type RunRequest struct {
	RunID           string
	WorkingDir      string
	Prompt          []byte
	Schema          []byte
	Permission      domain.Permission
	Budget          domain.Budget
	Model           string
	ReasoningEffort string
	Commands        [][]string
	// NoTools leaves the model nothing that reads or changes files: the call
	// needs only its prompt, like the orchestrator's, and may run outside a
	// repository. A template's arguments decide its own tools, so for one
	// only its read-only arguments apply.
	NoTools bool
	// Resumable keeps the call's session in the CLI's home so that a later
	// call can resume it; otherwise the CLI is told to keep nothing.
	Resumable bool
	// Session, when set, is the session of an earlier call in the same
	// working directory to continue: Prompt is then only what is new, and
	// the CLI sends the rest from the session. Tools, permission, and model
	// come from this request, never from the session. Only a runtime whose
	// Capabilities report Resume takes one.
	Session string
	// SessionUsage is the totals the session's last call reported
	// (RunResult.SessionUsage), which a resumed call's totals include.
	SessionUsage domain.Usage
	// Grants are permissions the user approved for this run after an earlier
	// turn was denied them. They widen the step's allowlist and nothing else.
	Grants []domain.PermissionGrant
	// Environment holds extra KEY=value pairs (resolved secrets) exported to
	// the worker process only. Nothing else is added beyond the inherited
	// environment.
	Environment []string
	// MCPServers are the MCP servers the call is granted: the CLI starts or
	// connects to these and no others, and allows only their listed tools.
	MCPServers []MCPServer
	// UserServers are the user's own MCP servers the call is given, with all
	// their tools, as ResolveUserServers reported them: the CLI starts
	// these from the user's own configuration, beside MCPServers, and no
	// other server there.
	UserServers []UserServer
	// MCPDir is where a CLI that reads its servers from a file is given one
	// for the call: an absolute directory outside every working directory.
	// The file holds the servers' secrets, so it is readable only by the user
	// and removed when the call ends.
	MCPDir string
	// mcpFile is the file Start wrote there for this call.
	mcpFile string
	// claudeSettingsFile is where Start wrote a Claude Code call's settings
	// when the call is given servers: Inkuna's own over the user's, without
	// the user's allow rules that would widen the grants (claudeUserSettings).
	// It sits beside mcpFile, readable only by the user, because the user's
	// settings can hold credentials.
	claudeSettingsFile string
}

// RunResult carries the worker's final structured output, when the runtime
// produced one that matched the requested schema, plus usage totals. When
// Start fails after the CLI started, the result still carries the last usage
// the stream reported, because a failed or timed-out call is often the most
// expensive one.
type RunResult struct {
	Output json.RawMessage
	// Usage is what this call used, without what a resumed session used
	// before it.
	Usage domain.Usage
	// Model is the model the stream says the call used, and AuthMode how the
	// runtime was signed in; each is empty when the stream does not say.
	Model    string
	AuthMode string
	// Session is the session the call ran in, for a later call to resume;
	// empty when the runtime reported none, or reported no totals for the
	// call, as when its output outran the budget. SessionUsage is the totals
	// the runtime reported for it, which a call that resumes it passes back.
	Session      string
	SessionUsage domain.Usage
	// Acted reports that the model did something in the call: wrote a
	// message or called a tool. A failed call reports it too, so a caller can
	// tell one that failed before the model was reached from one that failed
	// part way through its work.
	Acted bool
	// CacheUntil is when the provider stops keeping the call's prompt cached,
	// which is what makes resuming the session cheap; past it, a resumed call
	// sends the whole session again at full price. It counts from the call's
	// last model request, not from its end, since a long final reply ages the
	// cache while it is written. Zero means resuming costs the same whenever
	// it happens.
	CacheUntil time.Time
	// Denials lists tool calls the runtime refused for lack of permission.
	Denials []domain.PermissionDenial
}

type EventSink func(domain.RunEvent) error

type AgentRuntime interface {
	Probe(context.Context) (Capabilities, error)
	Start(context.Context, RunRequest, EventSink) (RunResult, error)
	Cancel(string) error
	Capabilities() Capabilities
}

// Closer is a runtime that can end every process it started, for the app
// to call when it quits.
type Closer interface {
	Close()
}

// ErrClosed is what a closed runtime's Start returns.
var ErrClosed = errors.New("the runtime is closed: Inkuna is quitting")

// closeGrace is how long Close waits for the processes it asked to stop
// before it kills them.
var closeGrace = proc.WaitDelay

// stopAndReap ends a worker the call no longer wants when no context was
// cancelled, so exec's WaitDelay never starts. It asks the process tree to
// stop and waits on the CLI, so it is not left a zombie. Whatever of the
// tree is still running closeGrace later is killed: the CLI, if it ignored
// SIGTERM, or a process it started that did, which would otherwise outlive
// the call where neither Cancel nor Close could reach it, as Close kills
// what a group still holds (proc.Alive).
func stopAndReap(command *exec.Cmd) {
	_ = proc.Terminate(command)
	ended := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(ended)
	}()
	grace := time.NewTimer(closeGrace)
	defer grace.Stop()
	select {
	case <-ended:
	case <-grace.C:
		_ = proc.Kill(command)
		<-ended
		return
	}
	for proc.Alive(command) {
		select {
		case <-grace.C:
			_ = proc.Kill(command)
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// runState collects the structured result while scanning stdout. Only the
// scanning goroutine writes to it; Start reads it after the scanners finish.
type runState struct {
	output  json.RawMessage
	usage   domain.Usage
	failure string
	session string
	acted   bool
	// cacheFor is the shortest cache lifetime the stream reported: a resume
	// reads the whole session, so its least lasting part decides.
	cacheFor time.Duration
	// requested is when the call's last model request was sent, as near as
	// the stream tells. sent is when the CLI last had a request to send (the
	// start, or a tool result), and answered says the model has replied to
	// it since.
	requested, sent time.Time
	answered        bool
	denials         []domain.PermissionDenial
	// final is set once the runtime reported its totals for the call; until
	// then usage is the sum of messages, each message's last report by id.
	// before is what a resumed session had reported, which the totals
	// include.
	final    bool
	messages map[string]domain.Usage
	before   domain.Usage
	model    string
	authMode string
	// granted names the call's MCP servers, and aborted says why the call
	// was stopped when one of them did not start; stop ends the CLI. given
	// names the user's own servers the call was given, and noted those the
	// stream said did not start.
	granted map[string]bool
	aborted string
	stop    func()
	given   map[string]bool
	noted   map[string]bool
}

// callUsage is what this call used: the runtime's totals less what the
// session had used before it, or, before the totals, the call's messages.
func (state *runState) callUsage(kind domain.Runtime) domain.Usage {
	if !state.final {
		return state.usage
	}
	return CallUsage(kind, state.usage, state.before)
}

// observeUsage keeps the best usage the stream has reported so far. The
// runtime's totals replace everything; before them, the messages it has
// finished add up, so a call cut off before its totals still reports them.
func (state *runState) observeUsage(kind domain.Runtime, payload map[string]any) {
	if usage, ok := ExtractUsage(kind, payload); ok {
		state.usage, state.final = usage, true
		return
	}
	// Copilot's result adds its premium requests to the tokens its messages
	// reported, which stay the call's measure.
	if premium, ok := ExtractPremiumRequests(kind, payload); ok {
		state.usage.PremiumRequests, state.final = premium, true
		return
	}
	id, usage, ok := ExtractMessageUsage(kind, payload)
	if !ok || state.final {
		return
	}
	if state.messages == nil {
		state.messages = map[string]domain.Usage{}
	}
	if id == "" {
		// Without an id a message can only count once, as itself.
		id = fmt.Sprintf("#%d", len(state.messages))
	}
	state.messages[id] = usage
	state.usage = domain.Usage{}
	for _, message := range state.messages {
		state.usage = state.usage.Add(message)
	}
}

// observeRequest keeps when the call's last model request was sent. The CLI
// sends one when it starts and another after each tool result, so the
// model's first output after one answers a request sent no earlier than it:
// that is when the cache was last refreshed.
func (state *runState) observeRequest(kind domain.Runtime, payload map[string]any, at time.Time) {
	switch {
	case toolResult(kind, payload):
		state.sent, state.answered = at, false
	case !state.answered && ExtractActivity(kind, payload):
		state.requested, state.answered = state.sent, true
	}
}

// toolResult reports whether an event hands a tool's result back to the
// model, after which the CLI sends it a new request: a Claude Code user
// message, which carries tool results, or a Codex item finished other than
// a message, reasoning, or warning. Codex also counts a tool's start and
// finish as the model acting, so only this tells its requests apart.
func toolResult(kind domain.Runtime, payload map[string]any) bool {
	rawType, _ := payload["type"].(string)
	switch kind {
	case domain.RuntimeClaude:
		return rawType == "user"
	case domain.RuntimeCodex:
		item, _ := payload["item"].(map[string]any)
		switch itemType, _ := item["type"].(string); itemType {
		case "", "agent_message", "reasoning", "error":
			return false
		}
		return rawType == "item.completed"
	}
	return false
}

// partial is the result of a run that failed: its usage, and whether the
// model acted before it failed. An output or denials from a failed run must
// not be acted on.
func (state *runState) partial(kind domain.Runtime) RunResult {
	return RunResult{Usage: state.callUsage(kind), Model: state.model, AuthMode: state.authMode, Acted: state.acted}
}

// Outcome names how a call to Start ended, from the error it returned.
func Outcome(err error) string {
	switch {
	case err == nil:
		return domain.OutcomeCompleted
	case errors.Is(err, context.DeadlineExceeded):
		return domain.OutcomeTimedOut
	case errors.Is(err, context.Canceled):
		return domain.OutcomeCancelled
	default:
		return domain.OutcomeFailed
	}
}

// commandRuntime runs one declared CLI. kind is the built-in CLI whose event
// stream stdout is read as, and is empty for plain text.
type commandRuntime struct {
	spec      Spec
	kind      domain.Runtime
	binary    string
	mu        sync.Mutex
	processes map[string]*exec.Cmd
	// closed refuses new calls once Close began, running counts the calls
	// in Start, for Close to wait on, and closing ends when Close begins,
	// cancelling every call.
	closed  bool
	running sync.WaitGroup
	closing context.Context
	close   context.CancelFunc
}

func NewCodex(binary string) AgentRuntime   { return newPreset(domain.RuntimeCodex, binary) }
func NewClaude(binary string) AgentRuntime  { return newPreset(domain.RuntimeClaude, binary) }
func NewCopilot(binary string) AgentRuntime { return newPreset(domain.RuntimeCopilot, binary) }

func newPreset(name domain.Runtime, binary string) AgentRuntime {
	runtime, err := New(Spec{Name: name, Binary: binary})
	if err != nil {
		// A built-in name with no template cannot fail validation.
		panic(err)
	}
	return runtime
}

func (runtime *commandRuntime) Capabilities() Capabilities {
	preset := runtime.spec.effectivePreset() != ""
	return Capabilities{
		Runtime: runtime.spec.Name, ReadOnly: true, WorktreeWrite: runtime.spec.CanWrite(), StructuredOutput: true, MCPDisabled: runtime.mcpDisabled(),
		Resume: preset, MCPServers: preset, UserMCP: append([]string(nil), runtime.spec.UserMCP...),
	}
}

// mcpDisabled reports whether the CLI starts no MCP server beyond those a
// step is granted. Claude Code is given a strict MCP config, Codex skips
// config.toml or is told to switch off each server there, and Copilot is
// told to disable each server it lists, whether or not the user's setup is
// loaded, so each starts none of the user's servers unless userMcp names
// them. Nothing is known about a template.
func (runtime *commandRuntime) mcpDisabled() bool {
	if runtime.spec.effectivePreset() == "" {
		return false
	}
	return len(runtime.spec.UserMCP) == 0
}

// copilotServerArgs disables every MCP server Copilot would load for a
// worker. Copilot has no switch against the user's mcp-config.json, and a
// server listed there starts with the worker even though the tool allowlist
// hides its tools, so each one is named. Copilot's own listing is asked
// rather than the file read, because Copilot accepts comments and trailing
// commas there and knows every place a server can come from. A listing that
// fails stops the worker rather than let a server start unseen. A server the
// step is granted is left on: the call's own configuration replaces the
// user's server of that name, and disabling the name would switch off both.
func (runtime *commandRuntime) copilotServerArgs(ctx context.Context, workingDir string, environment []string, granted map[string]bool) ([]string, error) {
	command := proc.Command(ctx, runtime.binary, "mcp", "list", "--json")
	command.Dir = workingDir
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := proc.Start(command)
	if err == nil {
		err = command.Wait()
	}
	output := stdout.Bytes()
	if err != nil {
		return nil, fmt.Errorf("list Copilot's MCP servers so the worker starts none: %w: %s", err, redact(truncate(strings.TrimSpace(stderr.String()), 512)))
	}
	var listing struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(output, &listing); err != nil {
		return nil, fmt.Errorf("read Copilot's MCP server list: %w", err)
	}
	names := make([]string, 0, len(listing.MCPServers))
	for name := range listing.MCPServers {
		if !granted[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	args := make([]string, 0, len(names))
	for _, name := range names {
		// One argument, so a name that starts with a dash stays a value.
		args = append(args, "--disable-mcp-server="+name)
	}
	return args, nil
}

func (runtime *commandRuntime) Probe(ctx context.Context) (Capabilities, error) {
	capabilities := runtime.Capabilities()
	path, err := exec.LookPath(runtime.binary)
	if err != nil {
		return capabilities, nil
	}
	command := exec.CommandContext(ctx, path, runtime.spec.VersionArgs...)
	output, err := command.Output()
	if err != nil {
		return capabilities, fmt.Errorf("probe %s: %w", runtime.spec.Name, err)
	}
	capabilities.Available = true
	capabilities.Version = strings.TrimSpace(string(output))
	return capabilities, nil
}

func (runtime *commandRuntime) Start(parent context.Context, request RunRequest, sink EventSink) (RunResult, error) {
	if request.RunID == "" || request.WorkingDir == "" || len(request.Prompt) == 0 || sink == nil {
		return RunResult{}, errors.New("run id, working directory, prompt, and event sink are required")
	}
	if request.Budget.WallTimeSeconds <= 0 {
		return RunResult{}, errors.New("positive wall time budget is required")
	}
	if request.Permission == domain.PermissionWorktree && !runtime.spec.CanWrite() {
		return RunResult{}, fmt.Errorf("runtime %q declares no write arguments, so it can only run read-only steps", runtime.spec.Name)
	}
	if request.Budget.MaxOutputBytes <= 0 {
		request.Budget.MaxOutputBytes = 4 << 20
	}
	// Admitted before anything starts, Copilot's server discovery included,
	// and cancelled by Close, so a call Close is waiting on ends promptly
	// even before its worker starts.
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return RunResult{}, ErrClosed
	}
	runtime.running.Add(1)
	runtime.mu.Unlock()
	defer runtime.running.Done()
	ctx, cancel := context.WithTimeout(parent, time.Duration(request.Budget.WallTimeSeconds)*time.Second)
	defer cancel()
	defer context.AfterFunc(runtime.closing, cancel)()

	if runtime.kind == domain.RuntimeCodex && len(request.Schema) > 0 {
		// OpenAI refuses a schema that is not strict before the model runs.
		// The shared schemas stay open for Claude Code and Copilot.
		strict, err := StrictSchema(request.Schema)
		if err != nil {
			return RunResult{}, err
		}
		request.Schema = strict
	}
	schemaPath, cleanup, err := writeSchema(request.Schema)
	if err != nil {
		return RunResult{}, err
	}
	defer cleanup()
	request, removeMCP, err := runtime.prepareMCP(request)
	if err != nil {
		return RunResult{}, err
	}
	defer removeMCP()
	args := runtime.arguments(request, schemaPath)
	if err := checkArgumentSizes(runtime.spec.Name, args); err != nil {
		return RunResult{}, err
	}
	environment := runtime.environment(request)
	granted := grantedServers(request)
	scrub := secretScrubber(request.MCPServers, request.Environment)
	var disabled []string
	switch runtime.spec.effectivePreset() {
	case domain.RuntimeCopilot:
		// The user's servers the call is given stay on, with the granted.
		given := userServerNames(request)
		for name := range granted {
			given[name] = true
		}
		disabled, err = runtime.copilotServerArgs(ctx, request.WorkingDir, environment, given)
	case domain.RuntimeCodex:
		if runtime.spec.UserConfig {
			given := userServerNames(request)
			for name := range granted {
				given[name] = true
			}
			disabled, err = runtime.codexDisabledArgs(ctx, request.WorkingDir, given)
		}
	}
	if err != nil {
		return RunResult{}, err
	}
	// The names come from the user's configuration, so they are checked too,
	// before the worker starts.
	if err := checkArgumentSizes(runtime.spec.Name, disabled); err != nil {
		return RunResult{}, err
	}
	if runtime.kind == domain.RuntimeCodex && len(disabled) > 0 {
		// Before the prompt's "-", which ends Codex's options.
		args = append(append(args[:len(args)-1:len(args)-1], disabled...), "-")
	} else {
		args = append(args, disabled...)
	}
	// proc.Command ends the whole tree when the call is cancelled. With
	// exec.CommandContext's own cancel, only the CLI's process is killed,
	// and on Windows, where the tree is found through it, what it started
	// is left running, holding the output open.
	command := proc.Command(ctx, runtime.binary, args...)
	command.Dir = request.WorkingDir
	if runtime.promptOnStdin() {
		command.Stdin = bytes.NewReader(request.Prompt)
	}
	command.Env = environment
	stdout, err := command.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return RunResult{}, err
	}
	if err := proc.Start(command); err != nil {
		return RunResult{}, fmt.Errorf("start %s: %w", runtime.spec.Name, err)
	}
	runtime.mu.Lock()
	// Close may have begun while the process started, after it listed the
	// processes to stop. The process has done nothing worth letting it
	// finish, and is killed, since one that ignored a request to stop would
	// outlive Close.
	if runtime.closed {
		runtime.mu.Unlock()
		_ = proc.Kill(command)
		_ = command.Wait()
		return RunResult{}, ErrClosed
	}
	if _, exists := runtime.processes[request.RunID]; exists {
		runtime.mu.Unlock()
		stopAndReap(command)
		return RunResult{}, fmt.Errorf("run %q is already active", request.RunID)
	}
	runtime.processes[request.RunID] = command
	runtime.mu.Unlock()
	defer func() {
		runtime.mu.Lock()
		delete(runtime.processes, request.RunID)
		runtime.mu.Unlock()
	}()

	_ = sink(event(request.RunID, 0, domain.EventLifecycle, "worker started", nil))
	streamEvents := make(chan domain.RunEvent, 32)
	scanResults := make(chan error, 2)
	state := &runState{requested: time.Now(), sent: time.Now(), granted: granted, given: userServerNames(request), stop: func() { _ = proc.Terminate(command) }}
	if request.Session != "" {
		state.before = request.SessionUsage
	}
	var scanners sync.WaitGroup
	scanners.Add(2)
	go func() {
		defer scanners.Done()
		runtime.scan(request, stdout, streamEvents, scanResults, state)
	}()
	// Codex prints on stderr which required server did not start; one of
	// the user's is told to the caller, which can go on without it.
	userFailures := make(chan *UserServerError, 1)
	go func() {
		defer scanners.Done()
		scanStderr(request.RunID, stderr, streamEvents, scanResults, func(line string) {
			if runtime.kind != domain.RuntimeCodex || len(request.UserServers) == 0 {
				return
			}
			if failure, ok := codexRequiredFailure(line, request.UserServers); ok {
				select {
				case userFailures <- failure:
				default:
				}
			}
		})
	}()
	go func() {
		scanners.Wait()
		close(streamEvents)
		close(scanResults)
	}()
	eventsChannel := (<-chan domain.RunEvent)(streamEvents)
	errorsChannel := (<-chan error)(scanResults)
	sequence := int64(1)
	for eventsChannel != nil || errorsChannel != nil {
		select {
		case entry, open := <-eventsChannel:
			if !open {
				eventsChannel = nil
				continue
			}
			entry.Sequence = sequence
			sequence++
			if err := sink(scrubEvent(entry, scrub)); err != nil {
				// Reaped before the streams are drained, as a cancelled
				// call is: Wait closes the pipes the CLI held.
				stopAndReap(command)
				drainRuntimeStreams(eventsChannel, errorsChannel)
				return state.partial(runtime.kind), fmt.Errorf("store run event: %w", err)
			}
		case scanErr, open := <-errorsChannel:
			if !open {
				errorsChannel = nil
				continue
			}
			if scanErr != nil {
				_ = sink(event(request.RunID, sequence, domain.EventWarning, redact(scanErr.Error()), nil))
				sequence++
			}
		case <-ctx.Done():
			_ = proc.Terminate(command)
			_ = sink(event(request.RunID, sequence, domain.EventError, ctx.Err().Error(), nil))
			// Waited on before the streams are drained: Wait closes the
			// pipes once the CLI's own process has ended, so a process it
			// started that outlived the request to stop, still holding them,
			// cannot keep the call from returning. Reaping it also leaves its
			// process group holding only what is still running, which is
			// what Close looks for.
			_ = command.Wait()
			drainRuntimeStreams(eventsChannel, errorsChannel)
			return state.partial(runtime.kind), ctx.Err()
		}
	}
	waitErr := command.Wait()
	if waitErr != nil {
		_ = sink(event(request.RunID, sequence, domain.EventError, redact(waitErr.Error()), nil))
		select {
		case failure := <-userFailures:
			return state.partial(runtime.kind), failure
		default:
		}
	}
	// A CLI that finished before it could be stopped still ran without a
	// server its step was granted.
	if state.aborted != "" {
		return state.partial(runtime.kind), fmt.Errorf("%s worker stopped: %s", runtime.spec.Name, scrub(redact(state.aborted)))
	}
	// The failure and the output are kept like events, so a server's secret
	// the worker repeated is hidden in them too.
	failure := scrub(redact(state.failure))
	if err := waitErr; err != nil {
		if state.failure != "" {
			return state.partial(runtime.kind), fmt.Errorf("%s worker failed: %s", runtime.spec.Name, failure)
		}
		return state.partial(runtime.kind), fmt.Errorf("%s worker failed: %w", runtime.spec.Name, err)
	}
	if state.failure != "" {
		_ = sink(event(request.RunID, sequence, domain.EventError, failure, nil))
		return state.partial(runtime.kind), fmt.Errorf("%s worker reported failure: %s", runtime.spec.Name, failure)
	}
	output := state.output
	if len(output) > 0 {
		output = scrubJSON(output, scrub)
	}
	result := RunResult{
		Output: output, Usage: state.callUsage(runtime.kind), Model: state.model, AuthMode: state.authMode, Denials: state.denials,
		Session: state.session, Acted: state.acted,
	}
	if state.final {
		result.SessionUsage = state.usage
	} else {
		// A call resuming the session could not tell its own usage from the
		// session's without these totals, so the next turn starts over.
		result.Session = ""
	}
	if lifetime := runtime.cacheFor(state); lifetime > 0 {
		result.CacheUntil = state.requested.Add(lifetime)
	}
	return result, sink(event(request.RunID, sequence, domain.EventComplete, "worker completed", nil))
}

// cacheFor is how long a call's prompt cache lasts: what Claude Code's stream
// reported, or else the provider's documented shortest lifetime. Anthropic's
// default cache lasts five minutes, and OpenAI keeps a prefix for five to
// ten minutes of inactivity. Copilot bills each request rather than its
// tokens, so resuming costs the same whenever it happens; so does a
// template, which never resumes.
func (runtime *commandRuntime) cacheFor(state *runState) time.Duration {
	switch runtime.kind {
	case domain.RuntimeClaude:
		if state.cacheFor > 0 {
			return state.cacheFor
		}
		return 5 * time.Minute
	case domain.RuntimeCodex:
		return 5 * time.Minute
	default:
		return 0
	}
}

func drainRuntimeStreams(events <-chan domain.RunEvent, results <-chan error) {
	for events != nil || results != nil {
		select {
		case _, open := <-events:
			if !open {
				events = nil
			}
		case _, open := <-results:
			if !open {
				results = nil
			}
		}
	}
}

// Close ends every process the runtime started and refuses new ones. A call
// made outside a run, such as planning, is otherwise ended by nothing when
// the app quits, and its model would keep running and billing. Each process
// group is asked to stop; a group with a process still in it closeGrace
// later is killed, even once its call has returned, since a descendant that
// ignored the request may no longer hold the call's output open. Close then
// returns without waiting further.
func (runtime *commandRuntime) Close() {
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.closed = true
	asked := runtime.liveProcesses()
	runtime.mu.Unlock()
	// Asked before the calls are cancelled, while each tree's root is
	// still running, since Windows finds a tree through its root.
	for _, command := range asked {
		_ = proc.Terminate(command)
	}
	runtime.close()
	ended := make(chan struct{})
	go func() {
		runtime.running.Wait()
		close(ended)
	}()
	grace := time.NewTimer(closeGrace)
	defer grace.Stop()
	select {
	case <-ended:
	case <-grace.C:
		killAll(asked)
		return
	}
	for {
		remaining := make([]*exec.Cmd, 0, len(asked))
		for _, command := range asked {
			if proc.Alive(command) {
				remaining = append(remaining, command)
			}
		}
		if len(remaining) == 0 {
			return
		}
		select {
		case <-grace.C:
			killAll(remaining)
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func killAll(commands []*exec.Cmd) {
	for _, command := range commands {
		_ = proc.Kill(command)
	}
}

// liveProcesses lists the processes running; the caller holds mu.
func (runtime *commandRuntime) liveProcesses() []*exec.Cmd {
	running := make([]*exec.Cmd, 0, len(runtime.processes))
	for _, command := range runtime.processes {
		running = append(running, command)
	}
	return running
}

func (runtime *commandRuntime) Cancel(runID string) error {
	runtime.mu.Lock()
	command := runtime.processes[runID]
	runtime.mu.Unlock()
	if command == nil {
		return fmt.Errorf("run %q is not active", runID)
	}
	return proc.Terminate(command)
}

// MaxArgumentBytes is the longest single command-line argument Inkuna
// passes. Linux holds one to 128 KiB with its terminating NUL, and refuses a
// longer one with a bare "argument list too long"; macOS holds all of them
// together to 1 MiB. Copilot reads its prompt only from an argument (-p -
// sends a dash, and -p @file sends the file name for the model to open).
const MaxArgumentBytes = 128<<10 - 1

// checkArgumentSizes refuses arguments the operating system would, saying
// how to make the prompt fit.
func checkArgumentSizes(name domain.Runtime, args []string) error {
	for _, argument := range args {
		if len(argument) > MaxArgumentBytes {
			return fmt.Errorf("%s gets its prompt on the command line, where one argument holds at most %d bytes, and this one is %d: for a worker, lower budgets.envelope in inkuna.yaml or shorten the agent's instructions; otherwise use a runtime that reads its prompt from standard input", name, MaxArgumentBytes, len(argument))
		}
	}
	return nil
}

// promptOnStdin reports whether the prompt is written to standard input. The
// Copilot adapter takes it as an argument; a template says which it wants.
func (runtime *commandRuntime) promptOnStdin() bool {
	if runtime.spec.effectivePreset() != "" {
		return runtime.kind != domain.RuntimeCopilot
	}
	return runtime.spec.Stdin == StdinPrompt
}

func (runtime *commandRuntime) arguments(request RunRequest, schemaPath string) []string {
	if runtime.spec.effectivePreset() == "" {
		template := append([]string(nil), runtime.spec.Args...)
		if request.Permission == domain.PermissionWorktree {
			template = append(template, runtime.spec.WriteArgs...)
		} else {
			template = append(template, runtime.spec.ReadOnlyArgs...)
		}
		return expand(template, map[string]string{
			"{prompt}": string(request.Prompt), "{model}": request.Model, "{reasoning_effort}": request.ReasoningEffort,
			"{schema}": string(request.Schema), "{schema_path}": schemaPath,
		})
	}
	if runtime.kind == domain.RuntimeCodex {
		sandbox := "read-only"
		if request.Permission == domain.PermissionWorktree {
			sandbox = "workspace-write"
		}
		args := []string{"exec", "--json", "--sandbox", sandbox}
		if request.Session != "" {
			// exec resume has no --sandbox flag; the configuration key sets
			// the same policy, and the resumed turn is told the new one.
			args = []string{"exec", "resume", request.Session, "--json", "--config", fmt.Sprintf("sandbox_mode=%q", sandbox)}
		}
		args = append(args, "--output-schema", schemaPath)
		if !request.Resumable {
			args = append(args, "--ephemeral")
		}
		args = append(args, codexContextArgs(runtime.spec.UserConfig)...)
		if request.NoTools {
			args = append(args, codexNoToolArgs()...)
		}
		args = append(args, codexMCPArgs(request.MCPServers)...)
		if runtime.spec.UserConfig {
			args = append(args, codexOwnServerArgs(request.UserServers)...)
		}
		// prepareMCP refused servers whose variables clash, so this cannot fail.
		if setup, err := codexMCPEnvironment(request); err == nil {
			args = append(args, codexShellArgs(setup.excluded)...)
		}
		if request.Model != "" {
			args = append(args, "--model", request.Model)
		}
		if request.ReasoningEffort != "" {
			args = append(args, "--config", fmt.Sprintf("model_reasoning_effort=%q", request.ReasoningEffort))
		}
		return append(args, "-")
	}
	if runtime.kind == domain.RuntimeCopilot {
		tools := "view,glob,grep"
		permissions := "read"
		if request.Permission == domain.PermissionWorktree {
			tools = "view,glob,grep,apply_patch,create,edit,bash,powershell"
			permissions = "read,write"
		}
		if request.NoTools {
			// An empty allowlist gives Copilot every tool, so this one names
			// a tool that does not exist, which leaves none.
			tools = "none"
		} else if servers := append(copilotMCPTools(request.MCPServers), userServerList(request)...); len(servers) > 0 {
			tools += "," + strings.Join(servers, ",")
		}
		args := []string{
			"-p", copilotPrompt(request), "--output-format=json", "--sandbox", "--no-ask-user", "--no-auto-update", "--no-color", "--no-custom-instructions",
			"--disable-builtin-mcps", "--available-tools=" + tools, "--allow-tool=" + permissions,
			"--secret-env-vars=LINEAR_API_KEY,ANTHROPIC_API_KEY,OPENAI_API_KEY,GH_TOKEN,GITHUB_TOKEN,COPILOT_GITHUB_TOKEN",
		}
		if request.Session != "" {
			// One argument: --resume takes its value only after =.
			args = append(args, "--resume="+request.Session)
		}
		for _, command := range allowedCommands(request) {
			args = append(args, "--allow-tool=shell("+command+")")
		}
		if request.mcpFile != "" {
			args = append(args, "--additional-mcp-config", "@"+request.mcpFile)
			args = append(args, copilotMCPPermissions(request.MCPServers)...)
		}
		if !request.NoTools {
			for _, name := range userServerList(request) {
				args = append(args, "--allow-tool="+name)
			}
		}
		if request.Model != "" {
			args = append(args, "--model", request.Model)
		}
		if request.ReasoningEffort != "" {
			args = append(args, "--effort", request.ReasoningEffort)
		}
		return args
	}
	// With no servers granted the configuration is empty, and strict keeps
	// every other place Claude Code finds servers out. Given the user's own
	// servers, the call loads the user's configuration instead, and the
	// settings' allowedMcpServers starts only those and the granted.
	servers := `{"mcpServers":{}}`
	if request.mcpFile != "" {
		servers = request.mcpFile
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if len(request.UserServers) == 0 {
		args = append(args, "--strict-mcp-config", "--mcp-config", servers)
	} else if request.mcpFile != "" {
		args = append(args, "--mcp-config", servers)
	}
	args = append(args, "--json-schema", string(request.Schema))
	// The repository's own Claude Code settings never load: a headless call
	// skips the workspace trust prompt, so a cloned repository's
	// .claude/settings.json would otherwise run its apiKeyHelper and apply
	// its env in every worker, read-only ones too (#268). The user's settings
	// come in --settings without the rules that would widen the grants
	// (claudeUserSettings), so Claude Code must not load that file itself
	// either. Given the user's own servers, it must: without the user's
	// source, Claude Code starts none of the servers in its user
	// configuration, and prepareMCP has refused a file whose rules would
	// widen a grant. A call with no settings file loads the user's as it is.
	sources := "user"
	if request.claudeSettingsFile != "" && len(request.UserServers) == 0 {
		sources = ""
	}
	args = append(args, "--setting-sources", sources)
	if request.WorkingDir != "" {
		// The project source also governs the repository's CLAUDE.md, which
		// a worker keeps: Claude Code reads it from an added directory, with
		// CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD set (environment), and
		// never that directory's settings.
		args = append(args, "--add-dir", request.WorkingDir)
	}
	settings := request.claudeSettingsFile
	if settings == "" {
		// Inkuna's own settings hold nothing secret, so they go inline. A map
		// of strings, bools, and slices always encodes.
		encoded, _ := json.Marshal(claudeSettingsFor(runtime.spec.UserConfig, claudeSandbox(request, nil), nil))
		settings = string(encoded)
	}
	args = append(args, claudeContextArgs(runtime.spec.UserConfig, request.Resumable, settings)...)
	if request.Session != "" {
		args = append(args, "--resume", request.Session)
	}
	args = append(args, "--tools", strings.Join(claudeTools(request, runtime.spec.UserConfig), ","))
	args = append(args, claudePermissionArgs(request)...)
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if request.ReasoningEffort != "" {
		args = append(args, "--effort", request.ReasoningEffort)
	}
	if request.Budget.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(request.Budget.MaxTurns))
	}
	if request.Budget.MaxCostCents > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", float64(request.Budget.MaxCostCents)/100))
	}
	return args
}

// codexContextArgs keeps what a step never asked for out of every Codex
// worker's context: connector apps, subagents (each with a prompt of its
// own), image generation, and web search. Without the user's opt-in it also
// skips config.toml, which is where MCP servers and hook trust live (a -c
// override cannot clear mcp_servers), switches hooks off, and drops the
// skill listing, since Inkuna hands each step its skills. Authentication
// stays in CODEX_HOME. Codex ignores a -c key it does not know, where
// --disable refuses an unknown feature, so an older CLI still starts.
//
// features.multi_agent removes subagents only from a model whose catalog
// entry says multi_agent_version v1. A v2 model, which is most of them, keeps
// its collaboration tools whatever the feature flags say, so a thread limit
// of one, the worker's own thread, makes spawn_agent fail instead, and the
// hints that tell the model to delegate are emptied.
func codexContextArgs(userConfig bool) []string {
	var args []string
	if !userConfig {
		args = append(args, "--ignore-user-config", "--config", "features.hooks=false", "--config", "skills.include_instructions=false")
	}
	return append(args,
		"--config", "features.apps=false", "--config", "features.multi_agent=false",
		"--config", "features.multi_agent_v2.max_concurrent_threads_per_session=1", "--config", "features.multi_agent_v2.wait_agent_enabled=false",
		"--config", `features.multi_agent_v2.root_agent_usage_hint_text=""`, "--config", `features.multi_agent_v2.multi_agent_mode_hint_text=""`,
		"--config", "features.image_generation=false", "--config", `web_search="disabled"`,
	)
}

// codexNoToolArgs switches off the Codex tools that have a switch: the
// shell, image viewing, sleep, and goals. The rest have none: the patch
// tool, which the read-only sandbox refuses, the MCP resource tools, which
// find no servers, and the clock, question, and subagent tools. With no
// shell the call reads no files, so it may run outside a git repository,
// which Codex otherwise refuses.
func codexNoToolArgs() []string {
	return []string{
		"--skip-git-repo-check",
		"--config", "features.shell_tool=false", "--config", "features.unified_exec=false",
		"--config", "features.view_image=false", "--config", "features.sleep_tool=false", "--config", "features.goals=false",
	}
}

// claudeContextArgs keeps the user's personal setup out of a Claude Code
// worker without touching how they sign in. User settings still load, since
// an apiKeyHelper or provider env there is the only way some users
// authenticate; flag settings override what they would add instead: hooks
// (the repository's too, as for Copilot), auto-memory, the global CLAUDE.md
// and rules, and an output style. Skills and slash commands are off because
// Inkuna gives each step its skills. The repository's CLAUDE.md still loads.
// Every worker drops the git workflow instructions, since Inkuna commits for
// it, and moves the per-machine sections out of the system prompt so that
// prompt is the same in every worktree and stays cached across runs. A call
// no later call will resume keeps its session out of the user's resume
// history. settings is the --settings value: claudeSettingsFor's, inline or
// in a file.
func claudeContextArgs(userConfig, resumable bool, settings string) []string {
	args := []string{"--exclude-dynamic-system-prompt-sections"}
	if !resumable {
		args = append(args, "--no-session-persistence")
	}
	if !userConfig {
		args = append(args, "--disable-slash-commands")
	}
	return append(args, "--settings", settings)
}

// claudeSettingsFor is what a Claude Code call's --settings carries (see
// claudeContextArgs), with the sandbox when the call needs one
// (claudeSandbox). inherited, when set, is the user's settings to start
// from; what is set here wins over them, and the user's own CLAUDE.md
// exclusions are kept.
func claudeSettingsFor(userConfig bool, sandbox, inherited map[string]any) map[string]any {
	settings := make(map[string]any, len(inherited)+6)
	for key, value := range inherited {
		settings[key] = value
	}
	settings["includeGitInstructions"] = false
	if sandbox != nil {
		settings["sandbox"] = sandbox
	}
	if !userConfig {
		settings["disableAllHooks"] = true
		settings["autoMemoryEnabled"] = false
		settings["outputStyle"] = "default"
		if excludes := claudeUserInstructions(); len(excludes) > 0 {
			own, _ := inherited["claudeMdExcludes"].([]any)
			combined := make([]any, 0, len(own)+len(excludes))
			combined = append(combined, own...)
			for _, exclude := range excludes {
				combined = append(combined, exclude)
			}
			settings["claudeMdExcludes"] = combined
		}
	}
	return settings
}

// claudeUserInstructions lists the instruction files Claude Code loads into
// every session from its configuration directory: the global CLAUDE.md and
// the user's rules.
func claudeUserInstructions() []string {
	directory := claudeConfigDir()
	if directory == "" {
		return nil
	}
	directory = filepath.ToSlash(directory)
	return []string{directory + "/CLAUDE.md", directory + "/rules/**"}
}

// claudeConfigDir is Claude Code's configuration directory, or "" when there
// is no home to find it in.
func claudeConfigDir() string {
	if directory := os.Getenv("CLAUDE_CONFIG_DIR"); directory != "" {
		return directory
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeTools is every tool a Claude Code worker can call at all; the
// allowlist decides which of them run without asking. Bash stays so a step
// can run its commands and the CLI's built-in read-only ones, and so a denied
// command reaches the user as a grant. Subagents, web access, and the CLI's
// scheduling and session tools are left out. An approved tool grant is added,
// and the Skill tool is added when the user's own skills are loaded. A call
// with no tools gets none, which --tools takes as an empty argument; the
// structured output tool is not one of them and stays.
func claudeTools(request RunRequest, userConfig bool) []string {
	if request.NoTools {
		return nil
	}
	tools := []string{"Read", "Glob", "Grep", "Bash"}
	if request.Permission == domain.PermissionWorktree {
		tools = append(tools, claudeEditTools...)
	}
	for _, grant := range request.Grants {
		if tool := strings.TrimSpace(grant.Tool); grant.Command == "" && claudeGrantableTool(tool) {
			tools = append(tools, tool)
		}
	}
	if userConfig {
		tools = append(tools, "Skill")
	}
	return tools
}

// claudeEditTools are the Claude Code tools that change files. The step's
// permission alone decides them; a grant can never add one.
var claudeEditTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

// claudePermissionArgs turns the step's permission into Claude Code flags. A
// headless run denies every tool call that would need a prompt, so a writable
// step must say up front that edits inside its working directory are
// accepted; otherwise every write in the worktree is refused. A read-only
// step denies the edit tools outright. Shell commands stay denied unless the
// step or an approved grant names them. No bypass mode is ever used.
func claudePermissionArgs(request RunRequest) []string {
	allowed := []string{"Read", "Glob", "Grep"}
	var args []string
	if request.Permission == domain.PermissionWorktree {
		args = append(args, "--permission-mode", "acceptEdits")
		allowed = append(allowed, claudeEditTools...)
	}
	for _, command := range allowedCommands(request) {
		allowed = append(allowed, "Bash("+command+")")
	}
	for _, grant := range request.Grants {
		if tool := strings.TrimSpace(grant.Tool); grant.Command == "" && claudeGrantableTool(tool) {
			allowed = append(allowed, tool)
		}
	}
	allowed = append(allowed, claudeMCPRules(request.MCPServers)...)
	allowed = append(allowed, claudeUserRules(request.UserServers)...)
	// Each rule is its own argument so a command with commas stays one rule.
	args = append(append(args, "--allowedTools"), allowed...)
	var disallowed []string
	if request.Permission != domain.PermissionWorktree {
		disallowed = append(disallowed, claudeEditTools...)
	}
	if file := firstNonEmptyText(request.mcpFile, request.claudeSettingsFile); file != "" {
		disallowed = append(disallowed, claudeMCPFileRule(file))
	}
	if len(request.UserServers) > 0 {
		disallowed = append(disallowed, claudeUserServerRules()...)
	}
	if len(disallowed) > 0 {
		args = append(append(args, "--disallowedTools"), disallowed...)
	}
	return args
}

// claudeGrantableTool reports whether a tool grant may be passed to Claude
// Code: a plain tool name that is neither an edit tool nor the shell, which
// is only ever allowed one command at a time, nor an MCP server's tool,
// which only inkuna.yaml's declaration allows.
func claudeGrantableTool(tool string) bool {
	if tool == "" || tool == "Bash" || strings.ContainsAny(tool, "()*, \t\n") || strings.HasPrefix(tool, "-") || strings.HasPrefix(tool, "mcp__") {
		return false
	}
	for _, edit := range claudeEditTools {
		if tool == edit {
			return false
		}
	}
	return true
}

// allowedCommands lists the shell command lines a worker may run: the step's
// own commands followed by the ones the user approved for this run. An
// approved line the rule check refuses allows nothing, so one stored before
// GrantFor held grants to that check cannot widen a worker (#270).
func allowedCommands(request RunRequest) []string {
	commands := make([]string, 0, len(request.Commands)+len(request.Grants))
	for _, command := range request.Commands {
		if len(command) > 0 {
			commands = append(commands, strings.Join(command, " "))
		}
	}
	for _, grant := range request.Grants {
		if argv, ok := AllowedCommandFor(grant.Command); ok && strings.Join(argv, " ") == strings.TrimSpace(grant.Command) {
			commands = append(commands, strings.Join(argv, " "))
		}
	}
	return commands
}

func copilotPrompt(request RunRequest) string {
	prompt := string(request.Prompt)
	if len(request.Schema) == 0 {
		return prompt
	}
	return prompt + "\n\nReturn the final response as JSON matching this schema:\n" + string(request.Schema)
}

// baselineEnvironment lists the non-secret variables a CLI needs to start:
// path and home resolution, locale, temp directories, TLS and proxy settings,
// and the providers' own configuration-directory overrides. Credentials are
// deliberately absent; they reach a worker only through RunRequest.Environment,
// which the service fills from secrets declared in inkuna.yaml.
var baselineEnvironment = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "TERM": true, "COLORTERM": true, "TZ": true,
	"TMPDIR": true, "TMP": true, "TEMP": true, "LANG": true, "LANGUAGE": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
	"APPDATA": true, "LOCALAPPDATA": true, "USERPROFILE": true, "SYSTEMROOT": true, "SystemRoot": true, "PROGRAMDATA": true, "COMSPEC": true, "PATHEXT": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"CLAUDE_CONFIG_DIR": true, "CODEX_HOME": true, "COPILOT_HOME": true, "GH_CONFIG_DIR": true,
}

var baselinePrefixes = []string{"LC_", "XDG_"}

// BaselineEnvironment is the part of Inkuna's own environment every process
// it starts for a run may see: the non-secret variables of
// baselineEnvironment. Ambient variables such as API keys in the user's
// shell are left out.
func BaselineEnvironment() []string {
	environment := make([]string, 0, 32)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if ReservedVariable(name) {
			environment = append(environment, entry)
		}
	}
	return environment
}

// environment builds the worker process environment from the non-secret
// baseline plus the secrets the service explicitly declared for this run.
// Ambient variables such as API keys in the user's shell never pass through.
func (runtime *commandRuntime) environment(request RunRequest) []string {
	environment := append(BaselineEnvironment(), request.Environment...)
	if runtime.spec.Instance != "" {
		environment = append(environment, proc.Mark(runtime.spec.Instance))
	}
	if runtime.spec.effectivePreset() == domain.RuntimeCodex {
		// prepareMCP refused servers whose variables clash, so this cannot fail.
		if setup, err := codexMCPEnvironment(request); err == nil {
			environment = append(environment, setup.environment...)
		}
	}
	if runtime.spec.effectivePreset() == domain.RuntimeClaude {
		// The repository's CLAUDE.md comes from the working directory added
		// with --add-dir, since the project setting source stays off.
		environment = append(environment, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1")
	}
	if runtime.spec.effectivePreset() != domain.RuntimeCopilot {
		return environment
	}
	environment = append(environment,
		"COPILOT_ALLOW_ALL=false",
		"GITHUB_COPILOT_PROMPT_MODE_EXTENSIONS=false",
		"GITHUB_COPILOT_PROMPT_MODE_REPO_HOOKS=false",
		"GITHUB_COPILOT_PROMPT_MODE_WORKSPACE_MCP=false",
		// --sandbox is ignored, with only a warning, unless the sandbox
		// feature is on; this turns on that one feature rather than all of
		// --experimental, so shell commands run inside the OS sandbox.
		"COPILOT_CLI_ENABLED_FEATURE_FLAGS=SANDBOX",
	)
	if runtime.spec.UserConfig {
		return environment
	}
	// Only plugins named with --plugin-dir load, and Inkuna names none, so the
	// user's installed plugins stay out of the worker.
	return append(environment, "COPILOT_PLUGIN_DIR_ONLY=true")
}

func hasBaselinePrefix(name string) bool {
	for _, prefix := range baselinePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// longestLine is the longest line of a worker's stdout Inkuna reads; a
// longer one is skipped, with a warning, rather than ending the stream.
const longestLine = 4 << 20

// longestStderrLine is the same for stderr, which carries only warnings.
const longestStderrLine = 64 << 10

// stderrBudget is how much of a worker's stderr becomes warnings; the rest
// is read and dropped.
const stderrBudget = 256 << 10

// lineReader scans a worker's stream line by line to its end. A worker
// blocks once its pipe fills, so a stream is always read to the end, even
// past a budget or over a line too long to keep: such a line is skipped and
// counted rather than ending the scan.
type lineReader struct {
	scanner *bufio.Scanner
	// skipped counts the lines dropped as too long since the last Scan.
	skipped int
	// read counts the bytes read so far, skipped lines and newlines
	// included, so a worker cannot pass a budget in lines too long to keep.
	read int64
	// start is where in the stream the line Scan last returned begins.
	start int64
}

func newLineReader(reader io.Reader, longest int) *lineReader {
	lines := &lineReader{scanner: bufio.NewScanner(reader)}
	lines.scanner.Buffer(make([]byte, 0, min(64<<10, longest)), longest)
	skipping := false
	split := func(data []byte, atEOF bool) (int, []byte, error) {
		if end := bytes.IndexByte(data, '\n'); end >= 0 {
			if skipping {
				skipping = false
				return end + 1, nil, nil
			}
			return end + 1, bytes.TrimSuffix(data[:end], []byte{'\r'}), nil
		}
		switch {
		case skipping:
			return len(data), nil, nil
		case len(data) >= longest:
			// The scanner holds no more than longest bytes, so the rest of
			// this line is dropped as it arrives.
			skipping = true
			lines.skipped++
			return len(data), nil, nil
		case atEOF && len(data) > 0:
			return len(data), bytes.TrimSuffix(data, []byte{'\r'}), nil
		}
		return 0, nil, nil
	}
	lines.scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := split(data, atEOF)
		if token != nil {
			lines.start = lines.read
		}
		lines.read += int64(advance)
		return advance, token, err
	})
	return lines
}

// Scan reads the next line, as bufio.Scanner does.
func (lines *lineReader) Scan() bool { return lines.scanner.Scan() }

func (lines *lineReader) Bytes() []byte { return lines.scanner.Bytes() }

func (lines *lineReader) Text() string { return lines.scanner.Text() }

func (lines *lineReader) Err() error { return lines.scanner.Err() }

// takeSkipped returns the lines skipped since it was last called.
func (lines *lineReader) takeSkipped() int {
	skipped := lines.skipped
	lines.skipped = 0
	return skipped
}

// skippedWarning tells the timeline that lines were too long to read.
func skippedWarning(runID string, count, longest int) domain.RunEvent {
	return event(runID, 0, domain.EventWarning, fmt.Sprintf("Skipped %d line(s) of the worker's output longer than %s.", count, byteSize(int64(longest))), nil)
}

// stderrBudgetWarning tells the timeline that the rest of the worker's
// stderr is dropped.
func stderrBudgetWarning(runID string) domain.RunEvent {
	return event(runID, 0, domain.EventWarning, fmt.Sprintf("The worker's stderr passed its %s budget. Inkuna reads the rest but records none of it.", byteSize(stderrBudget)), nil)
}

// budgetWarning tells the timeline that the worker's events stop being kept.
func budgetWarning(runID string, budget int64) domain.RunEvent {
	return event(runID, 0, domain.EventWarning, fmt.Sprintf("The worker's output passed its %s budget. Inkuna keeps reading it for the result but records no more of its events.", byteSize(budget)), nil)
}

func byteSize(size int64) string {
	switch {
	case size >= 1<<20 && size%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", size>>20)
	case size >= 1<<10 && size%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", size>>10)
	}
	return fmt.Sprintf("%d-byte", size)
}

// scan reads a worker's stdout to its end. Its events are recorded until
// the output passes the run's budget; past it, one warning says so, and
// the stream is still read, and its result, usage, session, and failure
// still taken, so the step ends as it would have instead of blocking the
// worker on a full pipe.
func (runtime *commandRuntime) scan(request RunRequest, reader io.Reader, events chan<- domain.RunEvent, done chan<- error, state *runState) {
	lines := newLineReader(reader, longestLine)
	budget := request.Budget.MaxOutputBytes
	if runtime.kind == "" {
		scanText(request.RunID, lines, budget, events, state)
		done <- lines.Err()
		return
	}
	over := false
	emit := func(entry domain.RunEvent) {
		if !over {
			events <- entry
		}
	}
	for lines.Scan() {
		if skipped := lines.takeSkipped(); skipped > 0 {
			emit(skippedWarning(request.RunID, skipped, longestLine))
		}
		if !over && lines.read > budget {
			events <- budgetWarning(request.RunID, budget)
			over = true
		}
		line := lines.Bytes()
		var payload map[string]any
		if err := json.Unmarshal(line, &payload); err != nil {
			emit(event(request.RunID, 0, domain.EventWarning, "malformed runtime event", map[string]any{"line": redact(truncate(string(line), 512))}))
			continue
		}
		if state != nil {
			if output, ok := ExtractOutput(runtime.kind, payload); ok {
				state.output = output
			}
			state.observeUsage(runtime.kind, payload)
			if failure, ok := ExtractFailure(runtime.kind, payload); ok {
				state.failure = failure
			}
			// A step granted a server that did not start would work without
			// it, so the call stops before the model spends anything.
			if reason, ok := mcpFailure(runtime.kind, payload, state.granted); ok && state.aborted == "" {
				state.aborted = reason
				if state.stop != nil {
					state.stop()
				}
			}
			for _, name := range sortedKeys(userServerProblems(runtime.kind, payload, state.given)) {
				if state.noted[name] {
					continue
				}
				if state.noted == nil {
					state.noted = map[string]bool{}
				}
				state.noted[name] = true
				emit(event(request.RunID, 0, domain.EventWarning, userServerProblems(runtime.kind, payload, state.given)[name], nil))
			}
			if denials := ExtractDenials(runtime.kind, payload); len(denials) > 0 {
				state.denials = denials
			}
			if session, ok := ExtractSession(runtime.kind, payload); ok {
				state.session = session
			}
			if model, ok := ExtractModel(runtime.kind, payload); ok {
				state.model = model
			}
			if mode, ok := ExtractAuthMode(runtime.kind, payload); ok {
				state.authMode = mode
			}
			state.acted = state.acted || ExtractActivity(runtime.kind, payload)
			if lifetime, ok := ExtractCacheLifetime(runtime.kind, payload); ok && (state.cacheFor == 0 || lifetime < state.cacheFor) {
				state.cacheFor = lifetime
			}
			state.observeRequest(runtime.kind, payload, time.Now())
		}
		kind, summary := normalize(runtime.kind, payload)
		normalized := event(request.RunID, 0, kind, summary, sanitize(payload))
		normalized.Reads = ExtractReads(runtime.kind, payload)
		emit(normalized)
	}
	if skipped := lines.takeSkipped(); skipped > 0 {
		emit(skippedWarning(request.RunID, skipped, longestLine))
	}
	done <- lines.Err()
}

// scanText reads a plain-text runtime: every line is a message, and the last
// JSON object in the output is the structured result. Past the budget, the
// lines stop being recorded as messages, and only the latest budget's worth
// of the transcript is kept to find the result in.
func scanText(runID string, lines *lineReader, budget int64, events chan<- domain.RunEvent, state *runState) {
	var transcript []byte
	over := false
	for lines.Scan() {
		if skipped := lines.takeSkipped(); skipped > 0 && !over {
			events <- skippedWarning(runID, skipped, longestLine)
		}
		if !over && lines.read > budget {
			events <- budgetWarning(runID, budget)
			over = true
		}
		line := lines.Text()
		transcript = append(append(transcript, line...), '\n')
		if over && int64(len(transcript)) > 2*budget {
			transcript = append(transcript[:0], transcript[int64(len(transcript))-budget:]...)
		}
		if strings.TrimSpace(line) != "" && !over {
			events <- event(runID, 0, domain.EventMessage, redact(truncate(line, 500)), nil)
		}
	}
	if skipped := lines.takeSkipped(); skipped > 0 && !over {
		events <- skippedWarning(runID, skipped, longestLine)
	}
	if state == nil {
		return
	}
	if output, ok := lastJSONObject(string(transcript)); ok {
		state.output = output
	}
}

// lastJSONObject returns the last top-level JSON object in text, compacted.
// Prose around and between objects is skipped, so a model that thinks aloud
// before answering still yields its answer.
func lastJSONObject(text string) (json.RawMessage, bool) {
	var found json.RawMessage
	for offset := 0; offset < len(text); {
		start := strings.IndexByte(text[offset:], '{')
		if start < 0 {
			break
		}
		offset += start
		decoder := json.NewDecoder(strings.NewReader(text[offset:]))
		var value map[string]any
		if err := decoder.Decode(&value); err != nil {
			offset++
			continue
		}
		if data, err := json.Marshal(value); err == nil {
			found = data
		}
		offset += int(decoder.InputOffset())
	}
	return found, found != nil
}

// scanStderr turns a worker's stderr into warnings, up to stderrBudget, and
// reads and drops the rest so the worker never blocks on it. A line too long
// to read, or the stderr past the budget, is said in a warning, since it may
// have been the worker's only word on why it failed.
func scanStderr(runID string, reader io.Reader, events chan<- domain.RunEvent, done chan<- error, observe func(string)) {
	lines := newLineReader(reader, longestStderrLine)
	over := false
	reportSkipped := func() {
		if skipped := lines.takeSkipped(); skipped > 0 && !over {
			events <- skippedWarning(runID, skipped, longestStderrLine)
		}
	}
	for lines.Scan() {
		// The budget is spent once a line starts at it, skipped lines
		// included, and the warning comes only when a line is dropped.
		if lines.start >= stderrBudget {
			if !over {
				events <- stderrBudgetWarning(runID)
				over = true
			}
			continue
		}
		reportSkipped()
		if observe != nil {
			observe(lines.Text())
		}
		events <- event(runID, 0, domain.EventWarning, redact(truncate(lines.Text(), 1024)), nil)
	}
	reportSkipped()
	done <- lines.Err()
}

func normalize(runtime domain.Runtime, payload map[string]any) (domain.EventKind, string) {
	if runtime == domain.RuntimeClaude {
		if kind, summary, ok := normalizeClaude(payload); ok {
			return kind, summary
		}
	}
	rawType, _ := payload["type"].(string)
	value := strings.ToLower(rawType)
	switch {
	case strings.Contains(value, "usage"):
		return domain.EventUsage, string(runtime) + " usage update"
	case strings.Contains(value, "tool"):
		return domain.EventTool, string(runtime) + " tool execution"
	case strings.Contains(value, "error"):
		return domain.EventError, string(runtime) + " error"
	case strings.Contains(value, "result"), strings.Contains(value, "complete"):
		return domain.EventComplete, string(runtime) + " result"
	case strings.Contains(value, "file"):
		return domain.EventFile, string(runtime) + " file change"
	default:
		return domain.EventMessage, string(runtime) + " message"
	}
}

func normalizeClaude(payload map[string]any) (domain.EventKind, string, bool) {
	rawType, _ := payload["type"].(string)
	switch rawType {
	case "assistant", "user":
		message, _ := payload["message"].(map[string]any)
		content, _ := message["content"].([]any)
		for _, rawBlock := range content {
			block, _ := rawBlock.(map[string]any)
			switch block["type"] {
			case "text":
				if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
					return domain.EventMessage, truncate(redact(strings.TrimSpace(text)), 500), true
				}
			case "tool_use":
				name, _ := block["name"].(string)
				name = redact(strings.TrimSpace(name))
				detail := redact(claudeToolDetail(block["input"]))
				kind := domain.EventTool
				if name == "Write" || name == "Edit" || name == "MultiEdit" || name == "NotebookEdit" {
					kind = domain.EventFile
				}
				summary := "Claude tool: " + name
				if detail != "" {
					summary += " — " + detail
				}
				return kind, truncate(summary, 500), true
			case "tool_result":
				detail := redact(claudeResultDetail(block["content"]))
				if failed, _ := block["is_error"].(bool); failed {
					return domain.EventWarning, truncate("Claude tool failed: "+detail, 500), true
				}
				return domain.EventTool, truncate("Claude tool result: "+detail, 500), true
			}
		}
	case "system":
		if subtype, _ := payload["subtype"].(string); subtype == "permission_denied" {
			detail, _ := payload["message"].(string)
			if detail == "" {
				detail, _ = payload["decision_reason"].(string)
			}
			return domain.EventWarning, truncate("Claude permission denied: "+redact(detail), 500), true
		}
	}
	return "", "", false
}

func claudeToolDetail(raw any) string {
	input, _ := raw.(map[string]any)
	for _, key := range []string{"command", "file_path", "path", "pattern", "query"} {
		if value, _ := input[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func claudeResultDetail(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for _, entry := range value {
			if block, ok := entry.(map[string]any); ok {
				if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
				if content, _ := block["content"].(string); strings.TrimSpace(content) != "" {
					return strings.TrimSpace(content)
				}
			}
		}
	}
	return "completed"
}

// secretPattern captures the whole value after a credential-like key up to
// the next quote, comma, brace, or line end so multi-word values such as
// "Bearer sk-..." are removed entirely rather than only their first word.
var secretPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|authorization)(\s*["'=:]+\s*)([^",}\r\n]+)`)

func redact(value string) string { return secretPattern.ReplaceAllString(value, "$1$2[REDACTED]") }

// Redact hides the value after each credential-like key in text, as the
// runtime does for the events it stores, for output Inkuna keeps itself.
func Redact(value string) string { return redact(value) }

func sanitize(payload map[string]any) map[string]any {
	data, err := json.Marshal(payload)
	if err != nil {
		return map[string]any{"warning": "payload could not be serialized"}
	}
	var sanitized map[string]any
	_ = json.Unmarshal([]byte(redact(truncate(string(data), 16<<10))), &sanitized)
	return sanitized
}

func truncate(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	if limit <= 0 {
		return "…"
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "…"
}

func event(runID string, sequence int64, kind domain.EventKind, summary string, payload map[string]any) domain.RunEvent {
	return domain.RunEvent{Version: domain.ContractVersion, RunID: runID, Sequence: sequence, Timestamp: time.Now().UTC(), Kind: kind, Summary: summary, Payload: payload}
}

func writeSchema(schema []byte) (string, func(), error) {
	if len(schema) == 0 {
		schema = []byte(`{"type":"object"}`)
	}
	var value any
	if err := json.Unmarshal(schema, &value); err != nil {
		return "", func() {}, fmt.Errorf("invalid output schema: %w", err)
	}
	directory, err := os.MkdirTemp("", "inkuna-schema-*")
	if err != nil {
		return "", func() {}, err
	}
	path := filepath.Join(directory, "output.json")
	if err := os.WriteFile(path, schema, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return "", func() {}, err
	}
	return path, func() { _ = os.RemoveAll(directory) }, nil
}
