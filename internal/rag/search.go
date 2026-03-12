package rag

import (
	"context"
	"fmt"

	"github.com/pdf-rag/internal/ollama"
	"github.com/pdf-rag/internal/store"
)

// Searcher performs semantic search against the vector store.
type Searcher struct {
	ollama *ollama.Client
	db     *store.DB
}

// NewSearcher creates a Searcher.
func NewSearcher(o *ollama.Client, db *store.DB) *Searcher {
	return &Searcher{ollama: o, db: db}
}

// Search embeds the query and returns the top-k most similar chunks.
func (s *Searcher) Search(ctx context.Context, query string, k int) ([]store.Result, error) {
	vec, err := s.ollama.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	results, err := s.db.SearchSimilar(ctx, vec, k)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}
	return results, nil
}
