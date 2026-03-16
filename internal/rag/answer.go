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

	sb.WriteString("You are a helpful assistant that answers questions about insurance policy documents.\n")
	sb.WriteString("Answer ONLY using the context provided below. Do not guess or make up information.\n")
	sb.WriteString("If the context does not contain enough information to answer, say so clearly.\n\n")

	sb.WriteString("## Formatting rules\n")
	sb.WriteString("- Write in clear, plain English that anyone can understand — avoid jargon.\n")
	sb.WriteString("- Start with a direct one-sentence answer (yes/no when applicable).\n")
	sb.WriteString("- Use bullet points for lists of conditions, exclusions, or requirements.\n")
	sb.WriteString("- Use **bold** to highlight key terms, amounts, and important limits.\n")
	sb.WriteString("- Cite the page number inline where the information comes from, e.g. (page 5).\n")
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
