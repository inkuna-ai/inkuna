package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

type Store struct{ root string }

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact store: %w", err)
	}
	return &Store{root: root}, nil
}

func (s *Store) Put(name, mediaType string, reader io.Reader) (domain.ArtifactRef, error) {
	temporary, err := os.CreateTemp(s.root, ".incoming-*")
	if err != nil {
		return domain.ArtifactRef{}, fmt.Errorf("create artifact: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hash), reader)
	closeErr := temporary.Close()
	if copyErr != nil {
		return domain.ArtifactRef{}, fmt.Errorf("write artifact: %w", copyErr)
	}
	if closeErr != nil {
		return domain.ArtifactRef{}, fmt.Errorf("close artifact: %w", closeErr)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	directory := filepath.Join(s.root, digest[:2])
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return domain.ArtifactRef{}, fmt.Errorf("create artifact shard: %w", err)
	}
	destination := filepath.Join(directory, digest)
	if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporaryName, destination); err != nil {
			return domain.ArtifactRef{}, fmt.Errorf("commit artifact: %w", err)
		}
	} else if err != nil {
		return domain.ArtifactRef{}, fmt.Errorf("inspect artifact: %w", err)
	}
	return domain.ArtifactRef{Digest: digest, MediaType: mediaType, Size: size, Name: name}, nil
}

func (s *Store) Open(digest string) (io.ReadCloser, error) {
	if err := validDigest(digest); err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(s.root, digest[:2], digest))
	if err != nil {
		return nil, fmt.Errorf("open artifact: %w", err)
	}
	return file, nil
}

// Remove deletes a blob and returns how many bytes it freed. A blob that is
// already gone frees nothing and is not an error. Whether anything still
// refers to the blob is the caller's to check: the store does not know.
func (s *Store) Remove(digest string) (int64, error) {
	if err := validDigest(digest); err != nil {
		return 0, err
	}
	path := filepath.Join(s.root, digest[:2], digest)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect artifact: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("remove artifact: %w", err)
	}
	return info.Size(), nil
}

// Usage counts the blobs in the store and the bytes they take. A blob
// still being written is not one yet, so it is left out.
func (s *Store) Usage() (int, int64, error) {
	var count int
	var size int64
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || validDigest(entry.Name()) != nil {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		count++
		size += info.Size()
		return nil
	})
	if err != nil {
		return count, size, fmt.Errorf("measure artifacts: %w", err)
	}
	return count, size, nil
}

func validDigest(digest string) error {
	if len(digest) != sha256.Size*2 {
		return errors.New("invalid artifact digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return errors.New("invalid artifact digest")
	}
	return nil
}
