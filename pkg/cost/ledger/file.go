package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"

	"github.com/gocrane/crane/pkg/cost/model"
)

const snapshotVersion = 1

type snapshot struct {
	Version int                           `json:"version"`
	Items   map[string]model.CostLineItem `json:"items"`
	Cursors map[string]model.BillCursor   `json:"cursors"`
}

// FileStore is a crash-safe, single-process ledger for the cost-only
// deployment. The Store interface lets larger installations replace it with
// PostgreSQL or ClickHouse without coupling cloud adapters to a database.
type FileStore struct {
	mu       sync.RWMutex
	path     string
	snapshot snapshot
}

func OpenFile(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("ledger file path is required")
	}
	store := &FileStore{path: path, snapshot: emptySnapshot()}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("read ledger %s: %w", path, err)
	}
	if len(data) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store.snapshot); err != nil {
		return nil, fmt.Errorf("decode ledger %s: %w", path, err)
	}
	if store.snapshot.Version != snapshotVersion {
		return nil, fmt.Errorf("ledger %s has unsupported version %d", path, store.snapshot.Version)
	}
	if store.snapshot.Items == nil {
		store.snapshot.Items = make(map[string]model.CostLineItem)
	}
	if store.snapshot.Cursors == nil {
		store.snapshot.Cursors = make(map[string]model.BillCursor)
	}
	for key, item := range store.snapshot.Items {
		if err := item.Validate(); err != nil {
			return nil, fmt.Errorf("ledger item %s: %w", key, err)
		}
		if key != item.IdempotencyKey() {
			return nil, fmt.Errorf("ledger item %s has mismatched idempotency key", key)
		}
	}
	return store, nil
}

func emptySnapshot() snapshot {
	return snapshot{
		Version: snapshotVersion,
		Items:   make(map[string]model.CostLineItem),
		Cursors: make(map[string]model.BillCursor),
	}
}

func (s *FileStore) Upsert(ctx context.Context, items []model.CostLineItem) (UpsertResult, error) {
	if err := ctx.Err(); err != nil {
		return UpsertResult{}, err
	}
	for index, item := range items {
		if err := item.Validate(); err != nil {
			return UpsertResult{}, fmt.Errorf("line item %d: %w", index, err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneSnapshot(s.snapshot)
	result := UpsertResult{}
	for _, item := range items {
		key := item.IdempotencyKey()
		item = cloneItem(item)
		current, exists := next.Items[key]
		switch {
		case !exists:
			result.Inserted++
		case reflect.DeepEqual(current, item):
			result.Unchanged++
		default:
			result.Updated++
		}
		next.Items[key] = item
	}
	if result.Inserted == 0 && result.Updated == 0 {
		return result, nil
	}
	if err := s.writeLocked(next); err != nil {
		return UpsertResult{}, err
	}
	s.snapshot = next
	return result, nil
}

func (s *FileStore) Query(ctx context.Context, query Query) ([]model.CostLineItem, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	type keyedItem struct {
		key  string
		item model.CostLineItem
	}
	matches := make([]keyedItem, 0)
	for key, item := range s.snapshot.Items {
		if query.Provider != "" && item.Provider != query.Provider ||
			query.PayerAccount != "" && item.PayerAccountID != query.PayerAccount ||
			query.ClusterID != "" && item.ClusterID != query.ClusterID ||
			query.BillingPeriod != "" && item.BillingPeriod != query.BillingPeriod ||
			query.ResourceID != "" && item.ResourceID != query.ResourceID ||
			query.Category != "" && item.Category != query.Category {
			continue
		}
		matches = append(matches, keyedItem{key: key, item: item})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].item.UsageStart.Equal(matches[j].item.UsageStart) {
			return matches[i].key < matches[j].key
		}
		return matches[i].item.UsageStart.Before(matches[j].item.UsageStart)
	})
	start := query.Offset
	if start > len(matches) {
		start = len(matches)
	}
	end := len(matches)
	if query.Limit > 0 && start+query.Limit < end {
		end = start + query.Limit
	}
	result := make([]model.CostLineItem, 0, end-start)
	for _, match := range matches[start:end] {
		result = append(result, cloneItem(match.item))
	}
	return result, nil
}

func (s *FileStore) LoadCursor(ctx context.Context, key string) (model.BillCursor, bool, error) {
	if err := ctx.Err(); err != nil {
		return model.BillCursor{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cursor, ok := s.snapshot.Cursors[key]
	return cloneCursor(cursor), ok, nil
}

func (s *FileStore) SaveCursor(ctx context.Context, key string, cursor model.BillCursor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("cursor key is required")
	}
	if err := cursor.Period.Validate(); err != nil {
		return fmt.Errorf("cursor period: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneSnapshot(s.snapshot)
	next.Cursors[key] = cloneCursor(cursor)
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.snapshot = next
	return nil
}

func (s *FileStore) writeLocked(value snapshot) error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create ledger directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".cost-ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create ledger temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure ledger temporary file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode ledger: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync ledger: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close ledger: %w", err)
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("replace ledger: %w", err)
	}
	committed = true
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

func cloneSnapshot(source snapshot) snapshot {
	result := emptySnapshot()
	for key, item := range source.Items {
		result.Items[key] = cloneItem(item)
	}
	for key, cursor := range source.Cursors {
		result.Cursors[key] = cloneCursor(cursor)
	}
	return result
}

func cloneItem(item model.CostLineItem) model.CostLineItem {
	item.Tags = cloneStringMap(item.Tags)
	item.Raw = cloneStringMap(item.Raw)
	return item
}

func cloneCursor(cursor model.BillCursor) model.BillCursor {
	cursor.Metadata = cloneStringMap(cursor.Metadata)
	return cursor
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
