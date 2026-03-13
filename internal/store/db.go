package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

// DB wraps a pgx connection pool.
type DB struct {
	Pool *pgxpool.Pool
}

// New opens a connection pool to PostgreSQL, ensures the vector extension
// exists, registers the pgvector type, and runs the schema migration.
func New(ctx context.Context, dsn string) (*DB, error) {
	if err := ensureVectorExtension(ctx, dsn); err != nil {
		return nil, err
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}

	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvector.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.NewWithConfig: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	db := &DB{Pool: pool}
	if err := db.migrate(ctx); err != nil {
		return nil, err
	}
	return db, nil
}

func ensureVectorExtension(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect for vector extension setup: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector;`); err != nil {
		return fmt.Errorf("create vector extension: %w", err)
	}

	return nil
}

// Close releases all pool connections.
func (db *DB) Close() {
	db.Pool.Close()
}

const schema = `
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS documents (
    id          SERIAL PRIMARY KEY,
    source      TEXT        NOT NULL,
    chunk_index INT         NOT NULL,
    page_number INT         NOT NULL DEFAULT 0,
    section_title TEXT      NOT NULL DEFAULT '',
    captions    TEXT        NOT NULL DEFAULT '',
    content     TEXT        NOT NULL,
    embedding   vector(1024),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, chunk_index)
);

ALTER TABLE documents ADD COLUMN IF NOT EXISTS page_number INT NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS section_title TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN IF NOT EXISTS captions TEXT NOT NULL DEFAULT '';
`

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.Pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("schema migration: %w", err)
	}
	return db.ensureEmbeddingIndex(ctx)
}

// EnsureEmbeddingDimensions updates the embedding column to the required vector
// size when the table is empty. This is useful when switching providers, e.g.
// from 1024-dim Ollama embeddings to 3072-dim Azure embeddings.
func (db *DB) EnsureEmbeddingDimensions(ctx context.Context, dimensions int) error {
	if dimensions <= 0 {
		return fmt.Errorf("invalid embedding dimensions: %d", dimensions)
	}

	currentDimensions, err := db.embeddingDimensions(ctx)
	if err != nil {
		return err
	}
	if currentDimensions == dimensions {
		return nil
	}

	var rowCount int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM documents`).Scan(&rowCount); err != nil {
		return fmt.Errorf("count documents: %w", err)
	}
	if rowCount > 0 {
		return fmt.Errorf("documents table uses vector(%d) but current model returns %d dimensions; clear the table before switching providers", currentDimensions, dimensions)
	}

	statements := []string{
		`DROP INDEX IF EXISTS documents_embedding_idx;`,
		fmt.Sprintf(`ALTER TABLE documents ALTER COLUMN embedding TYPE vector(%d);`, dimensions),
	}
	for _, stmt := range statements {
		if _, err := db.Pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("update embedding dimensions: %w", err)
		}
	}

	return db.ensureEmbeddingIndex(ctx)
}

func (db *DB) embeddingDimensions(ctx context.Context) (int, error) {
	const q = `
SELECT format_type(a.atttypid, a.atttypmod)
FROM   pg_attribute a
JOIN   pg_class c ON a.attrelid = c.oid
WHERE  c.relname = 'documents'
AND    a.attname = 'embedding'
AND    a.attnum > 0
AND    NOT a.attisdropped`

	var format string
	if err := db.Pool.QueryRow(ctx, q).Scan(&format); err != nil {
		return 0, fmt.Errorf("query embedding dimensions: %w", err)
	}

	start := strings.Index(format, "(")
	end := strings.Index(format, ")")
	if start == -1 || end == -1 || end <= start+1 {
		return 0, fmt.Errorf("unexpected embedding type format: %s", format)
	}

	dimensions, err := strconv.Atoi(format[start+1 : end])
	if err != nil {
		return 0, fmt.Errorf("parse embedding dimensions from %q: %w", format, err)
	}
	return dimensions, nil
}

func (db *DB) ensureEmbeddingIndex(ctx context.Context) error {
	dimensions, err := db.embeddingDimensions(ctx)
	if err != nil {
		return err
	}

	if _, err := db.Pool.Exec(ctx, `DROP INDEX IF EXISTS documents_embedding_idx;`); err != nil {
		return fmt.Errorf("drop embedding index: %w", err)
	}

	if dimensions > 2000 {
		return nil
	}

	const createIndex = `
CREATE INDEX IF NOT EXISTS documents_embedding_idx
    ON documents
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);`
	if _, err := db.Pool.Exec(ctx, createIndex); err != nil {
		return fmt.Errorf("create embedding index: %w", err)
	}

	return nil
}
