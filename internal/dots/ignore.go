package dots

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type ignoreMatcher struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	raw       string
	directory bool
}

func loadDotsIgnore(path string) (ignoreMatcher, error) {
	cleaned := filepath.Clean(path)
	file, err := os.Open(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			return ignoreMatcher{}, nil
		}
		return ignoreMatcher{}, err
	}
	defer func() { _ = file.Close() }()

	var patterns []ignorePattern
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "/")
		patterns = append(patterns, ignorePattern{
			raw:       strings.TrimSuffix(line, "/"),
			directory: strings.HasSuffix(line, "/"),
		})
	}
	if err := scanner.Err(); err != nil {
		return ignoreMatcher{}, err
	}
	return ignoreMatcher{patterns: patterns}, nil
}

func (m ignoreMatcher) ignored(rel string, isDir bool) bool {
	rel = path.Clean(strings.TrimPrefix(rel, "./"))
	if rel == "." || rel == ".dotsignore" {
		return false
	}
	for _, pattern := range m.patterns {
		if pattern.raw == "" {
			continue
		}
		if pattern.directory && !isDir && !strings.HasPrefix(rel, pattern.raw+"/") {
			continue
		}
		if matchesIgnorePattern(pattern.raw, rel) {
			return true
		}
		if pattern.directory && strings.HasPrefix(rel, pattern.raw+"/") {
			return true
		}
	}
	return false
}

func matchesIgnorePattern(pattern, rel string) bool {
	if strings.Contains(pattern, "/") {
		matched, err := path.Match(pattern, rel)
		if err == nil && matched {
			return true
		}
		return strings.HasPrefix(rel, pattern+"/")
	}
	for _, part := range strings.Split(rel, "/") {
		matched, err := path.Match(pattern, part)
		if err == nil && matched {
			return true
		}
	}
	return false
}
