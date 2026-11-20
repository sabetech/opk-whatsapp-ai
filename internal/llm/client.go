package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type AssistantMessage struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Client struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

func NewClient(apiKey, baseURL, model string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type chatRequest struct {
	Model      string           `json:"model"`
	Messages   []Message        `json:"messages"`
	Tools      []ToolDefinition `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      AssistantMessage `json:"message"`
		FinishReason string           `json:"finish_reason"`
	} `json:"choices"`

	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat preserves the original text-only interface.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	result, err := c.ChatWithTools(ctx, messages, nil)
	if err != nil {
		return "", err
	}

	return result.Content, nil
}

// ChatWithTools sends a conversation and optional tool definitions.
func (c *Client) ChatWithTools(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
) (AssistantMessage, error) {
	if c.apiKey == "" {
		return AssistantMessage{}, fmt.Errorf("OpenRouter API key is not configured")
	}

	if c.model == "" {
		return AssistantMessage{}, fmt.Errorf("OpenRouter model is not configured")
	}

	payload := chatRequest{
		Model:      c.model,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: "auto",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return AssistantMessage{}, fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return AssistantMessage{}, fmt.Errorf("create chat request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return AssistantMessage{}, fmt.Errorf("send OpenRouter request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return AssistantMessage{}, fmt.Errorf("read OpenRouter response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AssistantMessage{}, fmt.Errorf(
			"OpenRouter returned HTTP %d: %s",
			resp.StatusCode,
			string(responseBody),
		)
	}

	var result chatResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return AssistantMessage{}, fmt.Errorf("decode OpenRouter response: %w", err)
	}

	if result.Error != nil {
		return AssistantMessage{}, fmt.Errorf("OpenRouter error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return AssistantMessage{}, fmt.Errorf("OpenRouter returned no response choices")
	}

	return result.Choices[0].Message, nil
}
