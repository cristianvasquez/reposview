package main

import (
	"net/url"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchRowsCmd(), m.fetchStatusCmd(), tickCmd())
}

func (m model) fetchRowsCmd() tea.Cmd {
	query := url.Values{}
	query.Set("sort", "path")
	query.Set("dir", "asc")
	if prefix := m.activeTreeFilter[m.treeKind]; prefix != "" {
		if m.treeKind == treePath {
			query.Set("path_prefix", prefix)
		} else {
			query.Set("identifier_prefix", prefix)
		}
	}
	return func() tea.Msg {
		data, err := m.client.getRows(query)
		return rowsMsg{data: data, err: err}
	}
}

func (m model) fetchDetailsCmd(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		data, err := m.client.getRepoDetails(path)
		return detailsMsg{path: path, data: data, err: err}
	}
}

func (m model) fetchStatusCmd() tea.Cmd {
	return func() tea.Msg {
		data, err := m.client.getStatus()
		return statusMsg{data: data, err: err}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) fetchDetailsForSelection() tea.Cmd {
	sel := m.selectedRow()
	if sel.Path == "" {
		return nil
	}
	return m.fetchDetailsCmd(sel.Path)
}

func (m model) afterFilterChangeCmd() tea.Cmd {
	if m.focus == focusTree {
		return tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
	}
	return m.fetchDetailsForSelection()
}
