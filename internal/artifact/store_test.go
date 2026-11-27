package artifact

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreHashesAndReconstructsArtifact(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Put("trace.jsonl", "application/x-ndjson", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put("copy", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Size != 5 {
		t.Fatalf("content addressing failed: %#v %#v", first, second)
	}
	reader, err := store.Open(first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	contents, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "hello" {
		t.Fatalf("got %q", contents)
	}
}

func TestRemoveFreesABlobOnceAndRefusesABadDigest(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put("note", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if freed, err := store.Remove(ref.Digest); err != nil || freed != 5 {
		t.Fatalf("Remove = %d, %v; want 5 bytes", freed, err)
	}
	if _, err := store.Open(ref.Digest); err == nil {
		t.Fatal("a removed blob cannot be opened")
	}
	if freed, err := store.Remove(ref.Digest); err != nil || freed != 0 {
		t.Fatalf("removing it again = %d, %v; want nothing freed", freed, err)
	}
	if _, err := store.Remove("../../etc/passwd"); err == nil {
		t.Fatal("a path is not a digest")
	}
}

func TestUsageCountsBlobsAndTheirBytes(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if count, size, err := store.Usage(); err != nil || count != 0 || size != 0 {
		t.Fatalf("an empty store = %d blobs, %d bytes, %v", count, size, err)
	}
	for _, content := range []string{"hello", "hello", "a longer blob"} {
		if _, err := store.Put("blob", "text/plain", strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	// A blob still being written is not counted.
	if err := os.WriteFile(filepath.Join(store.root, ".incoming-123"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	count, size, err := store.Usage()
	if err != nil || count != 2 || size != int64(len("hello")+len("a longer blob")) {
		t.Fatalf("usage = %d blobs, %d bytes, %v; want 2 blobs, %d bytes", count, size, err, len("hello")+len("a longer blob"))
	}
}
