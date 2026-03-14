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
	Help      key.Binding
	List      key.Binding
	Refresh   key.Binding
	Sync      key.Binding
	Terminal  key.Binding
	Toggle    key.Binding
	PathTree  key.Binding
	IdentTree key.Binding
	Cancel    key.Binding
	Quit      key.Binding
}

func defaultKeys() keyMap {
	return keyMapFromConfig(defaultAppConfig())
}

func keyMapFromConfig(cfg appConfig) keyMap {
	return keyMap{
		Left:      key.NewBinding(key.WithKeys(cfg.TUI.Keys.Left...), key.WithHelp(displayKey(cfg.TUI.Keys.Left), "left pane")),
		Right:     key.NewBinding(key.WithKeys(cfg.TUI.Keys.Right...), key.WithHelp(displayKey(cfg.TUI.Keys.Right), "right pane")),
		CycleTree: key.NewBinding(key.WithKeys(cfg.TUI.Keys.CycleTree...), key.WithHelp(firstKey(cfg.TUI.Keys.CycleTree), "cycle tree")),
		Up:        key.NewBinding(key.WithKeys(cfg.TUI.Keys.Up...), key.WithHelp(displayKey(cfg.TUI.Keys.Up), "move")),
		Down:      key.NewBinding(key.WithKeys(cfg.TUI.Keys.Down...), key.WithHelp(displayKey(cfg.TUI.Keys.Down), "move")),
		Apply:     key.NewBinding(key.WithKeys(cfg.TUI.Keys.Apply...), key.WithHelp(firstKey(cfg.TUI.Keys.Apply), "apply/select")),
		Filter:    key.NewBinding(key.WithKeys(cfg.TUI.Keys.Filter...), key.WithHelp(firstKey(cfg.TUI.Keys.Filter), "filter pane")),
		Help:      key.NewBinding(key.WithKeys(cfg.TUI.Keys.Help...), key.WithHelp(firstKey(cfg.TUI.Keys.Help), "help")),
		List:      key.NewBinding(key.WithKeys(cfg.TUI.Keys.List...), key.WithHelp(firstKey(cfg.TUI.Keys.List), "list osg repos")),
		Refresh:   key.NewBinding(key.WithKeys(cfg.TUI.Keys.Refresh...), key.WithHelp(firstKey(cfg.TUI.Keys.Refresh), "refresh")),
		Sync:      key.NewBinding(key.WithKeys(cfg.TUI.Keys.Sync...), key.WithHelp(firstKey(cfg.TUI.Keys.Sync), "sync")),
		Terminal:  key.NewBinding(key.WithKeys(cfg.TUI.Keys.OpenTerminal...), key.WithHelp(firstKey(cfg.TUI.Keys.OpenTerminal), "open terminal")),
		Toggle:    key.NewBinding(key.WithKeys(cfg.TUI.Keys.ToggleOSG...), key.WithHelp(firstKey(cfg.TUI.Keys.ToggleOSG), "toggle osg")),
		PathTree:  key.NewBinding(key.WithKeys(cfg.TUI.Keys.PathTree...), key.WithHelp(firstKey(cfg.TUI.Keys.PathTree), "path tree")),
		IdentTree: key.NewBinding(key.WithKeys(cfg.TUI.Keys.IdentifierTree...), key.WithHelp(firstKey(cfg.TUI.Keys.IdentifierTree), "identifier tree")),
		Cancel:    key.NewBinding(key.WithKeys(cfg.TUI.Keys.Cancel...), key.WithHelp(firstKey(cfg.TUI.Keys.Cancel), "cancel/clear")),
		Quit:      key.NewBinding(key.WithKeys(cfg.TUI.Keys.Quit...), key.WithHelp(firstKey(cfg.TUI.Keys.Quit), "quit")),
	}
}

func firstKey(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func displayKey(keys []string) string {
	switch firstKey(keys) {
	case "left":
		return "←"
	case "right":
		return "→"
	case "up":
		return "↑"
	case "down":
		return "↓"
	default:
		return firstKey(keys)
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Terminal, k.Toggle, k.List, k.Refresh, k.Sync, k.Filter, k.Help}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Left, k.Right, k.CycleTree, k.Up, k.Down, k.Filter},
		{k.List, k.PathTree, k.IdentTree, k.Refresh, k.Sync, k.Terminal, k.Toggle, k.Cancel, k.Quit},
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

type syncFinishedMsg struct {
	label string
	err   error
}

type connectionStatus struct {
	Path      string
	Identity  string
	Connected bool
	Known     bool
	Error     string
}

type connectionMsg struct {
	state connectionStatus
	label string
}

type promptMsg struct {
	path   string
	prompt string
	err    error
}

type fzfResultMsg struct {
	focus     focusPane
	treeKind  treeMode
	query     string
	selection string
	setFilter bool
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
	showHelp  bool

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
	connections      map[string]connectionStatus

	statusLine string
	promptLine string
	errLine    string
	loading    bool
}

func newModel(client *apiClient, initialPathFilter string) model {
	return newModelWithConfig(client, initialPathFilter, defaultAppConfig())
}

func newModelWithConfig(client *apiClient, initialPathFilter string, cfg appConfig) model {
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
		keys:        keyMapFromConfig(cfg),
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
			treePath:       initialPathFilter,
			treeIdentifier: "",
		},
		treePaneFilter: map[treeMode]string{
			treePath:       "",
			treeIdentifier: "",
		},
		repoFilter:  "",
		connections: map[string]connectionStatus{},
		statusLine:  fmt.Sprintf("Connecting to %s", client.base),
		loading:     true,
	}
}
