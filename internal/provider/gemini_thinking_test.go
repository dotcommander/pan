package provider

import "testing"

func TestGeminiThinkingDisabledUsesLevelFloorOn3Series(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		model string
		want  map[string]any
	}{
		{"3.6 flash floors at minimal", "gemini-3.6-flash", map[string]any{"thinkingLevel": "minimal", "includeThoughts": false}},
		{"3.7 flash floors at low", "gemini-3.7-flash", map[string]any{"thinkingLevel": "low", "includeThoughts": false}},
		{"3 pro preview floors at low", "gemini-3-pro-preview", map[string]any{"thinkingLevel": "low", "includeThoughts": false}},
		{"models prefix normalizes", "models/gemini-3.5-flash", map[string]any{"thinkingLevel": "minimal", "includeThoughts": false}},
		{"2.5 flash keeps budget off", "gemini-2.5-flash", map[string]any{"thinkingBudget": 0, "includeThoughts": false}},
		{"unknown model keeps budget off", "gemini-fixture", map[string]any{"thinkingBudget": 0, "includeThoughts": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload, err := geminiPayload(Request{
				Model:           tc.model,
				Messages:        []Message{{Role: roleUser, Content: "hi"}},
				ProviderOptions: map[string]any{"thinking": map[string]any{"type": "disabled"}},
			})
			if err != nil {
				t.Fatalf("geminiPayload: %v", err)
			}
			config := payload["generationConfig"].(map[string]any)
			thinking := config["thinkingConfig"].(map[string]any)
			if len(thinking) != len(tc.want) {
				t.Fatalf("thinkingConfig = %#v, want %#v", thinking, tc.want)
			}
			for key, want := range tc.want {
				if thinking[key] != want {
					t.Fatalf("thinkingConfig[%q] = %v, want %v (full: %#v)", key, thinking[key], want, thinking)
				}
			}
		})
	}
}
