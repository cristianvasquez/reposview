package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetchStatusCmd(), tickCmd())

	case rowsMsg:
		return m.handleRowsMsg(msg)

	case detailsMsg:
		if msg.err != nil {
			m.lastDetails = repoDetailsResponse{OK: false, Error: msg.err.Error(), Path: msg.path}
		} else {
			m.lastDetails = msg.data
		}
		m.syncPreviewContent()
		return m, nil

	case statusMsg:
		if msg.err != nil {
			m.errLine = msg.err.Error()
			return m, nil
		}
		m.lastStatus = msg.data
		if msg.data.LastRunAt != "" && msg.data.LastRunAt != m.lastRunAt {
			m.lastRunAt = msg.data.LastRunAt
			return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
		}
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.errLine = msg.err.Error()
			return m, nil
		}
		m.errLine = ""
		m.statusLine = msg.label
		return m, nil

	case fzfResultMsg:
		return m.handleFzfResult(msg)

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	return m, nil
}

func (m model) handleRowsMsg(msg rowsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loading = false
		m.errLine = msg.err.Error()
		return m, nil
	}

	selectedPath := m.selectedPath
	m.loading = false
	m.errLine = ""
	m.allRows = msg.data.Rows
	m.totalCount = msg.data.TotalCount
	m.databaseTotal = msg.data.DatabaseTotal
	m.facets = msg.data.Facets
	pathTree := m.treeData[treePath]
	pathTree.setItems(buildTreeItems(msg.data.Facets.LocalPathTree))
	m.treeData[treePath] = pathTree
	identifierTree := m.treeData[treeIdentifier]
	identifierTree.setItems(buildTreeItems(msg.data.Facets.IdentifierTree))
	m.treeData[treeIdentifier] = identifierTree
	m.applyTreePaneFilters()
	m.applyRepoFilter(selectedPath)
	for _, kind := range []treeMode{treePath, treeIdentifier} {
		m.treeIndex[kind] = alignTreeCursor(m.visibleTreeItems(kind), m.activeTreeFilter[kind], m.treeIndex[kind])
	}
	if m.activeTreeFilter[m.treeKind] == "" {
		m.activeTreeFilter[m.treeKind] = m.currentTreeSelectionPrefix(m.treeKind)
	}
	m.statusLine = fmt.Sprintf("Loaded %d/%d repos", len(m.rows), m.totalCount)
	m.syncPreviewContent()
	if sel := m.selectedRow(); sel.Path != "" {
		m.syncPivotTreeToRow(sel)
		return m, m.fetchDetailsCmd(sel.Path)
	}
	return m, nil
}

func (m model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Quit) {
		return m, tea.Quit
	}
	if key.Matches(msg, m.keys.PathTree) {
		return m.switchTreeMode(treePath)
	}
	if key.Matches(msg, m.keys.IdentTree) {
		return m.switchTreeMode(treeIdentifier)
	}
	if key.Matches(msg, m.keys.CycleTree) && m.focus == focusTree && !m.filtering {
		if m.treeKind == treePath {
			return m.switchTreeMode(treeIdentifier)
		}
		return m.switchTreeMode(treePath)
	}
	if m.filtering {
		return m.handleFilterInput(msg)
	}
	if key.Matches(msg, m.keys.Left) {
		m.focus = clampPane(m.focus - 1)
		if m.focus == focusTree {
			m.pivotTreesToRow(m.selectedRow(), true)
		}
		m.applyFocus()
		return m, nil
	}
	if key.Matches(msg, m.keys.Right) {
		m.focus = clampPane(m.focus + 1)
		if m.focus == focusTree {
			m.pivotTreesToRow(m.selectedRow(), true)
		}
		m.applyFocus()
		return m, nil
	}
	if key.Matches(msg, m.keys.Filter) {
		if m.focus == focusPreview {
			m.statusLine = "Preview pane has no filter mode"
			return m, nil
		}
		if fzfAvailable() {
			m.statusLine = "Opening fzf..."
			return m, m.openFzfFilterCmd()
		}
		m.filtering = true
		m.filterOriginal = m.currentPaneFilterValue()
		m.filterInput.SetValue(m.currentPaneFilterValue())
		m.filterInput.CursorEnd()
		m.statusLine = "fzf not found, using inline filter"
		m.applyFocus()
		return m, nil
	}
	if key.Matches(msg, m.keys.Refresh) {
		m.statusLine = "Refreshing..."
		return m, tea.Batch(m.fetchRowsCmd(), m.fetchStatusCmd(), m.fetchDetailsForSelection())
	}
	if key.Matches(msg, m.keys.Sync) {
		return m, func() tea.Msg {
			err := m.client.triggerSync()
			return actionMsg{label: "Sync requested", err: err}
		}
	}
	if key.Matches(msg, m.keys.Open) {
		sel := m.selectedRow()
		if sel.Path == "" {
			return m, nil
		}
		return m, func() tea.Msg {
			err := m.client.openTerminal(sel.Path)
			return actionMsg{label: "Opened terminal for " + sel.Path, err: err}
		}
	}
	if key.Matches(msg, m.keys.Cancel) {
		if m.focus != focusPreview && m.currentPaneFilterValue() != "" {
			m.setCurrentPaneFilterValue("")
			m.applyCurrentPaneFilter()
			if m.focus == focusTree {
				return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
			}
		}
		return m, nil
	}

	switch m.focus {
	case focusTree:
		return m.updateTree(msg)
	case focusRepos:
		return m.updateRepos(msg)
	case focusPreview:
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m model) switchTreeMode(kind treeMode) (tea.Model, tea.Cmd) {
	m.treeKind = kind
	m.focus = focusTree
	m.anchorTreeModeToSelectedRepo(kind)
	if m.filtering {
		m.filterInput.SetValue(m.currentPaneFilterValue())
		m.filterInput.CursorEnd()
	}
	m.applyFocus()
	m.syncPreviewContent()
	return m, nil
}

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

func (m model) updateTree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.visibleTreeItems(m.treeKind)
	if len(items) == 0 {
		return m, nil
	}
	index := m.treeIndex[m.treeKind]
	switch msg.String() {
	case "up", "k":
		m.treeIndex[m.treeKind] = clamp(index-1, 0, len(items)-1)
		m.activeTreeFilter[m.treeKind] = m.currentTreeSelectionPrefix(m.treeKind)
		return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
	case "down", "j":
		m.treeIndex[m.treeKind] = clamp(index+1, 0, len(items)-1)
		m.activeTreeFilter[m.treeKind] = m.currentTreeSelectionPrefix(m.treeKind)
		return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
	case "enter":
		m.activeTreeFilter[m.treeKind] = m.currentTreeSelectionPrefix(m.treeKind)
		m.statusLine = fmt.Sprintf("%s selection: %s", m.treeKind, m.activeTreeFilter[m.treeKind])
		return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
	case " ":
		current := items[clamp(index, 0, len(items)-1)]
		data := m.treeData[m.treeKind]
		collapsed := data.toggleCollapsed(current.Prefix)
		m.treeData[m.treeKind] = data
		m.treeIndex[m.treeKind] = alignTreeCursor(m.visibleTreeItems(m.treeKind), current.Prefix, m.treeIndex[m.treeKind])
		if m.treeData[m.treeKind].hasChildren(current.Prefix) {
			state := "expanded"
			if collapsed {
				state = "collapsed"
			}
			m.statusLine = fmt.Sprintf("%s %s: %s", m.treeKind, state, current.Prefix)
		}
		return m, nil
	}
	return m, nil
}

func (m model) updateRepos(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		m.setSelectedRepoIndex(clamp(m.repoIndex-1, 0, len(m.rows)-1))
		m.pivotTreesToRow(m.selectedRow(), true)
		m.syncPreviewContent()
		return m, m.fetchDetailsForSelection()
	case "down", "j":
		m.setSelectedRepoIndex(clamp(m.repoIndex+1, 0, len(m.rows)-1))
		m.pivotTreesToRow(m.selectedRow(), true)
		m.syncPreviewContent()
		return m, m.fetchDetailsForSelection()
	case "enter":
		m.pivotTreesToRow(m.selectedRow(), true)
		return m, m.fetchDetailsForSelection()
	}
	return m, nil
}

func (m *model) resize() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	top := 5
	bottom := 4
	bodyHeight := max(6, m.height-top-bottom)
	_, _, rightWidth := layoutColumns(m.width)
	m.preview.Width = max(10, rightWidth-4)
	m.preview.Height = max(3, bodyHeight-4)
}

func (m *model) applyFocus() {
	if m.filtering {
		m.filterInput.Focus()
	} else {
		m.filterInput.Blur()
	}
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

func (m model) afterFilterChangeCmd() tea.Cmd {
	if m.focus == focusTree {
		return tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
	}
	return m.fetchDetailsForSelection()
}

func (m model) handleFzfResult(msg fzfResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errLine = msg.err.Error()
		return m, nil
	}
	if msg.cancelled {
		m.statusLine = "fzf cancelled"
		return m, nil
	}

	m.errLine = ""
	m.focus = msg.focus
	m.treeKind = msg.treeKind

	switch msg.focus {
	case focusTree:
		if msg.selection == "" {
			m.statusLine = "No tree item selected"
			return m, nil
		}
		data := m.treeData[msg.treeKind]
		data.expandToPrefix(msg.selection)
		m.treeData[msg.treeKind] = data
		m.activeTreeFilter[msg.treeKind] = msg.selection
		m.treeIndex[msg.treeKind] = alignTreeCursor(m.visibleTreeItems(msg.treeKind), msg.selection, m.treeIndex[msg.treeKind])
		m.applyFocus()
		m.syncPreviewContent()
		m.statusLine = fmt.Sprintf("%s jump: %s", msg.treeKind, msg.selection)
		return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
	case focusRepos:
		if msg.selection == "" {
			m.statusLine = "No repository selected"
			return m, nil
		}
		for i, repo := range m.rows {
			if repo.Path == msg.selection {
				m.setSelectedRepoIndex(i)
				m.pivotTreesToRow(m.selectedRow(), true)
				m.applyFocus()
				m.syncPreviewContent()
				m.statusLine = "repo jump: " + msg.selection
				return m, m.fetchDetailsForSelection()
			}
		}
		for i, repo := range m.allRows {
			if repo.Path == msg.selection {
				m.rows = append([]row(nil), m.allRows...)
				m.repoFilter = ""
				m.setSelectedRepoIndex(i)
				m.pivotTreesToRow(m.selectedRow(), true)
				m.applyFocus()
				m.syncPreviewContent()
				m.statusLine = "repo jump: " + msg.selection
				return m, m.fetchDetailsForSelection()
			}
		}
		m.pivotTreesToRow(m.selectedRow(), true)
		m.applyFocus()
		m.syncPreviewContent()
		m.statusLine = "Selected repository is not available"
		return m, nil
	default:
		return m, nil
	}
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

func (m model) selectedTreeItem(kind treeMode) treeItem {
	items := m.visibleTreeItems(kind)
	if len(items) == 0 {
		return treeItem{}
	}
	index := clamp(m.treeIndex[kind], 0, len(items)-1)
	return items[index]
}

func (m model) currentTreeSelectionPrefix(kind treeMode) string {
	return m.selectedTreeItem(kind).Prefix
}

func (m model) selectedRow() row {
	if len(m.rows) == 0 {
		return row{}
	}
	if m.selectedPath != "" {
		for i, candidate := range m.rows {
			if candidate.Path == m.selectedPath {
				return m.rows[i]
			}
		}
	}
	return m.rows[clamp(m.repoIndex, 0, len(m.rows)-1)]
}

func (m model) fetchDetailsForSelection() tea.Cmd {
	sel := m.selectedRow()
	if sel.Path == "" {
		return nil
	}
	return m.fetchDetailsCmd(sel.Path)
}

func (m *model) syncPreviewContent() {
	sel := m.selectedRow()
	if sel.Path == "" {
		m.preview.SetContent("No repository selected.")
		return
	}
	lines := []string{
		"path: " + sel.Path,
		"identifier: " + emptyFallback(sel.Identifier),
		"branch: " + emptyFallback(sel.Branch),
		"author: " + emptyFallback(sel.LastCommitAuthor),
		"last commit: " + emptyFallback(sel.LastCommitAt),
		"last seen: " + emptyFallback(sel.LastSeenAt),
		"",
	}

	if m.lastDetails.Path != sel.Path {
		lines = append(lines, "README: loading...")
		m.preview.SetContent(strings.Join(lines, "\n"))
		return
	}

	if !m.lastDetails.OK && m.lastDetails.Error != "" {
		lines = append(lines, "details error: "+m.lastDetails.Error)
		m.preview.SetContent(strings.Join(lines, "\n"))
		return
	}

	if m.lastDetails.Readme.Exists {
		lines = append(lines, "README: "+m.lastDetails.Readme.Path, "")
		lines = append(lines, stripMarkdown(m.lastDetails.Readme.Content))
		if m.lastDetails.Readme.Truncated {
			lines = append(lines, "", "[README truncated]")
		}
	} else {
		lines = append(lines, "README: not found")
	}

	m.preview.SetContent(strings.Join(lines, "\n"))
}

func (m *model) syncPivotTreeToRow(selected row) {
	m.pivotTreesToRow(selected, false)
}

func (m *model) anchorTreeModeToSelectedRepo(kind treeMode) {
	selected := m.selectedRow()
	if selected.Path == "" && selected.Identifier == "" {
		return
	}

	if prefix := bestTreePrefixForRow(m.treeData[kind].all, kind, selected); prefix != "" {
		m.activeTreeFilter[kind] = prefix

		data := m.treeData[kind]
		data.expandToPrefix(prefix)
		m.treeData[kind] = data
		m.treeIndex[kind] = alignTreeCursor(m.visibleTreeItems(kind), prefix, m.treeIndex[kind])
	}
}

func (m *model) pivotTreesToRow(selected row, includeActive bool) {
	if selected.Path == "" && selected.Identifier == "" {
		return
	}
	for _, kind := range []treeMode{treePath, treeIdentifier} {
		if !includeActive && kind == m.treeKind {
			continue
		}
		prefix := bestTreePrefixForRow(m.treeData[kind].all, kind, selected)
		if prefix == "" {
			continue
		}
		m.activeTreeFilter[kind] = prefix
		data := m.treeData[kind]
		data.expandToPrefix(prefix)
		m.treeData[kind] = data
		m.treeIndex[kind] = alignTreeCursor(m.visibleTreeItems(kind), prefix, m.treeIndex[kind])
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

func (m model) visibleTreeItems(kind treeMode) []treeItem {
	return m.treeData[kind].visible
}

func (m *model) setSelectedRepoIndex(index int) {
	if len(m.rows) == 0 {
		m.repoIndex = 0
		m.selectedPath = ""
		return
	}
	m.repoIndex = clamp(index, 0, len(m.rows)-1)
	m.selectedPath = m.rows[m.repoIndex].Path
}
