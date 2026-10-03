package config

import (
	"fmt"
	"sort"
	"time"
)

type OutgoingGitRules struct {
	Timeout        time.Duration `yaml:"timeout" json:"timeout"`
	MaxLogBytes    int           `yaml:"max_log_bytes" json:"max_log_bytes"`
	MaxHeaderBytes int           `yaml:"max_header_bytes" json:"max_header_bytes"`
	MaxStderrBytes int           `yaml:"max_stderr_bytes" json:"max_stderr_bytes"`
}
type CommandHelpRules struct {
	InvocationTimeout   time.Duration `yaml:"invocation_timeout" json:"invocation_timeout"`
	Timeout             time.Duration `yaml:"timeout" json:"timeout"`
	MaxInvocations      int           `yaml:"max_invocations" json:"max_invocations"`
	MaxDepth            int           `yaml:"max_depth" json:"max_depth"`
	MaxOutputBytes      int           `yaml:"max_output_bytes" json:"max_output_bytes"`
	MaxTotalOutputBytes int           `yaml:"max_total_output_bytes" json:"max_total_output_bytes"`
}

func (r OutgoingGitRules) Normalized() OutgoingGitRules {
	d := Default().OutgoingGit
	if r.Timeout == 0 {
		r.Timeout = d.Timeout
	}
	if r.MaxLogBytes == 0 {
		r.MaxLogBytes = d.MaxLogBytes
	}
	if r.MaxHeaderBytes == 0 {
		r.MaxHeaderBytes = d.MaxHeaderBytes
	}
	if r.MaxStderrBytes == 0 {
		r.MaxStderrBytes = d.MaxStderrBytes
	}
	return r
}
func (r CommandHelpRules) Normalized() CommandHelpRules {
	d := Default().CommandHelp
	if r.Timeout == 0 {
		r.Timeout = d.Timeout
	}
	if r.InvocationTimeout == 0 {
		r.InvocationTimeout = d.InvocationTimeout
	}
	if r.MaxInvocations == 0 {
		r.MaxInvocations = d.MaxInvocations
	}
	if r.MaxDepth == 0 {
		r.MaxDepth = d.MaxDepth
	}
	if r.MaxOutputBytes == 0 {
		r.MaxOutputBytes = d.MaxOutputBytes
	}
	if r.MaxTotalOutputBytes == 0 {
		r.MaxTotalOutputBytes = d.MaxTotalOutputBytes
	}
	return r
}
func (c Config) validateProcessLimits() error {
	values := map[string]int64{
		"lsp.max_frame_bytes": int64(c.Lsp.MaxFrameBytes), "lsp.max_header_line_bytes": int64(c.Lsp.MaxHeaderLineBytes), "lsp.max_header_bytes": int64(c.Lsp.MaxHeaderBytes), "lsp.max_headers": int64(c.Lsp.MaxHeaders),
		"improve.max_stdout_bytes": int64(c.Improve.MaxStdoutBytes), "improve.max_stderr_bytes": int64(c.Improve.MaxStderrBytes),
		"outgoing_git.timeout": int64(c.OutgoingGit.Timeout), "outgoing_git.max_log_bytes": int64(c.OutgoingGit.MaxLogBytes), "outgoing_git.max_header_bytes": int64(c.OutgoingGit.MaxHeaderBytes), "outgoing_git.max_stderr_bytes": int64(c.OutgoingGit.MaxStderrBytes),
		"command_help.timeout": int64(c.CommandHelp.Timeout), "command_help.invocation_timeout": int64(c.CommandHelp.InvocationTimeout), "command_help.max_invocations": int64(c.CommandHelp.MaxInvocations), "command_help.max_depth": int64(c.CommandHelp.MaxDepth), "command_help.max_output_bytes": int64(c.CommandHelp.MaxOutputBytes), "command_help.max_total_output_bytes": int64(c.CommandHelp.MaxTotalOutputBytes),
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		if value < 0 {
			return fmt.Errorf("config: %s must not be negative", key)
		}
	}
	return nil
}
