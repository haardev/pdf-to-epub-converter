package pdf

import (
	"fmt"
	"strings"
	"unicode"

	ledpdf "github.com/ledongthuc/pdf"
)

// PageContent holds extracted text and lightweight metadata for a single PDF page.
type PageContent struct {
	Number       int
	Text         string
	Lines        []string
	SectionTitle string
	Captions     []string
}

// ExtractText reads all text from the given PDF file path and returns it as a
// single string.
func ExtractText(path string) (string, error) {
	pages, err := ExtractPages(path)
	if err != nil {
		return "", err
	}

	var sections []string
	for _, page := range pages {
		if page.Text != "" {
			sections = append(sections, page.Text)
		}
	}

	return strings.Join(sections, "\n\n"), nil
}

// ExtractPages extracts text and metadata for each page in the PDF.
func ExtractPages(path string) ([]PageContent, error) {
	f, r, err := ledpdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open pdf %q: %w", path, err)
	}
	defer f.Close()

	var pages []PageContent
	for pageNumber := 1; pageNumber <= r.NumPage(); pageNumber++ {
		page := r.Page(pageNumber)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			return nil, fmt.Errorf("get plain text from page %d: %w", pageNumber, err)
		}

		lines, err := extractLines(page)
		if err != nil {
			return nil, fmt.Errorf("extract lines from page %d: %w", pageNumber, err)
		}

		pages = append(pages, PageContent{
			Number:       pageNumber,
			Text:         normalizeWhitespace(text),
			Lines:        lines,
			SectionTitle: detectSectionTitle(lines),
			Captions:     detectCaptions(lines),
		})
	}

	return pages, nil
}

func extractLines(page ledpdf.Page) ([]string, error) {
	rows, err := page.GetTextByRow()
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		var line strings.Builder
		for _, fragment := range row.Content {
			line.WriteString(fragment.S)
		}

		trimmed := normalizeWhitespace(line.String())
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}

	return lines, nil
}

func detectSectionTitle(lines []string) string {
	for _, line := range lines {
		if looksLikeSectionTitle(line) {
			return line
		}
	}
	return ""
}

func detectCaptions(lines []string) []string {
	captions := make([]string, 0, 4)
	for _, line := range lines {
		if looksLikeCaption(line) {
			captions = append(captions, line)
		}
	}
	return captions
}

func looksLikeSectionTitle(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	words := strings.Fields(trimmed)
	if len(words) == 0 || len(words) > 12 {
		return false
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "figure ") || strings.HasPrefix(lower, "fig. ") || strings.HasPrefix(lower, "table ") {
		return false
	}

	letterCount := 0
	upperCount := 0
	for _, r := range trimmed {
		if unicode.IsLetter(r) {
			letterCount++
			if unicode.IsUpper(r) {
				upperCount++
			}
		}
	}

	if letterCount == 0 {
		return false
	}

	if upperCount >= letterCount*2/3 {
		return true
	}

	first := []rune(words[0])[0]
	return unicode.IsDigit(first) || unicode.IsUpper(first)
}

func looksLikeCaption(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(lower, "figure ") ||
		strings.HasPrefix(lower, "fig. ") ||
		strings.HasPrefix(lower, "fig ") ||
		strings.HasPrefix(lower, "table ") ||
		strings.HasPrefix(lower, "diagram ") ||
		strings.HasPrefix(lower, "chart ")
}

func normalizeWhitespace(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := strings.Split(text, "\n")
	clean := make([]string, 0, len(lines))
	blank := false

	for _, line := range lines {
		trimmed := strings.Join(strings.Fields(line), " ")
		if trimmed == "" {
			if !blank {
				clean = append(clean, "")
			}
			blank = true
			continue
		}

		clean = append(clean, trimmed)
		blank = false
	}

	return strings.TrimSpace(strings.Join(clean, "\n"))
}
