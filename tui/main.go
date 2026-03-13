package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	apiOrigin := flag.String("api-origin", "http://127.0.0.1:8787", "Inspect API origin")
	spawnAPI := flag.Bool("spawn-api", true, "Start the inspect API automatically when needed")
	dbPath := flag.String("db", "", "SQLite database path for spawned API (default: ../data/reposview.sqlite)")
	scanner := flag.String("scanner", "auto", "Scanner mode for spawned API")
	flag.Parse()

	client := newAPIClient(*apiOrigin)
	server, err := ensureAPI(client, *spawnAPI, *dbPath, *scanner)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if server != nil {
		defer server.Stop()
	}

	p := tea.NewProgram(newModel(client), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
