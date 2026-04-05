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

// LangEntry describes a programming language or file type for agent heuristics.
// All language-aware logic is driven from LangRegistry so that adding support
// for a new language only requires a single new entry here.
type LangEntry struct {
	Ext       string   // canonical file extension including dot, e.g. ".go"
	IsCode    bool     // true → counted by the dominant-extension heuristic
	TechKW    []string // task keywords indicating this technology is present (tech-mismatch detection)
	ImpliedKW []string // ordered task keywords implying a new file of this type should be created
	AllowGlob string   // default filter allow-pattern glob, e.g. "*.go"; "" = omit from defaults
}

// LangRegistry is the single source of truth for all language and file-type
// heuristics used by the agent. Entries are ordered to preserve first-match
// semantics in implied-extension detection.
//
// To add support for a new language: add ONE entry here.
// No other code needs to change.
var LangRegistry = []LangEntry{
	// ── documentation / config formats ───────────────────────────────────────
	{Ext: ".md", AllowGlob: "*.md",
		ImpliedKW: []string{".md", "md file", "markdown", "readme"}},
	{Ext: ".yaml", AllowGlob: "*.yaml",
		ImpliedKW: []string{".yaml", "yaml file"}},
	{Ext: ".yml", AllowGlob: "*.yml",
		ImpliedKW: []string{".yml", "yml file"}},
	{Ext: ".json", AllowGlob: "*.json",
		ImpliedKW: []string{".json", "json file"}},
	{Ext: ".toml", AllowGlob: "*.toml",
		ImpliedKW: []string{".toml", "toml file"}},
	{Ext: ".txt", AllowGlob: "*.txt",
		ImpliedKW: []string{".txt", "txt file", "text file"}},
	{Ext: ".sh", AllowGlob: "*.sh",
		ImpliedKW: []string{".sh", "shell script", "bash script"}},
	{Ext: ".html", AllowGlob: "",
		ImpliedKW: []string{".html", "html file"}},
	{Ext: ".css", AllowGlob: "",
		ImpliedKW: []string{".css", "css file"}},
	{Ext: ".env", AllowGlob: "*.env",
		ImpliedKW: []string{".env", "env file"}},
	// ── web / JS family ──────────────────────────────────────────────────────
	{Ext: ".ts", IsCode: true, AllowGlob: "*.ts",
		TechKW:    []string{"typescript", " ts ", ".ts "},
		ImpliedKW: []string{"typescript", ".ts ", "angular"}},
	{Ext: ".tsx", IsCode: true, AllowGlob: "",
		TechKW:    []string{"react", ".tsx"},
		ImpliedKW: []string{".tsx", "react"}},
	{Ext: ".vue", IsCode: true, AllowGlob: "",
		TechKW:    []string{"vue"},
		ImpliedKW: []string{"vue"}},
	{Ext: ".js", IsCode: true, AllowGlob: "*.js",
		TechKW:    []string{"javascript", " js ", ".js ", "nodejs", "node.js"},
		ImpliedKW: []string{"javascript", ".js ", "node"}},
	// ── Python ───────────────────────────────────────────────────────────────
	{Ext: ".py", IsCode: true, AllowGlob: "*.py",
		TechKW:    []string{"python", ".py "},
		ImpliedKW: []string{"python", ".py "}},
	// ── Go ───────────────────────────────────────────────────────────────────
	{Ext: ".go", IsCode: true, AllowGlob: "*.go",
		ImpliedKW: []string{"golang", " go app", " go server"}},
	// ── Rust ─────────────────────────────────────────────────────────────────
	{Ext: ".rs", IsCode: true, AllowGlob: "*.rs",
		TechKW:    []string{"rust", ".rs "},
		ImpliedKW: []string{"rust"}},
	// ── Java / JVM ───────────────────────────────────────────────────────────
	{Ext: ".java", IsCode: true, AllowGlob: "*.java",
		TechKW:    []string{"java "},
		ImpliedKW: []string{"java "}},
	{Ext: ".kt", IsCode: true, AllowGlob: "*.kt",
		TechKW:    []string{"kotlin"},
		ImpliedKW: []string{"kotlin"}},
	{Ext: ".scala", IsCode: true, AllowGlob: "*.scala"},
	// ── .NET / C# ────────────────────────────────────────────────────────────
	{Ext: ".cs", IsCode: true, AllowGlob: "",
		TechKW:    []string{"c# ", "csharp", ".net"},
		ImpliedKW: []string{"c# ", "csharp", ".net"}},
	// ── C / C++ ──────────────────────────────────────────────────────────────
	{Ext: ".cpp", IsCode: true, AllowGlob: "*.cpp"},
	{Ext: ".c", IsCode: true, AllowGlob: "*.c"},
	{Ext: ".h", IsCode: true, AllowGlob: "*.h"},
	// ── other scripted languages ──────────────────────────────────────────────
	{Ext: ".rb", IsCode: true, AllowGlob: "*.rb",
		TechKW:    []string{"ruby", ".rb "},
		ImpliedKW: []string{"ruby"}},
	{Ext: ".php", IsCode: true, AllowGlob: "*.php",
		TechKW:    []string{"php"},
		ImpliedKW: []string{"php"}},
	{Ext: ".swift", IsCode: true, AllowGlob: "*.swift",
		TechKW:    []string{"swift"},
		ImpliedKW: []string{"swift"}},
}
