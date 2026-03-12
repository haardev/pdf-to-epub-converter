package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/pdf-rag/internal/store"
)

// Answer holds the generated response and the source chunks used as context.
type Answer struct {
	Text    string
	Sources []store.Result
}

// Ask retrieves relevant chunks for the question, builds a RAG prompt, sends
// it to the generative model, and returns the answer with sources.
func (s *Searcher) Ask(ctx context.Context, question string, k int) (*Answer, error) {
	sources, err := s.Search(ctx, question, k)
	if err != nil {
		return nil, err
	}

	prompt := buildPrompt(question, sources)

	text, err := s.ollama.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("generate answer: %w", err)
	}

	return &Answer{Text: text, Sources: sources}, nil
}

func buildPrompt(question string, chunks []store.Result) string {
	var sb strings.Builder
	sb.WriteString("You are a helpful assistant. Answer the question below using ONLY the provided context.\n")
	sb.WriteString("If the context does not contain enough information, say so honestly.\n\n")
	sb.WriteString("### Context\n\n")

	for i, c := range chunks {
		fmt.Fprintf(&sb, "[%d] (source: %s, chunk %d)\n%s\n\n", i+1, c.Source, c.ChunkIndex, c.Content)
	}

	sb.WriteString("### Question\n\n")
	sb.WriteString(question)
	sb.WriteString("\n\n### Answer\n\n")
	return sb.String()
}
