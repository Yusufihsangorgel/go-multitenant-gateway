package notes

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yusufihsangorgel/go-multitenant-gateway/internal/db"
)

// PGStore runs the notes queries against Postgres. Every call goes through
// db.WithTenantTx, so the SQL stays plain and unqualified: the pinned
// search_path decides which tenant's notes table the statements hit. The table
// itself comes from the embedded migrations, so a registered tenant always has
// it; an unregistered tenant has no schema and the query fails loudly instead
// of reading someone else's rows.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore wraps an already-connected pool. The caller owns the pool's
// lifecycle; the server closes it on shutdown.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (s *PGStore) List(ctx context.Context, schema string) ([]Note, error) {
	notes := []Note{}
	err := db.WithTenantTx(ctx, s.pool, schema, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT id, user_id, text, created_at FROM notes ORDER BY id")
		if err != nil {
			return fmt.Errorf("query notes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var n Note
			if err := rows.Scan(&n.ID, &n.UserID, &n.Text, &n.CreatedAt); err != nil {
				return fmt.Errorf("scan note: %w", err)
			}
			notes = append(notes, n)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read notes: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return notes, nil
}

func (s *PGStore) Create(ctx context.Context, schema, userID, text string) (Note, error) {
	n := Note{UserID: userID, Text: text}
	err := db.WithTenantTx(ctx, s.pool, schema, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"INSERT INTO notes (user_id, text) VALUES ($1, $2) RETURNING id, created_at",
			userID, text).Scan(&n.ID, &n.CreatedAt)
	})
	if err != nil {
		return Note{}, fmt.Errorf("insert note: %w", err)
	}
	return n, nil
}
