package storyboard

import (
	"sort"
	"strings"
)

func helpRootName(help string) string {
	lines := strings.Split(help, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, commandHelpUsage) && trimmed != commandHelpUsage {
			fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(trimmed, commandHelpUsage)))
			if len(fields) > 0 {
				return fields[0]
			}
		}
		if trimmed != commandHelpUsage {
			continue
		}
		for _, usageLine := range lines[i+1:] {
			fields := strings.Fields(strings.TrimSpace(usageLine))
			if len(fields) == 0 {
				continue
			}
			return fields[0]
		}
	}
	return ""
}

func helpRunnableCommand(help string) bool {
	lines := strings.Split(help, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != commandHelpUsage {
			continue
		}
		for _, usageLine := range lines[i+1:] {
			trimmed := strings.TrimSpace(usageLine)
			if trimmed == "" {
				break
			}
			fields := strings.Fields(trimmed)
			if len(fields) == 0 {
				continue
			}
			if !strings.Contains(trimmed, "[command]") {
				return true
			}
		}
		break
	}
	return false
}

func helpCommandSignature(help string) string {
	description := helpSummary(help)
	var flags []string
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		for _, field := range strings.Fields(trimmed) {
			field = strings.TrimRight(field, ",")
			if !strings.HasPrefix(field, "--") || field == "--help" {
				continue
			}
			flags = append(flags, field)
		}
	}
	sort.Strings(flags)
	return description + "|" + strings.Join(flags, ",")
}

func helpSummary(help string) string {
	var description string
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if description == "" &&
			!strings.HasPrefix(trimmed, commandHelpUsage) &&
			trimmed != commandHelpUsage &&
			trimmed != commandHelpAvailable &&
			trimmed != commandHelpSubcommands &&
			trimmed != "Flags:" &&
			trimmed != "Global Flags:" &&
			!strings.HasPrefix(trimmed, "Use ") &&
			!strings.HasPrefix(trimmed, "-") {
			description = trimmed
		}
	}
	return description
}

func helpAvailableCommands(help string) []string {
	rows := helpCommandRows(help)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Name)
	}
	return out
}

type helpCommandRow struct {
	Name        string
	Description string
}

func helpUsesFlatSubcommands(help string) bool {
	for _, line := range strings.Split(help, "\n") {
		switch strings.TrimSpace(line) {
		case commandHelpSubcommands:
			return true
		case commandHelpAvailable:
			return false
		}
	}
	return false
}

func helpCommandRows(help string) []helpCommandRow {
	var rows []helpCommandRow
	inBlock := false
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == commandHelpAvailable || trimmed == commandHelpSubcommands {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			break
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		rows = append(rows, helpCommandRow{
			Name:        fields[0],
			Description: strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0])),
		})
	}
	return rows
}
