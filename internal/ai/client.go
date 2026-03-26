package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is the abstraction used by ingestion and RAG to create embeddings and
// generate answers.
type Client interface {
	Embed(text string) ([]float32, error)
	Generate(prompt string) (string, error)
}

// Config controls which inference provider to use.
type Config struct {
	Provider string

	OllamaURL       string
	EmbedModel      string
	GenModel        string
	EmbedDimensions int

	AzureEndpoint        string
	AzureAPIKey          string
	AzureAPIVersion      string
	AzureEmbedDeployment string
	AzureChatDeployment  string
}

// New creates a provider-backed AI client.
func New(cfg Config) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "ollama":
		return newOllamaClient(cfg), nil
	case "azure-openai":
		return newAzureOpenAIClient(cfg)
	case "azure-foundry":
		return newAzureFoundryClient(cfg)
	default:
		return nil, fmt.Errorf("unsupported AI_PROVIDER %q", cfg.Provider)
	}
}

type ollamaClient struct {
	baseURL    string
	embedModel string
	genModel   string
	http       *http.Client
}

func newOllamaClient(cfg Config) Client {
	return &ollamaClient{
		baseURL:    strings.TrimRight(cfg.OllamaURL, "/"),
		embedModel: cfg.EmbedModel,
		genModel:   cfg.GenModel,
		http:       &http.Client{Timeout: 5 * time.Minute},
	}
}

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

func (c *ollamaClient) Embed(text string) ([]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: c.embedModel, Prompt: text})
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

	var res ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode ollama embed response: %w", err)
	}
	return res.Embedding, nil
}

type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaGenerateResponse struct {
	Response string `json:"response"`
}

func (c *ollamaClient) Generate(prompt string) (string, error) {
	body, err := json.Marshal(ollamaGenerateRequest{
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

	var res ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("decode ollama generate response: %w", err)
	}
	return res.Response, nil
}

type azureClient struct {
	endpoint          string
	apiKey            string
	apiVersion        string
	embedModel        string
	embedDimensions   int
	genModel          string
	embedDeployment   string
	chatDeployment    string
	useDeploymentsURL bool
	http              *http.Client
}

func newAzureOpenAIClient(cfg Config) (Client, error) {
	if strings.TrimSpace(cfg.AzureEndpoint) == "" || strings.TrimSpace(cfg.AzureAPIKey) == "" {
		return nil, fmt.Errorf("AZURE_AI_ENDPOINT and AZURE_AI_API_KEY are required for azure-openai")
	}
	if strings.TrimSpace(cfg.AzureEmbedDeployment) == "" || strings.TrimSpace(cfg.AzureChatDeployment) == "" {
		return nil, fmt.Errorf("AZURE_EMBED_DEPLOYMENT and AZURE_CHAT_DEPLOYMENT are required for azure-openai")
	}

	return &azureClient{
		endpoint:          strings.TrimRight(cfg.AzureEndpoint, "/"),
		apiKey:            cfg.AzureAPIKey,
		apiVersion:        defaultString(cfg.AzureAPIVersion, "2024-06-01"),
		embedModel:        cfg.EmbedModel,
		embedDimensions:   cfg.EmbedDimensions,
		genModel:          cfg.GenModel,
		embedDeployment:   cfg.AzureEmbedDeployment,
		chatDeployment:    cfg.AzureChatDeployment,
		useDeploymentsURL: true,
		http:              &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func newAzureFoundryClient(cfg Config) (Client, error) {
	if strings.TrimSpace(cfg.AzureEndpoint) == "" || strings.TrimSpace(cfg.AzureAPIKey) == "" {
		return nil, fmt.Errorf("AZURE_AI_ENDPOINT and AZURE_AI_API_KEY are required for azure-foundry")
	}
	if strings.TrimSpace(cfg.EmbedModel) == "" || strings.TrimSpace(cfg.GenModel) == "" {
		return nil, fmt.Errorf("EMBED_MODEL and GEN_MODEL are required for azure-foundry")
	}

	return &azureClient{
		endpoint:          strings.TrimRight(cfg.AzureEndpoint, "/"),
		apiKey:            cfg.AzureAPIKey,
		apiVersion:        defaultString(cfg.AzureAPIVersion, "2024-05-01-preview"),
		embedModel:        cfg.EmbedModel,
		embedDimensions:   cfg.EmbedDimensions,
		genModel:          cfg.GenModel,
		useDeploymentsURL: false,
		http:              &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

type azureEmbeddingsRequest struct {
	Model      string   `json:"model,omitempty"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type azureEmbeddingsResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func (c *azureClient) Embed(text string) ([]float32, error) {
	reqBody := azureEmbeddingsRequest{Input: []string{text}}
	if c.embedDimensions > 0 {
		reqBody.Dimensions = c.embedDimensions
	}
	if !c.useDeploymentsURL {
		reqBody.Model = c.embedModel
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.embeddingsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.apiKey)

	resp, err := c.doWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("azure embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure embed HTTP %d: %s", resp.StatusCode, raw)
	}

	var res azureEmbeddingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode azure embed response: %w", err)
	}
	if len(res.Data) == 0 {
		return nil, fmt.Errorf("azure embed response contained no data")
	}

	vector := make([]float32, len(res.Data[0].Embedding))
	for i, v := range res.Data[0].Embedding {
		vector[i] = float32(v)
	}
	return vector, nil
}

type azureChatRequest struct {
	Model       string             `json:"model,omitempty"`
	Messages    []azureChatMessage `json:"messages"`
	Temperature float64            `json:"temperature,omitempty"`
	MaxTokens   int                `json:"max_tokens,omitempty"`
}

type azureChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type azureChatResponse struct {
	Choices []struct {
		Message azureChatMessage `json:"message"`
	} `json:"choices"`
}

func (c *azureClient) Generate(prompt string) (string, error) {
	reqBody := azureChatRequest{
		Messages: []azureChatMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
		MaxTokens:   800,
	}
	if !c.useDeploymentsURL {
		reqBody.Model = c.genModel
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, c.chatURL(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.apiKey)

	resp, err := c.doWithRetry(req)
	if err != nil {
		return "", fmt.Errorf("azure generate request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("azure generate HTTP %d: %s", resp.StatusCode, raw)
	}

	var res azureChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("decode azure generate response: %w", err)
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("azure generate response contained no choices")
	}
	return res.Choices[0].Message.Content, nil
}

func (c *azureClient) embeddingsURL() string {
	if c.useDeploymentsURL {
		return fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s",
			c.endpoint, url.PathEscape(c.embedDeployment), url.QueryEscape(c.apiVersion))
	}
	if isFoundryProjectEndpoint(c.endpoint) {
		return c.endpoint + "/openai/v1/embeddings"
	}
	return fmt.Sprintf("%s/models/embeddings?api-version=%s", c.endpoint, url.QueryEscape(c.apiVersion))
}

func (c *azureClient) chatURL() string {
	if c.useDeploymentsURL {
		return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
			c.endpoint, url.PathEscape(c.chatDeployment), url.QueryEscape(c.apiVersion))
	}
	if isFoundryProjectEndpoint(c.endpoint) {
		return c.endpoint + "/openai/v1/chat/completions"
	}
	return fmt.Sprintf("%s/models/chat/completions?api-version=%s", c.endpoint, url.QueryEscape(c.apiVersion))
}

func (c *azureClient) doWithRetry(req *http.Request) (*http.Response, error) {
	const maxAttempts = 3

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		cloned := req.Clone(req.Context())
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			cloned.Body = body
		}

		resp, err := c.http.Do(cloned)
		if err == nil {
			if shouldRetryStatus(resp.StatusCode) && attempt < maxAttempts {
				_ = resp.Body.Close()
				time.Sleep(backoffDuration(attempt))
				continue
			}
			return resp, nil
		}

		lastErr = err
		if !isTransientAzureError(err) || attempt == maxAttempts {
			return nil, err
		}
		time.Sleep(backoffDuration(attempt))
	}

	return nil, lastErr
}

func isTransientAzureError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "eof") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "timeout")
}

func shouldRetryStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func backoffDuration(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 200 * time.Millisecond
	case 2:
		return 500 * time.Millisecond
	default:
		return time.Second
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func isFoundryProjectEndpoint(endpoint string) bool {
	return strings.Contains(strings.ToLower(endpoint), "/api/projects/")
}
