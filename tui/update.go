package main

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

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
	if sel := m.selectedRow(); sel.Path != "" {
		m.syncPivotTreeToRow(sel)
	}
	for _, kind := range []treeMode{treePath, treeIdentifier} {
		m.treeIndex[kind] = alignTreeCursor(m.visibleTreeItems(kind), m.activeTreeFilter[kind], m.treeIndex[kind])
	}
	if m.activeTreeFilter[m.treeKind] == "" {
		m.activeTreeFilter[m.treeKind] = m.currentTreeSelectionPrefix(m.treeKind)
	}
	m.statusLine = fmt.Sprintf("Loaded %d/%d repos", len(m.rows), m.totalCount)
	m.syncPreviewContent()
	if sel := m.selectedRow(); sel.Path != "" {
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
	if key.Matches(msg, m.keys.CycleTree) && (m.focus == focusTree || m.focus == focusRepos) && !m.filtering {
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
	m.anchorTreeModeToSelectedRepo(kind)
	if m.filtering {
		m.filterInput.SetValue(m.currentPaneFilterValue())
		m.filterInput.CursorEnd()
	}
	m.applyFocus()
	m.syncPreviewContent()
	return m, nil
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
		sel := m.selectedRow()
		m.pivotTreesToRow(sel, true)
		if sel.Path == "" {
			return m, nil
		}
		if m.treeKind == treePath {
			return m, func() tea.Msg {
				err := m.client.openTerminal(sel.Path)
				return actionMsg{label: "Opened yazi for " + sel.Path, err: err}
			}
		}
		target := identifierToBrowserURL(sel.Identifier)
		if target == "" {
			m.statusLine = "Selected repository has no browser link"
			return m, m.fetchDetailsForSelection()
		}
		return m, func() tea.Msg {
			err := m.client.openBrowser(target)
			return actionMsg{label: "Opened " + target, err: err}
		}
	}
	return m, nil
}
