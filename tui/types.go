package main

import (
	"fmt"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
)

type row struct {
	Path             string `json:"path"`
	Identifier       string `json:"identifier"`
	Lineage          string `json:"lineage"`
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

type fzfResultMsg struct {
	focus     focusPane
	treeKind  treeMode
	query     string
	selection string
	cancelled bool
	err       error
}

type tickMsg struct{}

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
	allRows       []row
	totalCount    int
	databaseTotal int
	facets        facets
	treeData      map[treeMode]treeData

	repoIndex        int
	selectedPath     string
	treeIndex        map[treeMode]int
	activeTreeFilter map[treeMode]string
	treePaneFilter   map[treeMode]string
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
		treeData: map[treeMode]treeData{
			treePath:       newTreeData(nil),
			treeIdentifier: newTreeData(nil),
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
		repoFilter: "",
		statusLine: fmt.Sprintf("Connecting to %s", client.base),
		loading:    true,
	}
}
