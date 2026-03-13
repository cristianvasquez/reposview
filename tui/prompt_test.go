package main

import (
	"os"
	"testing"
)

func TestPromptConfigPathPrefersPOSHTheme(t *testing.T) {
	t.Setenv("POSH_THEME", "/tmp/posh.json")
	t.Setenv("OMP_THEME", "/tmp/omp.json")

	got := promptConfigPath()
	if got != "/tmp/posh.json" {
		t.Fatalf("promptConfigPath = %q, want /tmp/posh.json", got)
	}
}

func TestPromptConfigPathFallsBackToOMPTheme(t *testing.T) {
	_ = os.Unsetenv("POSH_THEME")
	t.Setenv("OMP_THEME", "/tmp/omp.json")

	got := promptConfigPath()
	if got != "/tmp/omp.json" {
		t.Fatalf("promptConfigPath = %q, want /tmp/omp.json", got)
	}
}

func TestFallbackPrompt(t *testing.T) {
	got := fallbackPrompt("/workspace/src/example/reposview")
	if got != "repo src/example/reposview" {
		t.Fatalf("fallbackPrompt = %q, want repo src/example/reposview", got)
	}
}
