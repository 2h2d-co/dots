package dots

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	gitleaksconfig "github.com/zricethezav/gitleaks/v8/config"
	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/sources"
)

const (
	gitleaksRuleGenericAPIKey = "generic-api-key"
	gitleaksRuleNPMToken      = "npm-access-token"
)

type canonicalHomeFile struct {
	Content []byte
	Record  FileRecord
}

type secretFindingSummary struct {
	Path        string
	RuleID      string
	Description string
	StartLine   int
	EndLine     int
}

type secretScanError struct {
	Findings []secretFindingSummary
}

var (
	gitleaksConfigOnce sync.Once
	gitleaksConfig     gitleaksconfig.Config
	gitleaksConfigErr  error
)

func (e secretScanError) Error() string {
	if len(e.Findings) == 0 {
		return "secret scan blocked unsupported finding"
	}
	parts := make([]string, 0, len(e.Findings))
	for _, finding := range e.Findings {
		location := fmt.Sprintf("%s:%d", finding.Path, finding.StartLine)
		if finding.EndLine > finding.StartLine {
			location = fmt.Sprintf("%s:%d-%d", finding.Path, finding.StartLine, finding.EndLine)
		}
		parts = append(parts, fmt.Sprintf("%s %s: %s", location, finding.RuleID, finding.Description))
	}
	return fmt.Sprintf("secret scan blocked %d finding(s): %s", len(e.Findings), strings.Join(parts, "; "))
}

func readCanonicalHomeFile(trackedPath, filePath string, enforceClean bool) (canonicalHomeFile, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return canonicalHomeFile{}, fmt.Errorf("stat %s: %w", filePath, err)
	}
	if !info.Mode().IsRegular() {
		return canonicalHomeFile{}, fmt.Errorf("unsupported file type at %s", filePath)
	}
	fileHandle, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		return canonicalHomeFile{}, fmt.Errorf("open %s: %w", filePath, err)
	}
	content, err := io.ReadAll(fileHandle)
	if err != nil {
		return canonicalHomeFile{}, errors.Join(fmt.Errorf("read %s: %w", filePath, err), fileHandle.Close())
	}
	if err := fileHandle.Close(); err != nil {
		return canonicalHomeFile{}, fmt.Errorf("close %s: %w", filePath, err)
	}
	canonical, err := canonicalizeHomeContent(trackedPath, content, enforceClean)
	if err != nil {
		return canonicalHomeFile{}, err
	}
	return canonicalHomeFile{
		Content: canonical,
		Record: FileRecord{
			Path:   trackedPath,
			SHA256: hashBytes(canonical),
			Mode:   int64(info.Mode().Perm()),
			Size:   int64(len(canonical)),
		},
	}, nil
}

func destinationCanonicalFingerprint(trackedPath, filePath string) (sha string, mode int64, err error) {
	file, err := readCanonicalHomeFile(trackedPath, filePath, false)
	if err != nil {
		return "", 0, err
	}
	return file.Record.SHA256, file.Record.Mode, nil
}

func canonicalizeHomeContent(trackedPath string, content []byte, enforceClean bool) ([]byte, error) {
	if !shouldScanCanonicalContent(trackedPath, content, enforceClean) {
		return content, nil
	}

	findings, err := scanSecrets(trackedPath, content)
	if err != nil {
		return nil, err
	}
	scrubLines := secretScrubLines(trackedPath, content, findings)
	canonical := content
	if len(scrubLines) > 0 {
		canonical = blankLines(content, scrubLines)
	}
	if !enforceClean {
		return canonical, nil
	}

	remaining, err := scanSecrets(trackedPath, canonical)
	if err != nil {
		return nil, err
	}
	if len(remaining) > 0 {
		return nil, secretScanError{Findings: remaining}
	}
	return canonical, nil
}

func scanSecrets(trackedPath string, content []byte) ([]secretFindingSummary, error) {
	cfg, err := defaultGitleaksConfig()
	if err != nil {
		return nil, err
	}
	detector := detect.NewDetector(cfg)
	detector.Redact = 100
	detector.MaxDecodeDepth = 5

	findings, err := detector.DetectSource(context.Background(), &sources.File{
		Content: bytes.NewReader(content),
		Path:    path.Clean(trackedPath),
		Config:  &cfg,
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s for secrets: %w", trackedPath, err)
	}
	summaries := make([]secretFindingSummary, 0, len(findings))
	for _, finding := range findings {
		summaryPath := finding.File
		if summaryPath == "" {
			summaryPath = trackedPath
		}
		summaries = append(summaries, secretFindingSummary{
			Path:        summaryPath,
			RuleID:      finding.RuleID,
			Description: finding.Description,
			StartLine:   finding.StartLine,
			EndLine:     finding.EndLine,
		})
	}
	sortSecretFindings(summaries)
	return summaries, nil
}

func defaultGitleaksConfig() (gitleaksconfig.Config, error) {
	gitleaksConfigOnce.Do(func() {
		detector, err := detect.NewDetectorDefaultConfig()
		if err != nil {
			gitleaksConfigErr = fmt.Errorf("load gitleaks default config: %w", err)
			return
		}
		gitleaksConfig = detector.Config
	})
	return gitleaksConfig, gitleaksConfigErr
}

func shouldScanCanonicalContent(trackedPath string, content []byte, enforceClean bool) bool {
	if enforceClean || isNPMRCPath(trackedPath) {
		return true
	}
	return bytes.Contains(bytes.ToLower(content), []byte("npm_"))
}

func secretScrubLines(trackedPath string, content []byte, findings []secretFindingSummary) map[int]struct{} {
	lines := make(map[int]struct{})
	for _, finding := range findings {
		if finding.StartLine <= 0 || finding.EndLine != finding.StartLine {
			continue
		}
		switch finding.RuleID {
		case gitleaksRuleNPMToken:
			lines[finding.StartLine] = struct{}{}
		case gitleaksRuleGenericAPIKey:
			if isNPMRCPath(trackedPath) && isNPMRCAuthLine(lineAt(content, finding.StartLine)) {
				lines[finding.StartLine] = struct{}{}
			}
		}
	}
	return lines
}

func isNPMRCPath(trackedPath string) bool {
	cleaned := path.Clean(trackedPath)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return false
	}
	base := path.Base(cleaned)
	return base == ".npmrc" || base == "npmrc"
}

func isNPMRCAuthLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
		return false
	}
	left, _, ok := strings.Cut(trimmed, "=")
	if !ok {
		return false
	}
	key := strings.TrimSpace(left)
	if idx := strings.LastIndex(key, ":"); idx >= 0 {
		key = strings.TrimSpace(key[idx+1:])
	}
	return strings.EqualFold(key, "_authToken") || strings.EqualFold(key, "_auth") || strings.EqualFold(key, "_password")
}

func lineAt(content []byte, lineNumber int) string {
	if lineNumber <= 0 {
		return ""
	}
	currentLine := 1
	start := 0
	for i, b := range content {
		if b != '\n' {
			continue
		}
		if currentLine == lineNumber {
			return strings.TrimSuffix(string(content[start:i]), "\r")
		}
		start = i + 1
		currentLine++
	}
	if currentLine == lineNumber && start <= len(content) {
		return strings.TrimSuffix(string(content[start:]), "\r")
	}
	return ""
}

func blankLines(content []byte, lines map[int]struct{}) []byte {
	if len(lines) == 0 {
		return content
	}
	var out bytes.Buffer
	out.Grow(len(content))

	lineNumber := 1
	start := 0
	for i, b := range content {
		if b != '\n' {
			continue
		}
		if _, ok := lines[lineNumber]; ok {
			if i > start && content[i-1] == '\r' {
				out.WriteByte('\r')
			}
			out.WriteByte('\n')
		} else {
			out.Write(content[start : i+1])
		}
		start = i + 1
		lineNumber++
	}
	if start < len(content) {
		if _, ok := lines[lineNumber]; !ok {
			out.Write(content[start:])
		}
	}
	return out.Bytes()
}

func sortSecretFindings(findings []secretFindingSummary) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].StartLine != findings[j].StartLine {
			return findings[i].StartLine < findings[j].StartLine
		}
		if findings[i].EndLine != findings[j].EndLine {
			return findings[i].EndLine < findings[j].EndLine
		}
		return findings[i].RuleID < findings[j].RuleID
	})
}

func hashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func writeFileBytes(dst string, content []byte, mode int64) error {
	fileMode, err := fileModeFromRecord(mode)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("create destination directory for %s: %w", dst, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dots-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", dst, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(content); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", dst, err), tmp.Close())
	}
	if err := tmp.Chmod(fileMode); err != nil {
		return errors.Join(fmt.Errorf("set mode on %s: %w", tmpPath, err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", dst, err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("install %s: %w", dst, err)
	}
	return nil
}
