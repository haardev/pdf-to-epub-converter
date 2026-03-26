package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/pdf-rag/internal/ai"
	"github.com/pdf-rag/internal/guardrails"
	"github.com/pdf-rag/internal/rag"
	"github.com/pdf-rag/internal/store"
)

type evalCase struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Question         string   `json:"question"`
	Source           string   `json:"source"`
	ExpectedSources  []string `json:"expected_sources"`
	ForbiddenSources []string `json:"forbidden_sources"`
	RequireCitation  bool     `json:"require_citation"`
	ShouldBlock      bool     `json:"should_block"`
}

type evalResult struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Passed   bool     `json:"passed"`
	Skipped  bool     `json:"skipped"`
	Blocked  bool     `json:"blocked"`
	Issues   []string `json:"issues,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}

func main() {
	_ = godotenv.Load()

	datasetPath := "evals/policy_eval_set.jsonl"
	if len(os.Args) > 1 {
		datasetPath = os.Args[1]
	}

	cases, err := loadEvalCases(datasetPath)
	if err != nil {
		log.Fatalf("load eval cases: %v", err)
	}

	dbURL := getEnv("DB_URL", "postgres://rag:rag@localhost:5432/ragdb")
	topK := normalizeTopK(getEnvInt("TOP_K", 3))
	recallK := getEnvInt("RECALL_K", 20)

	aiClient, err := ai.New(ai.Config{
		Provider:             getEnv("AI_PROVIDER", "ollama"),
		OllamaURL:            getEnv("OLLAMA_URL", "http://localhost:11434"),
		EmbedModel:           getEnv("EMBED_MODEL", "mxbai-embed-large"),
		GenModel:             getEnv("GEN_MODEL", "llama3"),
		EmbedDimensions:      getEnvInt("EMBED_DIM", 0),
		AzureEndpoint:        getEnv("AZURE_AI_ENDPOINT", ""),
		AzureAPIKey:          getEnv("AZURE_AI_API_KEY", ""),
		AzureAPIVersion:      getEnv("AZURE_AI_API_VERSION", ""),
		AzureEmbedDeployment: getEnv("AZURE_EMBED_DEPLOYMENT", ""),
		AzureChatDeployment:  getEnv("AZURE_CHAT_DEPLOYMENT", ""),
	})
	if err != nil {
		log.Fatalf("ai.New: %v", err)
	}

	ctx := context.Background()
	db, err := store.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("store.New: %v", err)
	}
	defer db.Close()

	guardrailService := guardrails.New(guardrails.Config{
		Enabled:                       getEnvBool("GUARDRAILS_ENABLED", false),
		ContentSafetyEndpoint:         getEnv("CONTENT_SAFETY_ENDPOINT", ""),
		ContentSafetyAPIKey:           getEnv("CONTENT_SAFETY_API_KEY", ""),
		ContentSafetyAPIVersion:       getEnv("CONTENT_SAFETY_API_VERSION", "2024-09-01"),
		Blocklists:                    splitCSV(getEnv("CONTENT_SAFETY_BLOCKLISTS", "")),
		InputSeverityThreshold:        getEnvInt("GUARDRAILS_INPUT_SEVERITY_THRESHOLD", 4),
		ToolCallSeverityThreshold:     getEnvInt("GUARDRAILS_TOOL_CALL_SEVERITY_THRESHOLD", 4),
		OutputSeverityThreshold:       getEnvInt("GUARDRAILS_OUTPUT_SEVERITY_THRESHOLD", 4),
		EnableUserPromptShield:        getEnvBool("GUARDRAILS_USER_PROMPT_SHIELD", true),
		EnableDocumentPromptShield:    getEnvBool("GUARDRAILS_DOCUMENT_PROMPT_SHIELD", true),
		EnableOutputProtectedMaterial: getEnvBool("GUARDRAILS_OUTPUT_PROTECTED_MATERIAL", false),
	})

	searcher := rag.NewSearcherWithConfig(aiClient, db, rag.SearchConfig{
		RecallK:       recallK,
		FinalContextK: topK,
		Guardrails:    guardrailService,
	})

	results := make([]evalResult, 0, len(cases))
	for _, testCase := range cases {
		results = append(results, runEvalCase(ctx, searcher, guardrailService, testCase, topK))
	}

	var passed, failed, skipped int
	for _, result := range results {
		switch {
		case result.Skipped:
			skipped++
		case result.Passed:
			passed++
		default:
			failed++
		}
	}

	categorySummary := map[string]map[string]int{}
	for _, result := range results {
		if _, ok := categorySummary[result.Category]; !ok {
			categorySummary[result.Category] = map[string]int{"passed": 0, "failed": 0, "skipped": 0}
		}
		switch {
		case result.Skipped:
			categorySummary[result.Category]["skipped"]++
		case result.Passed:
			categorySummary[result.Category]["passed"]++
		default:
			categorySummary[result.Category]["failed"]++
		}
	}

	orderedCategories := make([]string, 0, len(categorySummary))
	for category := range categorySummary {
		orderedCategories = append(orderedCategories, category)
	}
	sort.Strings(orderedCategories)

	fmt.Printf("Evaluated %d cases: %d passed, %d failed, %d skipped\n", len(results), passed, failed, skipped)
	for _, category := range orderedCategories {
		stats := categorySummary[category]
		fmt.Printf("- %s: %d passed, %d failed, %d skipped\n", category, stats["passed"], stats["failed"], stats["skipped"])
	}

	if failed > 0 {
		fmt.Println("\nFailing cases:")
		for _, result := range results {
			if result.Passed || result.Skipped {
				continue
			}
			fmt.Printf("- %s: %s\n", result.ID, strings.Join(result.Issues, "; "))
		}
		os.Exit(1)
	}
}

func runEvalCase(ctx context.Context, searcher *rag.Searcher, guardrailService *guardrails.Service, testCase evalCase, topK int) evalResult {
	result := evalResult{
		ID:       testCase.ID,
		Category: testCase.Category,
	}

	answer, err := searcher.AskWithSource(ctx, testCase.Question, topK, strings.TrimSpace(testCase.Source))
	if err != nil {
		var violation *guardrails.ViolationError
		if errors.As(err, &violation) {
			result.Blocked = true
			if testCase.ShouldBlock {
				result.Passed = true
				return result
			}
			result.Issues = []string{violation.Error()}
			return result
		}

		if testCase.ShouldBlock && (guardrailService == nil || !guardrailService.Enabled()) {
			result.Skipped = true
			result.Issues = []string{"guardrails disabled; safety case skipped"}
			return result
		}

		result.Issues = []string{err.Error()}
		return result
	}

	if testCase.ShouldBlock {
		if guardrailService == nil || !guardrailService.Enabled() {
			result.Skipped = true
			result.Issues = []string{"guardrails disabled; safety case skipped"}
			return result
		}
		result.Issues = []string{"expected request to be blocked, but it completed"}
		return result
	}

	result.Sources = uniqueSources(answer.Sources)
	issues := make([]string, 0)
	if testCase.RequireCitation && !citationPattern.MatchString(answer.Text) {
		issues = append(issues, "answer did not include inline citations")
	}
	if strings.TrimSpace(testCase.Source) != "" {
		for _, source := range result.Sources {
			if source != testCase.Source {
				issues = append(issues, fmt.Sprintf("source-scoped query returned %s instead of %s", source, testCase.Source))
			}
		}
	}
	if len(testCase.ExpectedSources) > 0 && !hasAnyExpectedSource(result.Sources, testCase.ExpectedSources) {
		issues = append(issues, "returned sources did not include an expected document")
	}
	if hasForbiddenSource(result.Sources, testCase.ForbiddenSources) {
		issues = append(issues, "returned sources included a forbidden document")
	}
	if len(issues) == 0 {
		result.Passed = true
		return result
	}

	result.Issues = issues
	return result
}

var citationPattern = regexp.MustCompile(`\([^)]+,\s*page\s+\d+\)`)

func uniqueSources(results []store.Result) []string {
	seen := make(map[string]struct{})
	sources := make([]string, 0, len(results))
	for _, result := range results {
		if _, ok := seen[result.Source]; ok {
			continue
		}
		seen[result.Source] = struct{}{}
		sources = append(sources, result.Source)
	}
	sort.Strings(sources)
	return sources
}

func hasAnyExpectedSource(actual, expected []string) bool {
	expectedSet := make(map[string]struct{}, len(expected))
	for _, source := range expected {
		expectedSet[source] = struct{}{}
	}
	for _, source := range actual {
		if _, ok := expectedSet[source]; ok {
			return true
		}
	}
	return false
}

func hasForbiddenSource(actual, forbidden []string) bool {
	forbiddenSet := make(map[string]struct{}, len(forbidden))
	for _, source := range forbidden {
		forbiddenSet[source] = struct{}{}
	}
	for _, source := range actual {
		if _, ok := forbiddenSet[source]; ok {
			return true
		}
	}
	return false
}

func loadEvalCases(path string) ([]evalCase, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cases []evalCase
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var testCase evalCase
		if err := json.Unmarshal([]byte(line), &testCase); err != nil {
			return nil, err
		}
		cases = append(cases, testCase)
	}
	return cases, scanner.Err()
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		var parsed int
		if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if value := strings.TrimSpace(strings.ToLower(os.Getenv(key))); value != "" {
		return value == "1" || value == "true" || value == "yes" || value == "on"
	}
	return fallback
}

func normalizeTopK(value int) int {
	if value <= 0 {
		return 3
	}
	if value > 3 {
		return 3
	}
	return value
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}
