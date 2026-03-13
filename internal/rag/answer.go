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

	text, err := s.ai.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("generate answer: %w", err)
	}

	return &Answer{Text: text, Sources: sources}, nil
}

func buildPrompt(question string, chunks []store.Result) string {
	var sb strings.Builder
	sb.WriteString("You are a careful assistant answering questions about a PDF.\n")
	sb.WriteString("Use ONLY the provided context.\n")
	sb.WriteString("If the context is insufficient, say that clearly instead of guessing.\n")
	sb.WriteString("Prefer precise answers grounded in the retrieved text.\n")
	sb.WriteString("When relevant, mention page numbers and section titles.\n")
	sb.WriteString("If figure information appears only in captions, say that explicitly.\n\n")
	sb.WriteString("### Context\n\n")

	for i, c := range chunks {
		fmt.Fprintf(&sb, "[%d] (source: %s, page: %d, chunk: %d", i+1, c.Source, c.PageNumber, c.ChunkIndex)
		if c.SectionTitle != "" {
			fmt.Fprintf(&sb, ", section: %s", c.SectionTitle)
		}
		sb.WriteString(")\n")
		if len(c.Captions) > 0 {
			fmt.Fprintf(&sb, "Captions: %s\n", strings.Join(c.Captions, " | "))
		}
		fmt.Fprintf(&sb, "%s\n\n", c.Content)
	}

	sb.WriteString("### Question\n\n")
	sb.WriteString(question)
	sb.WriteString("\n\n### Answer\n\n")
	sb.WriteString("Answer in a grounded way and cite supporting pages inline, for example '(page 3)'.\n\n")
	return sb.String()
}
