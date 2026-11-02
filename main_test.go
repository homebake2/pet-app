package main

import "testing"

func TestSentryEnvironment(t *testing.T) {
	tests := []struct {
		name   string
		sentry string
		render string
		want   string
	}{
		{"явное значение важнее платформы", "staging", "true", "staging"},
		{"на Render по умолчанию production", "", "true", "production"},
		{"локально по умолчанию development", "", "", "development"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SENTRY_ENVIRONMENT", tt.sentry)
			t.Setenv("RENDER", tt.render)
			if got := sentryEnvironment(); got != tt.want {
				t.Fatalf("sentryEnvironment() = %q, want %q", got, tt.want)
			}
		})
	}
}
