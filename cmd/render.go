package cmd

import (
	"strings"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

// Plain-text rendering helpers shared by the commands. Richer terminal
// rendering is on the roadmap; these keep output stable and pipeable.

func changeTypeLabel(t kdeltav1.ChangeType) string {
	return strings.TrimPrefix(t.String(), "CHANGE_TYPE_")
}

func severityLabel(s kdeltav1.ImpactSeverity) string {
	return strings.TrimPrefix(s.String(), "IMPACT_SEVERITY_")
}

func primaryStream(r *kdeltav1.Resource) *kdeltav1.VersionStream {
	if streams := r.GetStreams(); len(streams) > 0 {
		return streams[0]
	}
	return nil
}
