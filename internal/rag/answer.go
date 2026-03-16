package rag

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/pdf-rag/internal/store"
)

// Answer holds the generated response and the source chunks used as context.
type Answer struct {
	Text       string
	Sources    []store.Result
	Assessment AnswerAssessment
}

type AnswerAssessment struct {
	RiskLevel       string   `json:"risk_level"`
	ImpactLevel     string   `json:"impact_level"`
	EvidenceLevel   string   `json:"evidence_level"`
	RiskScore       int      `json:"risk_score"`
	ConfidenceScore int      `json:"confidence_score"`
	Summary         string   `json:"summary"`
	Reasons         []string `json:"reasons"`
}

// Ask retrieves relevant chunks for the question, builds a RAG prompt, sends
// it to the generative model, and returns the answer with sources.
func (s *Searcher) Ask(ctx context.Context, question string, k int) (*Answer, error) {
	return s.AskWithSource(ctx, question, k, "")
}

func (s *Searcher) AskWithSource(ctx context.Context, question string, k int, source string) (*Answer, error) {
	sources, err := s.SearchWithSource(ctx, question, k, source)
	if err != nil {
		return nil, err
	}

	prompt := buildPrompt(question, sources)

	text, err := s.ai.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("generate answer: %w", err)
	}

	return &Answer{
		Text:       text,
		Sources:    sources,
		Assessment: assessAnswer(question, sources),
	}, nil
}

func buildPrompt(question string, chunks []store.Result) string {
	var sb strings.Builder

	sb.WriteString("You are a helpful assistant that answers questions about insurance policy documents.\n")
	sb.WriteString("Answer ONLY using the context provided below. Do not guess or make up information.\n")
	sb.WriteString("If the context does not contain enough information to answer, say so clearly.\n\n")
	sb.WriteString("Treat each source document as a separate policy document.\n")
	sb.WriteString("Never merge coverage, exclusions, or limits from different source documents into one policy answer.\n")
	sb.WriteString("If multiple source documents are relevant, compare them explicitly and say which document each statement comes from.\n\n")

	sb.WriteString("## Formatting rules\n")
	sb.WriteString("- Write in clear, plain English that anyone can understand — avoid jargon.\n")
	sb.WriteString("- Start with a direct one-sentence answer (yes/no when applicable).\n")
	sb.WriteString("- Use bullet points for lists of conditions, exclusions, or requirements.\n")
	sb.WriteString("- Use **bold** to highlight key terms, amounts, and important limits.\n")
	sb.WriteString("- Cite every factual statement inline using this exact format using the real filename from the context, e.g. (flex-plus.pdf, page 5).\n")
	sb.WriteString("- Do not add labels like 'source:' or 'policy:' inside citations.\n")
	sb.WriteString("- If more than one policy document is relevant, keep the answer grouped by source document so the user can tell them apart.\n")
	sb.WriteString("- End with a short summary or any important caveats to be aware of.\n\n")

	sb.WriteString("## Context from the document\n\n")
	for i, c := range chunks {
		fmt.Fprintf(&sb, "### Excerpt %d — %s, page %d", i+1, c.Source, c.PageNumber)
		if c.SectionTitle != "" {
			fmt.Fprintf(&sb, " (%s)", c.SectionTitle)
		}
		sb.WriteString("\n")
		if len(c.Captions) > 0 {
			fmt.Fprintf(&sb, "_Captions: %s_\n", strings.Join(c.Captions, " | "))
		}
		fmt.Fprintf(&sb, "%s\n\n", c.Content)
	}

	sb.WriteString("## Question\n\n")
	sb.WriteString(question)
	sb.WriteString("\n\n## Answer\n\n")
	return sb.String()
}

func assessAnswer(question string, sources []store.Result) AnswerAssessment {
	impactLevel, impactScore, impactReasons := assessImpact(question, sources)
	evidenceLevel, confidenceScore, evidenceReasons := assessEvidence(sources)

	uncertaintyScore := 100 - confidenceScore
	riskScore := clampInt(int(math.Round(float64(impactScore)*0.65+float64(uncertaintyScore)*0.35)), 0, 100)
	riskLevel := "low"
	if riskScore >= 62 {
		riskLevel = "high"
	} else if riskScore >= 45 {
		riskLevel = "medium"
	}

	reasons := append([]string{}, impactReasons...)
	reasons = append(reasons, evidenceReasons...)

	if len(reasons) > 3 {
		reasons = reasons[:3]
	}

	return AnswerAssessment{
		RiskLevel:       riskLevel,
		ImpactLevel:     impactLevel,
		EvidenceLevel:   evidenceLevel,
		RiskScore:       riskScore,
		ConfidenceScore: confidenceScore,
		Summary:         assessmentSummary(riskLevel, evidenceLevel),
		Reasons:         reasons,
	}
}

func assessImpact(question string, sources []store.Result) (string, int, []string) {
	q := strings.ToLower(question)
	intentLabel, intentScore, intentReason := detectQuestionIntent(q)
	topicLabel, topicScore, topicReason := detectPolicyTopic(q, sources)

	conditionalBoost := 0
	if strings.Contains(q, " if ") || strings.Contains(q, " when ") || strings.Contains(q, " unless ") || strings.Contains(q, " under what") {
		conditionalBoost = 5
	}

	impactScore := clampInt(intentScore+topicScore+conditionalBoost, 24, 94)
	impactLevel := "low"
	if impactScore >= 68 {
		impactLevel = "high"
	} else if impactScore >= 45 {
		impactLevel = "medium"
	}

	reasons := []string{
		fmt.Sprintf("This is a %s question about %s, which can materially affect what the policy pays or allows.", intentLabel, topicLabel),
	}
	if intentReason != "" {
		reasons = append(reasons, intentReason)
	}
	if topicReason != "" {
		reasons = append(reasons, topicReason)
	}
	if conditionalBoost > 0 {
		reasons = append(reasons, "The question depends on conditions or exceptions, which makes mistakes more consequential.")
	}

	return impactLevel, impactScore, reasons
}

func assessEvidence(sources []store.Result) (string, int, []string) {
	if len(sources) == 0 {
		return "weak", 15, []string{"The answer is backed by little or no retrieved policy evidence."}
	}

	sampleSize := min(len(sources), 4)
	topScore := sources[0].Score
	minScore := sources[0].Score
	total := 0.0
	uniqueSources := map[string]struct{}{}
	uniquePages := map[string]struct{}{}
	for _, source := range sources {
		uniqueSources[source.Source] = struct{}{}
		pageKey := fmt.Sprintf("%s:%d", source.Source, source.PageNumber)
		uniquePages[pageKey] = struct{}{}
	}
	for _, source := range sources[:sampleSize] {
		total += source.Score
		if source.Score < minScore {
			minScore = source.Score
		}
		uniqueSources[source.Source] = struct{}{}
	}
	avgScore := total / float64(sampleSize)
	spread := topScore - minScore

	scoreStrength := 0.5*normalizeRange(topScore, 0.45, 1.05) +
		0.35*normalizeRange(avgScore, 0.4, 0.95) +
		0.15*normalizeRange(minScore, 0.35, 0.9)
	consistency := 1 - normalizeRange(spread, 0.03, 0.22)
	quantity := normalizeRange(float64(sampleSize), 1, 4)
	sourcePenalty := normalizeRange(float64(len(uniqueSources)), 1, 3) * 12

	confidence := int(math.Round(28 + scoreStrength*42 + consistency*18 + quantity*12 - sourcePenalty))
	if len(uniquePages) >= 3 {
		confidence += 4
	}
	confidence = clampInt(confidence, 12, 96)

	level := "moderate"
	reasons := []string{
		fmt.Sprintf("This answer is based on %d retrieved excerpt(s) across %d policy document(s).", sampleSize, len(uniqueSources)),
	}

	switch {
	case confidence >= 78:
		level = "strong"
		reasons = append(reasons, fmt.Sprintf("The retrieved excerpts are strong and consistent (top %.2f, average %.2f).", topScore, avgScore))
	case confidence >= 52:
		level = "moderate"
		reasons = append(reasons, fmt.Sprintf("The evidence is usable but mixed in strength (top %.2f, average %.2f).", topScore, avgScore))
	default:
		level = "weak"
		reasons = append(reasons, fmt.Sprintf("The retrieved evidence is limited or uneven (top %.2f, average %.2f).", topScore, avgScore))
	}

	if len(uniqueSources) > 1 {
		reasons = append(reasons, fmt.Sprintf("Evidence came from %d policy documents, which increases the chance of cross-policy confusion.", len(uniqueSources)))
	} else if len(uniquePages) >= 2 {
		reasons = append(reasons, fmt.Sprintf("The answer is supported by multiple pages from the same policy (%d distinct pages).", len(uniquePages)))
	}

	return level, clampInt(confidence, 0, 100), reasons
}

func assessmentSummary(riskLevel, evidenceLevel string) string {
	switch {
	case riskLevel == "high" && evidenceLevel == "weak":
		return "High-impact answer with weak evidence. Verify the cited policy pages carefully before relying on it."
	case riskLevel == "high":
		return "High-impact answer. Use the cited policy pages to verify the final decision."
	case evidenceLevel == "weak":
		return "Evidence is limited, so treat this answer as directional and confirm the policy wording."
	case riskLevel == "medium":
		return "Useful answer, but it still deserves a quick check against the cited policy wording."
	default:
		return "Lower-risk answer with stronger supporting evidence, though the policy wording still governs."
	}
}

func clampInt(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}
	return v
}

func normalizeRange(value, minValue, maxValue float64) float64 {
	if maxValue <= minValue {
		return 0
	}
	normalized := (value - minValue) / (maxValue - minValue)
	if normalized < 0 {
		return 0
	}
	if normalized > 1 {
		return 1
	}
	return normalized
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func detectQuestionIntent(question string) (string, int, string) {
	switch {
	case containsAny(question, "not covered", "excluded", "exclusion", "eligible", "eligibility", "qualify", "qualifies"):
		return "eligibility and exclusion", 80, "It asks for a coverage boundary or exclusion, where a wrong answer can directly change claim expectations."
	case containsAny(question, "cover", "covered", "insured", "insurance cover", "am i covered", "will it pay", "reimburse", "refund"):
		return "coverage decision", 76, "It asks whether the policy covers an event, which is a high-impact decision point."
	case containsAny(question, "how do i claim", "make a claim", "claim process", "submit a claim", "report a claim"):
		return "claims process", 58, "It asks how to take claim action, so the answer can affect whether the user follows the right process."
	case containsAny(question, "deadline", "how long", "when do i", "by when", "report within", "notify within"):
		return "deadline and timing", 72, "It asks about time-sensitive policy requirements, so getting it wrong can jeopardize a claim."
	case containsAny(question, "documents", "proof", "receipt", "evidence", "paperwork"):
		return "documentation requirement", 50, "It asks what documentation is needed, which matters for claim support but is usually less final than a coverage decision."
	default:
		return "general policy guidance", 34, "It looks more informational than decisional, so the direct downside of an error is lower."
	}
}

func detectPolicyTopic(question string, sources []store.Result) (string, int, string) {
	questionText := strings.ToLower(question)
	sourceParts := make([]string, 0, min(len(sources), 3)*3)
	for _, source := range sources[:min(len(sources), 3)] {
		sourceParts = append(sourceParts, source.SectionTitle, strings.Join(source.Captions, " "), source.Content)
	}
	sourceText := strings.ToLower(strings.Join(sourceParts, " "))

	type topicRule struct {
		label  string
		score  int
		reason string
		terms  []string
	}

	rules := []topicRule{
		{
			label:  "trip cancellation or travel disruption",
			score:  14,
			reason: "Travel disruption and cancellation questions usually affect reimbursement, eligibility, and policy conditions.",
			terms:  []string{"flight cancellation", "cancelled flight", "trip cancellation", "travel delay", "missed departure", "trip delay", "curtailment", "abandonment"},
		},
		{
			label:  "medical or emergency cover",
			score:  14,
			reason: "Medical and emergency sections are high-impact because they can affect urgent care, limits, and exclusions.",
			terms:  []string{"medical", "hospital", "doctor", "emergency", "illness", "injury", "treatment"},
		},
		{
			label:  "theft, baggage, or personal belongings",
			score:  12,
			reason: "Loss and theft questions often determine whether the policy reimburses a significant personal loss.",
			terms:  []string{"stolen", "theft", "lost", "baggage", "luggage", "phone", "mobile", "belongings", "personal possessions"},
		},
		{
			label:  "liability or legal cover",
			score:  14,
			reason: "Liability and legal cover can expose the user to large financial consequences if misunderstood.",
			terms:  []string{"liability", "legal", "lawsuit", "legal expenses", "personal liability"},
		},
		{
			label:  "claims evidence or reporting",
			score:  9,
			reason: "Claims reporting and evidence sections are operationally important because they can affect whether a claim is accepted.",
			terms:  []string{"claim", "report", "documents", "receipt", "proof", "evidence", "notify"},
		},
		{
			label:  "limits, excess, or payout amounts",
			score:  11,
			reason: "Benefit limits and excess wording directly affect what the user may receive or pay.",
			terms:  []string{"limit", "limits", "excess", "deductible", "benefit", "payout", "reimbursement"},
		},
	}

	bestLabel := "general policy wording"
	bestScore := 4
	bestReason := "The topic still affects policy interpretation, but it is less obviously tied to a high-stakes coverage area."
	bestQuestionMatches := 0
	bestTotalMatches := 0

	for _, rule := range rules {
		questionMatches := matchCount(questionText, rule.terms...)
		sourceMatches := matchCount(sourceText, rule.terms...)
		if questionMatches == 0 && sourceMatches == 0 {
			continue
		}

		score := rule.score
		if questionMatches > 0 {
			score += clampInt((questionMatches-1)*3, 0, 6)
			score += clampInt(sourceMatches, 0, 2)
		} else {
			score = maxInt(4, rule.score-5+clampInt(sourceMatches, 0, 2))
		}

		totalMatches := questionMatches + sourceMatches
		if questionMatches > bestQuestionMatches ||
			(questionMatches == bestQuestionMatches && (score > bestScore || (score == bestScore && totalMatches > bestTotalMatches))) {
			bestLabel = rule.label
			bestScore = score
			bestReason = rule.reason
			bestQuestionMatches = questionMatches
			bestTotalMatches = totalMatches
		}
	}

	return bestLabel, bestScore, bestReason
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func matchCount(text string, terms ...string) int {
	count := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			count++
		}
	}
	return count
}
