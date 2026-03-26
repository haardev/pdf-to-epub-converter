package evals

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Question struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Question    string `json:"question"`
	Source      string `json:"source,omitempty"`
	ShouldBlock bool   `json:"should_block,omitempty"`
}

func LoadQuestions(path string, includeSafety bool) ([]Question, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	questions := make([]Question, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var question Question
		if err := json.Unmarshal([]byte(line), &question); err != nil {
			return nil, err
		}
		if question.ShouldBlock && !includeSafety {
			continue
		}
		questions = append(questions, question)
	}

	return questions, scanner.Err()
}
