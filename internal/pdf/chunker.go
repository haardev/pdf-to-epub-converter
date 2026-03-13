package pdf

import "strings"

// Chunk holds a single text chunk produced by the chunker.
type Chunk struct {
	Index        int
	Text         string
	PageNumber   int
	SectionTitle string
	Captions     []string
}

// ChunkText splits plain text into overlapping word windows.
func ChunkText(text string, chunkSize, overlap int) []Chunk {
	page := PageContent{Number: 1, Text: text}
	return ChunkPages([]PageContent{page}, chunkSize, overlap)
}

// ChunkPages splits page-aware text into smaller chunks while preserving
// metadata like page number, section title, and captions.
func ChunkPages(pages []PageContent, chunkSize, overlap int) []Chunk {
	if chunkSize <= 0 {
		chunkSize = 90
	}
	if overlap < 0 {
		overlap = 0
	}

	var chunks []Chunk
	nextIndex := 0

	for _, page := range pages {
		paragraphs := splitParagraphs(page)
		if len(paragraphs) == 0 {
			continue
		}

		var current []string
		currentWords := 0

		emit := func() {
			text := strings.TrimSpace(strings.Join(current, "\n\n"))
			if text == "" {
				return
			}

			chunks = append(chunks, Chunk{
				Index:        nextIndex,
				Text:         text,
				PageNumber:   page.Number,
				SectionTitle: page.SectionTitle,
				Captions:     append([]string(nil), page.Captions...),
			})
			nextIndex++
		}

		for _, paragraph := range paragraphs {
			paragraphWords := len(strings.Fields(paragraph))
			if paragraphWords == 0 {
				continue
			}

			if currentWords > 0 && currentWords+paragraphWords > chunkSize {
				emit()

				overlapText := trailingWords(strings.Join(current, " "), overlap)
				current = nil
				currentWords = 0
				if overlapText != "" {
					current = append(current, overlapText)
					currentWords = len(strings.Fields(overlapText))
				}
			}

			current = append(current, paragraph)
			currentWords += paragraphWords
		}

		if currentWords > 0 {
			emit()
		}
	}

	return chunks
}

func splitParagraphs(page PageContent) []string {
	var paragraphs []string

	for _, block := range strings.Split(page.Text, "\n\n") {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" {
			continue
		}
		paragraphs = append(paragraphs, trimmed)
	}

	if len(paragraphs) > 0 {
		return paragraphs
	}

	for _, line := range page.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			paragraphs = append(paragraphs, trimmed)
		}
	}

	return paragraphs
}

func trailingWords(text string, count int) string {
	if count <= 0 {
		return ""
	}

	words := strings.Fields(text)
	if len(words) <= count {
		return strings.Join(words, " ")
	}

	return strings.Join(words[len(words)-count:], " ")
}
