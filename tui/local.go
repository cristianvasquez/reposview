package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type localBackend struct {
	repoRoot string
	dbPath   string
	scanner  string

	mu         sync.Mutex
	syncState  syncStatus
	syncLoaded bool
}

type syncProgressEvent struct {
	Type              string `json:"type"`
	Phase             string `json:"phase"`
	Message           string `json:"message"`
	Scanner           string `json:"scanner"`
	DiscoveredGitDirs int    `json:"discoveredGitDirs"`
	ProcessedGitDirs  int    `json:"processedGitDirs"`
	PersistedRepos    int    `json:"persistedRepos"`
	IndexedRepos      int    `json:"indexedRepos"`
	ScannedGitDirs    int    `json:"scannedGitDirs"`
	At                string `json:"at"`
	DurationMs        int64  `json:"durationMs"`
	Error             string `json:"error"`
}

type localDBRow struct {
	Path             string `json:"path"`
	Identifier       string `json:"identifier"`
	Lineage          string `json:"lineage"`
	Branch           string `json:"branch"`
	LastCommitAuthor string `json:"last_commit_author"`
	LastCommitAt     string `json:"last_commit_at"`
	LastSeenAt       string `json:"last_seen_at"`
	IsBare           int    `json:"is_bare"`
}

type localFetchOptions struct {
	ignorePathPrefix       bool
	ignoreIdentifierPrefix bool
}

func newLocalAPIClient(dbPath string, scanner string) (*apiClient, error) {
	repoRoot, err := discoverRepoRoot()
	if err != nil {
		return nil, err
	}
	if dbPath == "" {
		dbPath = filepath.Join(repoRoot, "data", "reposview.sqlite")
	} else if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(repoRoot, dbPath)
	}

	backend := &localBackend{
		repoRoot: repoRoot,
		dbPath:   dbPath,
		scanner:  scanner,
	}

	return &apiClient{
		base:  "local:" + dbPath,
		local: backend,
	}, nil
}

func discoverRepoRoot() (string, error) {
	candidates := make([]string, 0, 4)
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd, filepath.Dir(wd))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, exeDir, filepath.Dir(exeDir))
	}

	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if fileExists(filepath.Join(candidate, "scripts", "sync-repos.mjs")) {
			return candidate, nil
		}
	}

	return "", errors.New("could not locate reposview repository root")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (l *localBackend) getRows(q url.Values) (rowsResponse, error) {
	filters := parseLocalFilters(q)
	matchedRows, err := l.fetchRows(filters, localFetchOptions{})
	if err != nil {
		return rowsResponse{}, err
	}

	sortRowsLocal(matchedRows, filters.sort, filters.dir)
	resultRows := make([]row, 0, min(len(matchedRows), 1000))
	for _, item := range matchedRows[:min(len(matchedRows), 1000)] {
		resultRows = append(resultRows, row{
			Path:             item.Path,
			Identifier:       identifierDisplayFromParts(item.Identifier, item.Lineage),
			Lineage:          item.Lineage,
			Branch:           item.Branch,
			LastCommitAuthor: item.LastCommitAuthor,
			LastCommitAt:     item.LastCommitAt,
			LastSeenAt:       item.LastSeenAt,
		})
	}

	databaseTotal, err := l.countRepositories()
	if err != nil {
		return rowsResponse{}, err
	}

	localPathTreeRows, err := l.fetchRows(filters, localFetchOptions{ignorePathPrefix: true})
	if err != nil {
		return rowsResponse{}, err
	}
	identifierTreeRows, err := l.fetchRows(filters, localFetchOptions{ignoreIdentifierPrefix: true})
	if err != nil {
		return rowsResponse{}, err
	}

	return rowsResponse{
		Rows:          resultRows,
		TotalCount:    len(matchedRows),
		DatabaseTotal: databaseTotal,
		Facets: facets{
			LocalPathTree:  buildLocalPathTreeFacet(localPathTreeRows),
			IdentifierTree: buildIdentifierTreeFacet(identifierTreeRows),
		},
	}, nil
}

func (l *localBackend) getRepoDetails(repoPath string) (repoDetailsResponse, error) {
	resolved := filepath.Clean(repoPath)
	if !filepath.IsAbs(resolved) {
		return repoDetailsResponse{OK: false, Error: "path must be absolute"}, nil
	}
	if !directoryExists(resolved) {
		return repoDetailsResponse{OK: false, Error: "repository path not found"}, nil
	}

	readmePath := findReadmeFile(resolved)
	if readmePath == "" {
		return repoDetailsResponse{
			OK:   true,
			Path: resolved,
			Readme: readmePayload{
				Exists:    false,
				Content:   "",
				Truncated: false,
			},
		}, nil
	}

	const maxBytes = 250 * 1024
	content, truncated, err := readTruncatedFile(readmePath, maxBytes)
	if err != nil {
		return repoDetailsResponse{OK: false, Path: resolved, Error: err.Error()}, nil
	}

	return repoDetailsResponse{
		OK:   true,
		Path: resolved,
		Readme: readmePayload{
			Exists:    true,
			Path:      readmePath,
			Content:   content,
			Truncated: truncated,
		},
	}, nil
}

func (l *localBackend) getStatus() (syncStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.syncLoaded {
		l.syncLoaded = true
		lastRunAt, _ := l.maxLastSeenAt()
		l.syncState.LastRunAt = lastRunAt
	}

	return l.syncState, nil
}

func (l *localBackend) triggerSync() error {
	l.mu.Lock()
	if l.syncState.Running {
		l.mu.Unlock()
		return nil
	}

	nodePath, err := exec.LookPath("node")
	if err != nil {
		l.mu.Unlock()
		return errors.New("node executable not found")
	}

	scriptPath := filepath.Join(l.repoRoot, "scripts", "sync-progress.mjs")
	args := []string{scriptPath, "--db", l.dbPath, "--scanner", l.scanner}
	cmd := exec.Command(nodePath, args...)
	cmd.Dir = l.repoRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		l.mu.Unlock()
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	startedAt := time.Now().UTC()
	l.syncLoaded = true
	l.syncState = syncStatus{
		Running:     true,
		Phase:       "running",
		Message:     "sync running",
		Scanner:     l.scanner,
		LastRunAt:   l.syncState.LastRunAt,
		LastIndexed: l.syncState.LastIndexed,
	}
	l.mu.Unlock()

	if err := cmd.Start(); err != nil {
		l.mu.Lock()
		l.syncState.Running = false
		l.syncState.Phase = "error"
		l.syncState.Message = "sync failed to start"
		l.syncState.Error = err.Error()
		l.mu.Unlock()
		return err
	}

	go l.consumeSyncProgress(stdout)

	go func() {
		runErr := cmd.Wait()
		durationMs := time.Since(startedAt).Milliseconds()
		stderrText := strings.TrimSpace(stderr.String())

		l.mu.Lock()
		defer l.mu.Unlock()

		l.syncState.Running = false
		l.syncState.DurationMs = durationMs
		l.syncState.ProcessedGitDirs = 0
		l.syncState.DiscoveredGitDirs = 0
		l.syncState.PersistedRepos = 0

		if runErr != nil {
			l.syncState.Phase = "error"
			l.syncState.Message = "sync failed"
			if stderrText != "" {
				l.syncState.Error = stderrText
			} else {
				l.syncState.Error = runErr.Error()
			}
			return
		}

		l.syncState.Phase = "done"
		l.syncState.Message = "sync completed"
		l.syncState.Error = ""
		if l.syncState.LastRunAt == "" {
			l.syncState.LastRunAt = startedAt.Format(time.RFC3339)
		}
		if l.syncState.LastIndexed == 0 {
			l.syncState.LastIndexed = l.syncState.PersistedRepos
		}
	}()

	return nil
}

func (l *localBackend) syncCommand() (*exec.Cmd, string, error) {
	pnpmPath, err := exec.LookPath("pnpm")
	if err != nil {
		return nil, "", errors.New("pnpm executable not found")
	}

	args := []string{"run", "sync", "--", "--db", l.dbPath, "--scanner", l.scanner}
	cmd := exec.Command(pnpmPath, args...)
	cmd.Dir = l.repoRoot
	label := strings.Join(append([]string{"pnpm"}, args...), " ")
	return cmd, label, nil
}

func (l *localBackend) recordSyncResult(runErr error, startedAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.syncLoaded = true
	l.syncState.DurationMs = time.Since(startedAt).Milliseconds()
	l.syncState.Running = false

	if runErr != nil {
		l.syncState.Phase = "error"
		l.syncState.Message = "sync failed"
		l.syncState.Error = runErr.Error()
		return
	}

	l.syncState.Phase = "done"
	l.syncState.Message = "sync completed"
	l.syncState.Error = ""
	if lastRunAt, err := l.maxLastSeenAt(); err == nil {
		l.syncState.LastRunAt = lastRunAt
	}
	if total, err := l.countRepositories(); err == nil {
		l.syncState.LastIndexed = total
		l.syncState.PersistedRepos = total
	}
}

func (l *localBackend) consumeSyncProgress(r io.Reader) {
	scanner := bufio.NewScanner(r)
	const maxScanToken = 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScanToken)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event syncProgressEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		l.applySyncProgress(event)
	}
}

func (l *localBackend) applySyncProgress(event syncProgressEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch event.Type {
	case "progress":
		if event.Phase != "" {
			l.syncState.Phase = event.Phase
		}
		if event.Message != "" {
			l.syncState.Message = event.Message
		}
		if event.Scanner != "" {
			l.syncState.Scanner = event.Scanner
		}
		if event.DiscoveredGitDirs > 0 {
			l.syncState.DiscoveredGitDirs = event.DiscoveredGitDirs
		}
		if event.ProcessedGitDirs > 0 {
			l.syncState.ProcessedGitDirs = event.ProcessedGitDirs
		}
		if event.PersistedRepos > 0 {
			l.syncState.PersistedRepos = event.PersistedRepos
		}
		if event.IndexedRepos > 0 {
			l.syncState.LastIndexed = event.IndexedRepos
		}
	case "result":
		l.syncState.Phase = "done"
		l.syncState.Message = "sync completed"
		l.syncState.Scanner = nonEmptyOrFallback(event.Scanner, l.syncState.Scanner)
		if event.ScannedGitDirs > 0 {
			l.syncState.DiscoveredGitDirs = event.ScannedGitDirs
			l.syncState.ProcessedGitDirs = event.ScannedGitDirs
		}
		if event.IndexedRepos > 0 {
			l.syncState.LastIndexed = event.IndexedRepos
			l.syncState.PersistedRepos = event.IndexedRepos
		}
		if event.At != "" {
			l.syncState.LastRunAt = event.At
		}
		if event.DurationMs > 0 {
			l.syncState.DurationMs = event.DurationMs
		}
		l.syncState.Error = ""
	case "error":
		l.syncState.Phase = "error"
		l.syncState.Message = "sync failed"
		l.syncState.Error = event.Error
	}
}

func (l *localBackend) openTerminal(repoPath string) error {
	return launchTerminalAtDir(repoPath)
}

func (l *localBackend) openYazi(repoPath string) error {
	return launchYaziAtDir(repoPath)
}

func launchTerminalAtDir(dirPath string) error {
	return launchDetachedInDir(dirPath, []launcherSpec{
		{command: "ghostty", args: []string{"--working-directory={dir}", "--gtk-single-instance=false"}, unsetEnv: []string{"DBUS_SESSION_BUS_ADDRESS"}},
		{command: "gnome-terminal", args: []string{"--working-directory={dir}"}},
	})
}

func launchYaziAtDir(dirPath string) error {
	if _, err := exec.LookPath("yazi"); err != nil {
		return errors.New("yazi command not found")
	}
	return launchDetachedInDir(dirPath, []launcherSpec{
		{command: "ghostty", args: []string{"--working-directory={dir}", "--gtk-single-instance=false", "-e", "yazi", "{dir}"}, unsetEnv: []string{"DBUS_SESSION_BUS_ADDRESS"}},
		{command: "gnome-terminal", args: []string{"--working-directory={dir}", "--", "yazi", "{dir}"}},
	})
}

type launcherSpec struct {
	command  string
	args     []string
	unsetEnv []string
}

func launchDetachedInDir(dirPath string, specs []launcherSpec) error {
	resolved := filepath.Clean(dirPath)
	if !filepath.IsAbs(resolved) || !directoryExists(resolved) {
		return errors.New("invalid path")
	}

	for _, spec := range specs {
		cmdPath, err := exec.LookPath(spec.command)
		if err != nil {
			continue
		}
		args := make([]string, 0, len(spec.args))
		for _, arg := range spec.args {
			args = append(args, strings.ReplaceAll(arg, "{dir}", resolved))
		}
		cmd := exec.Command(cmdPath, args...)
		cmd.Dir = resolved
		cmd.Env = envWithout(spec.unsetEnv)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		return nil
	}

	return errors.New("no supported terminal command found")
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

func readTruncatedFile(path string, maxBytes int) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	if len(data) <= maxBytes {
		return string(data), false, nil
	}
	return string(data[:maxBytes]), true, nil
}

func findReadmeFile(repoPath string) string {
	preferred := []string{"README.md", "readme.md", "CLAUDE.md", "claude.md"}
	for _, name := range preferred {
		candidate := filepath.Join(repoPath, name)
		if fileExists(candidate) {
			return candidate
		}
	}

	markdownFiles := walkMarkdownFiles(repoPath)
	if len(markdownFiles) == 0 {
		return ""
	}
	return markdownFiles[0]
}

func walkMarkdownFiles(repoPath string) []string {
	ignoredDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		".next":        true,
		".cache":       true,
		"dist":         true,
		"build":        true,
		"target":       true,
		".venv":        true,
		"venv":         true,
	}

	stack := []string{repoPath}
	found := make([]string, 0)
	scanned := 0
	const maxEntries = 12000

	for len(stack) > 0 && scanned < maxEntries {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			scanned++
			if scanned >= maxEntries {
				break
			}
			full := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if !ignoredDirs[entry.Name()] {
					stack = append(stack, full)
				}
				continue
			}
			if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") || strings.HasSuffix(strings.ToLower(entry.Name()), ".markdown") {
				found = append(found, full)
			}
		}
	}

	sort.Strings(found)
	return found
}

type localFilters struct {
	q                string
	sort             string
	dir              string
	pathPrefix       string
	identifierPrefix string
}

func parseLocalFilters(q url.Values) localFilters {
	sortBy := q.Get("sort")
	if sortBy == "" {
		sortBy = "path"
	}
	dir := strings.ToLower(q.Get("dir"))
	if dir != "desc" {
		dir = "asc"
	}
	return localFilters{
		q:                strings.TrimSpace(q.Get("q")),
		sort:             sortBy,
		dir:              dir,
		pathPrefix:       q.Get("path_prefix"),
		identifierPrefix: q.Get("identifier_prefix"),
	}
}

func (l *localBackend) fetchRows(filters localFilters, options localFetchOptions) ([]localDBRow, error) {
	whereParts := make([]string, 0)

	if filters.q != "" {
		like := "%" + escapeLike(filters.q) + "%"
		quoted := sqlQuote(like)
		whereParts = append(whereParts, fmt.Sprintf("(path LIKE %s ESCAPE '\\' OR identifier LIKE %s ESCAPE '\\' OR branch LIKE %s ESCAPE '\\' OR lineage LIKE %s ESCAPE '\\' OR last_commit_author LIKE %s ESCAPE '\\' OR last_commit_at LIKE %s ESCAPE '\\')",
			quoted, quoted, quoted, quoted, quoted, quoted))
	}

	if !options.ignorePathPrefix {
		pathPrefix := normalizeLocalPath(filters.pathPrefix)
		switch {
		case pathPrefix == "/":
			whereParts = append(whereParts, "path LIKE '/%'")
		case pathPrefix != "":
			whereParts = append(whereParts, fmt.Sprintf("(path = %s OR path LIKE %s)", sqlQuote(pathPrefix), sqlQuote(pathPrefix+"/%")))
		}
	}

	sql := "SELECT path, identifier, lineage, branch, last_commit_author, last_commit_at, last_seen_at, is_bare FROM repositories"
	if len(whereParts) > 0 {
		sql += " WHERE " + strings.Join(whereParts, " AND ")
	}
	sql += ";"

	rows, err := runSQLiteJSON[localDBRow](l.dbPath, sql)
	if err != nil {
		return nil, err
	}
	if !options.ignoreIdentifierPrefix && filters.identifierPrefix != "" {
		filtered := rows[:0]
		for _, item := range rows {
			if identifierHasPrefix(item, filters.identifierPrefix) {
				filtered = append(filtered, item)
			}
		}
		rows = filtered
	}
	return rows, nil
}

func (l *localBackend) countRepositories() (int, error) {
	type countRow struct {
		Count int `json:"c"`
	}

	rows, err := runSQLiteJSON[countRow](l.dbPath, "SELECT count(*) AS c FROM repositories;")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}

func (l *localBackend) maxLastSeenAt() (string, error) {
	type valueRow struct {
		LastSeenAt string `json:"last_seen_at"`
	}

	rows, err := runSQLiteJSON[valueRow](l.dbPath, "SELECT COALESCE(max(last_seen_at), '') AS last_seen_at FROM repositories;")
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].LastSeenAt, nil
}

func runSQLiteJSON[T any](dbPath string, sql string) ([]T, error) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, errors.New("sqlite3 executable not found")
	}
	cmd := exec.Command(sqlitePath, "-json", dbPath, sql)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	raw := bytes.TrimSpace(stdout.Bytes())
	if len(raw) == 0 {
		return []T{}, nil
	}
	var out []T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func escapeLike(raw string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(raw)
}

func sqlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func normalizeLocalPath(raw string) string {
	value := strings.ReplaceAll(strings.TrimSpace(raw), `\`, `/`)
	if value == "" {
		return ""
	}
	if value == "/" {
		return "/"
	}
	return strings.TrimRight(value, "/")
}

func identifierHasPrefix(row localDBRow, prefix string) bool {
	key := identifierKeyFromParts(row.Identifier, row.Lineage)
	filter := strings.TrimSpace(prefix)
	if filter == "" {
		return true
	}
	return key == filter || strings.HasPrefix(key, filter+"/")
}

func identifierKeyFromParts(identifier string, lineage string) string {
	upstream := strings.TrimSpace(identifier)
	if upstream == "" {
		lineageValue := strings.TrimSpace(lineage)
		if lineageValue != "" {
			return "local/" + strings.TrimPrefix(lineageValue, "local:")
		}
		return "local/none"
	}
	return parseIdentifierToCanonicalKey(upstream)
}

func identifierDisplayFromParts(identifier string, lineage string) string {
	upstream := strings.TrimSpace(identifier)
	if upstream != "" {
		return upstream
	}
	lineageValue := strings.TrimSpace(lineage)
	if strings.HasPrefix(lineageValue, "local:") {
		return lineageValue
	}
	if lineageValue != "" {
		return "local:" + lineageValue
	}
	return "local:none"
}

func parseIdentifierToCanonicalKey(rawIdentifier string) string {
	raw := strings.TrimSpace(rawIdentifier)
	if raw == "" {
		return "(unknown)"
	}

	if matches := regexp.MustCompile(`^[^@]+@([^:/\s]+):(.+)$`).FindStringSubmatch(raw); len(matches) == 3 {
		host := strings.ToLower(matches[1])
		pathPart := strings.TrimLeft(strings.TrimSuffix(matches[2], ".git"), "/")
		parts := filterEmpty(strings.Split(pathPart, "/"))
		return strings.Join(append([]string{host}, parts...), "/")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "(unknown)"
	}
	host := strings.ToLower(parsed.Host)
	pathPart := strings.TrimLeft(strings.TrimSuffix(parsed.Path, ".git"), "/")
	parts := filterEmpty(strings.Split(pathPart, "/"))
	return strings.Join(append([]string{host}, parts...), "/")
}

func filterEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func sortRowsLocal(rows []localDBRow, sortBy string, dir string) {
	if sortBy == "origin" {
		sortBy = "identifier"
	}
	validSort := map[string]bool{
		"path":               true,
		"identifier":         true,
		"branch":             true,
		"last_commit_author": true,
		"last_commit_at":     true,
		"last_seen_at":       true,
	}
	if !validSort[sortBy] {
		sortBy = "path"
	}
	desc := dir == "desc"

	sort.Slice(rows, func(i, j int) bool {
		left := sortableValue(rows[i], sortBy)
		right := sortableValue(rows[j], sortBy)
		if left == right {
			if desc {
				return rows[i].Path > rows[j].Path
			}
			return rows[i].Path < rows[j].Path
		}
		if desc {
			return left > right
		}
		return left < right
	})
}

func sortableValue(row localDBRow, sortBy string) string {
	switch sortBy {
	case "identifier":
		return row.Identifier
	case "branch":
		return row.Branch
	case "last_commit_author":
		return row.LastCommitAuthor
	case "last_commit_at":
		return row.LastCommitAt
	case "last_seen_at":
		return row.LastSeenAt
	default:
		return row.Path
	}
}

func buildLocalPathTreeFacet(rows []localDBRow) []treeNode {
	return buildTreeFacet(rows, func(row localDBRow) string {
		normalized := normalizeLocalPath(row.Path)
		if normalized == "" || !strings.HasPrefix(normalized, "/") {
			return ""
		}
		return strings.TrimPrefix(normalized, "/")
	}, 650, true)
}

func buildIdentifierTreeFacet(rows []localDBRow) []treeNode {
	return buildTreeFacet(rows, func(row localDBRow) string {
		return identifierKeyFromParts(row.Identifier, row.Lineage)
	}, 650, false)
}

func buildTreeFacet(rows []localDBRow, keySelector func(localDBRow) string, maxNodes int, leadingSlash bool) []treeNode {
	counts := map[string]int{}
	labels := map[string]string{}
	parents := map[string]string{}
	depths := map[string]int{}

	for _, row := range rows {
		key := keySelector(row)
		if key == "" {
			continue
		}
		segments := filterEmpty(strings.Split(key, "/"))
		if len(segments) == 0 {
			continue
		}

		prefix := ""
		for i, segment := range segments {
			if prefix == "" {
				prefix = segment
			} else {
				prefix += "/" + segment
			}
			counts[prefix]++
			labels[prefix] = segment
			depths[prefix] = i + 1
			if i > 0 {
				parents[prefix] = strings.Join(segments[:i], "/")
			}
		}
	}

	nodes := make([]treeNode, 0, len(counts))
	for prefix, count := range counts {
		parent := parents[prefix]
		nodePrefix := prefix
		parentPrefix := parent
		if leadingSlash {
			nodePrefix = "/" + nodePrefix
			if parentPrefix != "" {
				parentPrefix = "/" + parentPrefix
			}
		}
		nodes = append(nodes, treeNode{
			Prefix:       nodePrefix,
			Label:        labels[prefix],
			ParentPrefix: parentPrefix,
			Depth:        depths[prefix],
			Count:        count,
		})
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Depth != nodes[j].Depth {
			return nodes[i].Depth < nodes[j].Depth
		}
		if nodes[i].Count != nodes[j].Count {
			return nodes[i].Count > nodes[j].Count
		}
		return nodes[i].Prefix < nodes[j].Prefix
	})
	if len(nodes) > maxNodes {
		nodes = nodes[:maxNodes]
	}
	return nodes
}
