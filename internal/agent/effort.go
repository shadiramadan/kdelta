package agent

import (
	"os"
	"strings"
)

// effortLevels are the reasoning-effort levels the model accepts. Lower
// levels trade reasoning depth for latency and cost.
var effortLevels = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
	"max":    true,
}

// effortFromEnv reads KDELTA_EFFORT, the shared reasoning-effort override
// for both agent backends. Unset or unrecognized values return "" and leave
// each backend on its own default.
func effortFromEnv() string {
	effort := strings.ToLower(strings.TrimSpace(os.Getenv("KDELTA_EFFORT")))
	if effortLevels[effort] {
		return effort
	}
	return ""
}
