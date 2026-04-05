package sessionlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/michalswi/local-agent-on-steroids/types"
)

// sessionDir is the directory where session JSON records are written.
// It can be overridden at startup via SetDir.
var sessionDir = defaultSessionDir()

func defaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "local-agent-on-steroids")
	}
	return filepath.Join(home, "Downloads", "local-agent-on-steroids")
}

// SetDir overrides the directory used to store session JSON records.
func SetDir(path string) {
	sessionDir = path
}

// Dir returns the current session directory (may have been overridden by SetDir).
func Dir() string {
	return sessionDir
}

// DefaultDir returns the default or a temp fallback if
// the home directory cannot be determined.
func DefaultDir() string {
	return defaultSessionDir()
}

type ScanSummary struct {
	TotalFiles    int           `json:"total_files"`
	FilteredFiles int           `json:"filtered_files"`
	TotalSize     int64         `json:"total_size"`
	Duration      time.Duration `json:"duration"`
}

type Record struct {
	Timestamp   time.Time       `json:"timestamp"`
	Mode        string          `json:"mode"`
	Directory   string          `json:"directory"`
	Task        string          `json:"task,omitempty"`
	Focus       string          `json:"focus,omitempty"`
	Model       string          `json:"model,omitempty"`
	TokensUsed  int             `json:"tokens_used,omitempty"`
	FileTokens  map[string]int  `json:"file_tokens,omitempty"`
	Duration    time.Duration   `json:"duration,omitempty"`
	Files       []string        `json:"files,omitempty"`
	Findings    []types.Finding `json:"findings,omitempty"`
	Response    string          `json:"response,omitempty"`
	ScanSummary *ScanSummary    `json:"scan_summary,omitempty"`
}

func Save(record *Record) (string, error) {
	if record == nil {
		return "", errors.New("record is nil")
	}

	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if record.Mode == "" {
		record.Mode = "standalone"
	}

	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return "", fmt.Errorf("create session dir: %w", err)
	}

	filename := fmt.Sprintf("local-agent-%s.json", record.Timestamp.Format("20060102-150405"))
	path := filepath.Join(sessionDir, filename)

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write session file: %w", err)
	}

	return path, nil
}

func FilesFromTokens(fileTokens map[string]int, focus string) []string {
	if len(fileTokens) == 0 {
		if focus != "" {
			return []string{focus}
		}
		return nil
	}

	files := make([]string, 0, len(fileTokens))
	for file := range fileTokens {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}
