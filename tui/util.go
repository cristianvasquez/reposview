package main

import (
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func fuzzyFilterRows(rows []row, query string) []row {
	if strings.TrimSpace(query) == "" {
		out := make([]row, len(rows))
		copy(out, rows)
		return out
	}

	type scoredRow struct {
		row   row
		score int
	}

	needle := strings.ToLower(strings.TrimSpace(query))
	scored := make([]scoredRow, 0, len(rows))
	for _, r := range rows {
		haystacks := []string{
			strings.ToLower(r.Path),
			strings.ToLower(r.Identifier),
			strings.ToLower(r.Branch),
		}
		best := -1
		for _, haystack := range haystacks {
			score := fuzzyScore(haystack, needle)
			if score > best {
				best = score
			}
		}
		if best >= 0 {
			scored = append(scored, scoredRow{row: r, score: best})
		}
	}

	slices.SortFunc(scored, func(a, b scoredRow) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.row.Path, b.row.Path)
	})

	out := make([]row, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.row)
	}
	return out
}

func fuzzyScore(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	if haystack == "" {
		return -1
	}

	if strings.Contains(haystack, needle) {
		score := 200 + len(needle)*10
		if strings.HasPrefix(haystack, needle) {
			score += 80
		}
		return score
	}

	score := 0
	lastIndex := -1
	consecutive := 0
	for _, ch := range needle {
		idx := strings.IndexRune(haystack[lastIndex+1:], ch)
		if idx < 0 {
			return -1
		}
		pos := lastIndex + 1 + idx
		score += 10
		if pos == 0 || isWordBoundary(haystack, pos) {
			score += 18
		}
		if pos == lastIndex+1 {
			consecutive++
			score += 12 + consecutive*4
		} else {
			consecutive = 0
			score -= idx
		}
		lastIndex = pos
	}

	score -= len(haystack) / 8
	return score
}

func isWordBoundary(s string, idx int) bool {
	if idx <= 0 || idx > len(s)-1 {
		return idx == 0
	}
	prev := s[idx-1]
	switch prev {
	case '/', '-', '_', '.', ' ':
		return true
	default:
		return false
	}
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

func emptyFallback(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(none)"
	}
	return v
}

func compactPathLabel(pathValue string, segments int) string {
	normalized := filepath.ToSlash(strings.TrimSpace(pathValue))
	if normalized == "" {
		return "(none)"
	}
	parts := strings.FieldsFunc(normalized, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return normalized
	}
	if segments <= 0 || len(parts) <= segments {
		return strings.Join(parts, "/")
	}
	return strings.Join(parts[len(parts)-segments:], "/")
}

func compactIdentifierLabel(identifier string, segments int) string {
	raw := strings.TrimSpace(identifier)
	if raw == "" {
		return "(none)"
	}
	if strings.HasPrefix(raw, "local:") {
		localParts := []string{"local", strings.TrimPrefix(raw, "local:")}
		if segments <= 0 || len(localParts) <= segments {
			return strings.Join(localParts, "/")
		}
		return strings.Join(localParts[len(localParts)-segments:], "/")
	}

	if parsed := identifierPathParts(raw); len(parsed) > 0 {
		if segments <= 0 || len(parsed) <= segments {
			return strings.Join(parsed, "/")
		}
		return strings.Join(parsed[len(parsed)-segments:], "/")
	}

	return raw
}

func identifierToBrowserURL(identifier string) string {
	raw := strings.TrimSpace(identifier)
	if raw == "" || strings.HasPrefix(raw, "local:") {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}

	scpLike := regexp.MustCompile(`^[^@]+@([^:/\s]+):(.+)$`).FindStringSubmatch(raw)
	if len(scpLike) == 3 {
		host := scpLike[1]
		path := strings.TrimPrefix(strings.TrimSuffix(scpLike[2], ".git"), "/")
		if host != "" && path != "" {
			return "https://" + host + "/" + path
		}
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch parsed.Scheme {
	case "ssh", "git":
		path := strings.TrimPrefix(strings.TrimSuffix(parsed.Path, ".git"), "/")
		if parsed.Hostname() != "" && path != "" {
			return "https://" + parsed.Hostname() + "/" + path
		}
	case "http", "https":
		return raw
	}
	return ""
}

func identifierPathParts(identifier string) []string {
	browserURL := identifierToBrowserURL(identifier)
	if browserURL == "" {
		return nil
	}
	parsed, err := url.Parse(browserURL)
	if err != nil {
		return nil
	}
	path := strings.TrimPrefix(strings.TrimSuffix(parsed.Path, ".git"), "/")
	if path == "" {
		return nil
	}
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
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
