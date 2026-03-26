package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pdf-rag/internal/ai"
	"github.com/pdf-rag/internal/guardrails"
	"github.com/pdf-rag/internal/store"
)

// Searcher performs semantic search against the vector store.
type Searcher struct {
	ai            ai.Client
	db            *store.DB
	recallK       int
	finalContextK int
	guardrails    *guardrails.Service
	promptVersion string
	configVersion string
}

type SearchConfig struct {
	RecallK       int
	FinalContextK int
	Guardrails    *guardrails.Service
	PromptVersion string
	ConfigVersion string
}

// NewSearcher creates a Searcher.
func NewSearcher(client ai.Client, db *store.DB) *Searcher {
	return NewSearcherWithConfig(client, db, SearchConfig{})
}

func NewSearcherWithConfig(client ai.Client, db *store.DB, cfg SearchConfig) *Searcher {
	recallK := cfg.RecallK
	if recallK <= 0 {
		recallK = 20
	}

	finalContextK := cfg.FinalContextK
	if finalContextK <= 0 {
		finalContextK = 3
	}
	if finalContextK > 3 {
		finalContextK = 3
	}

	return &Searcher{
		ai:            client,
		db:            db,
		recallK:       recallK,
		finalContextK: finalContextK,
		guardrails:    cfg.Guardrails,
		promptVersion: defaultString(cfg.PromptVersion, "prompt-v1"),
		configVersion: defaultString(cfg.ConfigVersion, "retrieval-v1"),
	}
}

// Search embeds the query and returns the top-k most similar chunks.
func (s *Searcher) Search(ctx context.Context, query string, k int) ([]store.Result, error) {
	return s.SearchWithSource(ctx, query, k, "")
}

// SearchWithSource embeds the query and returns relevant chunks, optionally
// restricted to a single source document.
func (s *Searcher) SearchWithSource(ctx context.Context, query string, k int, source string) ([]store.Result, error) {
	results, _, err := s.SearchWithSourceTrace(ctx, query, k, source)
	return results, err
}

func (s *Searcher) SearchWithSourceTrace(ctx context.Context, query string, k int, source string) ([]store.Result, *RetrievalTrace, error) {
	startedAt := time.Now()
	trace := &RetrievalTrace{
		RunID:         fmt.Sprintf("run-%d", startedAt.UnixNano()),
		Query:         query,
		SourceScope:   strings.TrimSpace(source),
		PromptVersion: s.promptVersion,
		ConfigVersion: s.configVersion,
	}

	if s.guardrails != nil {
		if err := s.guardrails.CheckUserInput(ctx, query); err != nil {
			return nil, trace, err
		}
		toolCall := fmt.Sprintf("semantic_search scope=%q query=%s", strings.TrimSpace(source), query)
		if err := s.guardrails.CheckToolCall(ctx, toolCall); err != nil {
			return nil, trace, err
		}
	}

	embedStartedAt := time.Now()
	vec, err := s.ai.Embed(query)
	if err != nil {
		return nil, trace, fmt.Errorf("embed query: %w", err)
	}
	trace.EmbeddingLatencyMs = durationMs(embedStartedAt)

	finalK := clampFinalK(k, s.finalContextK)
	trace.RecallK = s.recallK
	trace.FinalContextK = finalK

	searchStartedAt := time.Now()
	if strings.TrimSpace(source) != "" {
		results, err := s.db.SearchSimilar(ctx, vec, s.recallK, source)
		if err != nil {
			return nil, trace, fmt.Errorf("similarity search: %w", err)
		}
		trace.SearchLatencyMs = durationMs(searchStartedAt)
		return s.finishTrace(ctx, startedAt, query, finalK, results, trace)
	}

	sources, err := s.db.ListSources(ctx)
	if err != nil {
		return nil, trace, fmt.Errorf("list sources: %w", err)
	}

	var aggregated []store.Result
	for _, documentID := range sources {
		results, searchErr := s.db.SearchSimilar(ctx, vec, s.recallK, documentID)
		if searchErr != nil {
			return nil, trace, fmt.Errorf("similarity search for %s: %w", documentID, searchErr)
		}
		aggregated = append(aggregated, results...)
	}
	trace.SearchLatencyMs = durationMs(searchStartedAt)
	return s.finishTrace(ctx, startedAt, query, finalK, aggregated, trace)
}

func (s *Searcher) finishTrace(ctx context.Context, startedAt time.Time, query string, finalK int, results []store.Result, trace *RetrievalTrace) ([]store.Result, *RetrievalTrace, error) {
	rerankStartedAt := time.Now()
	selected, candidates, summary := rerankResults(query, results, finalK)
	trace.RerankLatencyMs = durationMs(rerankStartedAt)
	trace.QueryTerms = summary.QueryTerms
	trace.TotalCandidates = summary.TotalCandidates
	trace.FilteredCandidates = summary.FilteredCandidates
	trace.SelectedCandidates = summary.SelectedCandidates
	trace.RelevanceFloor = summary.RelevanceFloor
	trace.Candidates = candidates
	trace.TotalLatencyMs = durationMs(startedAt)

	if s.guardrails != nil {
		if err := s.guardrails.CheckToolResponse(ctx, resultContents(selected)); err != nil {
			return nil, trace, err
		}
	}
	return selected, trace, nil
}

func clampFinalK(requested, fallback int) int {
	if requested <= 0 {
		requested = fallback
	}
	if requested <= 0 {
		requested = 3
	}
	if requested > 3 {
		return 3
	}
	return requested
}

func resultContents(results []store.Result) []string {
	documents := make([]string, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Content) == "" {
			continue
		}
		documents = append(documents, result.Content)
	}
	return documents
}

func durationMs(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}
