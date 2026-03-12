package store

import (
	"context"
	"fmt"

	"github.com/pgvector/pgvector-go"
)

// Result holds a retrieved document chunk and its similarity score.
type Result struct {
	ID         int
	Source     string
	ChunkIndex int
	Content    string
	Score      float64
}

// UpsertChunk inserts a new document chunk with its embedding.
// Duplicate (source, chunk_index) pairs are replaced.
func (db *DB) UpsertChunk(ctx context.Context, source, content string, idx int, embedding []float32) error {
	const q = `
INSERT INTO documents (source, chunk_index, content, embedding)
VALUES ($1, $2, $3, $4)
ON CONFLICT (source, chunk_index)
DO UPDATE SET content = EXCLUDED.content, embedding = EXCLUDED.embedding`

	if _, err := db.Pool.Exec(ctx, q, source, idx, content, pgvector.NewVector(embedding)); err != nil {
		return fmt.Errorf("upsert chunk: %w", err)
	}
	return nil
}

// SearchSimilar returns the k most similar chunks to the given embedding using
// cosine distance (operator <=>).
func (db *DB) SearchSimilar(ctx context.Context, embedding []float32, k int) ([]Result, error) {
	const q = `
SELECT id, source, chunk_index, content,
       1 - (embedding <=> $1) AS score
FROM   documents
ORDER  BY embedding <=> $1
LIMIT  $2`

	rows, err := db.Pool.Query(ctx, q, pgvector.NewVector(embedding), k)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.Source, &r.ChunkIndex, &r.Content, &r.Score); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
