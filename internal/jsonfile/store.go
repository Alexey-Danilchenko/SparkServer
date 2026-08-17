package jsonfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type record interface {
	GetID() string
}

// store persists one JSON document per record using the record ID as filename.
// It stays private; feature-specific repositories expose the consumer contracts.
type store[T record] struct {
	directory string
	notFound  error
	conflict  error
	mutex     sync.RWMutex
}

func newStore[T record](directory string, notFound error, conflict error) *store[T] {
	return &store[T]{directory: directory, notFound: notFound, conflict: conflict}
}

// Create writes a new record and fails if the ID already exists.
func (store *store[T]) Create(ctx context.Context, record *T) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()

	path, err := store.path((*record).GetID())
	if err != nil {
		return err
	}

	if _, err := os.Stat(path); err == nil {
		return store.conflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return store.writeFile(path, record)
}

func (store *store[T]) GetByID(ctx context.Context, id string) (*T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	store.mutex.RLock()
	defer store.mutex.RUnlock()

	path, err := store.path(id)
	if err != nil {
		return nil, err
	}

	return store.readFile(path)
}

// Save atomically replaces or creates a JSON record.
func (store *store[T]) Save(ctx context.Context, record *T) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()

	path, err := store.path((*record).GetID())
	if err != nil {
		return err
	}

	return store.writeFile(path, record)
}

func (store *store[T]) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()

	path, err := store.path(id)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store.notFound
		}
		return err
	}

	return nil
}

// List returns records sorted by filename for deterministic API/test behavior.
func (store *store[T]) List(ctx context.Context) ([]T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	store.mutex.RLock()
	defer store.mutex.RUnlock()

	entries, err := os.ReadDir(store.directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []T{}, nil
		}
		return nil, err
	}

	sort.Slice(entries, func(left int, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})

	records := make([]T, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		record, err := store.readFile(filepath.Join(store.directory, entry.Name()))
		if err != nil {
			return nil, err
		}

		records = append(records, *record)
	}

	return records, nil
}

func (store *store[T]) path(id string) (string, error) {
	if err := validateID(id); err != nil {
		return "", err
	}

	return filepath.Join(store.directory, id+".json"), nil
}

func (store *store[T]) readFile(path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, store.notFound
		}
		return nil, err
	}

	var record T
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	return &record, nil
}

func (store *store[T]) writeFile(path string, record *T) error {
	if err := os.MkdirAll(store.directory, 0o755); err != nil {
		return err
	}

	// Write-then-rename keeps each JSON document from being partially written.
	tempFile, err := os.CreateTemp(store.directory, ".tmp-*.json")
	if err != nil {
		return err
	}

	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		_ = tempFile.Close()
		return err
	}

	if err := tempFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tempPath, path); err != nil {
		return err
	}

	cleanup = false
	return nil
}

func validateID(id string) error {
	if id == "" || id == "." || id == ".." {
		return fmt.Errorf("invalid record id %q", id)
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid record id %q", id)
	}
	return nil
}
