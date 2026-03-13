package types

import (
	"time"
)

// Constants for file size thresholds and token limits
const (
	// File size categories
	SmallFileSizeBytes  = 10 * 1024        // 10KB
	MediumFileSizeBytes = 100 * 1024       // 100KB
	MaxFileSizeBytes    = 10 * 1024 * 1024 // 10MB

	// Token limits
	DefaultChunkSize = 1000
	DefaultOverlap   = 100

	// Scanning limits
	DefaultMaxDepth      = 20
	DefaultConcurrentOps = 10
)

// FileCategory represents the size category of a file
type FileCategory string

const (
	CategorySmall  FileCategory = "small"
	CategoryMedium FileCategory = "medium"
	CategoryLarge  FileCategory = "large"
)

// FileType represents the detected type of a file
type FileType string

const (
	TypeText      FileType = "text"
	TypeBinary    FileType = "binary"
	TypeArchive   FileType = "archive"
	TypeImage     FileType = "image"
	TypePDF       FileType = "pdf"
	TypePCAP      FileType = "pcap"
	TypeUnknown   FileType = "unknown"
	TypeSensitive FileType = "sensitive"
)

// FileInfo represents metadata and content information about a file
type FileInfo struct {
	Path        string              `json:"path"`
	RelPath     string              `json:"rel_path"`
	Size        int64               `json:"size"`
	Category    FileCategory        `json:"category"`
	Type        FileType            `json:"type"`
	Extension   string              `json:"extension"`
	ModTime     time.Time           `json:"mod_time"`
	IsReadable  bool                `json:"is_readable"`
	IsSensitive bool                `json:"is_sensitive"`
	Violations  []SecurityViolation `json:"violations,omitempty"`
	TokenCount  int                 `json:"token_count,omitempty"`
	Content     string              `json:"content,omitempty"`
	Summary     string              `json:"summary,omitempty"`
	Chunks      []FileChunk         `json:"chunks,omitempty"`
}

// FileChunk represents a portion of a large file
type FileChunk struct {
	Index       int    `json:"index"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	StartOffset int64  `json:"start_offset"`
	EndOffset   int64  `json:"end_offset"`
	Content     string `json:"content"`
	TokenCount  int    `json:"token_count"`
}

// ScanResult represents the result of scanning a directory
type ScanResult struct {
	RootPath      string         `json:"root_path"`
	TotalFiles    int            `json:"total_files"`
	FilteredFiles int            `json:"filtered_files"`
	TotalSize     int64          `json:"total_size"`
	Files         []FileInfo     `json:"files"`
	Errors        []ScanError    `json:"errors,omitempty"`
	Duration      time.Duration  `json:"duration"`
	Summary       map[string]int `json:"summary"` // category/type counts
}

// ScanError represents an error encountered during scanning
type ScanError struct {
	Path  string    `json:"path"`
	Error string    `json:"error"`
	Time  time.Time `json:"time"`
}

// AnalysisRequest represents a request to analyze files with an LLM
type AnalysisRequest struct {
	Task        string     `json:"task"`
	Files       []FileInfo `json:"files"`
	Context     string     `json:"context,omitempty"`
	MaxTokens   int        `json:"max_tokens"`
	Temperature float64    `json:"temperature"`
	Model       string     `json:"model"`
}

// AnalysisResponse represents the LLM's analysis response
type AnalysisResponse struct {
	Response    string         `json:"response"`
	Model       string         `json:"model"`
	TokensUsed  int            `json:"tokens_used"`
	FileTokens  map[string]int `json:"file_tokens,omitempty"`
	Duration    time.Duration  `json:"duration"`
	Findings    []Finding      `json:"findings,omitempty"`
	Suggestions []string       `json:"suggestions,omitempty"`
}

// Finding represents a specific finding in the analysis
type Finding struct {
	File        string   `json:"file"`
	Line        int      `json:"line,omitempty"`
	Severity    Severity `json:"severity"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Suggestion  string   `json:"suggestion,omitempty"`
}

// Severity represents the severity level of a finding
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// FilterRule represents a file filtering rule
type FilterRule struct {
	Pattern string `json:"pattern"`
	Type    string `json:"type"` // "allow" or "deny"
}

// AgentState represents the local state maintained by the agent
type AgentState struct {
	CurrentTask    string               `json:"current_task"`
	ScannedFiles   map[string]*FileInfo `json:"scanned_files"`
	Inventory      *ScanResult          `json:"inventory"`
	LastAnalysis   *AnalysisResponse    `json:"last_analysis,omitempty"`
	ConversationID string               `json:"conversation_id"`
	CreatedAt      time.Time            `json:"created_at"`
}

// ChunkingStrategy defines how large files should be chunked
type ChunkingStrategy string

const (
	StrategyLines  ChunkingStrategy = "lines"
	StrategyTokens ChunkingStrategy = "tokens"
	StrategySmart  ChunkingStrategy = "smart" // Context-aware chunking
)

// SecurityViolation represents a security or privacy concern
type SecurityViolation struct {
	File        string  `json:"file"`
	Line        int     `json:"line"`
	Type        string  `json:"type"` // "secret", "pii", "credential", etc.
	Pattern     string  `json:"pattern"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"` // 0.0 to 1.0
}
