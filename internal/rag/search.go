package rag

import (
	"context"
	"fmt"
	"math"
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

type scoredResult struct {
	result        store.Result
	score         float64
	tokens        map[string]struct{}
	matchedTokens int
	coverage      float64
}

// NewSearcher creates a Searcher.
func NewSearcher(client ai.Client, db *store.DB) *Searcher {
	return &Searcher{ai: client, db: db}
}

// Search embeds the query and returns the top-k most similar chunks.
func (s *Searcher) Search(ctx context.Context, query string, k int) ([]store.Result, error) {
	return s.SearchWithSource(ctx, query, k, "")
}

// SearchWithSource embeds the query and returns relevant chunks, optionally
// restricted to a single source document.
func (s *Searcher) SearchWithSource(ctx context.Context, query string, k int, source string) ([]store.Result, error) {
	vec, err := s.ai.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	recallSize := maxInt(k*5, 20)

	results, err := s.db.SearchSimilar(ctx, vec, recallSize, source)
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
	if k <= 0 {
		k = 1
	}

	scored := make([]scoredResult, 0, len(results))
	for _, result := range results {
		score := result.Score
		titleTokens := tokenize(result.SectionTitle)
		captionTokens := tokenize(strings.Join(result.Captions, " "))
		contentTokens := tokenize(result.Content)
		sourceTokens := tokenize(result.Source)
		allTokens := mergeTokenSets(titleTokens, captionTokens, contentTokens, sourceTokens)
		matchedTokens := sharedTokenCount(queryTokens, allTokens)
		coverage := 0.0
		if len(queryTokens) > 0 {
			coverage = float64(matchedTokens) / float64(len(queryTokens))
		}

		score += float64(sharedTokenCount(queryTokens, contentTokens)) * 0.02
		score += float64(sharedTokenCount(queryTokens, titleTokens)) * 0.08
		score += float64(sharedTokenCount(queryTokens, captionTokens)) * 0.05
		score += float64(sharedTokenCount(queryTokens, sourceTokens)) * 0.03
		score += coverage * 0.12
		if matchedTokens == 0 {
			score -= 0.12
		} else if matchedTokens == 1 && len(queryTokens) >= 4 {
			score -= 0.04
		}

		if result.SectionTitle != "" {
			score += 0.01
		}
		if len(result.Captions) > 0 {
			score += 0.01
		}

		scored = append(scored, scoredResult{
			result:        result,
			score:         score,
			tokens:        allTokens,
			matchedTokens: matchedTokens,
			coverage:      coverage,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	filtered := filterRelevantResults(scored, k)
	if len(filtered) == 0 {
		filtered = scored[:minInt(len(scored), k)]
	}

	limit := dynamicResultLimit(filtered, k)
	selected := selectDiverseResults(filtered, limit)
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].score > selected[j].score
	})

	reranked := make([]store.Result, 0, len(selected))
	for _, item := range selected {
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

func mergeTokenSets(sets ...map[string]struct{}) map[string]struct{} {
	merged := make(map[string]struct{})
	for _, set := range sets {
		for token := range set {
			merged[token] = struct{}{}
		}
	}
	return merged
}

func filterRelevantResults(scored []scoredResult, desiredK int) []scoredResult {
	if len(scored) == 0 {
		return nil
	}

	topScore := scored[0].score
	relativeFloor := math.Max(topScore*0.93, topScore-0.07)
	minKeep := minInt(len(scored), maxInt(2, minInt(3, desiredK/4+1)))

	filtered := make([]scoredResult, 0, len(scored))
	for i, item := range scored {
		if i < minKeep || (item.score >= relativeFloor && (item.matchedTokens > 0 || item.coverage >= 0.4)) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func dynamicResultLimit(scored []scoredResult, desiredK int) int {
	if len(scored) == 0 {
		return 0
	}

	maxLimit := minInt(len(scored), desiredK)
	if maxLimit == 0 {
		return 0
	}

	limit := minInt(maxLimit, 3)
	topScore := scored[0].score
	for limit < maxLimit {
		next := scored[limit]
		if next.score >= topScore*0.96 || scored[limit-1].score-next.score <= 0.02 {
			limit++
			continue
		}
		break
	}

	return maxInt(1, limit)
}

func selectDiverseResults(candidates []scoredResult, limit int) []scoredResult {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}

	selected := make([]scoredResult, 0, limit)
	remaining := append([]scoredResult(nil), candidates...)
	selected = append(selected, remaining[0])
	remaining = remaining[1:]

	for len(selected) < limit && len(remaining) > 0 {
		bestIdx := 0
		bestScore := math.Inf(-1)
		for i, candidate := range remaining {
			penalty := maxNoveltyPenalty(candidate, selected)
			mmrScore := candidate.score - penalty
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	return selected
}

func maxNoveltyPenalty(candidate scoredResult, selected []scoredResult) float64 {
	maxPenalty := 0.0
	for _, chosen := range selected {
		penalty := tokenOverlap(candidate.tokens, chosen.tokens) * 0.18
		if candidate.result.Source == chosen.result.Source {
			penalty += 0.05
			if candidate.result.SectionTitle != "" && candidate.result.SectionTitle == chosen.result.SectionTitle {
				penalty += 0.08
			}
			if absInt(candidate.result.ChunkIndex-chosen.result.ChunkIndex) <= 1 {
				penalty += 0.06
			}
			if absInt(candidate.result.PageNumber-chosen.result.PageNumber) <= 1 {
				penalty += 0.04
			}
		}
		if penalty > maxPenalty {
			maxPenalty = penalty
		}
	}
	return maxPenalty
}

func tokenOverlap(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	shared := float64(sharedTokenCount(a, b))
	denom := float64(minInt(len(a), len(b)))
	if denom == 0 {
		return 0
	}
	return shared / denom
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
