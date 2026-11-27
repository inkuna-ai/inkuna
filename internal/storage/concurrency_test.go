package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/a2a"
)

// AddA2AMessage reads its task and then writes. Writers on other
// connections commit in between, which fails a transaction that took no
// write lock up front with SQLITE_BUSY however long the busy timeout is.
func TestConcurrentWritersThatReadFirstAllSucceed(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.SaveA2ATask(ctx, a2a.Task{ID: "worker", ContextID: "task", Agent: "implementer", Status: a2a.TaskStatus{State: a2a.StateWorking}}); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errs := make(chan error, 8*25)
	for writer := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for message := range 25 {
				text := fmt.Sprintf("writer %d message %d", writer, message)
				errs <- store.AddA2AMessage(ctx, "worker", a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart(text)}})
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
