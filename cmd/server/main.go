package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/pdf-rag/internal/ollama"
	"github.com/pdf-rag/internal/rag"
	"github.com/pdf-rag/internal/store"
)

func main() {
	_ = godotenv.Load()

	dbURL := getEnv("DB_URL", "postgres://rag:rag@localhost:5432/ragdb")
	ollamaURL := getEnv("OLLAMA_URL", "http://localhost:11434")
	embedModel := getEnv("EMBED_MODEL", "mxbai-embed-large")
	genModel := getEnv("GEN_MODEL", "llama3")
	topK := getEnvInt("TOP_K", 5)
	port := getEnv("PORT", "8080")

	ctx := context.Background()

	log.Printf("connecting to postgres...")
	db, err := store.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("store.New: %v", err)
	}
	defer db.Close()

	ollamaClient := ollama.New(ollamaURL, embedModel, genModel)
	searcher := rag.NewSearcher(ollamaClient, db)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
				k = parsed
			}
		}

		results, err := searcher.Search(req.Context(), q, k)
		if err != nil {
			log.Printf("search error: %v", err)
			http.Error(w, `{"error":"search failed"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, results)
	})

	// POST /chat   {"question":"..."}
	r.Post("/chat", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Question string `json:"question"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Question == "" {
			http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
			return
		}

		answer, err := searcher.Ask(req.Context(), body.Question, topK)
		if err != nil {
			log.Printf("chat error: %v", err)
			http.Error(w, `{"error":"chat failed"}`, http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"answer":  answer.Text,
			"sources": answer.Sources,
		})
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
