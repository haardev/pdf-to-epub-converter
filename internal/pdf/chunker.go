package pdf

import "strings"

// Chunk holds a single text window produced by the chunker.
type Chunk struct {
	Index int
	Text  string
}

// ChunkText splits text into overlapping word windows.
// chunkSize is the number of words per chunk; overlap is the number of words
// shared between consecutive chunks.
func ChunkText(text string, chunkSize, overlap int) []Chunk {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var chunks []Chunk
	step := chunkSize - overlap
	if step <= 0 {
		step = 1
	}

	for start, idx := 0, 0; start < len(words); start, idx = start+step, idx+1 {
		end := start + chunkSize
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, Chunk{
			Index: idx,
			Text:  strings.Join(words[start:end], " "),
		})
		if end == len(words) {
			break
		}
	}

	return chunks
}
