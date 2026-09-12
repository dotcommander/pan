package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	roleSystem     = "system"
	roleAssistant  = "assistant"
	roleTool       = "tool"
	roleUser       = "user"
	wireType       = "type"
	wireContent    = "content"
	wireFunction   = "function"
	wireModel      = "model"
	wireName       = "name"
	wireText       = "text"
	choiceRequired = "required"
)

func encodeOpenAI(request Request) ([]byte, error) {
	payload := map[string]any{wireModel: request.Model, "messages": request.Messages}
	if len(request.Tools) > 0 {
		payload["tools"] = request.Tools
	}
	if request.ToolChoice != nil {
		payload["tool_choice"] = request.ToolChoice
	}
	if request.Temperature != nil {
		payload["temperature"] = request.Temperature
	}
	if request.ResponseFormat != nil {
		payload["response_format"] = request.ResponseFormat
	}
	mergeOptions(payload, request.ProviderOptions)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("encode provider request: unsupported value")
	}
	return body, nil
}

func encodeAnthropic(request Request) ([]byte, error) {
	system, messages, err := anthropicMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{wireModel: request.Model, "messages": messages, "max_tokens": 4096}
	if system != "" {
		payload[roleSystem] = system
	}
	if request.Temperature != nil {
		payload["temperature"] = request.Temperature
	}
	if len(request.Tools) > 0 {
		tools, toolErr := anthropicTools(request.Tools)
		if toolErr != nil {
			return nil, toolErr
		}
		payload["tools"] = tools
		if choice := anthropicToolChoice(request.ToolChoice); choice != nil {
			payload["tool_choice"] = choice
		}
	}
	mergeOptions(payload, request.ProviderOptions)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("encode provider request: unsupported value")
	}
	return body, nil
}

func anthropicMessages(messages []Message) (string, []map[string]any, error) {
	var system []string
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == roleSystem {
			system = append(system, message.Content)
			continue
		}
		role := message.Role
		if role != roleAssistant {
			role = roleUser
		}
		content, err := anthropicContent(message)
		if err != nil {
			return "", nil, err
		}
		if len(result) > 0 && result[len(result)-1]["role"] == role {
			result[len(result)-1][wireContent] = append(result[len(result)-1][wireContent].([]map[string]any), content...)
			continue
		}
		result = append(result, map[string]any{"role": role, wireContent: content})
	}
	return strings.Join(system, "\n"), result, nil
}

func anthropicContent(message Message) ([]map[string]any, error) {
	if message.Role == roleTool {
		return []map[string]any{{wireType: "tool_result", "tool_use_id": message.ToolCallID, wireContent: message.Content}}, nil
	}
	content := make([]map[string]any, 0, 1+len(message.ToolCalls))
	if message.Content != "" {
		content = append(content, map[string]any{wireType: wireText, wireText: message.Content})
	}
	for _, call := range message.ToolCalls {
		input := map[string]any{}
		if call.Function.Arguments != "" && json.Unmarshal([]byte(call.Function.Arguments), &input) != nil {
			return nil, fmt.Errorf("provider tool %q has invalid JSON arguments", call.Function.Name)
		}
		content = append(content, map[string]any{wireType: "tool_use", "id": call.ID, wireName: call.Function.Name, "input": input})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{wireType: wireText, wireText: ""})
	}
	return content, nil
}

func anthropicTools(tools []Tool) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != wireFunction {
			return nil, fmt.Errorf("unsupported provider tool type %q", tool.Type)
		}
		result = append(result, map[string]any{wireName: tool.Function.Name, "description": tool.Function.Description, "input_schema": tool.Function.Parameters})
	}
	return result, nil
}

func anthropicToolChoice(choice any) map[string]any {
	switch value := choice.(type) {
	case string:
		switch value {
		case "none", "auto", choiceRequired:
			if value == choiceRequired {
				return map[string]any{wireType: "any"}
			}
			return map[string]any{wireType: value}
		}
	case map[string]any:
		if value["type"] == wireFunction {
			if fn, ok := value[wireFunction].(map[string]any); ok {
				if name, ok := fn[wireName].(string); ok {
					return map[string]any{wireType: roleTool, wireName: name}
				}
			}
		}
	}
	return nil
}

func decodeAnthropic(data []byte) (Response, error) {
	var raw struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Stop    string `json:"stop_reason"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Response{}, errors.New("provider returned invalid completion JSON")
	}
	message := Message{Role: roleAssistant}
	for _, part := range raw.Content {
		if part.Type == wireText {
			message.Content += part.Text
		}
		if part.Type == "tool_use" {
			message.ToolCalls = append(message.ToolCalls, ToolCall{ID: part.ID, Type: wireFunction, Function: FunctionCall{Name: part.Name, Arguments: string(part.Input)}})
		}
	}
	if len(raw.Content) == 0 {
		return Response{}, errors.New("provider returned no completion choices")
	}
	return Response{ID: raw.ID, Model: raw.Model, Choices: []Choice{{Message: message, FinishReason: raw.Stop}}, Usage: Usage{PromptTokens: raw.Usage.Input, CompletionTokens: raw.Usage.Output, TotalTokens: raw.Usage.Input + raw.Usage.Output}}, nil
}

func mergeOptions(payload map[string]any, options map[string]any) {
	for key, value := range options {
		if _, reserved := payload[key]; !reserved {
			payload[key] = value
		}
	}
}

func encodeGemini(request Request, base string) ([]byte, string, error) {
	payload, err := geminiPayload(request)
	if err != nil {
		return nil, "", err
	}
	endpoint := strings.TrimRight(base, "/") + "/models/" + url.PathEscape(strings.TrimPrefix(request.Model, "models/")) + ":generateContent"
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", errors.New("encode provider request: unsupported value")
	}
	return body, endpoint, nil
}
