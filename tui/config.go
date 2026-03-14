package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type launcherConfigSpec struct {
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	UnsetEnv []string `json:"unset_env"`
}

type operationConfig struct {
	Requires  []string             `json:"requires"`
	Launchers []launcherConfigSpec `json:"launchers"`
	Commands  []string             `json:"commands"`
}

type appConfig struct {
	Paths struct {
		Database string `json:"database"`
	} `json:"paths"`
	API struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"api"`
	Web struct {
		Host      string  `json:"host"`
		Port      int     `json:"port"`
		APIOrigin *string `json:"api_origin"`
	} `json:"web"`
	TUI struct {
		APIOrigin string `json:"api_origin"`
		SpawnAPI  bool   `json:"spawn_api"`
		Scanner   string `json:"scanner"`
		Database  string `json:"database"`
		Keys      struct {
			Left           []string `json:"left"`
			Right          []string `json:"right"`
			CycleTree      []string `json:"cycle_tree"`
			Up             []string `json:"up"`
			Down           []string `json:"down"`
			Apply          []string `json:"apply"`
			Filter         []string `json:"filter"`
			Help           []string `json:"help"`
			List           []string `json:"list"`
			Refresh        []string `json:"refresh"`
			Sync           []string `json:"sync"`
			OpenTerminal   []string `json:"open_terminal"`
			ToggleOSG      []string `json:"toggle_osg"`
			PathTree       []string `json:"path_tree"`
			IdentifierTree []string `json:"identifier_tree"`
			Cancel         []string `json:"cancel"`
			Quit           []string `json:"quit"`
		} `json:"keys"`
	} `json:"tui"`
	Operations struct {
		OpenTerminal operationConfig `json:"open_terminal"`
		OpenRepo     operationConfig `json:"open_repo"`
		OpenWeb      operationConfig `json:"open_web"`
	} `json:"operations"`
}

type yamlLine struct {
	indent int
	text   string
}

func defaultAppConfig() appConfig {
	cfg := appConfig{}
	cfg.Paths.Database = "./data/reposview.sqlite"
	cfg.API.Host = "127.0.0.1"
	cfg.API.Port = 8787
	cfg.Web.Host = "127.0.0.1"
	cfg.Web.Port = 8790
	cfg.TUI.APIOrigin = ""
	cfg.TUI.SpawnAPI = false
	cfg.TUI.Scanner = "auto"
	cfg.TUI.Database = "./data/reposview.sqlite"
	cfg.TUI.Keys.Left = []string{"left"}
	cfg.TUI.Keys.Right = []string{"right"}
	cfg.TUI.Keys.CycleTree = []string{"tab"}
	cfg.TUI.Keys.Up = []string{"up"}
	cfg.TUI.Keys.Down = []string{"down"}
	cfg.TUI.Keys.Apply = []string{"enter"}
	cfg.TUI.Keys.Filter = []string{"f"}
	cfg.TUI.Keys.Help = []string{"?", "h"}
	cfg.TUI.Keys.List = []string{"l"}
	cfg.TUI.Keys.Refresh = []string{"r"}
	cfg.TUI.Keys.Sync = []string{"s"}
	cfg.TUI.Keys.OpenTerminal = []string{"t"}
	cfg.TUI.Keys.ToggleOSG = []string{"o"}
	cfg.TUI.Keys.PathTree = []string{"p"}
	cfg.TUI.Keys.IdentifierTree = []string{"i"}
	cfg.TUI.Keys.Cancel = []string{"esc"}
	cfg.TUI.Keys.Quit = []string{"q", "ctrl+c"}
	cfg.Operations.OpenTerminal.Launchers = []launcherConfigSpec{
		{Command: "ghostty", Args: []string{"--working-directory={dir}", "--gtk-single-instance=false"}, UnsetEnv: []string{"DBUS_SESSION_BUS_ADDRESS"}},
		{Command: "gnome-terminal", Args: []string{"--working-directory={dir}"}},
	}
	cfg.Operations.OpenRepo.Requires = []string{"yazi"}
	cfg.Operations.OpenRepo.Launchers = []launcherConfigSpec{
		{Command: "ghostty", Args: []string{"--working-directory={dir}", "--gtk-single-instance=false", "-e", "yazi", "{dir}"}, UnsetEnv: []string{"DBUS_SESSION_BUS_ADDRESS"}},
		{Command: "gnome-terminal", Args: []string{"--working-directory={dir}", "--", "yazi", "{dir}"}},
	}
	cfg.Operations.OpenWeb.Commands = []string{"xdg-open", "open"}
	return cfg
}

func loadAppConfig(repoRoot string) appConfig {
	cfg := defaultAppConfig()
	configPath := filepath.Join(repoRoot, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}

	parsed, err := parseYAMLConfig(string(data))
	if err != nil {
		return cfg
	}

	interpolated := interpolateValue(parsed, map[string]string{
		"HOME":         envOrDefault("HOME", "/"),
		"HOME_ESCAPED": escapeRegex(envOrDefault("HOME", "/")),
	})

	blob, err := json.Marshal(interpolated)
	if err != nil {
		return cfg
	}

	var loaded appConfig
	if err := json.Unmarshal(blob, &loaded); err != nil {
		return cfg
	}

	mergeAppConfig(&cfg, loaded)
	return cfg
}

func mergeAppConfig(dst *appConfig, src appConfig) {
	if strings.TrimSpace(src.Paths.Database) != "" {
		dst.Paths.Database = src.Paths.Database
	}
	if strings.TrimSpace(src.API.Host) != "" {
		dst.API.Host = src.API.Host
	}
	if src.API.Port > 0 {
		dst.API.Port = src.API.Port
	}
	if strings.TrimSpace(src.Web.Host) != "" {
		dst.Web.Host = src.Web.Host
	}
	if src.Web.Port > 0 {
		dst.Web.Port = src.Web.Port
	}
	if src.Web.APIOrigin != nil {
		dst.Web.APIOrigin = src.Web.APIOrigin
	}
	if strings.TrimSpace(src.TUI.APIOrigin) != "" {
		dst.TUI.APIOrigin = src.TUI.APIOrigin
	}
	dst.TUI.SpawnAPI = src.TUI.SpawnAPI
	if strings.TrimSpace(src.TUI.Scanner) != "" {
		dst.TUI.Scanner = src.TUI.Scanner
	}
	if strings.TrimSpace(src.TUI.Database) != "" {
		dst.TUI.Database = src.TUI.Database
	}
	if len(src.TUI.Keys.Left) > 0 {
		dst.TUI.Keys.Left = src.TUI.Keys.Left
	}
	if len(src.TUI.Keys.Right) > 0 {
		dst.TUI.Keys.Right = src.TUI.Keys.Right
	}
	if len(src.TUI.Keys.CycleTree) > 0 {
		dst.TUI.Keys.CycleTree = src.TUI.Keys.CycleTree
	}
	if len(src.TUI.Keys.Up) > 0 {
		dst.TUI.Keys.Up = src.TUI.Keys.Up
	}
	if len(src.TUI.Keys.Down) > 0 {
		dst.TUI.Keys.Down = src.TUI.Keys.Down
	}
	if len(src.TUI.Keys.Apply) > 0 {
		dst.TUI.Keys.Apply = src.TUI.Keys.Apply
	}
	if len(src.TUI.Keys.Filter) > 0 {
		dst.TUI.Keys.Filter = src.TUI.Keys.Filter
	}
	if len(src.TUI.Keys.Help) > 0 {
		dst.TUI.Keys.Help = src.TUI.Keys.Help
	}
	if len(src.TUI.Keys.List) > 0 {
		dst.TUI.Keys.List = src.TUI.Keys.List
	}
	if len(src.TUI.Keys.Refresh) > 0 {
		dst.TUI.Keys.Refresh = src.TUI.Keys.Refresh
	}
	if len(src.TUI.Keys.Sync) > 0 {
		dst.TUI.Keys.Sync = src.TUI.Keys.Sync
	}
	if len(src.TUI.Keys.OpenTerminal) > 0 {
		dst.TUI.Keys.OpenTerminal = src.TUI.Keys.OpenTerminal
	}
	if len(src.TUI.Keys.ToggleOSG) > 0 {
		dst.TUI.Keys.ToggleOSG = src.TUI.Keys.ToggleOSG
	}
	if len(src.TUI.Keys.PathTree) > 0 {
		dst.TUI.Keys.PathTree = src.TUI.Keys.PathTree
	}
	if len(src.TUI.Keys.IdentifierTree) > 0 {
		dst.TUI.Keys.IdentifierTree = src.TUI.Keys.IdentifierTree
	}
	if len(src.TUI.Keys.Cancel) > 0 {
		dst.TUI.Keys.Cancel = src.TUI.Keys.Cancel
	}
	if len(src.TUI.Keys.Quit) > 0 {
		dst.TUI.Keys.Quit = src.TUI.Keys.Quit
	}
	if len(src.Operations.OpenTerminal.Launchers) > 0 {
		dst.Operations.OpenTerminal = src.Operations.OpenTerminal
	}
	if len(src.Operations.OpenRepo.Launchers) > 0 || len(src.Operations.OpenRepo.Requires) > 0 {
		dst.Operations.OpenRepo = src.Operations.OpenRepo
	}
	if len(src.Operations.OpenWeb.Commands) > 0 {
		dst.Operations.OpenWeb = src.Operations.OpenWeb
	}
}

func resolveConfiguredPath(repoRoot string, value string, fallback string) string {
	target := strings.TrimSpace(value)
	if target == "" {
		target = fallback
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(repoRoot, target)
	}
	return filepath.Clean(target)
}

func configuredAPIOrigin(cfg appConfig) string {
	if strings.TrimSpace(cfg.TUI.APIOrigin) != "" {
		return strings.TrimSpace(cfg.TUI.APIOrigin)
	}
	if cfg.Web.APIOrigin != nil && strings.TrimSpace(*cfg.Web.APIOrigin) != "" {
		return strings.TrimSpace(*cfg.Web.APIOrigin)
	}
	return "http://" + cfg.API.Host + ":" + strconv.Itoa(cfg.API.Port)
}

func envWithout(unset []string) []string {
	if len(unset) == 0 {
		return os.Environ()
	}
	env := os.Environ()
	result := make([]string, 0, len(env))
outer:
	for _, item := range env {
		for _, key := range unset {
			if strings.HasPrefix(item, key+"=") {
				continue outer
			}
		}
		result = append(result, item)
	}
	return result
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func escapeRegex(value string) string {
	var b strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`.*+?^${}()|[]\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func stripInlineComment(line string) string {
	var quote rune
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if (r == '"' || r == '\'') && quote == 0 {
			quote = r
			continue
		}
		if r == quote {
			quote = 0
			continue
		}
		if r == '#' && quote == 0 {
			return strings.TrimRight(line[:i], " ")
		}
	}
	return strings.TrimRight(line, " ")
}

func tokenizeYAML(source string) []yamlLine {
	lines := strings.Split(source, "\n")
	tokens := make([]yamlLine, 0, len(lines))
	for _, raw := range lines {
		stripped := stripInlineComment(strings.TrimRight(raw, "\r"))
		trimmed := strings.TrimSpace(stripped)
		if trimmed == "" {
			continue
		}
		indent := len(stripped) - len(strings.TrimLeft(stripped, " "))
		tokens = append(tokens, yamlLine{indent: indent, text: trimmed})
	}
	return tokens
}

func splitKeyValue(text string) (string, string, bool) {
	var quote rune
	escaped := false
	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if (r == '"' || r == '\'') && quote == 0 {
			quote = r
			continue
		}
		if r == quote {
			quote = 0
			continue
		}
		if r == ':' && quote == 0 {
			return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:]), true
		}
	}
	return "", "", false
}

func parseScalar(text string) any {
	switch text {
	case "null":
		return nil
	case "true":
		return true
	case "false":
		return false
	case `""`, `''`:
		return ""
	}
	if n, err := strconv.Atoi(text); err == nil {
		return n
	}
	if len(text) >= 2 {
		if (text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'') {
			return text[1 : len(text)-1]
		}
	}
	return text
}

func parseYAMLConfig(source string) (map[string]any, error) {
	lines := tokenizeYAML(source)
	if len(lines) == 0 {
		return map[string]any{}, nil
	}
	value, _, err := parseBlock(lines, 0, lines[0].indent)
	if err != nil {
		return nil, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, os.ErrInvalid
	}
	return root, nil
}

func parseBlock(lines []yamlLine, index int, indent int) (any, int, error) {
	if index >= len(lines) {
		return nil, index, nil
	}
	if strings.HasPrefix(lines[index].text, "- ") {
		return parseSequence(lines, index, indent)
	}
	return parseMapping(lines, index, indent)
}

func parseMapping(lines []yamlLine, index int, indent int) (map[string]any, int, error) {
	out := map[string]any{}
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent || strings.HasPrefix(line.text, "- ") {
			return nil, index, os.ErrInvalid
		}
		key, rest, ok := splitKeyValue(line.text)
		if !ok || key == "" {
			return nil, index, os.ErrInvalid
		}
		index++
		if rest != "" {
			out[key] = parseScalar(rest)
			continue
		}
		if index < len(lines) && lines[index].indent > indent {
			value, nextIndex, err := parseBlock(lines, index, lines[index].indent)
			if err != nil {
				return nil, index, err
			}
			out[key] = value
			index = nextIndex
			continue
		}
		out[key] = nil
	}
	return out, index, nil
}

func parseSequence(lines []yamlLine, index int, indent int) ([]any, int, error) {
	out := []any{}
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent != indent || !strings.HasPrefix(line.text, "- ") {
			break
		}
		itemText := strings.TrimSpace(line.text[2:])
		index++
		if itemText == "" {
			if index < len(lines) && lines[index].indent > indent {
				value, nextIndex, err := parseBlock(lines, index, lines[index].indent)
				if err != nil {
					return nil, index, err
				}
				out = append(out, value)
				index = nextIndex
			} else {
				out = append(out, nil)
			}
			continue
		}
		if key, rest, ok := splitKeyValue(itemText); ok && key != "" {
			item := map[string]any{}
			if rest != "" {
				item[key] = parseScalar(rest)
			} else if index < len(lines) && lines[index].indent > indent {
				value, nextIndex, err := parseBlock(lines, index, lines[index].indent)
				if err != nil {
					return nil, index, err
				}
				item[key] = value
				index = nextIndex
			} else {
				item[key] = nil
			}
			if index < len(lines) && lines[index].indent > indent {
				extra, nextIndex, err := parseBlock(lines, index, lines[index].indent)
				if err != nil {
					return nil, index, err
				}
				extraMap, ok := extra.(map[string]any)
				if !ok {
					return nil, index, os.ErrInvalid
				}
				for k, v := range extraMap {
					item[k] = v
				}
				index = nextIndex
			}
			out = append(out, item)
			continue
		}
		out = append(out, parseScalar(itemText))
	}
	return out, index, nil
}

func interpolateValue(value any, vars map[string]string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = interpolateValue(v, vars)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, v := range typed {
			out = append(out, interpolateValue(v, vars))
		}
		return out
	case string:
		return interpolateString(typed, vars)
	default:
		return value
	}
}

func interpolateString(value string, vars map[string]string) string {
	out := value
	for key, replacement := range vars {
		out = strings.ReplaceAll(out, "${"+key+"}", replacement)
	}
	return out
}
