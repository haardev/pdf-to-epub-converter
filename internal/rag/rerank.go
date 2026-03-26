package rag

import (
	"math"
	"sort"
	"strings"

	"github.com/pdf-rag/internal/store"
)

type scoredResult struct {
	result                   store.Result
	baseScore                float64
	score                    float64
	tokens                   map[string]struct{}
	matchedTokens            int
	coverage                 float64
	matchedTerms             []string
	contentMatches           int
	titleMatches             int
	captionMatches           int
	sourceMatches            int
	distinctiveMatches       int
	distinctiveTitleMatches  int
	distinctiveSourceMatches int
	distinctiveTerms         []string
	diversityPenalty         float64
	breakdown                ScoreBreakdown
}

type rerankSummary struct {
	QueryTerms         []string
	TotalCandidates    int
	FilteredCandidates int
	SelectedCandidates int
	RelevanceFloor     float64
}

type rerankPolicy struct {
	ContentTokenWeight      float64
	TitleTokenWeight        float64
	CaptionTokenWeight      float64
	SourceTokenWeight       float64
	CoverageWeight          float64
	DistinctiveTokenWeight  float64
	DistinctiveTitleWeight  float64
	DistinctiveSourceWeight float64
	NoTokenPenalty          float64
	SparseTokenPenalty      float64
	NoDistinctivePenalty    float64
	SectionTitleBonus       float64
	CaptionBonus            float64
}

type queryProfile struct {
	Tokens           map[string]struct{}
	Terms            []string
	Distinctive      map[string]struct{}
	DistinctiveTerms []string
}

type resultMatchStats struct {
	AllTokens                map[string]struct{}
	MatchedTokens            int
	MatchedTerms             []string
	ContentMatches           int
	TitleMatches             int
	CaptionMatches           int
	SourceMatches            int
	DistinctiveMatches       int
	DistinctiveTitleMatches  int
	DistinctiveSourceMatches int
	DistinctiveTerms         []string
	Coverage                 float64
}

func rerankResults(query string, results []store.Result, k int) ([]store.Result, []RetrievalCandidateTrace, rerankSummary) {
	policy := defaultRerankPolicy()
	profile := buildQueryProfile(query)
	if len(results) == 0 {
		return results, nil, rerankSummary{QueryTerms: profile.Terms}
	}
	k = clampFinalK(k, 3)

	scored := make([]scoredResult, 0, len(results))
	for _, result := range results {
		stats := collectResultMatchStats(profile, result)
		score, breakdown := scoreResult(policy, profile, result, stats)
		scored = append(scored, scoredResult{
			result:                   result,
			baseScore:                result.Score,
			score:                    score,
			tokens:                   stats.AllTokens,
			matchedTokens:            stats.MatchedTokens,
			coverage:                 stats.Coverage,
			matchedTerms:             stats.MatchedTerms,
			contentMatches:           stats.ContentMatches,
			titleMatches:             stats.TitleMatches,
			captionMatches:           stats.CaptionMatches,
			sourceMatches:            stats.SourceMatches,
			distinctiveMatches:       stats.DistinctiveMatches,
			distinctiveTitleMatches:  stats.DistinctiveTitleMatches,
			distinctiveSourceMatches: stats.DistinctiveSourceMatches,
			distinctiveTerms:         stats.DistinctiveTerms,
			breakdown:                breakdown,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	filtered, relevanceFloor, _ := filterRelevantResults(scored, k, len(profile.Distinctive))
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

	return reranked, buildCandidateTrace(scored, filtered, selected, len(profile.Tokens), relevanceFloor), rerankSummary{
		QueryTerms:         profile.Terms,
		TotalCandidates:    len(scored),
		FilteredCandidates: len(filtered),
		SelectedCandidates: len(selected),
		RelevanceFloor:     relevanceFloor,
	}
}

func defaultRerankPolicy() rerankPolicy {
	return rerankPolicy{
		ContentTokenWeight:      0.02,
		TitleTokenWeight:        0.08,
		CaptionTokenWeight:      0.05,
		SourceTokenWeight:       0.03,
		CoverageWeight:          0.12,
		DistinctiveTokenWeight:  0.08,
		DistinctiveTitleWeight:  0.08,
		DistinctiveSourceWeight: 0.12,
		NoTokenPenalty:          0.12,
		SparseTokenPenalty:      0.04,
		NoDistinctivePenalty:    0.16,
		SectionTitleBonus:       0.01,
		CaptionBonus:            0.01,
	}
}

func buildQueryProfile(query string) queryProfile {
	tokens := tokenize(query)
	distinctive := distinctiveTokens(tokens)
	return queryProfile{
		Tokens:           tokens,
		Terms:            sortedTokens(tokens),
		Distinctive:      distinctive,
		DistinctiveTerms: sortedTokens(distinctive),
	}
}

func collectResultMatchStats(profile queryProfile, result store.Result) resultMatchStats {
	titleTokens := tokenize(result.SectionTitle)
	captionTokens := tokenize(strings.Join(result.Captions, " "))
	contentTokens := tokenize(result.Content)
	sourceTokens := tokenize(result.Source)
	allTokens := mergeTokenSets(titleTokens, captionTokens, contentTokens, sourceTokens)

	matchedTokens := sharedTokenCount(profile.Tokens, allTokens)
	coverage := 0.0
	if len(profile.Tokens) > 0 {
		coverage = float64(matchedTokens) / float64(len(profile.Tokens))
	}

	return resultMatchStats{
		AllTokens:                allTokens,
		MatchedTokens:            matchedTokens,
		MatchedTerms:             sharedTokens(profile.Tokens, allTokens),
		ContentMatches:           sharedTokenCount(profile.Tokens, contentTokens),
		TitleMatches:             sharedTokenCount(profile.Tokens, titleTokens),
		CaptionMatches:           sharedTokenCount(profile.Tokens, captionTokens),
		SourceMatches:            sharedTokenCount(profile.Tokens, sourceTokens),
		DistinctiveMatches:       sharedTokenCount(profile.Distinctive, allTokens),
		DistinctiveTitleMatches:  sharedTokenCount(profile.Distinctive, titleTokens),
		DistinctiveSourceMatches: sharedTokenCount(profile.Distinctive, sourceTokens),
		DistinctiveTerms:         sharedTokens(profile.Distinctive, allTokens),
		Coverage:                 coverage,
	}
}

func scoreResult(policy rerankPolicy, profile queryProfile, result store.Result, stats resultMatchStats) (float64, ScoreBreakdown) {
	breakdown := ScoreBreakdown{
		SemanticSimilarity: result.Score,
		ContentTokenBoost:  float64(stats.ContentMatches) * policy.ContentTokenWeight,
		TitleTokenBoost:    float64(stats.TitleMatches) * policy.TitleTokenWeight,
		CaptionTokenBoost:  float64(stats.CaptionMatches) * policy.CaptionTokenWeight,
		SourceTokenBoost:   float64(stats.SourceMatches) * policy.SourceTokenWeight,
		CoverageBoost:      stats.Coverage * policy.CoverageWeight,
	}
	breakdown.DistinctiveBoost += float64(stats.DistinctiveMatches) * policy.DistinctiveTokenWeight
	breakdown.DistinctiveBoost += float64(stats.DistinctiveTitleMatches) * policy.DistinctiveTitleWeight
	breakdown.DistinctiveBoost += float64(stats.DistinctiveSourceMatches) * policy.DistinctiveSourceWeight

	if stats.MatchedTokens == 0 {
		breakdown.TokenPenalty -= policy.NoTokenPenalty
	} else if stats.MatchedTokens == 1 && len(profile.Tokens) >= 4 {
		breakdown.TokenPenalty -= policy.SparseTokenPenalty
	}
	if len(profile.Distinctive) > 0 && stats.DistinctiveMatches == 0 {
		breakdown.TokenPenalty -= policy.NoDistinctivePenalty
	}

	if result.SectionTitle != "" {
		breakdown.MetadataBonus += policy.SectionTitleBonus
	}
	if len(result.Captions) > 0 {
		breakdown.MetadataBonus += policy.CaptionBonus
	}

	score := result.Score +
		breakdown.ContentTokenBoost +
		breakdown.TitleTokenBoost +
		breakdown.CaptionTokenBoost +
		breakdown.SourceTokenBoost +
		breakdown.CoverageBoost +
		breakdown.DistinctiveBoost +
		breakdown.MetadataBonus +
		breakdown.TokenPenalty

	return score, breakdown
}

func filterRelevantResults(scored []scoredResult, desiredK int, distinctiveQueryCount int) ([]scoredResult, float64, int) {
	if len(scored) == 0 {
		return nil, 0, 0
	}

	topScore := scored[0].score
	relativeFloor := math.Max(topScore*0.93, topScore-0.07)
	minKeep := minInt(len(scored), maxInt(2, minInt(3, desiredK/4+1)))

	filtered := make([]scoredResult, 0, len(scored))
	for i, item := range scored {
		if i < minKeep || (item.score >= relativeFloor &&
			(item.matchedTokens > 0 || item.coverage >= 0.4) &&
			(distinctiveQueryCount == 0 || item.distinctiveMatches > 0)) {
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

func distinctiveTokens(tokens map[string]struct{}) map[string]struct{} {
	if len(tokens) == 0 {
		return nil
	}

	distinctive := make(map[string]struct{})
	for token := range tokens {
		if isGenericQueryToken(token) {
			continue
		}
		distinctive[token] = struct{}{}
	}
	return distinctive
}

func isGenericQueryToken(token string) bool {
	switch token {
	case "about", "account", "against", "and", "are", "bank", "benefit", "benefits", "can", "cover", "covered", "coverage", "does", "family", "for", "from", "have", "how", "if", "insurance", "is", "it", "like", "member", "members", "my", "of", "or", "our", "phone", "plan", "plans", "policy", "service", "the", "their", "this", "what", "when", "where", "which", "who", "with", "your":
		return true
	default:
		return false
	}
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

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
