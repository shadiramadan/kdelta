package agent

import "testing"

func TestEffortFromEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "unset", value: "", want: ""},
		{name: "valid level", value: "medium", want: "medium"},
		{name: "normalized", value: "  XHigh ", want: "xhigh"},
		{name: "unrecognized falls back to default", value: "turbo", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KDELTA_EFFORT", tt.value)
			if got := effortFromEnv(); got != tt.want {
				t.Errorf("effortFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}
