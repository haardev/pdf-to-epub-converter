package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pdf-rag/internal/ai"
	"github.com/pdf-rag/internal/store"
)

// Searcher performs semantic search against the vector store.
type Searcher struct {
	ai ai.Client
	db *store.DB
}

// NewSearcher creates a Searcher.
func NewSearcher(client ai.Client, db *store.DB) *Searcher {
	return &Searcher{ai: client, db: db}
}

// Search embeds the query and returns the top-k most similar chunks.
func (s *Searcher) Search(ctx context.Context, query string, k int) ([]store.Result, error) {
	vec, err := s.ai.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	recallSize := k * 4
	if recallSize < k {
		recallSize = k
	}

	results, err := s.db.SearchSimilar(ctx, vec, recallSize)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}

	return rerankResults(query, results, k), nil
}

func rerankResults(query string, results []store.Result, k int) []store.Result {
	queryTokens := tokenize(query)
	if len(results) == 0 {
		return results
	}

	type scoredResult struct {
		result store.Result
		score  float64
	}

	scored := make([]scoredResult, 0, len(results))
	for _, result := range results {
		score := result.Score
		titleTokens := tokenize(result.SectionTitle)
		captionTokens := tokenize(strings.Join(result.Captions, " "))
		contentTokens := tokenize(result.Content)

		score += float64(sharedTokenCount(queryTokens, contentTokens)) * 0.02
		score += float64(sharedTokenCount(queryTokens, titleTokens)) * 0.08
		score += float64(sharedTokenCount(queryTokens, captionTokens)) * 0.05

		if result.SectionTitle != "" {
			score += 0.01
		}
		if len(result.Captions) > 0 {
			score += 0.01
		}

		scored = append(scored, scoredResult{result: result, score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	limit := k
	if limit > len(scored) {
		limit = len(scored)
	}

	reranked := make([]store.Result, 0, limit)
	for _, item := range scored[:limit] {
		item.result.Score = item.score
		reranked = append(reranked, item.result)
	}

	return reranked
}

func tokenize(text string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = strings.Trim(token, ".,;:!?()[]{}\"'")
		if len(token) < 3 {
			continue
		}
		tokens[token] = struct{}{}
	}
	return tokens
}

func sharedTokenCount(a, b map[string]struct{}) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	count := 0
	for token := range a {
		if _, ok := b[token]; ok {
			count++
		}
	}
	return count
}
