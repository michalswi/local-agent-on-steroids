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

const sessionDir = "/tmp/local-agent"

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
