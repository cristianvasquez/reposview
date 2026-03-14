package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	apiOrigin := flag.String("api-origin", "", "Optional inspect API origin; when empty the TUI reads the database directly")
	spawnAPI := flag.Bool("spawn-api", false, "Start the inspect API automatically when using --api-origin")
	dbPath := flag.String("db", "", "SQLite database path for local mode (default: ../data/reposview.sqlite)")
	scanner := flag.String("scanner", "auto", "Scanner mode for local sync or spawned API")
	flag.Parse()
	initialPathFilter, err := resolveSelectionTarget(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var client *apiClient
	if strings.TrimSpace(*apiOrigin) != "" {
		client = newAPIClient(*apiOrigin)
		server, err := ensureAPI(client, *spawnAPI, *dbPath, *scanner)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if server != nil {
			defer server.Stop()
		}
	} else {
		client, err = newLocalAPIClient(*dbPath, *scanner)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	p := tea.NewProgram(newModel(client, initialPathFilter), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func resolveSelectionTarget(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("usage: r [path]")
	}
	target := "."
	if len(args) == 1 {
		target = args[0]
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absTarget); err == nil {
		return resolved, nil
	}
	return absTarget, nil
}
