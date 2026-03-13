package main

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func renderPromptCmd(sel row, width int) tea.Cmd {
	path := strings.TrimSpace(sel.Path)
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		prompt, err := renderRepoPrompt(path, width)
		return promptMsg{path: path, prompt: prompt, err: err}
	}
}

func renderRepoPrompt(path string, width int) (string, error) {
	ompPath, err := exec.LookPath("oh-my-posh")
	if err != nil {
		return fallbackPrompt(path), nil
	}

	args := []string{
		"print", "primary",
		"--shell", promptShell(),
		"--pwd", path,
		"--terminal-width", strconv.Itoa(max(20, width)),
		"--escape=false",
		"--no-status",
	}
	if config := promptConfigPath(); config != "" {
		args = append([]string{"--config", config}, args...)
	}

	cmd := exec.Command(ompPath, args...)
	cmd.Dir = path
	cmd.Env = append(os.Environ(),
		"POSH_SHELL="+promptShell(),
		"POSH_SHELL_VERSION="+promptShellVersion(),
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fallbackPrompt(path), err
	}

	rendered := strings.TrimSpace(stdout.String())
	if rendered == "" {
		return fallbackPrompt(path), nil
	}
	return rendered, nil
}

func promptConfigPath() string {
	for _, key := range []string{"POSH_THEME", "OMP_THEME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func promptShell() string {
	if value := strings.TrimSpace(os.Getenv("POSH_SHELL")); value != "" {
		return value
	}
	return "bash"
}

func promptShellVersion() string {
	if value := strings.TrimSpace(os.Getenv("POSH_SHELL_VERSION")); value != "" {
		return value
	}
	return ""
}

func fallbackPrompt(path string) string {
	return "repo " + compactPathLabel(path, 3)
}
