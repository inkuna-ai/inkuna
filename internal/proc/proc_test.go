package proc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary plays both processes: a parent that starts a child and
// waits, and a child that appends to a file until it is ended. The child
// shares the parent's output, as a test runner shares a check's.
func TestMain(m *testing.M) {
	switch os.Getenv("PROC_TEST_ROLE") {
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "PROC_TEST_ROLE=child")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Println("started")
		_ = child.Wait()
		os.Exit(0)
	case "child":
		for {
			file, err := os.OpenFile(os.Getenv("PROC_TEST_FILE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				_, _ = file.WriteString(".")
				_ = file.Close()
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "leftover":
		leftOver()
	case "inkuna":
		standIn()
	}
	os.Exit(m.Run())
}

// standIn plays an Inkuna that starts a worker with Start, for
// start_windows_test.go: the worker starts a child of its own (leftOver),
// and once both are running the stand-in writes "ready" to PROC_TEST_READY
// and runs until it is ended. The worker writes to PROC_TEST_READY.worker,
// and its child to PROC_TEST_READY.worker.child.
func standIn() {
	ready := os.Getenv("PROC_TEST_READY")
	worker := exec.Command(os.Args[0])
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PROC_TEST_") {
			worker.Env = append(worker.Env, entry)
		}
	}
	worker.Env = append(worker.Env, "PROC_TEST_ROLE=leftover", "PROC_TEST_READY="+ready+".worker", "PROC_TEST_CHILD=1")
	if err := Start(worker); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for size(ready+".worker") == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		os.Exit(2)
	}
	_ = worker.Wait()
	os.Exit(0)
}

// leftOver plays a worker a crashed Inkuna left running, for
// leftover_unix_test.go. It is the test binary, not a shell, because macOS
// hides the environment of its own system binaries. With
// PROC_TEST_IGNORE_TERM it ignores a request to stop. With PROC_TEST_CHILD
// it first starts a child in its process group, configured by its
// PROC_TEST_CHILD_ variables with that prefix shortened to PROC_TEST_, so
// PROC_TEST_CHILD_CHILD starts a grandchild; PROC_TEST_UNMARK drops the mark
// from its environment, and PROC_TEST_MARK sets it. Once all that is set up
// it writes "ready" and its process ID to PROC_TEST_READY, and then runs
// until it is ended, or exits with PROC_TEST_EXIT.
func leftOver() {
	if os.Getenv("PROC_TEST_IGNORE_TERM") != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	ready := os.Getenv("PROC_TEST_READY")
	if os.Getenv("PROC_TEST_CHILD") != "" {
		child := exec.Command(os.Args[0])
		child.Env = []string{"PROC_TEST_ROLE=leftover", "PROC_TEST_READY=" + ready + ".child"}
		mark, marked := os.LookupEnv(InstanceVariable)
		for _, entry := range os.Environ() {
			name, value, _ := strings.Cut(entry, "=")
			switch {
			case strings.HasPrefix(name, "PROC_TEST_CHILD_"):
				child.Env = append(child.Env, "PROC_TEST_"+strings.TrimPrefix(name, "PROC_TEST_CHILD_")+"="+value)
			case strings.HasPrefix(name, "PROC_TEST_"), name == InstanceVariable:
			default:
				child.Env = append(child.Env, entry)
			}
		}
		if value := os.Getenv("PROC_TEST_CHILD_MARK"); value != "" {
			mark, marked = value, true
		}
		if marked && os.Getenv("PROC_TEST_CHILD_UNMARK") == "" {
			child.Env = append(child.Env, Mark(mark))
		}
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		// Reaped when it ends, so it is gone rather than a zombie.
		go func() { _ = child.Wait() }()
		for size(ready+".child") == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if err := os.WriteFile(ready, []byte(fmt.Sprintf("ready %d\n", os.Getpid())), 0o600); err != nil {
		os.Exit(2)
	}
	if os.Getenv("PROC_TEST_EXIT") != "" {
		os.Exit(0)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestCommandEndsEveryProcessItStartedWhenItsContextEnds(t *testing.T) {
	ticks := filepath.Join(t.TempDir(), "ticks")
	ctx, cancel := context.WithCancel(context.Background())
	command := Command(ctx, os.Args[0])
	command.Env = append(os.Environ(), "PROC_TEST_ROLE=parent", "PROC_TEST_FILE="+ticks)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line == "" {
		t.Fatalf("the parent did not start its child: %q, %v", line, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for size(ticks) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if size(ticks) == 0 {
		t.Fatal("the child never ran")
	}

	cancel()
	ended := make(chan error, 1)
	go func() { ended <- command.Wait() }()
	select {
	case err := <-ended:
		// The cancel ended the parent, so it did not exit on its own.
		if err == nil {
			t.Fatal("the cancelled parent exited as though it finished")
		}
	case <-time.After(WaitDelay + 10*time.Second):
		t.Fatal("Wait did not return, though the child holds the output open")
	}
	// Give a child that escaped time to write again.
	time.Sleep(300 * time.Millisecond)
	before := size(ticks)
	time.Sleep(500 * time.Millisecond)
	if after := size(ticks); after != before {
		t.Fatalf("the child is still running after its parent's context ended: %d then %d bytes", before, after)
	}
}

func TestTerminateWithoutAProcessIsNotAnError(t *testing.T) {
	if err := Terminate(exec.Command(os.Args[0])); err != nil {
		t.Fatal(err)
	}
}

func size(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
