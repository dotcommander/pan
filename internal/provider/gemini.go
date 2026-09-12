package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	thinkingOption   = "thinking"
	thinkingDisabled = "disabled"
)

func geminiPayload(request Request) (map[string]any, error) {
	payload := map[string]any{"contents": geminiContents(request.Messages)}
	if system := geminiSystem(request.Messages); system != "" {
		payload["systemInstruction"] = map[string]any{"parts": []map[string]any{{wireText: system}}}
	}
	if request.Temperature != nil {
		payload["generationConfig"] = map[string]any{"temperature": request.Temperature}
	}
	if len(request.Tools) > 0 {
		declarations := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			if tool.Type != "" && tool.Type != wireFunction {
				return nil, fmt.Errorf("unsupported provider tool type %q", tool.Type)
			}
			declarations = append(declarations, map[string]any{wireName: tool.Function.Name, "description": tool.Function.Description, "parameters": tool.Function.Parameters})
		}
		payload["tools"] = []map[string]any{{"functionDeclarations": declarations}}
		if choice := geminiToolChoice(request.ToolChoice); choice != nil {
			payload["toolConfig"] = choice
		}
	}
	geminiOptions(payload, request.ProviderOptions)
	return payload, nil
}

func geminiSystem(messages []Message) string {
	parts := make([]string, 0)
	for _, message := range messages {
		if message.Role == roleSystem {
			parts = append(parts, message.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func geminiContents(messages []Message) []map[string]any {
	callNames := map[string]string{}
	contents := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == roleSystem {
			continue
		}
		role := roleUser
		if message.Role == roleAssistant {
			role = wireModel
		}
		parts := make([]map[string]any, 0, 1+len(message.ToolCalls))
		if message.Role == roleTool {
			name := callNames[message.ToolCallID]
			if name == "" {
				name = message.ToolCallID
			}
			parts = append(parts, map[string]any{"functionResponse": map[string]any{wireName: name, "response": map[string]any{wireContent: message.Content}}})
		} else {
			if message.Content != "" {
				parts = append(parts, map[string]any{wireText: message.Content})
			}
			for _, call := range message.ToolCalls {
				args := map[string]any{}
				_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
				callNames[call.ID] = call.Function.Name
				parts = append(parts, map[string]any{"functionCall": map[string]any{wireName: call.Function.Name, "args": args}})
			}
		}
		if len(parts) == 0 {
			parts = append(parts, map[string]any{wireText: ""})
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	return contents
}

func geminiToolChoice(choice any) map[string]any {
	mode := ""
	config := map[string]any{}
	switch value := choice.(type) {
	case string:
		switch value {
		case "auto":
			mode = "AUTO"
		case "none":
			mode = "NONE"
		case choiceRequired:
			mode = "ANY"
		}
	case map[string]any:
		if value["type"] == wireFunction {
			if fn, ok := value[wireFunction].(map[string]any); ok {
				if name, ok := fn[wireName].(string); ok {
					mode = "ANY"
					config["allowedFunctionNames"] = []string{name}
				}
			}
		}
	}
	if mode == "" {
		return nil
	}
	config["mode"] = mode
	return map[string]any{"functionCallingConfig": config}
}

func geminiOptions(payload map[string]any, options map[string]any) {
	for key, value := range options {
		if key == thinkingOption {
			if thinking, ok := value.(map[string]any); ok && thinking["type"] == thinkingDisabled {
				generation(payload)["thinkingConfig"] = map[string]any{"thinkingBudget": 0, "includeThoughts": false}
				continue
			}
		}
		if key == "generationConfig" {
			if config, ok := value.(map[string]any); ok {
				for configKey, configValue := range config {
					generation(payload)[configKey] = configValue
				}
				continue
			}
		}
		payload[key] = value
	}
}

func generation(payload map[string]any) map[string]any {
	if config, ok := payload["generationConfig"].(map[string]any); ok {
		return config
	}
	config := map[string]any{}
	payload["generationConfig"] = config
	return config
}

func decodeGemini(data []byte, model string) (Response, error) {
	var raw struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			Finish string `json:"finishReason"`
		} `json:"candidates"`
		Usage struct {
			Prompt     int `json:"promptTokenCount"`
			Completion int `json:"candidatesTokenCount"`
			Total      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Response{}, errors.New("provider returned invalid completion JSON")
	}
	if len(raw.Candidates) == 0 {
		return Response{}, errors.New("provider returned no completion choices")
	}
	message := Message{Role: roleAssistant}
	for index, part := range raw.Candidates[0].Content.Parts {
		message.Content += part.Text
		if part.FunctionCall != nil {
			arguments, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return Response{}, errors.New("provider returned invalid completion JSON")
			}
			message.ToolCalls = append(message.ToolCalls, ToolCall{ID: fmt.Sprintf("gemini-call-%d-%s", index, part.FunctionCall.Name), Type: wireFunction, Function: FunctionCall{Name: part.FunctionCall.Name, Arguments: string(arguments)}})
		}
	}
	return Response{Model: model, Choices: []Choice{{Message: message, FinishReason: raw.Candidates[0].Finish}}, Usage: Usage{PromptTokens: raw.Usage.Prompt, CompletionTokens: raw.Usage.Completion, TotalTokens: raw.Usage.Total}}, nil
}
