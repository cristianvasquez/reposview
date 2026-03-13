package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type row struct {
	Path             string `json:"path"`
	Identifier       string `json:"identifier"`
	Branch           string `json:"branch"`
	LastCommitAuthor string `json:"last_commit_author"`
	LastCommitAt     string `json:"last_commit_at"`
	LastSeenAt       string `json:"last_seen_at"`
}

type treeNode struct {
	Prefix       string `json:"prefix"`
	Label        string `json:"label"`
	ParentPrefix string `json:"parentPrefix"`
	Depth        int    `json:"depth"`
	Count        int    `json:"count"`
}

type facets struct {
	LocalPathTree  []treeNode `json:"localPathTree"`
	IdentifierTree []treeNode `json:"identifierTree"`
}

type rowsResponse struct {
	Rows          []row  `json:"rows"`
	TotalCount    int    `json:"totalCount"`
	DatabaseTotal int    `json:"databaseTotal"`
	Facets        facets `json:"facets"`
}

type readmePayload struct {
	Exists    bool   `json:"exists"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type repoDetailsResponse struct {
	OK     bool          `json:"ok"`
	Path   string        `json:"path"`
	Error  string        `json:"error"`
	Readme readmePayload `json:"readme"`
}

type syncStatus struct {
	Running           bool   `json:"running"`
	Phase             string `json:"phase"`
	Message           string `json:"message"`
	Scanner           string `json:"scanner"`
	DiscoveredGitDirs int    `json:"discoveredGitDirs"`
	ProcessedGitDirs  int    `json:"processedGitDirs"`
	PersistedRepos    int    `json:"persistedRepos"`
	LastRunAt         string `json:"lastRunAt"`
	LastIndexed       int    `json:"lastIndexed"`
	DurationMs        int64  `json:"durationMs"`
	Error             string `json:"error"`
}

type actionResponse struct {
	Opened bool   `json:"opened"`
	Error  string `json:"error"`
}

type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(base string) *apiClient {
	return &apiClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *apiClient) health() error {
	resp, err := c.http.Get(c.base + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("healthz returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *apiClient) getRows(q url.Values) (rowsResponse, error) {
	var out rowsResponse
	err := c.getJSON("/rows?"+q.Encode(), &out)
	return out, err
}

func (c *apiClient) getStatus() (syncStatus, error) {
	var out syncStatus
	err := c.getJSON("/sync-status", &out)
	return out, err
}

func (c *apiClient) getRepoDetails(path string) (repoDetailsResponse, error) {
	var out repoDetailsResponse
	query := url.Values{}
	query.Set("path", path)
	err := c.getJSON("/repo-details?"+query.Encode(), &out)
	return out, err
}

func (c *apiClient) triggerSync() error {
	return c.postJSON("/sync", nil, nil)
}

func (c *apiClient) openTerminal(path string) error {
	body := map[string]string{"path": path}
	var out actionResponse
	if err := c.postJSON("/actions/open-terminal", body, &out); err != nil {
		return err
	}
	if !out.Opened && out.Error != "" {
		return errors.New(out.Error)
	}
	return nil
}

func (c *apiClient) getJSON(path string, out any) error {
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *apiClient) postJSON(path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf := bytes.NewBuffer(nil)
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return err
		}
		reader = buf
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload map[string]any
		if json.Unmarshal(body, &payload) == nil {
			if msg, ok := payload["error"].(string); ok && msg != "" {
				return errors.New(msg)
			}
		}
		return fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

type focusPane int

const (
	focusTree focusPane = iota
	focusRepos
	focusPreview
)

func (f focusPane) String() string {
	switch f {
	case focusTree:
		return "tree"
	case focusRepos:
		return "repos"
	case focusPreview:
		return "preview"
	default:
		return "?"
	}
}

type treeMode string

const (
	treePath       treeMode = "path"
	treeIdentifier treeMode = "identifier"
)

type treeItem struct {
	Prefix       string
	Label        string
	Depth        int
	Count        int
	ParentPrefix string
}

type keyMap struct {
	Left      key.Binding
	Right     key.Binding
	CycleTree key.Binding
	Up        key.Binding
	Down      key.Binding
	Apply     key.Binding
	Filter    key.Binding
	Refresh   key.Binding
	Sync      key.Binding
	Open      key.Binding
	PathTree  key.Binding
	IdentTree key.Binding
	Cancel    key.Binding
	Quit      key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Left:      key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "left pane")),
		Right:     key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "right pane")),
		CycleTree: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "cycle tree")),
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "move")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "move")),
		Apply:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply/select")),
		Filter:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter pane")),
		Refresh:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Sync:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sync")),
		Open:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open terminal")),
		PathTree:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "path tree")),
		IdentTree: key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "identifier tree")),
		Cancel:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel/clear")),
		Quit:      key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Left, k.Right, k.CycleTree, k.Up, k.Filter, k.Refresh, k.Sync, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Left, k.Right, k.CycleTree, k.Up, k.Down, k.Filter},
		{k.PathTree, k.IdentTree, k.Refresh, k.Sync, k.Open, k.Cancel, k.Quit},
	}
}

type rowsMsg struct {
	data rowsResponse
	err  error
}

type detailsMsg struct {
	path string
	data repoDetailsResponse
	err  error
}

type statusMsg struct {
	data syncStatus
	err  error
}

type actionMsg struct {
	label string
	err   error
}

type tickMsg time.Time

type model struct {
	client *apiClient
	keys   keyMap
	help   help.Model

	filterInput textinput.Model
	preview     viewport.Model

	width  int
	height int

	focus     focusPane
	treeKind  treeMode
	filtering bool

	rows          []row
	totalCount    int
	databaseTotal int
	facets        facets
	allTreeItems  map[treeMode][]treeItem
	treeItems     map[treeMode][]treeItem

	repoIndex        int
	treeIndex        map[treeMode]int
	activeTreeFilter map[treeMode]string
	treePaneFilter   map[treeMode]string
	collapsedTree    map[treeMode]map[string]bool
	repoFilter       string
	lastDetails      repoDetailsResponse
	lastStatus       syncStatus
	lastRunAt        string
	filterOriginal   string

	statusLine string
	errLine    string
	loading    bool
}

func newModel(client *apiClient) model {
	filterInput := textinput.New()
	filterInput.Placeholder = "filter current pane"
	filterInput.CharLimit = 200
	filterInput.Prompt = "Filter: "
	filterInput.Blur()

	preview := viewport.New(0, 0)
	preview.SetContent("Loading...")

	h := help.New()
	h.ShowAll = false

	return model{
		client:      client,
		keys:        defaultKeys(),
		help:        h,
		filterInput: filterInput,
		preview:     preview,
		focus:       focusTree,
		treeKind:    treePath,
		allTreeItems: map[treeMode][]treeItem{
			treePath:       {},
			treeIdentifier: {},
		},
		treeItems: map[treeMode][]treeItem{
			treePath:       {},
			treeIdentifier: {},
		},
		treeIndex: map[treeMode]int{
			treePath:       0,
			treeIdentifier: 0,
		},
		activeTreeFilter: map[treeMode]string{
			treePath:       "",
			treeIdentifier: "",
		},
		treePaneFilter: map[treeMode]string{
			treePath:       "",
			treeIdentifier: "",
		},
		collapsedTree: map[treeMode]map[string]bool{
			treePath:       {},
			treeIdentifier: {},
		},
		repoFilter: "",
		statusLine: fmt.Sprintf("Connecting to %s", client.base),
		loading:    true,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchRowsCmd(), m.fetchStatusCmd(), tickCmd())
}

func (m model) fetchRowsCmd() tea.Cmd {
	query := url.Values{}
	if q := strings.TrimSpace(m.repoFilter); q != "" {
		query.Set("q", q)
	}
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
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
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
		if msg.err != nil {
			m.loading = false
			m.errLine = msg.err.Error()
			return m, nil
		}
		m.loading = false
		m.errLine = ""
		m.rows = msg.data.Rows
		m.totalCount = msg.data.TotalCount
		m.databaseTotal = msg.data.DatabaseTotal
		m.facets = msg.data.Facets
		m.allTreeItems[treePath] = buildTreeItems(msg.data.Facets.LocalPathTree)
		m.allTreeItems[treeIdentifier] = buildTreeItems(msg.data.Facets.IdentifierTree)
		m.applyTreePaneFilters()
		m.repoIndex = clamp(m.repoIndex, 0, max(0, len(m.rows)-1))
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

	case tea.KeyMsg:
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		if key.Matches(msg, m.keys.PathTree) {
			m.treeKind = treePath
			m.focus = focusTree
			if m.filtering {
				m.filterInput.SetValue(m.currentPaneFilterValue())
				m.filterInput.CursorEnd()
			}
			m.applyFocus()
			return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
		}
		if key.Matches(msg, m.keys.IdentTree) {
			m.treeKind = treeIdentifier
			m.focus = focusTree
			if m.filtering {
				m.filterInput.SetValue(m.currentPaneFilterValue())
				m.filterInput.CursorEnd()
			}
			m.applyFocus()
			return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
		}
		if key.Matches(msg, m.keys.CycleTree) && m.focus == focusTree && !m.filtering {
			if m.treeKind == treePath {
				m.treeKind = treeIdentifier
			} else {
				m.treeKind = treePath
			}
			m.applyFocus()
			return m, tea.Batch(m.fetchRowsCmd(), m.fetchDetailsForSelection())
		}
		if m.filtering {
			before := m.filterInput.Value()
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			if key.Matches(msg, m.keys.Cancel) {
				m.setCurrentPaneFilterValue(m.filterOriginal)
				m.filtering = false
				m.filterInput.SetValue(m.filterOriginal)
				m.applyCurrentPaneFilter()
				m.applyFocus()
				return m, m.filterChangeCmd()
			}
			if key.Matches(msg, m.keys.Apply) {
				m.applyCurrentPaneFilter()
				m.filtering = false
				m.applyFocus()
				return m, m.filterChangeCmd()
			}
			if m.filterInput.Value() != before {
				m.setCurrentPaneFilterValue(strings.TrimSpace(m.filterInput.Value()))
				m.applyCurrentPaneFilter()
				return m, tea.Batch(cmd, m.filterChangeCmd())
			}
			return m, cmd
		}
		if key.Matches(msg, m.keys.Left) {
			m.focus = clampPane(m.focus - 1)
			m.applyFocus()
			return m, nil
		}
		if key.Matches(msg, m.keys.Right) {
			m.focus = clampPane(m.focus + 1)
			m.applyFocus()
			return m, nil
		}
		if key.Matches(msg, m.keys.Filter) {
			if m.focus == focusPreview {
				m.statusLine = "Preview pane has no filter mode"
				return m, nil
			}
			m.filtering = true
			m.filterOriginal = m.currentPaneFilterValue()
			m.filterInput.SetValue(m.currentPaneFilterValue())
			m.filterInput.CursorEnd()
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
				if m.focus == focusRepos || m.focus == focusTree {
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
		}
	}

	return m, nil
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
		if !m.treeHasChildren(m.treeKind, current.Prefix) {
			return m, nil
		}
		if m.collapsedTree[m.treeKind][current.Prefix] {
			delete(m.collapsedTree[m.treeKind], current.Prefix)
			m.statusLine = fmt.Sprintf("%s expanded: %s", m.treeKind, current.Prefix)
		} else {
			m.collapsedTree[m.treeKind][current.Prefix] = true
			m.statusLine = fmt.Sprintf("%s collapsed: %s", m.treeKind, current.Prefix)
		}
		m.treeIndex[m.treeKind] = alignTreeCursor(m.visibleTreeItems(m.treeKind), current.Prefix, m.treeIndex[m.treeKind])
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
		m.repoIndex = clamp(m.repoIndex-1, 0, len(m.rows)-1)
		m.syncPivotTreeToRow(m.selectedRow())
		return m, m.fetchDetailsForSelection()
	case "down", "j":
		m.repoIndex = clamp(m.repoIndex+1, 0, len(m.rows)-1)
		m.syncPivotTreeToRow(m.selectedRow())
		return m, m.fetchDetailsForSelection()
	case "enter":
		m.syncPivotTreeToRow(m.selectedRow())
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
		m.treeIndex[m.treeKind] = alignTreeCursor(m.visibleTreeItems(m.treeKind), m.activeTreeFilter[m.treeKind], m.treeIndex[m.treeKind])
	case focusRepos:
		m.repoIndex = 0
	}
}

func (m model) filterChangeCmd() tea.Cmd {
	switch m.focus {
	case focusTree:
		return nil
	case focusRepos:
		return m.fetchRowsCmd()
	default:
		return nil
	}
}

func (m *model) applyTreePaneFilters() {
	for _, kind := range []treeMode{treePath, treeIdentifier} {
		m.treeItems[kind] = filterTreeItems(m.allTreeItems[kind], m.treePaneFilter[kind])
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

	if !m.lastDetails.OK && m.lastDetails.Error != "" && m.lastDetails.Path == sel.Path {
		lines = append(lines, "details error: "+m.lastDetails.Error)
		m.preview.SetContent(strings.Join(lines, "\n"))
		return
	}

	if m.lastDetails.Path == sel.Path && m.lastDetails.Readme.Exists {
		lines = append(lines, "README: "+m.lastDetails.Readme.Path, "")
		lines = append(lines, stripMarkdown(m.lastDetails.Readme.Content))
		if m.lastDetails.Readme.Truncated {
			lines = append(lines, "", "[README truncated]")
		}
	} else {
		lines = append(lines, "README: not loaded")
	}

	m.preview.SetContent(strings.Join(lines, "\n"))
}

func (m model) View() string {
	if m.width < 72 || m.height < 16 {
		return "Terminal too small for reposview TUI. Need at least 72x16."
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

	if m.lastStatus.Running {
		parts = append(parts, fmt.Sprintf("sync %s %d/%d", emptyFallback(m.lastStatus.Phase), m.lastStatus.ProcessedGitDirs, m.lastStatus.DiscoveredGitDirs))
	} else if m.lastStatus.Error != "" {
		parts = append(parts, "sync error")
	} else if m.lastStatus.LastRunAt != "" {
		parts = append(parts, "last sync "+relativeTime(m.lastStatus.LastRunAt))
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("reposview tui")
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(strings.Join(parts, "  |  "))
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
	start := max(0, index-(height-4)/2)
	end := min(len(items), start+height-2)
	start = max(0, end-(height-2))
	for i := start; i < end; i++ {
		item := items[i]
		cursor := " "
		if i == index {
			cursor = ">"
		}
		activeMarker := " "
		if item.Prefix == active {
			activeMarker = "*"
		}
		indent := strings.Repeat("  ", max(0, item.Depth-1))
		label := fmt.Sprintf("%s%s%s (%d)", indent, m.treeGlyph(m.treeKind, item), item.Label, item.Count)
		label = truncateText(label, max(8, width-6))
		if i == index {
			label = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Render(label)
		}
		lines = append(lines, cursor+activeMarker+" "+label)
	}
	return panelStyle(m.focus == focusTree).Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m model) renderRepos(width, height int) string {
	lines := []string{"Repositories"}
	if len(m.rows) == 0 {
		lines = append(lines, "", "No repositories match the current filters.")
		return panelStyle(m.focus == focusRepos).Width(width).Height(height).Render(strings.Join(lines, "\n"))
	}
	start := max(0, m.repoIndex-(height-4)/2)
	end := min(len(m.rows), start+height-2)
	start = max(0, end-(height-2))
	for i := start; i < end; i++ {
		r := m.rows[i]
		cursor := " "
		if i == m.repoIndex {
			cursor = ">"
		}
		summary := r.Path
		if r.Branch != "" {
			summary += " [" + r.Branch + "]"
		}
		summary = truncateText(summary, max(10, width-6))
		if i == m.repoIndex {
			summary = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Render(summary)
		}
		lines = append(lines, cursor+" "+summary)
	}
	return panelStyle(m.focus == focusRepos).Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m model) renderPreview(width, height int) string {
	header := "Preview"
	content := m.preview.View()
	return panelStyle(m.focus == focusPreview).Width(width).Height(height).Render(header + "\n" + content)
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

func buildTreeItems(nodes []treeNode) []treeItem {
	if len(nodes) == 0 {
		return nil
	}
	byParent := make(map[string][]treeItem)
	parentByPrefix := make(map[string]string, len(nodes))
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
		parentByPrefix[item.Prefix] = parent
	}
	for parent := range byParent {
		slices.SortFunc(byParent[parent], func(a, b treeItem) int {
			return strings.Compare(a.Label, b.Label)
		})
	}

	rootKey := ""
	if _, ok := byParent[""]; !ok {
		byParent[""] = nil
	}
	items := make([]treeItem, 0, len(nodes))
	var walk func(string)
	walk = func(parent string) {
		for _, item := range byParent[parent] {
			items = append(items, item)
			walk(item.Prefix)
		}
	}
	walk(rootKey)

	if len(items) == 0 {
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
	for _, node := range nodes {
		_ = parentByPrefix[node.Prefix]
	}
	return items
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

func (m *model) syncPivotTreeToRow(selected row) {
	if selected.Path == "" && selected.Identifier == "" {
		return
	}
	for _, kind := range []treeMode{treePath, treeIdentifier} {
		if kind == m.treeKind {
			continue
		}
		prefix := bestTreePrefixForRow(m.treeItems[kind], kind, selected)
		if prefix == "" {
			continue
		}
		m.activeTreeFilter[kind] = prefix
		m.treeIndex[kind] = alignTreeCursor(m.treeItems[kind], prefix, m.treeIndex[kind])
	}
}

func bestTreePrefixForRow(items []treeItem, kind treeMode, selected row) string {
	target := selected.Identifier
	if kind == treePath {
		target = selected.Path
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

func filterTreeItems(items []treeItem, query string) []treeItem {
	if strings.TrimSpace(query) == "" {
		return items
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

func (m model) treeGlyph(kind treeMode, item treeItem) string {
	if m.treeHasChildren(kind, item.Prefix) {
		if m.collapsedTree[kind][item.Prefix] {
			return "▸ "
		}
		return "▾ "
	}
	if item.Depth <= 1 {
		return ""
	}
	return "· "
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

func truncateText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(runes) <= width {
		return string(runes)
	}
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "…"
}

func clampPane(p focusPane) focusPane {
	if p < focusTree {
		return focusTree
	}
	if p > focusPreview {
		return focusPreview
	}
	return p
}

func (m model) visibleTreeItems(kind treeMode) []treeItem {
	items := m.treeItems[kind]
	if len(items) == 0 {
		return nil
	}
	collapsed := m.collapsedTree[kind]
	visible := make([]treeItem, 0, len(items))
	for _, item := range items {
		if m.hasCollapsedAncestor(kind, item) {
			continue
		}
		visible = append(visible, item)
		if collapsed[item.Prefix] {
			continue
		}
	}
	return visible
}

func (m model) hasCollapsedAncestor(kind treeMode, item treeItem) bool {
	parent := item.ParentPrefix
	for parent != "" {
		if m.collapsedTree[kind][parent] {
			return true
		}
		parent = parentPrefixForTreeItem(m.treeItems[kind], parent)
	}
	return false
}

func parentPrefixForTreeItem(items []treeItem, prefix string) string {
	for _, item := range items {
		if item.Prefix == prefix {
			return item.ParentPrefix
		}
	}
	return ""
}

func (m model) treeHasChildren(kind treeMode, prefix string) bool {
	for _, item := range m.treeItems[kind] {
		if item.ParentPrefix == prefix {
			return true
		}
	}
	return false
}

func stripMarkdown(markdown string) string {
	replacer := strings.NewReplacer(
		"```", "",
		"`", "",
		"#", "",
		"*", "",
		"_", "",
		">", "",
	)
	return replacer.Replace(markdown)
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

func emptyFallback(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(none)"
	}
	return v
}

func clamp(v, low, high int) int {
	if high < low {
		return low
	}
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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

type spawnedServer struct {
	cmd    *exec.Cmd
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (s *spawnedServer) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}

	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = s.cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
}

func ensureAPI(client *apiClient, spawn bool, dbPath string, scanner string) (*spawnedServer, error) {
	if err := client.health(); err == nil {
		return nil, nil
	}

	if !spawn {
		return nil, fmt.Errorf("inspect API is unavailable at %s", client.base)
	}

	server, err := startInspectServer(client.base, dbPath, scanner)
	if err != nil {
		return nil, err
	}

	if err := waitForHealth(client, 8*time.Second); err != nil {
		logs := strings.TrimSpace(server.stderr.String())
		if logs == "" {
			logs = strings.TrimSpace(server.stdout.String())
		}
		server.Stop()
		if logs != "" {
			return nil, fmt.Errorf("inspect API failed to start: %w\n%s", err, logs)
		}
		return nil, fmt.Errorf("inspect API failed to start: %w", err)
	}

	return server, nil
}

func startInspectServer(apiOrigin string, dbPath string, scanner string) (*spawnedServer, error) {
	parsed, err := url.Parse(apiOrigin)
	if err != nil {
		return nil, fmt.Errorf("invalid api origin %q: %w", apiOrigin, err)
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" || port == "" {
		return nil, fmt.Errorf("api origin must include host and port: %s", apiOrigin)
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repoRoot := filepath.Dir(wd)
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("node executable not found; cannot auto-start inspect API")
	}

	if dbPath == "" {
		dbPath = filepath.Join(repoRoot, "data", "reposview.sqlite")
	}
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(repoRoot, dbPath)
	}

	scriptPath := filepath.Join(repoRoot, "scripts", "inspect-table.mjs")
	args := []string{
		scriptPath,
		"--db", dbPath,
		"--host", host,
		"--port", port,
		"--scanner", scanner,
	}

	cmd := exec.Command(nodePath, args...)
	cmd.Dir = repoRoot
	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start inspect API: %w", err)
	}

	return &spawnedServer{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func waitForHealth(client *apiClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.health(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout waiting for healthz")
	}
	return lastErr
}
