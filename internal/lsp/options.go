package lsp

import "github.com/dotcommander/pan/internal/config"

// TransportOptions bounds framing before allocating server-controlled data.
type TransportOptions struct {
	MaxFrameBytes      int
	MaxHeaderLineBytes int
	MaxHeaderBytes     int
	MaxHeaders         int
}

func transportOptions(r config.LspRules) TransportOptions {
	r = r.Normalized()
	return TransportOptions{r.MaxFrameBytes, r.MaxHeaderLineBytes, r.MaxHeaderBytes, r.MaxHeaders}
}

func (o TransportOptions) normalized() TransportOptions {
	d := transportOptions(config.LspRules{})
	if o.MaxFrameBytes <= 0 {
		o.MaxFrameBytes = d.MaxFrameBytes
	}
	if o.MaxHeaderLineBytes <= 0 {
		o.MaxHeaderLineBytes = d.MaxHeaderLineBytes
	}
	if o.MaxHeaderBytes <= 0 {
		o.MaxHeaderBytes = d.MaxHeaderBytes
	}
	if o.MaxHeaders <= 0 {
		o.MaxHeaders = d.MaxHeaders
	}
	return o
}
