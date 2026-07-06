package dots

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

type repoGitIgnoreMatcher struct {
	root          string
	basePatterns  []gitignore.Pattern
	patternsByDir map[string][]gitignore.Pattern
}

func newRepoGitIgnoreMatcher(root string) (*repoGitIgnoreMatcher, error) {
	cleanRoot := filepath.Clean(root)
	var basePatterns []gitignore.Pattern
	gitInfo, err := os.Stat(filepath.Join(cleanRoot, ".git"))
	switch {
	case err == nil && gitInfo.IsDir():
		basePatterns, err = readGitIgnorePatterns(filepath.Join(cleanRoot, ".git", "info", "exclude"), nil)
		if err != nil {
			return nil, fmt.Errorf("load .git/info/exclude: %w", err)
		}
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Errorf("stat .git: %w", err)
	}
	return &repoGitIgnoreMatcher{
		root:          cleanRoot,
		basePatterns:  basePatterns,
		patternsByDir: make(map[string][]gitignore.Pattern),
	}, nil
}

func (m *repoGitIgnoreMatcher) ignored(repoRel string, isDir bool) (bool, error) {
	cleaned := path.Clean(filepath.ToSlash(repoRel))
	if cleaned == "." {
		return false, nil
	}
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false, fmt.Errorf("gitignore path %q is outside repo", repoRel)
	}

	patterns, err := m.patternsForDir(path.Dir(cleaned))
	if err != nil {
		return false, err
	}
	if len(patterns) == 0 {
		return false, nil
	}
	return gitignore.NewMatcher(patterns).Match(strings.Split(cleaned, "/"), isDir), nil
}

func (m *repoGitIgnoreMatcher) patternsForDir(dir string) ([]gitignore.Pattern, error) {
	dir = path.Clean(strings.TrimPrefix(dir, "./"))
	if dir == "" {
		dir = "."
	}
	if path.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, "../") {
		return nil, fmt.Errorf("gitignore directory %q is outside repo", dir)
	}
	if patterns, ok := m.patternsByDir[dir]; ok {
		return patterns, nil
	}

	var patterns []gitignore.Pattern
	if dir == "." {
		patterns = append([]gitignore.Pattern(nil), m.basePatterns...)
	} else {
		parentPatterns, err := m.patternsForDir(path.Dir(dir))
		if err != nil {
			return nil, err
		}
		patterns = append([]gitignore.Pattern(nil), parentPatterns...)
	}

	localPatterns, err := readGitIgnorePatterns(filepath.Join(m.root, filepath.FromSlash(dir), ".gitignore"), gitIgnoreDomain(dir))
	if err != nil {
		return nil, fmt.Errorf("load .gitignore for %s: %w", dir, err)
	}
	patterns = append(patterns, localPatterns...)
	m.patternsByDir[dir] = patterns
	return patterns, nil
}

func gitIgnoreDomain(dir string) []string {
	if dir == "." {
		return nil
	}
	return strings.Split(dir, "/")
}

func readGitIgnorePatterns(filePath string, domain []string) ([]gitignore.Pattern, error) {
	file, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return patterns, nil
}
