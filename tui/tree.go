package main

import (
	"slices"
	"strings"
)

type treeData struct {
	all           []treeItem
	filtered      []treeItem
	visible       []treeItem
	byPrefix      map[string]treeItem
	childrenCount map[string]int
	collapsed     map[string]bool
}

func newTreeData(items []treeItem) treeData {
	data := treeData{
		all:           append([]treeItem(nil), items...),
		filtered:      append([]treeItem(nil), items...),
		byPrefix:      make(map[string]treeItem, len(items)),
		childrenCount: make(map[string]int, len(items)),
		collapsed:     map[string]bool{},
	}
	for _, item := range items {
		data.byPrefix[item.Prefix] = item
		data.childrenCount[item.ParentPrefix]++
	}
	data.rebuildVisible()
	return data
}

func (t *treeData) setItems(items []treeItem) {
	next := newTreeData(items)
	for prefix := range t.collapsed {
		if _, ok := next.byPrefix[prefix]; ok {
			next.collapsed[prefix] = true
		}
	}
	*t = next
	t.rebuildVisible()
}

func (t *treeData) applyFilter(query string) {
	t.filtered = filterTreeItems(t.all, query)
	t.rebuildVisible()
}

func (t *treeData) rebuildVisible() {
	if len(t.filtered) == 0 {
		t.visible = nil
		return
	}
	visible := make([]treeItem, 0, len(t.filtered))
	for _, item := range t.filtered {
		if t.hasCollapsedAncestor(item) {
			continue
		}
		visible = append(visible, item)
	}
	t.visible = visible
}

func (t treeData) hasCollapsedAncestor(item treeItem) bool {
	parent := item.ParentPrefix
	for parent != "" {
		if t.collapsed[parent] {
			return true
		}
		next, ok := t.byPrefix[parent]
		if !ok {
			return false
		}
		parent = next.ParentPrefix
	}
	return false
}

func (t treeData) hasChildren(prefix string) bool {
	return t.childrenCount[prefix] > 0
}

func (t *treeData) toggleCollapsed(prefix string) bool {
	if !t.hasChildren(prefix) {
		return false
	}
	if t.collapsed[prefix] {
		delete(t.collapsed, prefix)
	} else {
		t.collapsed[prefix] = true
	}
	t.rebuildVisible()
	return t.collapsed[prefix]
}

func (t *treeData) expandToPrefix(prefix string) {
	current, ok := t.byPrefix[prefix]
	if !ok {
		return
	}
	parent := current.ParentPrefix
	for parent != "" {
		delete(t.collapsed, parent)
		next, ok := t.byPrefix[parent]
		if !ok {
			break
		}
		parent = next.ParentPrefix
	}
	t.rebuildVisible()
}

func buildTreeItems(nodes []treeNode) []treeItem {
	if len(nodes) == 0 {
		return nil
	}
	byParent := make(map[string][]treeItem)
	for _, node := range nodes {
		parent := node.ParentPrefix
		item := treeItem{
			Prefix:       node.Prefix,
			Label:        node.Label,
			Depth:        max(1, node.Depth),
			Count:        node.Count,
			ParentPrefix: parent,
		}
		byParent[parent] = append(byParent[parent], item)
	}
	for parent := range byParent {
		slices.SortFunc(byParent[parent], func(a, b treeItem) int {
			return strings.Compare(a.Label, b.Label)
		})
	}

	items := make([]treeItem, 0, len(nodes))
	var walk func(string)
	walk = func(parent string) {
		for _, item := range byParent[parent] {
			items = append(items, item)
			walk(item.Prefix)
		}
	}
	walk("")

	if len(items) > 0 {
		return items
	}

	orphans := make([]treeItem, 0, len(nodes))
	for _, children := range byParent {
		orphans = append(orphans, children...)
	}
	slices.SortFunc(orphans, func(a, b treeItem) int {
		if a.Depth != b.Depth {
			return a.Depth - b.Depth
		}
		return strings.Compare(a.Prefix, b.Prefix)
	})
	return orphans
}

func alignTreeCursor(items []treeItem, activePrefix string, fallback int) int {
	if len(items) == 0 {
		return 0
	}
	if activePrefix != "" {
		for idx, item := range items {
			if item.Prefix == activePrefix {
				return idx
			}
		}
	}
	return clamp(fallback, 0, len(items)-1)
}

func bestTreePrefixForRow(items []treeItem, kind treeMode, selected row) string {
	target := selected.Identifier
	if kind == treePath {
		target = selected.Path
	} else {
		target = identifierTreeKey(selected)
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	best := ""
	for _, item := range items {
		prefix := strings.TrimSpace(item.Prefix)
		if prefix == "" {
			continue
		}
		if target == prefix || strings.HasPrefix(target, prefix+"/") {
			if len(prefix) > len(best) {
				best = prefix
			}
		}
	}
	return best
}

func identifierTreeKey(selected row) string {
	upstream := strings.TrimSpace(selected.Identifier)
	if upstream != "" {
		return upstream
	}
	lineage := strings.TrimSpace(selected.Lineage)
	if lineage == "" {
		return "local/none"
	}
	return "local/" + lineage
}

func filterTreeItems(items []treeItem, query string) []treeItem {
	if strings.TrimSpace(query) == "" {
		return append([]treeItem(nil), items...)
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	filtered := make([]treeItem, 0, len(items))
	seen := make(map[string]bool, len(items))
	indexByPrefix := make(map[string]treeItem, len(items))
	for _, item := range items {
		indexByPrefix[item.Prefix] = item
	}
	for _, item := range items {
		haystack := strings.ToLower(item.Prefix + " " + item.Label)
		if !strings.Contains(haystack, needle) {
			continue
		}
		lineage := []treeItem{item}
		parent := item.ParentPrefix
		for parent != "" {
			parentItem, ok := indexByPrefix[parent]
			if !ok {
				break
			}
			lineage = append([]treeItem{parentItem}, lineage...)
			parent = parentItem.ParentPrefix
		}
		for _, entry := range lineage {
			if !seen[entry.Prefix] {
				seen[entry.Prefix] = true
				filtered = append(filtered, entry)
			}
		}
	}
	return filtered
}

func bestTreeItemForQuery(items []treeItem, query string) string {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" || len(items) == 0 {
		return ""
	}

	bestPrefix := ""
	bestScore := -1
	for _, item := range items {
		label := strings.ToLower(item.Label)
		prefix := strings.ToLower(item.Prefix)
		score := -1
		switch {
		case label == needle:
			score = 500 + item.Depth*10
		case prefix == needle:
			score = 480 + item.Depth*10
		case strings.HasSuffix(prefix, "/"+needle):
			score = 420 + item.Depth*10
		case strings.Contains(label, needle):
			score = 300 + len(needle)*5 + item.Depth*10
		case strings.Contains(prefix, needle):
			score = 200 + len(needle)*5 + item.Depth*10
		}
		if score > bestScore {
			bestScore = score
			bestPrefix = item.Prefix
		}
	}
	return bestPrefix
}
