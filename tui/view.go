package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
)

var (
	titleStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	metaStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	activeText         = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	repoText           = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	previewHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("223"))
	previewLabelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("109"))
	previewValueStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	previewMutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func (m model) View() string {
	if m.width < 72 || m.height < 16 {
		return "Terminal too small for reposview TUI. Need at least 72x16."
	}

	if m.showHelp {
		return m.renderHelpOverlay()
	}

	bodyHeight := max(6, m.height-8)
	leftWidth, middleWidth, rightWidth := layoutColumns(m.width)

	header := m.renderHeader()
	searchPane := m.renderFilterBar(m.width)
	left := m.renderTree(leftWidth, bodyHeight)
	middle := m.renderRepos(middleWidth, bodyHeight)
	right := m.renderPreview(rightWidth, bodyHeight)
	rowView := lipgloss.JoinHorizontal(lipgloss.Top, left, middle, right)

	footerText := m.errLine
	if footerText == "" {
		footerText = m.statusLine
	}
	if footerText == "" {
		footerText = m.promptLine
	}
	footer := panelStyle(false).Width(m.width - 2).Render(truncateText(footerText, max(10, m.width-6)))
	helpView := lipgloss.NewStyle().Width(m.width).Render(m.help.View(m.keys))

	return lipgloss.JoinVertical(lipgloss.Left, header, searchPane, rowView, footer, helpView)
}

func (m model) renderHeader() string {
	parts := []string{
		fmt.Sprintf("repos %d/%d/%d", len(m.rows), m.totalCount, m.databaseTotal),
		fmt.Sprintf("focus %s", m.focus),
		fmt.Sprintf("tree %s", m.treeKind),
	}

	if m.loading {
		parts = append(parts, "loading")
	}
	if m.lastStatus.Running {
		parts = append(parts, fmt.Sprintf("sync %s %d/%d", emptyFallback(m.lastStatus.Phase), m.lastStatus.ProcessedGitDirs, m.lastStatus.DiscoveredGitDirs))
	} else if m.lastStatus.Error != "" {
		parts = append(parts, "sync error")
	} else if m.lastStatus.LastRunAt != "" {
		parts = append(parts, "last sync "+relativeTime(m.lastStatus.LastRunAt))
	}

	title := titleStyle.Render("reposview tui")
	meta := metaStyle.Render(strings.Join(parts, "  |  "))
	return lipgloss.JoinVertical(lipgloss.Left, title, meta)
}

func (m model) renderFilterBar(width int) string {
	style := panelStyle(m.filtering).Width(width - 2)
	current := m.currentPaneFilterValue()
	label := fmt.Sprintf("Pane %s filter", m.focus)
	if m.focus == focusTree {
		label = fmt.Sprintf("Pane %s/%s filter", m.focus, m.treeKind)
	}
	contentWidth := max(10, width-6)
	if m.filtering {
		content := label + ": " + m.filterInput.Value()
		if m.filterInput.Cursor.Blink {
			content += "_"
		}
		return style.Render(truncateText(content, contentWidth))
	}
	if current == "" {
		current = "(none)"
	}
	return style.Render(truncateText(label+": "+current, contentWidth))
}

func (m model) renderTree(width, height int) string {
	items := m.visibleTreeItems(m.treeKind)
	lines := make([]string, 0, height)
	pathLabel := "path"
	identLabel := "identifier"
	if m.treeKind == treePath {
		pathLabel = "[PATH]"
	} else {
		identLabel = "[IDENTIFIER]"
	}
	title := fmt.Sprintf("Tree %s | %s", pathLabel, identLabel)
	active := m.currentTreeSelectionPrefix(m.treeKind)
	if active != "" {
		title += " [" + truncateText(active, max(8, width-18)) + "]"
	}
	lines = append(lines, truncateText(title, max(10, width-4)))
	if len(items) == 0 {
		lines = append(lines, "", "No facet nodes.")
		return panelStyle(m.focus == focusTree).Width(width).Height(height).Render(strings.Join(lines, "\n"))
	}

	index := clamp(m.treeIndex[m.treeKind], 0, len(items)-1)
	start, end := windowBounds(index, len(items), height-2)
	for i := start; i < end; i++ {
		item := items[i]
		indent := strings.Repeat("  ", max(0, item.Depth-1))
		label := fmt.Sprintf("%s%s%s (%d)", indent, m.treeGlyph(m.treeKind, item), item.Label, item.Count)
		label = truncateText(label, max(8, width-3))
		if i == index {
			label = activeText.Render(label)
		}
		lines = append(lines, label)
	}
	return panelStyle(m.focus == focusTree).Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m model) renderRepos(width, height int) string {
	lines := []string{}
	if len(m.rows) == 0 {
		lines = append(lines, "No repositories match the current filters.")
		return panelStyle(m.focus == focusRepos).Width(width).Height(height).Render(strings.Join(lines, "\n"))
	}
	start, end := windowBounds(m.repoIndex, len(m.rows), height)
	for i := start; i < end; i++ {
		r := m.rows[i]
		summary := compactPathLabel(r.Path, 2)
		if m.treeKind == treeIdentifier {
			summary = compactIdentifierLabel(r.Identifier, 2)
		}
		if m.treeKind == treePath && r.Branch != "" {
			summary += " [" + r.Branch + "]"
		}
		summary = truncateText(summary, max(10, width-3))
		if i == m.repoIndex {
			summary = repoText.Render(summary)
		}
		lines = append(lines, summary)
	}
	return panelStyle(m.focus == focusRepos).Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m model) renderPreview(width, height int) string {
	header := previewHeaderStyle.Render("Details")
	content := m.preview.View()
	return panelStyle(m.focus == focusPreview).Width(width).Height(height).Render(header + "\n" + content)
}

func (m model) treeGlyph(kind treeMode, item treeItem) string {
	if m.treeData[kind].hasChildren(item.Prefix) {
		if m.treeData[kind].collapsed[item.Prefix] {
			return "▸ "
		}
		return "▾ "
	}
	if item.Depth <= 1 {
		return ""
	}
	return "· "
}

func panelStyle(active bool) lipgloss.Style {
	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Padding(0, 1)
	if active {
		return style.BorderForeground(lipgloss.Color("86"))
	}
	return style.BorderForeground(lipgloss.Color("240"))
}

func layoutColumns(total int) (int, int, int) {
	usable := max(60, total-4)
	left := max(18, usable*28/100)
	middle := max(24, usable*40/100)
	right := usable - left - middle
	if right < 18 {
		deficit := 18 - right
		if middle-deficit/2 >= 24 {
			middle -= deficit / 2
		}
		if left-(deficit-deficit/2) >= 18 {
			left -= deficit - deficit/2
		}
		right = usable - left - middle
	}
	if right < 18 {
		right = 18
		middle = max(24, usable-right-left)
	}
	return left, middle, usable - left - middle
}

func windowBounds(index, length, window int) (int, int) {
	start := max(0, index-window/2)
	end := min(length, start+window)
	start = max(0, end-window)
	return start, end
}

func truncateText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return truncate.StringWithTail(strings.ReplaceAll(s, "\n", " "), uint(width), "…")
}

func (m model) renderHelpOverlay() string {
	lines := []string{
		titleStyle.Render("reposview help"),
		"",
		"Navigation",
		"  left/right: move focus across tree, repos, preview",
		"  up/down: move inside the focused pane",
		"  tab: switch between path tree and identifier tree",
		"  p / i: jump directly to path tree or identifier tree",
		"",
		"Actions",
		"  enter on tree: apply the selected tree prefix",
		"  space on tree: collapse or expand a tree branch",
		"  enter on repos in path mode: open yazi",
		"  enter on repos in identifier mode: open browser URL",
		"  t: open terminal in selected repository",
		"  o: toggle OSG connection for selected repository",
		"  l: list configured OSG repositories with fzf",
		"  s: run sync against the database",
		"  r: refresh rows, details, and status",
		"",
		"Filtering",
		"  f: filter the current pane",
		"  esc: clear current pane filter or leave inline filter mode",
		"  tree filters narrow the repository list",
		"  repo filters narrow only the visible list",
		"",
		"Help",
		"  ? / h: toggle this help",
		"  esc / q: close help",
	}

	contentWidth := max(40, m.width-8)
	boxWidth := min(contentWidth, 88)
	content := panelStyle(true).
		Width(boxWidth).
		Height(max(12, m.height-4)).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func relativeTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
