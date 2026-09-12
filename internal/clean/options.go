package clean

import (
	"time"

	"github.com/dotcommander/pan/internal/config"
)

// Options configures one clean analysis or apply run. Zero-value fields fall
// back to configured CleanRules defaults, except MaxDepth and StaleDays when
// their corresponding Set field is true. Now zero means time.Now(); HomeDir
// empty means the real user home (live-binary protection).
type Options struct {
	Root           string
	MaxDepth       int
	MaxDepthSet    bool
	StaleDays      int
	StaleDaysSet   bool
	LargeFileBytes int64
	Now            time.Time
	Rules          config.CleanRules
	Exclude        []string
	HomeDir        string
}

func (o Options) maxDepth() int {
	if o.MaxDepthSet {
		return o.MaxDepth
	}
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return o.Rules.MaxDepth
}

func (o Options) staleDays() int {
	if o.StaleDaysSet {
		return o.StaleDays
	}
	if o.StaleDays > 0 {
		return o.StaleDays
	}
	return o.Rules.StaleDays
}

func (o Options) largeFileBytes() int64 {
	if o.LargeFileBytes > 0 {
		return o.LargeFileBytes
	}
	return o.Rules.LargeFileBytes
}

func (o Options) now() time.Time {
	if !o.Now.IsZero() {
		return o.Now
	}
	return time.Now()
}
