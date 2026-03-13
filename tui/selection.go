package main

import "strings"

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
	for i, candidate := range m.rows {
		if candidate.Path == m.selectedPath {
			return m.rows[i]
		}
	}
	return m.rows[clamp(m.repoIndex, 0, len(m.rows)-1)]
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
