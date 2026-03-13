package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func (m model) handleFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Up) {
		return m, m.navigateFilteredSelection(-1)
	}
	if key.Matches(msg, m.keys.Down) {
		return m, m.navigateFilteredSelection(1)
	}

	before := m.filterInput.Value()
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	if key.Matches(msg, m.keys.Cancel) {
		m.setCurrentPaneFilterValue(m.filterOriginal)
		m.filtering = false
		m.filterInput.SetValue(m.filterOriginal)
		m.applyCurrentPaneFilter()
		m.applyFocus()
		return m, m.afterFilterChangeCmd()
	}
	if key.Matches(msg, m.keys.Apply) {
		m.applyCurrentPaneFilter()
		m.filtering = false
		m.applyFocus()
		return m, m.afterFilterChangeCmd()
	}
	if m.filterInput.Value() != before {
		m.setCurrentPaneFilterValue(strings.TrimSpace(m.filterInput.Value()))
		m.applyCurrentPaneFilter()
		return m, tea.Batch(cmd, m.afterFilterChangeCmd())
	}
	return m, cmd
}

func (m model) currentPaneFilterValue() string {
	switch m.focus {
	case focusTree:
		return m.treePaneFilter[m.treeKind]
	case focusRepos:
		return m.repoFilter
	default:
		return ""
	}
}

func (m *model) setCurrentPaneFilterValue(value string) {
	switch m.focus {
	case focusTree:
		m.treePaneFilter[m.treeKind] = value
	case focusRepos:
		m.repoFilter = value
	}
}

func (m *model) applyCurrentPaneFilter() {
	switch m.focus {
	case focusTree:
		m.applyTreePaneFilters()
		m.syncTreeSelectionFromFilter(m.treeKind)
		m.treeIndex[m.treeKind] = alignTreeCursor(m.visibleTreeItems(m.treeKind), m.activeTreeFilter[m.treeKind], m.treeIndex[m.treeKind])
	case focusRepos:
		m.applyRepoFilter(m.selectedRow().Path)
	}
	m.syncPreviewContent()
}

func (m *model) applyTreePaneFilters() {
	for _, kind := range []treeMode{treePath, treeIdentifier} {
		data := m.treeData[kind]
		data.applyFilter(m.treePaneFilter[kind])
		m.treeData[kind] = data
	}
}

func (m *model) applyRepoFilter(selectedPath string) {
	filtered := fuzzyFilterRows(m.allRows, m.repoFilter)
	m.rows = filtered
	if len(m.rows) == 0 {
		m.repoIndex = 0
		m.selectedPath = ""
		return
	}
	if selectedPath == "" {
		selectedPath = m.selectedPath
	}
	for i, candidate := range m.rows {
		if candidate.Path == selectedPath {
			m.setSelectedRepoIndex(i)
			return
		}
	}
	m.setSelectedRepoIndex(0)
}

func (m *model) navigateFilteredSelection(delta int) tea.Cmd {
	switch m.focus {
	case focusTree:
		items := m.visibleTreeItems(m.treeKind)
		if len(items) == 0 {
			return nil
		}
		m.treeIndex[m.treeKind] = clamp(m.treeIndex[m.treeKind]+delta, 0, len(items)-1)
		return nil
	case focusRepos:
		if len(m.rows) == 0 {
			return nil
		}
		m.setSelectedRepoIndex(clamp(m.repoIndex+delta, 0, len(m.rows)-1))
		m.pivotTreesToRow(m.selectedRow(), true)
		m.syncPreviewContent()
		return m.fetchDetailsForSelection()
	default:
		return nil
	}
}

func (m *model) syncTreeSelectionFromFilter(kind treeMode) {
	visible := m.visibleTreeItems(kind)
	if len(visible) == 0 {
		m.activeTreeFilter[kind] = ""
		m.treeIndex[kind] = 0
		return
	}

	prefix := bestTreeItemForQuery(visible, m.treePaneFilter[kind])
	if prefix == "" {
		prefix = bestTreeItemForQuery(m.treeData[kind].filtered, m.treePaneFilter[kind])
	}
	if prefix == "" {
		prefix = visible[0].Prefix
	}
	data := m.treeData[kind]
	data.expandToPrefix(prefix)
	m.treeData[kind] = data
	m.activeTreeFilter[kind] = prefix
	m.treeIndex[kind] = alignTreeCursor(m.visibleTreeItems(kind), prefix, m.treeIndex[kind])
}
