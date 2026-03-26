package rag

import (
	"fmt"
	"strings"

	"github.com/pdf-rag/internal/store"
)

type ScoreBreakdown struct {
	SemanticSimilarity float64 `json:"semantic_similarity"`
	ContentTokenBoost  float64 `json:"content_token_boost"`
	TitleTokenBoost    float64 `json:"title_token_boost"`
	CaptionTokenBoost  float64 `json:"caption_token_boost"`
	SourceTokenBoost   float64 `json:"source_token_boost"`
	CoverageBoost      float64 `json:"coverage_boost"`
	DistinctiveBoost   float64 `json:"distinctive_boost"`
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

func explainCandidate(item scoredResult, stage string, diversityPenalty, relevanceFloor float64) (string, string) {
	reasons := make([]string, 0, 4)
	if len(item.matchedTerms) > 0 {
		reasons = append(reasons, fmt.Sprintf("matched terms %s", formatTerms(item.matchedTerms, 4)))
	}
	if item.titleMatches > 0 {
		reasons = append(reasons, fmt.Sprintf("section title matched %d term(s)", item.titleMatches))
	}
	if len(item.distinctiveTerms) > 0 {
		reasons = append(reasons, fmt.Sprintf("matched distinctive terms %s", formatTerms(item.distinctiveTerms, 3)))
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
