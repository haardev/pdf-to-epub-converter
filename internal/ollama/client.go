package ollama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to a local Ollama instance.
type Client struct {
	baseURL    string
	embedModel string
	genModel   string
	http       *http.Client
}

// New creates a new Ollama client.
func New(baseURL, embedModel, genModel string) *Client {
	return &Client{
		baseURL:    baseURL,
		embedModel: embedModel,
		genModel:   genModel,
		http:       &http.Client{Timeout: 5 * time.Minute},
	}
}

// ---- Embed ----------------------------------------------------------------

type embedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Embed returns a vector embedding for the given text.
func (c *Client) Embed(text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.embedModel, Prompt: text})
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Post(c.baseURL+"/api/embeddings", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed HTTP %d: %s", resp.StatusCode, raw)
	}

	var res embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	return res.Embedding, nil
}

// ---- Generate -------------------------------------------------------------

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
}

// Generate sends a prompt to the generative model and returns the full response.
func (c *Client) Generate(prompt string) (string, error) {
	body, err := json.Marshal(generateRequest{
		Model:  c.genModel,
		Prompt: prompt,
		Stream: false,
	})
	if err != nil {
		return "", err
	}

	resp, err := c.http.Post(c.baseURL+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama generate request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama generate HTTP %d: %s", resp.StatusCode, raw)
	}

	var res generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("decode generate response: %w", err)
	}
	return res.Response, nil
}
