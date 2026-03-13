package main

import "strings"

func normalizePathForMatch(raw string) string {
	value := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if value == "" {
		return ""
	}
	if value == "/" {
		return "/"
	}
	return strings.TrimRight(value, "/")
}

func pathMatchesPrefix(target, prefix string) bool {
	if prefix == "" {
		return false
	}
	if prefix == "/" {
		return strings.HasPrefix(target, "/")
	}
	return target == prefix || strings.HasPrefix(target, prefix+"/")
}
