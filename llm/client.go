package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/michalswi/local-agent-on-steroids/types"
)

// Client defines the interface for LLM interactions
type Client interface {
	Chat(ctx context.Context, request *ChatRequest) (*ChatResponse, error)
	IsAvailable() bool
	GetModel() string
}

// ChatRequest represents a request to the LLM
type ChatRequest struct {
	Model       string                 `json:"model"`
	Messages    []Message              `json:"messages"`
	Stream      bool                   `json:"stream"`
	Temperature float64                `json:"temperature,omitempty"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Options     map[string]interface{} `json:"options,omitempty"`
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"` // "user", "assistant", "system"
	Content string `json:"content"`
}

// ChatResponse represents a response from the LLM
type ChatResponse struct {
	Model     string    `json:"model"`
	Message   Message   `json:"message"`
	CreatedAt time.Time `json:"created_at"`
	Done      bool      `json:"done"`

	// Usage information
	TotalDuration   int64 `json:"total_duration,omitempty"`
	PromptEvalCount int   `json:"prompt_eval_count,omitempty"`
	EvalCount       int   `json:"eval_count,omitempty"`
}

// OllamaClient implements Client for Ollama
type OllamaClient struct {
	endpoint   string
	model      string
	numCtx     int
	httpClient *http.Client
	timeout    time.Duration
}

// NewOllamaClient creates a new Ollama client.
// numCtx sets the Ollama context window (num_ctx); pass 0 to use Ollama's default.
func NewOllamaClient(endpoint, model string, timeout, numCtx int) *OllamaClient {
	return &OllamaClient{
		endpoint: endpoint,
		model:    model,
		numCtx:   numCtx,
		httpClient: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
		timeout: time.Duration(timeout) * time.Second,
	}
}

// Chat sends a chat request to Ollama
func (c *OllamaClient) Chat(ctx context.Context, request *ChatRequest) (*ChatResponse, error) {
	// Set model if not specified
	if request.Model == "" {
		request.Model = c.model
	}

	// Ensure stream is false (we want complete responses)
	request.Stream = false

	// Inject num_ctx into options if configured and not already set by caller.
	if c.numCtx > 0 {
		if request.Options == nil {
			request.Options = make(map[string]interface{})
		}
		if _, already := request.Options["num_ctx"]; !already {
			request.Options["num_ctx"] = c.numCtx
		}
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", c.endpoint)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// IsAvailable checks if Ollama is available
func (c *OllamaClient) IsAvailable() bool {
	url := fmt.Sprintf("%s/api/tags", c.endpoint)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// GetModel returns the model name
func (c *OllamaClient) GetModel() string {
	return c.model
}

// ListModels lists available models in Ollama
func (c *OllamaClient) ListModels() ([]string, error) {
	url := fmt.Sprintf("%s/api/tags", c.endpoint)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	models := make([]string, len(result.Models))
	for i, m := range result.Models {
		models[i] = m.Name
	}

	return models, nil
}

// Analyze sends files for analysis with a specific task
func (c *OllamaClient) Analyze(task string, filesContent string, temperature float64) (*types.AnalysisResponse, error) {
	startTime := time.Now()

	systemMessage := Message{
		Role: "system",
		Content: `You are an assistant that analyzes files and documents. Answer only from the provided files and task; if something is not in the provided files, say 'Not found in provided files' instead of guessing.
Stay on the specific request (no generic advice unless asked). When user asks to 'show', 'copy', 'paste', or 'extract' specific content, provide the exact literal content first in fenced code blocks (for code/config) or quoted blocks (for text/data), then optionally add brief context.
For code-related tasks: include concrete, actionable fixes. If the user asks for new code or applied suggestions, include updated code blocks or concise patch-style snippets that implement the recommendations.
For analysis tasks: list findings with severity, then propose changes, then show any revised content. Keep the output concise and directly applicable.
When you present code, wrap it in fenced markdown blocks with a language tag (e.g., ` + "```go ... ```" + `). Separate multiple files or sections with clear headings.`,
	}

	userMessage := Message{
		Role:    "user",
		Content: fmt.Sprintf("**Task:** %s\n\nPlease complete this task based on the following files:\n\n%s", task, filesContent),
	}

	request := &ChatRequest{
		Model:       c.model,
		Messages:    []Message{systemMessage, userMessage},
		Temperature: temperature,
	}

	response, err := c.Chat(context.Background(), request)
	if err != nil {
		return nil, fmt.Errorf("failed to get LLM response: %w", err)
	}

	analysisResp := &types.AnalysisResponse{
		Response:   response.Message.Content,
		Model:      response.Model,
		TokensUsed: response.PromptEvalCount + response.EvalCount,
		Duration:   time.Since(startTime),
	}

	return analysisResp, nil
}

// AnalyzeChunk analyzes a specific file chunk
func (c *OllamaClient) AnalyzeChunk(task string, file *types.FileInfo, chunkIndex int, temperature float64) (*types.AnalysisResponse, error) {
	if chunkIndex < 0 || chunkIndex >= len(file.Chunks) {
		return nil, fmt.Errorf("invalid chunk index: %d", chunkIndex)
	}

	chunk := file.Chunks[chunkIndex]
	content := fmt.Sprintf("File: %s (Lines %d-%d)\n\n```\n%s\n```",
		file.RelPath, chunk.StartLine, chunk.EndLine, chunk.Content)

	return c.Analyze(task, content, temperature)
}

// StreamChat sends a streaming chat request (for future interactive mode)
func (c *OllamaClient) StreamChat(request *ChatRequest, callback func(string) error) error {
	// Set model if not specified
	if request.Model == "" {
		request.Model = c.model
	}

	// Enable streaming
	request.Stream = true

	jsonData, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", c.endpoint)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var response ChatResponse
		if err := decoder.Decode(&response); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode streaming response: %w", err)
		}

		// Call callback with content
		if err := callback(response.Message.Content); err != nil {
			return err
		}

		if response.Done {
			break
		}
	}

	return nil
}
