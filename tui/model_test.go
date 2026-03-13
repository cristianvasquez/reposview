package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompactPathLabel(t *testing.T) {
	got := compactPathLabel("/workspace/src/example/reposview", 2)
	if got != "example/reposview" {
		t.Fatalf("compactPathLabel = %q, want example/reposview", got)
	}
}

func TestCompactIdentifierLabel(t *testing.T) {
	got := compactIdentifierLabel("git@github.com:cristianvasquez/reposview.git", 2)
	if got != "cristianvasquez/reposview" {
		t.Fatalf("compactIdentifierLabel = %q, want cristianvasquez/reposview", got)
	}
}

func TestIdentifierToBrowserURL(t *testing.T) {
	got := identifierToBrowserURL("git@github.com:cristianvasquez/reposview.git")
	if got != "https://github.com/cristianvasquez/reposview" {
		t.Fatalf("identifierToBrowserURL = %q, want https://github.com/cristianvasquez/reposview", got)
	}
}

func TestHandleRowsMsgUsesInitialPathFilter(t *testing.T) {
	m := newModel(&apiClient{base: "http://example.test"}, "/repos/b")
	msg := rowsMsg{
		data: rowsResponse{
			Rows:          []row{{Path: "/repos/b", Identifier: "github.com/example/b"}},
			TotalCount:    1,
			DatabaseTotal: 2,
			Facets: facets{
				LocalPathTree: []treeNode{
					{Prefix: "/repos", Label: "repos", ParentPrefix: "", Depth: 1, Count: 2},
					{Prefix: "/repos/b", Label: "b", ParentPrefix: "/repos", Depth: 2, Count: 1},
				},
				IdentifierTree: []treeNode{
					{Prefix: "github.com", Label: "github.com", ParentPrefix: "", Depth: 1, Count: 1},
					{Prefix: "github.com/example", Label: "example", ParentPrefix: "github.com", Depth: 2, Count: 1},
					{Prefix: "github.com/example/b", Label: "b", ParentPrefix: "github.com/example", Depth: 3, Count: 1},
				},
			},
		},
	}

	nextModel, _ := m.handleRowsMsg(msg)
	got := nextModel.(model)

	if got.selectedPath != "/repos/b" {
		t.Fatalf("selectedPath = %q, want /repos/b", got.selectedPath)
	}
	if got.activeTreeFilter[treePath] != "/repos/b" {
		t.Fatalf("active path tree = %q, want /repos/b", got.activeTreeFilter[treePath])
	}
	if got.activeTreeFilter[treeIdentifier] != "github.com/example/b" {
		t.Fatalf("active identifier tree = %q, want github.com/example/b", got.activeTreeFilter[treeIdentifier])
	}
}

func TestApplyRepoFilterPreservesSelectedRepoWhenStillVisible(t *testing.T) {
	m := newModel(&apiClient{base: "http://example.test"}, "")
	m.allRows = []row{
		{Path: "/repos/a", Identifier: "github.com/example/a"},
		{Path: "/repos/b", Identifier: "github.com/example/b"},
		{Path: "/repos/c", Identifier: "github.com/example/c"},
	}
	m.rows = append([]row(nil), m.allRows...)
	m.setSelectedRepoIndex(1)
	m.repoFilter = "example"

	m.applyRepoFilter("")

	if m.selectedPath != "/repos/b" {
		t.Fatalf("selectedPath = %q, want /repos/b", m.selectedPath)
	}
	if m.repoIndex != 1 {
		t.Fatalf("repoIndex = %d, want 1", m.repoIndex)
	}
}

func TestHandleFzfResultSelectsRepoFromAllRowsAndClearsFilter(t *testing.T) {
	m := newModel(&apiClient{base: "http://example.test"}, "")
	m.focus = focusRepos
	m.repoFilter = "a"
	m.allRows = []row{
		{Path: "/repos/a", Identifier: "github.com/example/a"},
		{Path: "/repos/b", Identifier: "github.com/example/b"},
	}
	m.rows = []row{{Path: "/repos/a", Identifier: "github.com/example/a"}}
	m.setSelectedRepoIndex(0)

	nextModel, _ := m.handleFzfResult(fzfResultMsg{
		focus:     focusRepos,
		treeKind:  treePath,
		selection: "/repos/b",
	})
	got := nextModel.(model)

	if got.selectedPath != "/repos/b" {
		t.Fatalf("selectedPath = %q, want /repos/b", got.selectedPath)
	}
	if got.repoFilter != "" {
		t.Fatalf("repoFilter = %q, want empty", got.repoFilter)
	}
	if len(got.rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(got.rows))
	}
}

func TestHandleRowsMsgKeepsActiveTreeCursorOnRefresh(t *testing.T) {
	m := newModel(&apiClient{base: "http://example.test"}, "")
	m.treeKind = treePath
	m.activeTreeFilter[treePath] = "/repos/a"
	m.activeTreeFilter[treeIdentifier] = "github.com/example/b"
	m.selectedPath = "/repos/b"
	m.treeIndex[treePath] = 1
	msg := rowsMsg{
		data: rowsResponse{
			Rows: []row{
				{Path: "/repos/a", Identifier: "github.com/example/a"},
				{Path: "/repos/b", Identifier: "github.com/example/b"},
			},
			TotalCount:    2,
			DatabaseTotal: 2,
			Facets: facets{
				LocalPathTree: []treeNode{
					{Prefix: "/repos", Label: "repos", ParentPrefix: "", Depth: 1, Count: 2},
					{Prefix: "/repos/a", Label: "a", ParentPrefix: "/repos", Depth: 2, Count: 1},
					{Prefix: "/repos/b", Label: "b", ParentPrefix: "/repos", Depth: 2, Count: 1},
				},
				IdentifierTree: []treeNode{
					{Prefix: "github.com", Label: "github.com", ParentPrefix: "", Depth: 1, Count: 2},
					{Prefix: "github.com/example", Label: "example", ParentPrefix: "github.com", Depth: 2, Count: 2},
					{Prefix: "github.com/example/a", Label: "a", ParentPrefix: "github.com/example", Depth: 3, Count: 1},
					{Prefix: "github.com/example/b", Label: "b", ParentPrefix: "github.com/example", Depth: 3, Count: 1},
				},
			},
		},
	}

	nextModel, _ := m.handleRowsMsg(msg)
	got := nextModel.(model)

	if got.activeTreeFilter[treePath] != "/repos/a" {
		t.Fatalf("active path tree = %q, want /repos/a", got.activeTreeFilter[treePath])
	}
	if got.treeIndex[treePath] != 1 {
		t.Fatalf("path treeIndex = %d, want 1", got.treeIndex[treePath])
	}
	if got.activeTreeFilter[treeIdentifier] != "github.com/example/b" {
		t.Fatalf("active identifier tree = %q, want github.com/example/b", got.activeTreeFilter[treeIdentifier])
	}
}

func TestCompactPathTreeItemsCollapsesHomePrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	home = filepath.ToSlash(home)

	items := buildTreeItems([]treeNode{
		{Prefix: filepath.Dir(home), Label: filepath.Base(filepath.Dir(home)), ParentPrefix: "", Depth: 1, Count: 2},
		{Prefix: home, Label: filepath.Base(home), ParentPrefix: filepath.Dir(home), Depth: 2, Count: 2},
		{Prefix: home + "/github.com", Label: "github.com", ParentPrefix: home, Depth: 3, Count: 2},
		{Prefix: home + "/github.com/acme", Label: "acme", ParentPrefix: home + "/github.com", Depth: 4, Count: 2},
		{Prefix: home + "/github.com/acme/app", Label: "app", ParentPrefix: home + "/github.com/acme", Depth: 5, Count: 1},
		{Prefix: home + "/github.com/acme/lib", Label: "lib", ParentPrefix: home + "/github.com/acme", Depth: 5, Count: 1},
	})

	got := compactPathTreeItems(items)

	if len(got) != len(items) {
		t.Fatalf("len(compacted) = %d, want %d", len(got), len(items))
	}
	if got[1].Label != "~" {
		t.Fatalf("home label = %q, want ~", got[1].Label)
	}
	if got[2].Depth != 2 {
		t.Fatalf("github depth = %d, want 2", got[2].Depth)
	}
	if got[2].ParentPrefix != "" {
		t.Fatalf("github parent prefix = %q, want empty", got[2].ParentPrefix)
	}
}
