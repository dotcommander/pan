package storyboard

import "strings"

func inferStageRole(label, sourceFile string) string {
	switch {
	case strings.HasPrefix(label, "external:"):
		return roleIO
	case strings.HasPrefix(label, "fanout:"), strings.HasPrefix(label, "fork:"):
		return roleRoute
	}
	for _, rule := range stageRoleRules() {
		if rule.matches(sourceFile) {
			return rule.role
		}
	}
	return ""
}

type stageRoleRule struct {
	role     string
	contains string
	prefix   string
}

func (r stageRoleRule) matches(sourceFile string) bool {
	return (r.contains != "" && strings.Contains(sourceFile, r.contains)) || (r.prefix != "" && strings.HasPrefix(sourceFile, r.prefix))
}

func stageRoleRules() []stageRoleRule {
	return []stageRoleRule{
		{role: roleEntry, prefix: "cmd/"},
		{role: roleRoute, contains: "/commands/", prefix: "internal/commands/"},
		{role: roleParse, contains: "/spec/", prefix: "internal/spec/"},
		{role: roleAnalyze, contains: "/scan/", prefix: "internal/scan/"},
		{role: roleAnalyze, contains: "/review/", prefix: "internal/review/"},
		{role: roleRender, contains: "/render/", prefix: "internal/render/"},
		{role: roleWatch, contains: "/serve/watcher.go"},
		{role: roleServe, contains: "/serve/", prefix: "internal/serve/"},
		{role: roleWrite, contains: "/seed/", prefix: "internal/seed/"},
	}
}
