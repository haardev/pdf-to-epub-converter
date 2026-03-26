package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/pdf-rag/internal/ai"
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
	embedDim := getEnvInt("EMBED_DIM", 0)
	chunkSize := getEnvInt("CHUNK_SIZE", 400)
	chunkOverlap := getEnvInt("CHUNK_OVERLAP", 100)

	ctx := context.Background()

	log.Printf("connecting to postgres...")
	db, err := store.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("store.New: %v", err)
	}
	defer db.Close()

	log.Printf("extracting text from %s...", pdfPath)
	pages, err := pdfpkg.ExtractPages(pdfPath)
	if err != nil {
		log.Fatalf("ExtractPages: %v", err)
	}
	totalChars := 0
	for _, page := range pages {
		totalChars += len(page.Text)
	}
	log.Printf("extracted %d characters across %d pages", totalChars, len(pages))

	chunks := pdfpkg.ChunkPages(pages, chunkSize, chunkOverlap)
	log.Printf("produced %d chunks", len(chunks))

	source := filepath.Base(pdfPath)
	if err := db.DeleteSourceChunks(ctx, source); err != nil {
		log.Fatalf("DeleteSourceChunks: %v", err)
	}

	storedChunks := 0

	for i, chunk := range chunks {
		log.Printf("processing chunk %d/%d (page %d)...", i+1, len(chunks), chunk.PageNumber)
		if err := ingestChunk(ctx, db, aiClient, source, chunk, embedDim, &storedChunks); err != nil {
			log.Fatalf("process chunk %d: %v", i, err)
		}
	}

	log.Printf("done — ingested %d chunks from %q", storedChunks, source)
}

func ingestChunk(ctx context.Context, db *store.DB, aiClient ai.Client, source string, chunk pdfpkg.Chunk, embedDim int, chunkIndex *int) error {
	vec, err := aiClient.Embed(chunk.Text)
	if err != nil {
		if isContextLengthError(err) {
			left, right, splitErr := splitChunkInHalf(chunk)
			if splitErr != nil {
				return fmt.Errorf("chunk too long for embedding and could not be split further: %w", err)
			}

			log.Printf("chunk exceeded embedding context; splitting into smaller pieces")
			if err := ingestChunk(ctx, db, aiClient, source, left, embedDim, chunkIndex); err != nil {
				return err
			}
			return ingestChunk(ctx, db, aiClient, source, right, embedDim, chunkIndex)
		}

		return fmt.Errorf("embed chunk %d: %w", *chunkIndex, err)
	}

	if *chunkIndex == 0 {
		requiredDim := len(vec)
		if embedDim > 0 {
			requiredDim = embedDim
		}
		if err := db.EnsureEmbeddingDimensions(ctx, requiredDim); err != nil {
			return err
		}
	}

	if err := db.UpsertChunk(ctx, source, chunk.Text, *chunkIndex, chunk.PageNumber, chunk.SectionTitle, chunk.Captions, vec); err != nil {
		return fmt.Errorf("upsert chunk %d: %w", *chunkIndex, err)
	}

	*chunkIndex = *chunkIndex + 1
	return nil
}

func splitChunkInHalf(chunk pdfpkg.Chunk) (pdfpkg.Chunk, pdfpkg.Chunk, error) {
	words := strings.Fields(chunk.Text)
	if len(words) < 2 {
		return pdfpkg.Chunk{}, pdfpkg.Chunk{}, fmt.Errorf("chunk contains fewer than 2 words")
	}

	mid := len(words) / 2
	left := chunk
	right := chunk
	left.Text = strings.Join(words[:mid], " ")
	right.Text = strings.Join(words[mid:], " ")
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
