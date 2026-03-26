package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/pdf-rag/internal/ai"
	"github.com/pdf-rag/internal/guardrails"
	"github.com/pdf-rag/internal/rag"
	"github.com/pdf-rag/internal/store"
	"github.com/pdf-rag/internal/ui"
)

func main() {
	_ = godotenv.Load()

	dbURL := getEnv("DB_URL", "postgres://rag:rag@localhost:5432/ragdb")
	docsDir := getEnv("DOCS_DIR", ".")
	topK := normalizeTopK(getEnvInt("TOP_K", 3))
	recallK := getEnvInt("RECALL_K", 20)
	port := getEnv("PORT", "8080")

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

	log.Printf("connecting to postgres...")
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

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Get("/sources", func(w http.ResponseWriter, req *http.Request) {
		sources, err := db.ListSources(req.Context())
		if err != nil {
			log.Printf("sources error: %v", err)
			http.Error(w, `{"error":"sources failed"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
	})

	// GET /search?q=<query>&k=5
	r.Get("/search", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query().Get("q")
		if q == "" {
			http.Error(w, `{"error":"q is required"}`, http.StatusBadRequest)
			return
		}
		k := topK
		if ks := req.URL.Query().Get("k"); ks != "" {
			if parsed, err := strconv.Atoi(ks); err == nil && parsed > 0 {
				k = normalizeTopK(parsed)
			}
		}
		source := strings.TrimSpace(req.URL.Query().Get("source"))

		results, err := searcher.SearchWithSource(req.Context(), q, k, source)
		if err != nil {
			writeAppError(w, "search", err)
			return
		}
		writeJSON(w, http.StatusOK, results)
	})

	// POST /chat   {"question":"..."}
	r.Post("/chat", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Question string `json:"question"`
			Source   string `json:"source"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Question == "" {
			http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
			return
		}

		answer, err := searcher.AskWithSource(req.Context(), body.Question, topK, strings.TrimSpace(body.Source))
		if err != nil {
			writeAppError(w, "chat", err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"answer":     answer.Text,
			"sources":    answer.Sources,
			"assessment": answer.Assessment,
		})
	})

	r.Get("/documents", func(w http.ResponseWriter, req *http.Request) {
		source := strings.TrimSpace(req.URL.Query().Get("source"))
		if source == "" {
			http.Error(w, `{"error":"source is required"}`, http.StatusBadRequest)
			return
		}

		documentPath, err := resolveDocumentPath(docsDir, source)
		if err != nil {
			http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/pdf")
		http.ServeFile(w, req, documentPath)
	})

	// Serve React SPA — must come after API routes
	distFS, err := ui.FS()
	if err != nil {
		log.Fatalf("ui.FS: %v", err)
	}
	fileServer := http.FileServer(http.FS(distFS))
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		// Strip leading slash and fall back to index.html for unknown paths (SPA routing)
		fsPath := strings.TrimPrefix(req.URL.Path, "/")
		if fsPath == "" {
			fsPath = "."
		}
		if _, err := fs.Stat(distFS, fsPath); err != nil {
			req.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, req)
	})

	log.Printf("server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAppError(w http.ResponseWriter, operation string, err error) {
	log.Printf("%s error: %v", operation, err)

	var violation *guardrails.ViolationError
	if errors.As(err, &violation) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": violation.Error(),
			"phase": violation.Phase,
		})
		return
	}

	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": operation + " failed",
	})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
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

func normalizeTopK(value int) int {
	if value <= 0 {
		return 3
	}
	if value > 3 {
		return 3
	}
	return value
}

func resolveDocumentPath(docsDir, source string) (string, error) {
	cleaned := filepath.Clean(source)
	if cleaned == "." || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", os.ErrNotExist
	}

	candidates := []string{
		filepath.Join(docsDir, cleaned),
		filepath.Join(docsDir, filepath.Base(cleaned)),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", os.ErrNotExist
}
