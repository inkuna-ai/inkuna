package a2a

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnknownAgent = errors.New("a2a: unknown agent")
	ErrUnknownTask  = errors.New("a2a: unknown task")
	ErrTaskFinished = errors.New("a2a: task already finished")
	ErrTaskBusy     = errors.New("a2a: task is not waiting for input")
)

// Handler performs an agent's work for one message. It reports progress with
// the bus methods and returns when the turn is over. Returning an error moves
// the task to failed; returning nil without reaching a terminal or interrupted
// state completes it.
type Handler func(ctx context.Context, bus *Bus, task Task, message Message) error

// Persister mirrors task snapshots into durable storage. It is optional and
// failures are reported through the bus error callback rather than blocking
// agents.
type Persister interface {
	SaveA2ATask(ctx context.Context, task Task) error
}

type registration struct {
	card    AgentCard
	handler Handler
}

type subscriber struct {
	channel chan Event
	done    chan struct{}
}

// Bus is the in-process A2A broker. It is safe for concurrent use.
type Bus struct {
	mu          sync.RWMutex
	agents      map[string]registration
	tasks       map[string]*Task
	turns       map[string]int
	waiters     map[string][]chan struct{}
	subscribers map[string][]*subscriber
	persister   Persister
	observers   []func(Task)
	// revisions numbers each task's mutations under mu. persistMu and saved
	// make persistence follow that order even though it runs outside mu.
	revisions map[string]uint64
	persistMu sync.Mutex
	saved     map[string]uint64
	onError   func(error)
	clock     func() time.Time
}

// Option customizes a Bus.
type Option func(*Bus)

func WithPersister(persister Persister) Option { return func(bus *Bus) { bus.persister = persister } }

// WithObserver registers a callback that receives a snapshot after every
// task change. It runs on the caller's goroutine and must not block or call
// the bus. Observers are told one snapshot at a time, each in the order
// they were registered, and a task's snapshots in the order they were
// taken.
func WithObserver(observer func(Task)) Option {
	return func(bus *Bus) { bus.observers = append(bus.observers, observer) }
}

func WithErrorHandler(handler func(error)) Option {
	return func(bus *Bus) { bus.onError = handler }
}

func NewBus(options ...Option) *Bus {
	bus := &Bus{
		agents:      make(map[string]registration),
		tasks:       make(map[string]*Task),
		revisions:   make(map[string]uint64),
		saved:       make(map[string]uint64),
		turns:       make(map[string]int),
		waiters:     make(map[string][]chan struct{}),
		subscribers: make(map[string][]*subscriber),
		clock:       func() time.Time { return time.Now().UTC() },
		onError:     func(error) {},
	}
	for _, option := range options {
		option(bus)
	}
	return bus
}

// RegisterAgent publishes a card and the handler that serves it. Registering
// the same name again replaces the previous registration.
func (bus *Bus) RegisterAgent(card AgentCard, handler Handler) error {
	if strings.TrimSpace(card.Name) == "" {
		return errors.New("a2a: agent card needs a name")
	}
	if handler == nil {
		return fmt.Errorf("a2a: agent %q needs a handler", card.Name)
	}
	if card.ProtocolVersion == "" {
		card.ProtocolVersion = ProtocolVersion
	}
	if card.URL == "" {
		card.URL = "inkuna://agents/" + card.Name
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.agents[card.Name] = registration{card: card, handler: handler}
	return nil
}

// UnregisterAgent removes a card. In-flight tasks keep running.
func (bus *Bus) UnregisterAgent(name string) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	delete(bus.agents, name)
}

// Agents returns every published card sorted by name.
func (bus *Bus) Agents() []AgentCard {
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	cards := make([]AgentCard, 0, len(bus.agents))
	for _, entry := range bus.agents {
		cards = append(cards, entry.card)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
	return cards
}

// Agent returns one card.
func (bus *Bus) Agent(name string) (AgentCard, bool) {
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	entry, ok := bus.agents[name]
	return entry.card, ok
}

// SendMessage implements message/send. A message without a TaskID starts a
// new task; one with a TaskID continues an interrupted task. The handler runs
// asynchronously; use Wait or Subscribe to observe it.
func (bus *Bus) SendMessage(ctx context.Context, agent string, params MessageSendParams) (Task, error) {
	message := params.Message
	if len(message.Parts) == 0 {
		return Task{}, errors.New("a2a: message needs at least one part")
	}
	if message.Role == "" {
		message.Role = RoleUser
	}
	if message.MessageID == "" {
		message.MessageID = NewID("msg")
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = bus.clock()
	}
	bus.mu.Lock()
	entry, ok := bus.agents[agent]
	if !ok {
		bus.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}
	var task *Task
	if message.TaskID != "" {
		task = bus.tasks[message.TaskID]
		if task == nil {
			bus.mu.Unlock()
			return Task{}, fmt.Errorf("%w: %s", ErrUnknownTask, message.TaskID)
		}
		if task.Status.State.Terminal() {
			bus.mu.Unlock()
			return Task{}, fmt.Errorf("%w: %s is %s", ErrTaskFinished, task.ID, task.Status.State)
		}
		// A follow-up is only meaningful while the agent is paused; accepting
		// one mid-turn would run two handlers for the same task at once.
		if !task.Status.State.Interrupted() {
			bus.mu.Unlock()
			return Task{}, fmt.Errorf("%w: %s is %s", ErrTaskBusy, task.ID, task.Status.State)
		}
		// Only the agent that owns the task may continue it, and the message
		// must belong to the task's context.
		if task.Agent != agent {
			bus.mu.Unlock()
			return Task{}, fmt.Errorf("%w: task %s belongs to %s, not %s", ErrUnknownTask, task.ID, task.Agent, agent)
		}
		if message.ContextID != "" && message.ContextID != task.ContextID {
			bus.mu.Unlock()
			return Task{}, fmt.Errorf("a2a: message context %s does not match task context %s", message.ContextID, task.ContextID)
		}
		message.ContextID = task.ContextID
	} else {
		if message.ContextID == "" {
			message.ContextID = NewID("ctx")
		}
		task = &Task{ID: NewID("task"), ContextID: message.ContextID, Agent: agent, Metadata: params.Metadata}
		message.TaskID = task.ID
		bus.tasks[task.ID] = task
	}
	task.History = append(task.History, message)
	task.Status = TaskStatus{State: StateSubmitted, Timestamp: bus.clock()}
	bus.turns[task.ID]++
	turn := bus.turns[task.ID]
	snapshot := cloneTask(*task)
	revision := bus.nextRevision(task.ID)
	bus.mu.Unlock()
	bus.publish(snapshot.ID, Event{Kind: EventStatusUpdate, TaskID: snapshot.ID, ContextID: snapshot.ContextID, Status: &snapshot.Status, Timestamp: snapshot.Status.Timestamp})
	bus.persist(ctx, snapshot, revision)
	go bus.serve(ctx, entry.handler, snapshot, message, turn)
	return snapshot, nil
}

// serve runs one handler turn. It only finalizes the task if no newer turn
// has started since, so a follow-up message can never be completed away by
// the turn that asked for it.
func (bus *Bus) serve(ctx context.Context, handler Handler, task Task, message Message, turn int) {
	if err := bus.UpdateStatus(ctx, task.ID, StateWorking, nil); err != nil {
		return
	}
	err := handler(ctx, bus, task, message)
	bus.mu.RLock()
	current := bus.tasks[task.ID]
	state := StateUnknown
	if current != nil {
		state = current.Status.State
	}
	superseded := bus.turns[task.ID] != turn
	bus.mu.RUnlock()
	if superseded {
		return
	}
	switch {
	case err != nil && !state.Terminal():
		text := err.Error()
		if ctx.Err() != nil {
			_ = bus.UpdateStatus(context.Background(), task.ID, StateCanceled, &Message{Role: RoleAgent, Parts: []Part{TextPart(text)}})
			return
		}
		_ = bus.UpdateStatus(context.Background(), task.ID, StateFailed, &Message{Role: RoleAgent, Parts: []Part{TextPart(text)}})
	case err == nil && !state.Terminal() && !state.Interrupted():
		_ = bus.UpdateStatus(context.Background(), task.ID, StateCompleted, nil)
	}
}

// UpdateStatus moves a task to a new state and notifies subscribers. Agents
// call it to report working, input-required, completed, or failed.
func (bus *Bus) UpdateStatus(ctx context.Context, taskID string, state TaskState, message *Message) error {
	bus.mu.Lock()
	task := bus.tasks[taskID]
	if task == nil {
		bus.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	if task.Status.State.Terminal() {
		bus.mu.Unlock()
		return fmt.Errorf("%w: %s is %s", ErrTaskFinished, taskID, task.Status.State)
	}
	now := bus.clock()
	if message != nil {
		// Copy so later mutation by the caller cannot alter stored history.
		copied := *message
		copied.Parts = append([]Part(nil), message.Parts...)
		if copied.MessageID == "" {
			copied.MessageID = NewID("msg")
		}
		if copied.Role == "" {
			copied.Role = RoleAgent
		}
		copied.TaskID = task.ID
		copied.ContextID = task.ContextID
		copied.Timestamp = now
		task.History = append(task.History, copied)
		message = &copied
	}
	task.Status = TaskStatus{State: state, Message: message, Timestamp: now}
	snapshot := cloneTask(*task)
	revision := bus.nextRevision(task.ID)
	final := state.Terminal() || state.Interrupted()
	waiters := bus.waiters[taskID]
	if final {
		delete(bus.waiters, taskID)
	}
	bus.mu.Unlock()
	bus.publish(taskID, Event{Kind: EventStatusUpdate, TaskID: taskID, ContextID: snapshot.ContextID, Status: &snapshot.Status, Final: state.Terminal(), Timestamp: now})
	if final {
		for _, waiter := range waiters {
			close(waiter)
		}
	}
	if state.Terminal() {
		bus.closeSubscribers(taskID)
	}
	bus.persist(ctx, snapshot, revision)
	return nil
}

// AddArtifact attaches an output to a task and streams an artifact-update.
func (bus *Bus) AddArtifact(ctx context.Context, taskID string, artifact Artifact) error {
	if artifact.ArtifactID == "" {
		artifact.ArtifactID = NewID("artifact")
	}
	bus.mu.Lock()
	task := bus.tasks[taskID]
	if task == nil {
		bus.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	task.Artifacts = append(task.Artifacts, artifact)
	snapshot := cloneTask(*task)
	revision := bus.nextRevision(task.ID)
	bus.mu.Unlock()
	bus.publish(taskID, Event{Kind: EventArtifactUpdate, TaskID: taskID, ContextID: snapshot.ContextID, Artifact: &artifact, LastChunk: true, Timestamp: bus.clock()})
	bus.persist(ctx, snapshot, revision)
	return nil
}

// Restore puts back a task read from storage that has not ended, so it can
// be continued or ended after the process that started it has gone. A task
// is stored a moment after it changes, so one that was waiting may have been
// stored as still working. A task the bus already holds, or one that has
// ended, is refused.
func (bus *Bus) Restore(task Task) error {
	if task.Status.State.Terminal() {
		return fmt.Errorf("a2a: restore %s: it is %s", task.ID, task.Status.State)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if _, ok := bus.agents[task.Agent]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, task.Agent)
	}
	if bus.tasks[task.ID] != nil {
		return fmt.Errorf("a2a: restore %s: the bus already holds it", task.ID)
	}
	restored := cloneTask(task)
	bus.tasks[task.ID] = &restored
	return nil
}

// GetTask implements tasks/get. historyLength trims the returned history to
// the most recent messages when positive.
func (bus *Bus) GetTask(taskID string, historyLength int) (Task, error) {
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	task := bus.tasks[taskID]
	if task == nil {
		return Task{}, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	snapshot := cloneTask(*task)
	if historyLength > 0 && len(snapshot.History) > historyLength {
		snapshot.History = snapshot.History[len(snapshot.History)-historyLength:]
	}
	return snapshot, nil
}

// CancelTask implements tasks/cancel.
func (bus *Bus) CancelTask(ctx context.Context, taskID string) (Task, error) {
	err := bus.UpdateStatus(ctx, taskID, StateCanceled, &Message{Role: RoleUser, Parts: []Part{TextPart("cancelled by client")}})
	if err != nil && !errors.Is(err, ErrTaskFinished) {
		return Task{}, err
	}
	return bus.GetTask(taskID, 0)
}

// Tasks returns every task in a context (an Inkuna task ID) ordered by the
// time of their first message.
func (bus *Bus) Tasks(contextID string) []Task {
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	result := make([]Task, 0)
	for _, task := range bus.tasks {
		if contextID == "" || task.ContextID == contextID {
			result = append(result, cloneTask(*task))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i].History) == 0 || len(result[j].History) == 0 {
			return result[i].ID < result[j].ID
		}
		return result[i].History[0].Timestamp.Before(result[j].History[0].Timestamp)
	})
	return result
}

// Wait blocks until the task reaches a terminal or interrupted state.
func (bus *Bus) Wait(ctx context.Context, taskID string) (Task, error) {
	bus.mu.Lock()
	task := bus.tasks[taskID]
	if task == nil {
		bus.mu.Unlock()
		return Task{}, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	if task.Status.State.Terminal() || task.Status.State.Interrupted() {
		snapshot := cloneTask(*task)
		bus.mu.Unlock()
		return snapshot, nil
	}
	waiter := make(chan struct{})
	bus.waiters[taskID] = append(bus.waiters[taskID], waiter)
	bus.mu.Unlock()
	select {
	case <-waiter:
		return bus.GetTask(taskID, 0)
	case <-ctx.Done():
		return Task{}, ctx.Err()
	}
}

// WaitTerminal blocks until the task reaches a terminal state, passing
// through any interrupted states. Use it when another party (the inbox) is
// expected to resume the task.
func (bus *Bus) WaitTerminal(ctx context.Context, taskID string) (Task, error) {
	events, cancel, err := bus.Subscribe(taskID)
	if err != nil {
		return Task{}, err
	}
	defer cancel()
	current, err := bus.GetTask(taskID, 0)
	if err != nil {
		return Task{}, err
	}
	if current.Status.State.Terminal() {
		return current, nil
	}
	for {
		select {
		case event, open := <-events:
			if !open {
				return bus.GetTask(taskID, 0)
			}
			if event.Kind == EventStatusUpdate && event.Status != nil && event.Status.State.Terminal() {
				return bus.GetTask(taskID, 0)
			}
		case <-ctx.Done():
			return Task{}, ctx.Err()
		}
	}
}

// Subscribe implements tasks/resubscribe: events for one task until it ends.
// The returned cancel function must be called when the caller stops reading.
func (bus *Bus) Subscribe(taskID string) (<-chan Event, func(), error) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	task := bus.tasks[taskID]
	if task == nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	entry := &subscriber{channel: make(chan Event, 64), done: make(chan struct{})}
	if task.Status.State.Terminal() {
		close(entry.channel)
		return entry.channel, func() {}, nil
	}
	bus.subscribers[taskID] = append(bus.subscribers[taskID], entry)
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			bus.mu.Lock()
			defer bus.mu.Unlock()
			close(entry.done)
			remaining := bus.subscribers[taskID][:0]
			for _, current := range bus.subscribers[taskID] {
				if current != entry {
					remaining = append(remaining, current)
				}
			}
			bus.subscribers[taskID] = remaining
		})
	}
	return entry.channel, cancel, nil
}

// publish delivers an event without blocking. Sends happen under the read
// lock and closeSubscribers closes channels under the write lock, so a send
// can never race a close.
func (bus *Bus) publish(taskID string, event Event) {
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	for _, entry := range bus.subscribers[taskID] {
		select {
		case entry.channel <- event:
		case <-entry.done:
		default:
			bus.onError(fmt.Errorf("a2a: subscriber for %s is not keeping up; event dropped", taskID))
		}
	}
}

func (bus *Bus) closeSubscribers(taskID string) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, entry := range bus.subscribers[taskID] {
		close(entry.channel)
	}
	delete(bus.subscribers, taskID)
}

// nextRevision numbers a mutation of a task. Callers hold bus.mu, so revisions
// follow the order in which snapshots are taken.
func (bus *Bus) nextRevision(taskID string) uint64 {
	bus.revisions[taskID]++
	return bus.revisions[taskID]
}

// persist mirrors a snapshot and then tells the observers. Snapshots are taken
// under bus.mu but saved outside it, so two mutations of one task can reach
// here out of order. Every snapshot is cumulative, so an older one is simply
// dropped once a newer one is durable; it must never overwrite it. The
// observers only hear about changes a reader can actually find in storage,
// and they are told while persistMu is held, so each observer sees a task's
// snapshots one call at a time and in the order they were taken, never an
// older one after a newer.
func (bus *Bus) persist(ctx context.Context, task Task, revision uint64) {
	bus.persistMu.Lock()
	if revision <= bus.saved[task.ID] {
		bus.persistMu.Unlock()
		return
	}
	if bus.persister != nil {
		if err := bus.persister.SaveA2ATask(context.WithoutCancel(ctx), task); err != nil {
			bus.persistMu.Unlock()
			bus.onError(fmt.Errorf("a2a: persist task %s: %w", task.ID, err))
			return
		}
	}
	bus.saved[task.ID] = revision
	defer bus.persistMu.Unlock()
	for _, observer := range bus.observers {
		observer(task)
	}
}

func cloneTask(task Task) Task {
	task.History = append([]Message(nil), task.History...)
	task.Artifacts = append([]Artifact(nil), task.Artifacts...)
	return task
}

// NewID returns a prefixed random identifier.
func NewID(prefix string) string {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buffer[:])
}
