package notes

import (
	"context"
	"sync"
	"time"
)

// Note is one row in a tenant's notes table.
type Note struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is what the handlers call. The schema argument comes from the tenant
// already resolved on the request context, never from handler input, so a
// store implementation can trust it the same way WithTenantTx does.
type Store interface {
	List(ctx context.Context, schema string) ([]Note, error)
	Create(ctx context.Context, schema, userID, text string) (Note, error)
}

// memStore keeps notes in process memory, keyed by schema. It is the zero-dep
// default so the reference runs without any services, and it deliberately
// mimics the Postgres layout: one bucket per schema, ids assigned per schema,
// so switching to PGStore changes nothing about handler behavior.
type memStore struct {
	mu     sync.Mutex
	notes  map[string][]Note
	nextID map[string]int64
}

func newMemStore() *memStore {
	return &memStore{
		notes:  map[string][]Note{},
		nextID: map[string]int64{},
	}
}

// List returns a copy so callers can never mutate the store through the slice.
func (s *memStore) List(_ context.Context, schema string) ([]Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Note, len(s.notes[schema]))
	copy(out, s.notes[schema])
	return out, nil
}

func (s *memStore) Create(_ context.Context, schema, userID, text string) (Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID[schema]++
	n := Note{
		ID:        s.nextID[schema],
		UserID:    userID,
		Text:      text,
		CreatedAt: time.Now().UTC(),
	}
	s.notes[schema] = append(s.notes[schema], n)
	return n, nil
}
