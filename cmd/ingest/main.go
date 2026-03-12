package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/pdf-rag/internal/ollama"
	pdfpkg "github.com/pdf-rag/internal/pdf"
	"github.com/pdf-rag/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: ingest <path/to/file.pdf>")
		os.Exit(1)
	}
	pdfPath := os.Args[1]

	_ = godotenv.Load()

	dbURL := getEnv("DB_URL", "postgres://rag:rag@localhost:5432/ragdb")
	ollamaURL := getEnv("OLLAMA_URL", "http://localhost:11434")
	embedModel := getEnv("EMBED_MODEL", "mxbai-embed-large")
	genModel := getEnv("GEN_MODEL", "llama3")
	chunkSize := getEnvInt("CHUNK_SIZE", 120)
	chunkOverlap := getEnvInt("CHUNK_OVERLAP", 24)

	ctx := context.Background()

	log.Printf("connecting to postgres...")
	db, err := store.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("store.New: %v", err)
	}
	defer db.Close()

	ollamaClient := ollama.New(ollamaURL, embedModel, genModel)

	log.Printf("extracting text from %s...", pdfPath)
	text, err := pdfpkg.ExtractText(pdfPath)
	if err != nil {
		log.Fatalf("ExtractText: %v", err)
	}
	log.Printf("extracted %d characters", len(text))

	chunks := pdfpkg.ChunkText(text, chunkSize, chunkOverlap)
	log.Printf("produced %d chunks", len(chunks))

	source := filepath.Base(pdfPath)
	storedChunks := 0

	for i, chunk := range chunks {
		log.Printf("processing chunk %d/%d...", i+1, len(chunks))
		if err := ingestChunk(ctx, db, ollamaClient, source, chunk.Text, &storedChunks); err != nil {
			log.Fatalf("process chunk %d: %v", i, err)
		}
	}

	log.Printf("done — ingested %d chunks from %q", storedChunks, source)
}

func ingestChunk(ctx context.Context, db *store.DB, ollamaClient *ollama.Client, source, text string, chunkIndex *int) error {
	vec, err := ollamaClient.Embed(text)
	if err != nil {
		if isContextLengthError(err) {
			left, right, splitErr := splitTextInHalf(text)
			if splitErr != nil {
				return fmt.Errorf("chunk too long for embedding and could not be split further: %w", err)
			}

			log.Printf("chunk exceeded embedding context; splitting into smaller pieces")
			if err := ingestChunk(ctx, db, ollamaClient, source, left, chunkIndex); err != nil {
				return err
			}
			return ingestChunk(ctx, db, ollamaClient, source, right, chunkIndex)
		}

		return fmt.Errorf("embed chunk %d: %w", *chunkIndex, err)
	}

	if err := db.UpsertChunk(ctx, source, text, *chunkIndex, vec); err != nil {
		return fmt.Errorf("upsert chunk %d: %w", *chunkIndex, err)
	}

	*chunkIndex = *chunkIndex + 1
	return nil
}

func splitTextInHalf(text string) (string, string, error) {
	words := strings.Fields(text)
	if len(words) < 2 {
		return "", "", fmt.Errorf("chunk contains fewer than 2 words")
	}

	mid := len(words) / 2
	left := strings.Join(words[:mid], " ")
	right := strings.Join(words[mid:], " ")
	return left, right, nil
}

func isContextLengthError(err error) bool {
	return strings.Contains(err.Error(), "input length exceeds the context length")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var parsed int
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
