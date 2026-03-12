package pdf

import (
	"bytes"
	"fmt"
	"io"

	"github.com/ledongthuc/pdf"
)

// ExtractText reads all text from the given PDF file path and returns it as a
// single string.
func ExtractText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("open pdf %q: %w", path, err)
	}
	defer f.Close()

	reader, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("get plain text from %q: %w", path, err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		return "", fmt.Errorf("read plain text from %q: %w", path, err)
	}

	return buf.String(), nil
}
