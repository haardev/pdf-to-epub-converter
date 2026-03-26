package rag

import (
	"context"
	"fmt"
	"math"
	"sort"
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

type scoredResult struct {
	result           store.Result
	baseScore        float64
	score            float64
	tokens           map[string]struct{}
	matchedTokens    int
	coverage         float64
	matchedTerms     []string
	contentMatches   int
	titleMatches     int
	captionMatches   int
	sourceMatches    int
	diversityPenalty float64
	breakdown        ScoreBreakdown
}

type ScoreBreakdown struct {
	SemanticSimilarity float64 `json:"semantic_similarity"`
	ContentTokenBoost  float64 `json:"content_token_boost"`
	TitleTokenBoost    float64 `json:"title_token_boost"`
	CaptionTokenBoost  float64 `json:"caption_token_boost"`
	SourceTokenBoost   float64 `json:"source_token_boost"`
	CoverageBoost      float64 `json:"coverage_boost"`
	MetadataBonus      float64 `json:"metadata_bonus"`
	TokenPenalty       float64 `json:"token_penalty"`
}

type RetrievalCandidateTrace struct {
	ID                   int            `json:"id"`
	DocumentID           string         `json:"document_id"`
	Source               string         `json:"source"`
	ChunkIndex           int            `json:"chunk_index"`
	PageNumber           int            `json:"page_number"`
	SectionTitle         string         `json:"section_title"`
	Excerpt              string         `json:"excerpt"`
	BaseScore            float64        `json:"base_score"`
	FinalScore           float64        `json:"final_score"`
	RerankDelta          float64        `json:"rerank_delta"`
	DiversityPenalty     float64        `json:"diversity_penalty"`
	MatchedTokens        int            `json:"matched_tokens"`
	QueryTokenCount      int            `json:"query_token_count"`
	ContentMatches       int            `json:"content_matches"`
	TitleMatches         int            `json:"title_matches"`
	CaptionMatches       int            `json:"caption_matches"`
	SourceMatches        int            `json:"source_matches"`
	Coverage             float64        `json:"coverage"`
	MatchedTerms         []string       `json:"matched_terms"`
	Stage                string         `json:"stage"`
	ReasonSummary        string         `json:"reason_summary"`
	SelectionExplanation string         `json:"selection_explanation"`
	ScoreBreakdown       ScoreBreakdown `json:"score_breakdown"`
}

type RetrievalTrace struct {
	RunID              string                    `json:"run_id"`
	Query              string                    `json:"query"`
	QueryTerms         []string                  `json:"query_terms"`
	SourceScope        string                    `json:"source_scope"`
	PromptVersion      string                    `json:"prompt_version"`
	ConfigVersion      string                    `json:"config_version"`
	RecallK            int                       `json:"recall_k"`
	FinalContextK      int                       `json:"final_context_k"`
	TotalCandidates    int                       `json:"total_candidates"`
	FilteredCandidates int                       `json:"filtered_candidates"`
	SelectedCandidates int                       `json:"selected_candidates"`
	RelevanceFloor     float64                   `json:"relevance_floor"`
	EmbeddingLatencyMs int64                     `json:"embedding_latency_ms"`
	SearchLatencyMs    int64                     `json:"search_latency_ms"`
	RerankLatencyMs    int64                     `json:"rerank_latency_ms"`
	TotalLatencyMs     int64                     `json:"total_latency_ms"`
	Candidates         []RetrievalCandidateTrace `json:"candidates"`
}

type RunMetadata struct {
	RunID          string `json:"run_id"`
	PromptVersion  string `json:"prompt_version"`
	ConfigVersion  string `json:"config_version"`
	SourceScope    string `json:"source_scope"`
	TotalLatencyMs int64  `json:"total_latency_ms"`
}

type rerankSummary struct {
	QueryTerms         []string
	TotalCandidates    int
	FilteredCandidates int
	SelectedCandidates int
	RelevanceFloor     float64
}

// NewSearcher creates a Searcher.
func NewSearcher(client ai.Client, db *store.DB) *Searcher {
	return NewSearcherWithConfig(client, db, SearchConfig{})
}

type SearchConfig struct {
	RecallK       int
	FinalContextK int
	Guardrails    *guardrails.Service
	PromptVersion string
	ConfigVersion string
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

	rerankStartedAt := time.Now()
	selected, candidates, summary := rerankResults(query, aggregated, finalK)
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

func rerankResults(query string, results []store.Result, k int) ([]store.Result, []RetrievalCandidateTrace, rerankSummary) {
	queryTokens := tokenize(query)
	queryTerms := sortedTokens(queryTokens)
	if len(results) == 0 {
		return results, nil, rerankSummary{QueryTerms: queryTerms}
	}
	k = clampFinalK(k, 3)

	scored := make([]scoredResult, 0, len(results))
	for _, result := range results {
		score := result.Score
		titleTokens := tokenize(result.SectionTitle)
		captionTokens := tokenize(strings.Join(result.Captions, " "))
		contentTokens := tokenize(result.Content)
		sourceTokens := tokenize(result.Source)
		allTokens := mergeTokenSets(titleTokens, captionTokens, contentTokens, sourceTokens)

		matchedTokens := sharedTokenCount(queryTokens, allTokens)
		contentMatches := sharedTokenCount(queryTokens, contentTokens)
		titleMatches := sharedTokenCount(queryTokens, titleTokens)
		captionMatches := sharedTokenCount(queryTokens, captionTokens)
		sourceMatches := sharedTokenCount(queryTokens, sourceTokens)

		coverage := 0.0
		if len(queryTokens) > 0 {
			coverage = float64(matchedTokens) / float64(len(queryTokens))
		}

		breakdown := ScoreBreakdown{
			SemanticSimilarity: result.Score,
			ContentTokenBoost:  float64(contentMatches) * 0.02,
			TitleTokenBoost:    float64(titleMatches) * 0.08,
			CaptionTokenBoost:  float64(captionMatches) * 0.05,
			SourceTokenBoost:   float64(sourceMatches) * 0.03,
			CoverageBoost:      coverage * 0.12,
		}

		score += breakdown.ContentTokenBoost
		score += breakdown.TitleTokenBoost
		score += breakdown.CaptionTokenBoost
		score += breakdown.SourceTokenBoost
		score += breakdown.CoverageBoost

		if matchedTokens == 0 {
			breakdown.TokenPenalty -= 0.12
		} else if matchedTokens == 1 && len(queryTokens) >= 4 {
			breakdown.TokenPenalty -= 0.04
		}
		score += breakdown.TokenPenalty

		if result.SectionTitle != "" {
			breakdown.MetadataBonus += 0.01
		}
		if len(result.Captions) > 0 {
			breakdown.MetadataBonus += 0.01
		}
		score += breakdown.MetadataBonus

		scored = append(scored, scoredResult{
			result:         result,
			baseScore:      result.Score,
			score:          score,
			tokens:         allTokens,
			matchedTokens:  matchedTokens,
			coverage:       coverage,
			matchedTerms:   sharedTokens(queryTokens, allTokens),
			contentMatches: contentMatches,
			titleMatches:   titleMatches,
			captionMatches: captionMatches,
			sourceMatches:  sourceMatches,
			breakdown:      breakdown,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	filtered, relevanceFloor, _ := filterRelevantResults(scored, k)
	if len(filtered) == 0 {
		filtered = scored[:minInt(len(scored), k)]
	}

	limit := minInt(len(filtered), k)
	if limit == 0 {
		limit = minInt(len(scored), k)
	}
	selected := selectDiverseResults(filtered, limit)
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].score > selected[j].score
	})

	reranked := make([]store.Result, 0, len(selected))
	for _, item := range selected {
		item.result.Score = item.score
		reranked = append(reranked, item.result)
	}

	return reranked, buildCandidateTrace(scored, filtered, selected, len(queryTokens), relevanceFloor), rerankSummary{
		QueryTerms:         queryTerms,
		TotalCandidates:    len(scored),
		FilteredCandidates: len(filtered),
		SelectedCandidates: len(selected),
		RelevanceFloor:     relevanceFloor,
	}
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

func buildCandidateTrace(scored, filtered, selected []scoredResult, queryTokenCount int, relevanceFloor float64) []RetrievalCandidateTrace {
	filteredSet := make(map[string]struct{}, len(filtered))
	selectedSet := make(map[string]struct{}, len(selected))
	selectedPenalty := make(map[string]float64, len(selected))
	for _, item := range filtered {
		filteredSet[resultKey(item.result)] = struct{}{}
	}
	for _, item := range selected {
		key := resultKey(item.result)
		selectedSet[key] = struct{}{}
		selectedPenalty[key] = item.diversityPenalty
	}

	candidates := make([]RetrievalCandidateTrace, 0, len(scored))
	for _, item := range scored {
		stage := "discarded"
		key := resultKey(item.result)
		if _, ok := selectedSet[key]; ok {
			stage = "selected"
		} else if _, ok := filteredSet[key]; ok {
			stage = "filtered"
		}

		diversityPenalty := selectedPenalty[key]
		if stage != "selected" && len(selected) > 0 {
			diversityPenalty = maxNoveltyPenalty(item, selected)
		}
		reasonSummary, selectionExplanation := explainCandidate(item, stage, diversityPenalty, relevanceFloor)

		candidates = append(candidates, RetrievalCandidateTrace{
			ID:                   item.result.ID,
			DocumentID:           item.result.DocumentID,
			Source:               item.result.Source,
			ChunkIndex:           item.result.ChunkIndex,
			PageNumber:           item.result.PageNumber,
			SectionTitle:         item.result.SectionTitle,
			Excerpt:              trimExcerpt(item.result.Content, 240),
			BaseScore:            item.baseScore,
			FinalScore:           item.score,
			RerankDelta:          item.score - item.baseScore,
			DiversityPenalty:     diversityPenalty,
			MatchedTokens:        item.matchedTokens,
			QueryTokenCount:      queryTokenCount,
			ContentMatches:       item.contentMatches,
			TitleMatches:         item.titleMatches,
			CaptionMatches:       item.captionMatches,
			SourceMatches:        item.sourceMatches,
			Coverage:             item.coverage,
			MatchedTerms:         item.matchedTerms,
			Stage:                stage,
			ReasonSummary:        reasonSummary,
			SelectionExplanation: selectionExplanation,
			ScoreBreakdown:       item.breakdown,
		})
	}

	return candidates
}

func resultKey(result store.Result) string {
	return fmt.Sprintf("%s:%d:%d", result.Source, result.PageNumber, result.ChunkIndex)
}

func durationMs(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}

func (t *RetrievalTrace) Summary() RunMetadata {
	if t == nil {
		return RunMetadata{}
	}

	return RunMetadata{
		RunID:          t.RunID,
		PromptVersion:  t.PromptVersion,
		ConfigVersion:  t.ConfigVersion,
		SourceScope:    t.SourceScope,
		TotalLatencyMs: t.TotalLatencyMs,
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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

func sharedTokens(a, b map[string]struct{}) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	shared := make([]string, 0, len(a))
	for token := range a {
		if _, ok := b[token]; ok {
			shared = append(shared, token)
		}
	}
	sort.Strings(shared)
	return shared
}

func sortedTokens(tokens map[string]struct{}) []string {
	if len(tokens) == 0 {
		return nil
	}

	values := make([]string, 0, len(tokens))
	for token := range tokens {
		values = append(values, token)
	}
	sort.Strings(values)
	return values
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

func filterRelevantResults(scored []scoredResult, desiredK int) ([]scoredResult, float64, int) {
	if len(scored) == 0 {
		return nil, 0, 0
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
	return filtered, relativeFloor, minKeep
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
		bestPenalty := 0.0
		for i, candidate := range remaining {
			penalty := maxNoveltyPenalty(candidate, selected)
			mmrScore := candidate.score - penalty
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
				bestPenalty = penalty
			}
		}

		chosen := remaining[bestIdx]
		chosen.diversityPenalty = bestPenalty
		selected = append(selected, chosen)
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

func explainCandidate(item scoredResult, stage string, diversityPenalty, relevanceFloor float64) (string, string) {
	reasons := make([]string, 0, 4)
	if len(item.matchedTerms) > 0 {
		reasons = append(reasons, fmt.Sprintf("matched terms %s", formatTerms(item.matchedTerms, 4)))
	}
	if item.titleMatches > 0 {
		reasons = append(reasons, fmt.Sprintf("section title matched %d term(s)", item.titleMatches))
	}
	if item.coverage >= 0.5 {
		reasons = append(reasons, fmt.Sprintf("covered %.0f%% of the question terms", item.coverage*100))
	}
	if item.breakdown.TokenPenalty < 0 {
		reasons = append(reasons, "lost points because the overlap was sparse")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "ranked mostly on semantic similarity")
	}

	reasonSummary := strings.Join(reasons, "; ")
	switch stage {
	case "selected":
		selectionExplanation := "Selected for the final answer because it stayed highly relevant after reranking and added evidence worth keeping in the top context set."
		if diversityPenalty > 0.05 {
			selectionExplanation = "Selected for the final answer because it was still strong enough after the diversity check, even though it overlaps with other strong chunks."
		}
		return reasonSummary, selectionExplanation
	case "filtered":
		if diversityPenalty > 0.05 {
			return reasonSummary, "Passed the relevance filter, but the final selector left it out because it overlapped too much with stronger chunks that were already chosen."
		}
		return reasonSummary, "Passed the relevance filter, but another chunk had a better rerank score or gave more useful coverage for the final answer."
	default:
		if item.score < relevanceFloor {
			return reasonSummary, "Dropped before final selection because its rerank score fell below the relevance floor used to trim noisy candidates."
		}
		return reasonSummary, "Dropped before final selection because it did not offer enough additional value compared with the higher-ranked candidates."
	}
}

func formatTerms(terms []string, limit int) string {
	if len(terms) == 0 {
		return ""
	}
	if limit <= 0 || len(terms) <= limit {
		return strings.Join(terms, ", ")
	}
	return strings.Join(terms[:limit], ", ") + fmt.Sprintf(" +%d more", len(terms)-limit)
}

func trimExcerpt(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
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
