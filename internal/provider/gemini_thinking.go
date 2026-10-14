package provider

import (
	"regexp"
	"strconv"
	"strings"
)

// geminiMinimalLevelModels lists Gemini 3 models whose documented thinkingLevel
// floor is minimal; the remaining documented 3-series models bottom out at low.
// Source: https://aistudio.google.com/docs/thinking#thinking-levels
var geminiMinimalLevelModels = map[string]bool{
	"gemini-3.6-flash":             true,
	"gemini-3.5-flash":             true,
	"gemini-3.5-flash-lite":        true,
	"gemini-3-flash-preview":       true,
	"gemini-robotics-er-2-preview": true,
}

var geminiMajorVersionPattern = regexp.MustCompile(`^gemini-(\d+)`)

func geminiBaseModel(model string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/")
}

// geminiUsesThinkingLevel reports whether the model controls thinking through
// thinkingLevel (Gemini 3 series) rather than the legacy thinkingBudget knob.
func geminiUsesThinkingLevel(model string) bool {
	match := geminiMajorVersionPattern.FindStringSubmatch(geminiBaseModel(model))
	if match == nil {
		return false
	}
	major, err := strconv.Atoi(match[1])
	return err == nil && major >= 3
}

// geminiLowestThinkingLevel returns the cheapest documented thinkingLevel for
// a Gemini 3 model: minimal where supported, low otherwise. Low is the floor
// on every documented 3-series model and on unknown future releases.
func geminiLowestThinkingLevel(model string) string {
	if geminiMinimalLevelModels[geminiBaseModel(model)] {
		return "minimal"
	}
	return "low"
}

// geminiThinkingDisabled renders the disable-thinking provider option for one
// model. Gemini 3 models cannot turn thinking off, so they get the lowest
// supported level with thought summaries suppressed; 2.5-series models keep
// the thinkingBudget:0 hard off switch.
func geminiThinkingDisabled(model string) map[string]any {
	if geminiUsesThinkingLevel(model) {
		return map[string]any{"thinkingLevel": geminiLowestThinkingLevel(model), "includeThoughts": false}
	}
	return map[string]any{"thinkingBudget": 0, "includeThoughts": false}
}
