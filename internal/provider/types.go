// Package provider owns the bounded OpenAI-compatible transport shared by Pan workflows.
package provider

import (
	"net/http"
	"time"
)

// Config supplies caller-owned credentials, endpoint, and request bounds.
type Config struct {
	Provider           string
	BaseURL            string
	APIKey             string
	AuthHeader         string
	Model              string
	Timeout            time.Duration
	MaxResponseBytes   int64
	ProviderMaxRetries int
	MaxProviderBackoff time.Duration
	RateLimitBackoff   time.Duration
	HTTPClient         *http.Client
}

// Request is one chat completion. Workflow owners control models and tool loops.
type Request struct {
	Model          string    `json:"model"`
	Messages       []Message `json:"messages"`
	Tools          []Tool    `json:"tools,omitempty"`
	ToolChoice     any       `json:"tool_choice,omitempty"`
	Temperature    *float64  `json:"temperature,omitempty"`
	ResponseFormat any       `json:"response_format,omitempty"`
	// ProviderOptions carries provider-specific generation options. It is
	// translated into the active provider's native wire shape.
	ProviderOptions map[string]any `json:"-"`
}

// Message carries text or structured tool calls in conversation order.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// Tool declares a function available to a workflow.
type Tool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition is a named tool and its JSON schema.
type FunctionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

// ToolCall is a model's request to invoke a workflow-owned tool.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall retains raw JSON arguments for validation by the tool owner.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Response retains provider identity, choices, and usage for durable receipts.
type Response struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is one completion and its terminal reason.
type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage retains the provider's token accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
