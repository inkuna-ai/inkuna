package a2a

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryPersister struct {
	mu    sync.Mutex
	tasks map[string]Task
}

func (persister *memoryPersister) SaveA2ATask(_ context.Context, task Task) error {
	persister.mu.Lock()
	defer persister.mu.Unlock()
	if persister.tasks == nil {
		persister.tasks = make(map[string]Task)
	}
	persister.tasks[task.ID] = task
	return nil
}

func echoCard(name string) AgentCard {
	return AgentCard{Name: name, Description: "echo", Version: "1", Skills: []AgentSkill{{ID: "echo", Name: "Echo", Description: "repeats input"}}}
}

func TestSendMessageCompletesTaskWithArtifact(t *testing.T) {
	persister := &memoryPersister{}
	bus := NewBus(WithPersister(persister))
	err := bus.RegisterAgent(echoCard("echo"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		return bus.AddArtifact(ctx, task.ID, Artifact{Name: "reply", Parts: []Part{TextPart("echo: " + message.Text())}})
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := bus.SendMessage(ctx, "echo", MessageSendParams{Message: Message{Parts: []Part{TextPart("hello")}}})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status.State != StateSubmitted || task.Agent != "echo" || task.ContextID == "" {
		t.Fatalf("unexpected initial task %+v", task)
	}
	finished, err := bus.Wait(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status.State != StateCompleted || len(finished.Artifacts) != 1 || finished.Artifacts[0].Parts[0].Text != "echo: hello" {
		t.Fatalf("unexpected finished task %+v", finished)
	}
	if len(finished.History) != 1 || finished.History[0].Role != RoleUser {
		t.Fatalf("history should hold the user message, got %+v", finished.History)
	}
	persister.mu.Lock()
	saved := persister.tasks[task.ID]
	persister.mu.Unlock()
	if saved.Status.State != StateCompleted {
		t.Fatalf("persister did not receive the final snapshot: %+v", saved)
	}
	card, ok := bus.Agent("echo")
	if !ok || card.ProtocolVersion != ProtocolVersion || card.URL != "inkuna://agents/echo" {
		t.Fatalf("card defaults were not applied: %+v", card)
	}
}

func TestInputRequiredPausesUntilFollowUpMessage(t *testing.T) {
	bus := NewBus()
	var turns int
	var mu sync.Mutex
	err := bus.RegisterAgent(echoCard("asker"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		mu.Lock()
		turns++
		turn := turns
		mu.Unlock()
		if turn == 1 {
			return bus.UpdateStatus(ctx, task.ID, StateInputRequired, &Message{Parts: []Part{TextPart("Which database?")}})
		}
		if message.Text() != "sqlite" {
			return errors.New("expected the answer to be delivered")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, unsubscribe, err := func() (<-chan Event, func(), error) {
		task, err := bus.SendMessage(ctx, "asker", MessageSendParams{Message: Message{Parts: []Part{TextPart("start")}}})
		if err != nil {
			return nil, nil, err
		}
		return bus.Subscribe(task.ID)
	}()
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	var paused Task
	for event := range events {
		if event.Kind == EventStatusUpdate && event.Status.State == StateInputRequired {
			paused, _ = bus.GetTask(event.TaskID, 0)
			break
		}
	}
	if paused.Status.Message == nil || paused.Status.Message.Text() != "Which database?" {
		t.Fatalf("expected the question in the status message, got %+v", paused.Status)
	}
	if _, err := bus.Wait(ctx, paused.ID); err != nil {
		t.Fatalf("wait must return immediately for interrupted tasks: %v", err)
	}
	_ = bus.RegisterAgent(echoCard("other"), func(context.Context, *Bus, Task, Message) error { return nil })
	if _, err := bus.SendMessage(ctx, "other", MessageSendParams{Message: Message{TaskID: paused.ID, Parts: []Part{TextPart("hijack")}}}); err == nil {
		t.Fatal("a different agent must not be able to resume the task")
	}
	if _, err := bus.SendMessage(ctx, "asker", MessageSendParams{Message: Message{TaskID: paused.ID, ContextID: "ctx-other", Parts: []Part{TextPart("wrong context")}}}); err == nil {
		t.Fatal("a conflicting context id must be rejected")
	}
	resumed, err := bus.SendMessage(ctx, "asker", MessageSendParams{Message: Message{TaskID: paused.ID, Parts: []Part{TextPart("sqlite")}}})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != paused.ID || resumed.ContextID != paused.ContextID {
		t.Fatalf("follow-up must continue the same task: %+v", resumed)
	}
	final, err := bus.WaitTerminal(ctx, paused.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status.State != StateCompleted || len(final.History) != 3 {
		t.Fatalf("unexpected final task %+v", final)
	}
	if _, err := bus.SendMessage(ctx, "asker", MessageSendParams{Message: Message{TaskID: paused.ID, Parts: []Part{TextPart("again")}}}); !errors.Is(err, ErrTaskFinished) {
		t.Fatalf("finished tasks must reject new messages, got %v", err)
	}
}

func TestFollowUpIsRejectedWhileAgentIsWorking(t *testing.T) {
	bus := NewBus()
	release := make(chan struct{})
	started := make(chan struct{})
	_ = bus.RegisterAgent(echoCard("busy"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := bus.SendMessage(ctx, "busy", MessageSendParams{Message: Message{Parts: []Part{TextPart("start")}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler never started")
	}
	if _, err := bus.SendMessage(ctx, "busy", MessageSendParams{Message: Message{TaskID: task.ID, Parts: []Part{TextPart("too early")}}}); !errors.Is(err, ErrTaskBusy) {
		t.Fatalf("a working task must reject follow-ups, got %v", err)
	}
	close(release)
	final, err := bus.WaitTerminal(ctx, task.ID)
	if err != nil || final.Status.State != StateCompleted || len(final.History) != 1 {
		t.Fatalf("unexpected final task %+v %v", final, err)
	}
}

func TestHandlerErrorsAndCancellation(t *testing.T) {
	bus := NewBus()
	block := make(chan struct{})
	_ = bus.RegisterAgent(echoCard("failing"), func(context.Context, *Bus, Task, Message) error {
		return errors.New("boom")
	})
	_ = bus.RegisterAgent(echoCard("slow"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failed, err := bus.SendMessage(ctx, "failing", MessageSendParams{Message: Message{Parts: []Part{TextPart("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	final, _ := bus.Wait(ctx, failed.ID)
	if final.Status.State != StateFailed || final.Status.Message.Text() != "boom" {
		t.Fatalf("expected failure with reason, got %+v", final.Status)
	}
	slow, err := bus.SendMessage(ctx, "slow", MessageSendParams{Message: Message{Parts: []Part{TextPart("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := bus.CancelTask(ctx, slow.ID)
	close(block)
	if err != nil || cancelled.Status.State != StateCanceled {
		t.Fatalf("expected cancellation, got %+v %v", cancelled, err)
	}
	if _, err := bus.SendMessage(ctx, "nobody", MessageSendParams{Message: Message{Parts: []Part{TextPart("x")}}}); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("expected unknown agent, got %v", err)
	}
	tasks := bus.Tasks("")
	if len(tasks) != 2 {
		t.Fatalf("expected two tasks, got %d", len(tasks))
	}
	if len(bus.Agents()) != 2 || bus.Agents()[0].Name != "failing" {
		t.Fatalf("agents must be sorted: %+v", bus.Agents())
	}
}

func TestObserverSeesEveryTaskChange(t *testing.T) {
	var mu sync.Mutex
	states := []TaskState{}
	bus := NewBus(WithObserver(func(task Task) {
		mu.Lock()
		defer mu.Unlock()
		states = append(states, task.Status.State)
	}))
	if err := bus.RegisterAgent(echoCard("echo"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		return bus.AddArtifact(ctx, task.ID, Artifact{Name: "reply", Parts: []Part{TextPart("ok")}})
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := bus.SendMessage(ctx, "echo", MessageSendParams{Message: Message{Parts: []Part{TextPart("hello")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bus.WaitTerminal(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(states) < 2 || states[0] != StateSubmitted || states[len(states)-1] != StateCompleted {
		t.Fatalf("observed states = %v, want submitted ... completed", states)
	}
}

// Snapshots saved in order reach an observer in order, one at a time, even
// when its call for one is slow and the next is saved meanwhile.
func TestAnObserverSeesATasksSnapshotsInOrder(t *testing.T) {
	var mu sync.Mutex
	var seen []int
	running, most := 0, 0
	bus := NewBus(WithObserver(func(task Task) {
		mu.Lock()
		running++
		most = max(most, running)
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		running--
		seen = append(seen, len(task.Artifacts))
		mu.Unlock()
	}))
	started := make(chan struct{})
	if err := bus.RegisterAgent(echoCard("echo"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-started
				_ = bus.AddArtifact(ctx, task.ID, Artifact{Name: "part", Parts: []Part{TextPart("x")}})
			}()
		}
		close(started)
		wg.Wait()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task, err := bus.SendMessage(ctx, "echo", MessageSendParams{Message: Message{Parts: []Part{TextPart("hello")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bus.WaitTerminal(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if most != 1 {
		t.Fatalf("the observer ran %d calls at once, want one at a time", most)
	}
	for index := 1; index < len(seen); index++ {
		if seen[index] < seen[index-1] {
			t.Fatalf("the observer saw %d artifacts after %d: %v", seen[index], seen[index-1], seen)
		}
	}
}

func TestEveryObserverIsToldInTheOrderRegistered(t *testing.T) {
	var mu sync.Mutex
	var told []string
	observe := func(name string) Option {
		return WithObserver(func(task Task) {
			mu.Lock()
			defer mu.Unlock()
			if task.Status.State == StateCompleted {
				told = append(told, name)
			}
		})
	}
	bus := NewBus(observe("view"), observe("audit"))
	if err := bus.RegisterAgent(echoCard("echo"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		return bus.AddArtifact(ctx, task.ID, Artifact{Name: "reply", Parts: []Part{TextPart("ok")}})
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := bus.SendMessage(ctx, "echo", MessageSendParams{Message: Message{Parts: []Part{TextPart("hello")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bus.WaitTerminal(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 2 || told[0] != "view" || told[1] != "audit" {
		t.Fatalf("observers told of completion: %v, want view then audit", told)
	}
}

type failingPersister struct{}

func (failingPersister) SaveA2ATask(context.Context, Task) error {
	return errors.New("disk full")
}

func TestObserverIsNotToldAboutChangesThatWereNotPersisted(t *testing.T) {
	var mu sync.Mutex
	observed, reported := 0, 0
	bus := NewBus(WithPersister(failingPersister{}),
		WithObserver(func(Task) { mu.Lock(); observed++; mu.Unlock() }),
		WithErrorHandler(func(error) { mu.Lock(); reported++; mu.Unlock() }))
	if err := bus.RegisterAgent(echoCard("echo"), func(ctx context.Context, bus *Bus, task Task, message Message) error {
		return bus.AddArtifact(ctx, task.ID, Artifact{Name: "reply", Parts: []Part{TextPart("ok")}})
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := bus.SendMessage(ctx, "echo", MessageSendParams{Message: Message{Parts: []Part{TextPart("hello")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bus.WaitTerminal(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if observed != 0 || reported == 0 {
		t.Fatalf("observed=%d reported=%d; a failed save must be reported and never announced", observed, reported)
	}
}

func TestAnOlderSnapshotNeverOverwritesANewerOne(t *testing.T) {
	persister := &memoryPersister{}
	observed := 0
	bus := NewBus(WithPersister(persister), WithObserver(func(Task) { observed++ }))
	ctx := context.Background()
	older := Task{ID: "task-1", ContextID: "ctx", Agent: "echo", Status: TaskStatus{State: StateWorking}}
	newer := older
	newer.Status.State = StateCompleted
	newer.Artifacts = []Artifact{{ArtifactID: "a1", Name: "reply"}}

	// The newer mutation reaches storage first; the older one arrives late.
	bus.persist(ctx, newer, 2)
	bus.persist(ctx, older, 1)
	bus.persist(ctx, newer, 2)

	persister.mu.Lock()
	stored := persister.tasks["task-1"]
	persister.mu.Unlock()
	if stored.Status.State != StateCompleted || len(stored.Artifacts) != 1 {
		t.Fatalf("stale snapshot overwrote durable state: %#v", stored)
	}
	if observed != 1 {
		t.Fatalf("observer ran %d times, want once for the snapshot that was saved", observed)
	}
	// A genuinely newer snapshot still goes through.
	latest := newer
	latest.Artifacts = append(latest.Artifacts, Artifact{ArtifactID: "a2", Name: "more"})
	bus.persist(ctx, latest, 3)
	persister.mu.Lock()
	defer persister.mu.Unlock()
	if len(persister.tasks["task-1"].Artifacts) != 2 || observed != 2 {
		t.Fatalf("newer snapshot was not saved: %#v observed=%d", persister.tasks["task-1"], observed)
	}
}

func TestAWaitingTaskIsRestoredOnANewBus(t *testing.T) {
	persister := &memoryPersister{}
	ask := func(ctx context.Context, bus *Bus, task Task, message Message) error {
		return bus.UpdateStatus(ctx, task.ID, StateInputRequired, &Message{Parts: []Part{TextPart("Approve?")}})
	}
	before := NewBus(WithPersister(persister))
	if err := before.RegisterAgent(echoCard("human"), ask); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := before.SendMessage(ctx, "human", MessageSendParams{Message: Message{Parts: []Part{TextPart("Review")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := before.Wait(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	persister.mu.Lock()
	stored := persister.tasks[task.ID]
	persister.mu.Unlock()

	// The process that asked has gone; a new bus takes the stored task back.
	after := NewBus(WithPersister(persister))
	if err := after.Restore(stored); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("a task for an agent the bus does not know is refused: %v", err)
	}
	if err := after.RegisterAgent(echoCard("human"), ask); err != nil {
		t.Fatal(err)
	}
	if err := after.Restore(stored); err != nil {
		t.Fatal(err)
	}
	if err := after.Restore(stored); err == nil {
		t.Fatal("a task the bus holds is not restored twice")
	}
	if err := after.UpdateStatus(ctx, task.ID, StateCompleted, &Message{Role: RoleUser, Parts: []Part{TextPart("Approved")}}); err != nil {
		t.Fatal(err)
	}
	persister.mu.Lock()
	final := persister.tasks[task.ID]
	persister.mu.Unlock()
	if final.Status.State != StateCompleted || len(final.History) != len(stored.History)+1 {
		t.Fatalf("the restored task ends where it was stored: %+v", final)
	}
	if err := NewBus().Restore(final); err == nil {
		t.Fatal("a task that has ended is not restored")
	}
	// A task is stored a moment after it changes, so a waiting task can
	// have been stored as still working when its process ended.
	working := stored
	working.Status.State = StateWorking
	again := NewBus()
	if err := again.RegisterAgent(echoCard("human"), ask); err != nil {
		t.Fatal(err)
	}
	if err := again.Restore(working); err != nil {
		t.Fatalf("a task stored before it reported waiting is restored: %v", err)
	}
}
