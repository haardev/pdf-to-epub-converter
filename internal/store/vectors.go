package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/pgvector/pgvector-go"
)

// Result holds a retrieved document chunk and its similarity score.
type Result struct {
	ID           int      `json:"id"`
	DocumentID   string   `json:"document_id"`
	Source       string   `json:"source"`
	ChunkIndex   int      `json:"chunk_index"`
	PageNumber   int      `json:"page_number"`
	SectionTitle string   `json:"section_title"`
	Captions     []string `json:"captions"`
	Content      string   `json:"content"`
	Score        float64  `json:"score"`
}

// UpsertChunk inserts a new document chunk with its embedding.
// Duplicate (source, chunk_index) pairs are replaced.
func (db *DB) UpsertChunk(ctx context.Context, source, content string, idx int, pageNumber int, sectionTitle string, captions []string, embedding []float32) error {
	const q = `
INSERT INTO documents (document_id, source, chunk_index, page_number, section_title, captions, content, embedding)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (source, chunk_index)
DO UPDATE SET
    document_id = EXCLUDED.document_id,
    page_number = EXCLUDED.page_number,
    section_title = EXCLUDED.section_title,
    captions = EXCLUDED.captions,
    content = EXCLUDED.content,
    embedding = EXCLUDED.embedding`

	if _, err := db.Pool.Exec(ctx, q, source, source, idx, pageNumber, sectionTitle, strings.Join(captions, "\n"), content, pgvector.NewVector(embedding)); err != nil {
		return fmt.Errorf("upsert chunk: %w", err)
	}
	return nil
}

// DeleteSourceChunks removes all chunks for a specific source so re-ingestion
// with new chunking logic does not leave stale records behind.
func (db *DB) DeleteSourceChunks(ctx context.Context, source string) error {
	if _, err := db.Pool.Exec(ctx, `DELETE FROM documents WHERE source = $1`, source); err != nil {
		return fmt.Errorf("delete source chunks: %w", err)
	}
	return nil
}

// SearchSimilar returns the k most similar chunks to the given embedding using
// cosine distance (operator <=>).
func (db *DB) SearchSimilar(ctx context.Context, embedding []float32, k int, source string) ([]Result, error) {
	q := `
SELECT id, document_id, source, chunk_index, page_number, section_title, captions, content,
       1 - (embedding <=> $1) AS score
FROM   documents`
	args := []any{pgvector.NewVector(embedding)}
	if strings.TrimSpace(source) != "" {
		q += ` WHERE document_id = $2`
		args = append(args, source)
		q += ` ORDER BY embedding <=> $1 LIMIT $3`
		args = append(args, k)
	} else {
		q += ` ORDER BY embedding <=> $1 LIMIT $2`
		args = append(args, k)
	}

	rows, err := db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		var captions string
		if err := rows.Scan(&r.ID, &r.DocumentID, &r.Source, &r.ChunkIndex, &r.PageNumber, &r.SectionTitle, &captions, &r.Content, &r.Score); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		if captions != "" {
			r.Captions = strings.Split(captions, "\n")
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (db *DB) ListSources(ctx context.Context) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `SELECT DISTINCT document_id FROM documents ORDER BY document_id`)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	var sources []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sources = append(sources, source)
	}

	return sources, rows.Err()
}
