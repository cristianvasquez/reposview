package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type fzfItem struct {
	ID    string
	Label string
}

func fzfAvailable() bool {
	_, err := exec.LookPath("fzf")
	return err == nil
}

func (m model) openFzfFilterCmd() tea.Cmd {
	focus := m.focus
	kind := m.treeKind

	items, prompt := m.fzfItemsForCurrentPane()
	if len(items) == 0 {
		return func() tea.Msg {
			return fzfResultMsg{
				focus:    focus,
				treeKind: kind,
				err:      errors.New("nothing to filter in the current pane"),
			}
		}
	}

	inputFile, err := os.CreateTemp("", "reposview-fzf-input-*.txt")
	if err != nil {
		return func() tea.Msg { return fzfResultMsg{focus: focus, treeKind: kind, err: err} }
	}
	inputPath := inputFile.Name()
	defer inputFile.Close()

	for _, item := range items {
		if _, err := fmt.Fprintf(inputFile, "%s\t%s\n", item.ID, item.Label); err != nil {
			os.Remove(inputPath)
			return func() tea.Msg { return fzfResultMsg{focus: focus, treeKind: kind, err: err} }
		}
	}

	outputFile, err := os.CreateTemp("", "reposview-fzf-output-*.txt")
	if err != nil {
		os.Remove(inputPath)
		return func() tea.Msg { return fzfResultMsg{focus: focus, treeKind: kind, err: err} }
	}
	outputPath := outputFile.Name()

	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		os.Remove(inputPath)
		os.Remove(outputPath)
		return func() tea.Msg { return fzfResultMsg{focus: focus, treeKind: kind, err: err} }
	}

	args := fzfArgs(prompt)
	args = append(args,
		"--print-query",
		"--delimiter", "\t",
		"--with-nth=2..",
	)
	cmd := exec.Command(fzfPath, args...)
	cmd.Env = append(os.Environ(), "FZF_DEFAULT_COMMAND=cat "+shellQuote(inputPath))
	cmd.Stdout = outputFile

	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		outputFile.Close()
		defer os.Remove(inputPath)
		defer os.Remove(outputPath)

		msg := fzfResultMsg{focus: focus, treeKind: kind}
		if runErr != nil {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 130 {
				msg.cancelled = true
				return msg
			}
			msg.err = runErr
			return msg
		}

		query, selection, parseErr := parseFzfOutput(outputPath)
		if parseErr != nil {
			msg.err = parseErr
			return msg
		}
		msg.query = query
		msg.selection = selection
		return msg
	})
}

func (m model) fzfItemsForCurrentPane() ([]fzfItem, string) {
	switch m.focus {
	case focusTree:
		items := make([]fzfItem, 0, len(m.treeData[m.treeKind].all))
		for _, item := range m.treeData[m.treeKind].all {
			indent := strings.Repeat("  ", max(0, item.Depth-1))
			label := fmt.Sprintf("%s%s (%d)  [%s]", indent, item.Label, item.Count, item.Prefix)
			items = append(items, fzfItem{ID: item.Prefix, Label: label})
		}
		return items, fmt.Sprintf("%s/%s> ", m.focus, m.treeKind)
	case focusRepos:
		items := make([]fzfItem, 0, len(m.allRows))
		for _, row := range m.allRows {
			label := row.Path
			if row.Branch != "" {
				label += " [" + row.Branch + "]"
			}
			if row.Identifier != "" {
				label += "  {" + row.Identifier + "}"
			}
			items = append(items, fzfItem{ID: row.Path, Label: label})
		}
		return items, fmt.Sprintf("%s> ", m.focus)
	default:
		return nil, ""
	}
}

func parseFzfOutput(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	lines := make([]string, 0, 2)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if len(lines) == 0 {
		return "", "", nil
	}

	query := lines[0]
	selection := ""
	if len(lines) > 1 {
		selection = strings.SplitN(lines[1], "\t", 2)[0]
	}
	return strings.TrimSpace(query), strings.TrimSpace(selection), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func fzfArgs(prompt string) []string {
	args := []string{
		"--prompt", prompt,
		"--border",
	}
	if os.Getenv("TMUX") != "" {
		return append(args, "--tmux=center,70%,60%,border-native")
	}
	return append(args,
		"--height=80%",
		"--layout=reverse",
	)
}
