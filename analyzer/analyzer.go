package analyzer

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/michalswi/local-agent-on-steroids/config"
	"github.com/michalswi/local-agent-on-steroids/llm"
	"github.com/michalswi/local-agent-on-steroids/security"
	"github.com/michalswi/local-agent-on-steroids/types"
)

// Analyzer orchestrates file analysis
type Analyzer struct {
	config    *config.Config
	detector  *Detector
	chunker   *Chunker
	validator *security.Validator
	tokenizer *llm.Tokenizer
}

// NewAnalyzer creates a new file analyzer
func NewAnalyzer(cfg *config.Config) *Analyzer {
	return &Analyzer{
		config:    cfg,
		detector:  NewDetector(),
		chunker:   NewChunker(&cfg.Chunking),
		validator: security.NewValidator(),
		tokenizer: llm.NewTokenizer(),
	}
}

// AnalyzeFile performs complete analysis on a single file
func (a *Analyzer) AnalyzeFile(path string, rootPath string) (*types.FileInfo, error) {
	// Detect file metadata
	info, err := a.detector.DetectFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to detect file: %w", err)
	}

	// Mark obviously sensitive paths early
	if a.config.Security.DetectSecrets && a.validator.DetectSensitiveFile(path) {
		info.IsSensitive = true
	}

	// Calculate relative path
	relPath, err := filepath.Rel(rootPath, path)
	if err != nil {
		relPath = path
	}
	info.RelPath = relPath

	// Skip if not readable
	if !info.IsReadable {
		return info, nil
	}

	// Skip if too large
	if info.Size > int64(a.config.Agent.MaxFileSizeBytes) {
		return info, nil
	}

	// Read content based on category
	switch info.Category {
	case types.CategorySmall:
		// Read full content
		var content string
		var err error

		if info.Type == types.TypePDF {
			content, err = a.detector.ReadPDFContent(path)
		} else if info.Type == types.TypePCAP {
			content, err = a.detector.ReadPCAPContent(path)
		} else {
			content, err = a.detector.ReadContent(path, 0)
		}

		if err != nil {
			return info, fmt.Errorf("failed to read content: %w", err)
		}
		info.Content = content
		info.TokenCount = a.tokenizer.EstimateTokensSimple(content)
		a.flagViolations(info, content)

	case types.CategoryMedium:
		// Read full content but prepare for chunking
		var content string
		var err error

		if info.Type == types.TypePDF {
			content, err = a.detector.ReadPDFContent(path)
		} else if info.Type == types.TypePCAP {
			content, err = a.detector.ReadPCAPContent(path)
		} else {
			content, err = a.detector.ReadContent(path, 0)
		}

		if err != nil {
			return info, fmt.Errorf("failed to read content: %w", err)
		}
		info.Content = content
		info.TokenCount = a.tokenizer.EstimateTokensSimple(content)

		info.Summary = a.generateSummary(info)
		a.flagViolations(info, content)

	case types.CategoryLarge:
		var content string
		var err error

		if info.Type == types.TypePDF {
			content, err = a.detector.ReadPDFContent(path)
		} else if info.Type == types.TypePCAP {
			content, err = a.detector.ReadPCAPContent(path)
		} else {
			content, err = a.detector.ReadContent(path, 0)
		}

		if err != nil {
			return info, fmt.Errorf("failed to read content: %w", err)
		}
		info.Content = content

		info.Summary = a.generateSummary(info)

		chunks, err := a.chunker.ChunkFile(path)
		if err != nil {
			return info, fmt.Errorf("failed to chunk file: %w", err)
		}
		info.Chunks = chunks

		// Calculate total tokens from content
		info.TokenCount = a.tokenizer.EstimateTokensSimple(content)
		a.flagViolations(info, content)
	}

	return info, nil
}

// AnalyzeFiles analyzes multiple files concurrently
func (a *Analyzer) AnalyzeFiles(paths []string, rootPath string) ([]*types.FileInfo, []error) {
	var wg sync.WaitGroup
	results := make([]*types.FileInfo, len(paths))
	errors := make([]error, len(paths))

	// Create semaphore for concurrent limit
	sem := make(chan struct{}, a.config.Agent.ConcurrentFiles)

	for i, path := range paths {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			info, err := a.AnalyzeFile(p, rootPath)
			results[idx] = info
			errors[idx] = err
		}(i, path)
	}

	wg.Wait()
	return results, errors
}

// generateSummary creates a summary for a file
func (a *Analyzer) generateSummary(info *types.FileInfo) string {
	var parts []string

	// Add file type information
	parts = append(parts, fmt.Sprintf("File: %s", info.RelPath))
	parts = append(parts, fmt.Sprintf("Type: %s", info.Type))
	parts = append(parts, fmt.Sprintf("Size: %s", formatFileSize(info.Size)))

	// Add language/extension
	if info.Extension != "" {
		parts = append(parts, fmt.Sprintf("Extension: %s", info.Extension))
	}

	// Try to detect language
	lang := detectLanguage(info.Extension)
	if lang != "" {
		parts = append(parts, fmt.Sprintf("Language: %s", lang))
	}

	// Add line count if text file
	if info.Type == types.TypeText && info.Content != "" {
		lineCount := strings.Count(info.Content, "\n") + 1
		parts = append(parts, fmt.Sprintf("Lines: %d", lineCount))
	}

	// Add chunk information for large files
	if len(info.Chunks) > 0 {
		parts = append(parts, fmt.Sprintf("Chunks: %d", len(info.Chunks)))
	}

	return strings.Join(parts, " | ")
}

// PrepareForLLM prepares file content for sending to LLM
// Enforces maxTokens limit by including files until limit is reached
func (a *Analyzer) PrepareForLLM(files []*types.FileInfo, maxTokens int) string {
	var builder strings.Builder

	// Redact sensitive content before sending to LLM
	sanitize := func(text string) string {
		if a.validator == nil {
			return text
		}
		return a.validator.SanitizeContent(text)
	}

	// Determine which files can fit within token limit
	var includedFiles []*types.FileInfo
	var skippedFiles []*types.FileInfo
	currentTokens := 0

	for _, file := range files {
		if file == nil || !file.IsReadable || file.IsSensitive {
			continue
		}

		// Skip files that exceed token limit entirely
		if file.TokenCount > maxTokens {
			skippedFiles = append(skippedFiles, file)
			continue
		}

		// Stop if adding this file would exceed limit (unless it's the first file)
		if currentTokens+file.TokenCount > maxTokens && len(includedFiles) > 0 {
			break
		}

		includedFiles = append(includedFiles, file)
		currentTokens += file.TokenCount
	}

	builder.WriteString("# Project Files Summary\n\n")
	builder.WriteString(fmt.Sprintf("Total files to analyze: %d\n\n", len(includedFiles)))
	if len(skippedFiles) > 0 {
		builder.WriteString(fmt.Sprintf("⚠️  Skipped %d files (exceed token limit)\n\n", len(skippedFiles)))
	}
	builder.WriteString("## File List:\n")
	for _, file := range includedFiles {
		builder.WriteString(fmt.Sprintf("- %s\n", file.RelPath))
	}
	builder.WriteString("\n---\n\n")
	builder.WriteString("## File Contents:\n\n")

	for _, file := range includedFiles {
		builder.WriteString(fmt.Sprintf("### File: %s\n", file.RelPath))

		// Skip sensitive files
		if file.IsSensitive {
			builder.WriteString("[SENSITIVE FILE - SKIPPED]\n\n")
			continue
		}

		// Add content based on category
		switch file.Category {
		case types.CategorySmall, types.CategoryMedium:
			if file.Content != "" {
				safeContent := sanitize(file.Content)
				builder.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", getLanguageIdentifier(file.Extension), safeContent))
			} else {
				builder.WriteString("[Empty file]\n\n")
			}

		case types.CategoryLarge:
			// For single file analysis, include full content
			if len(includedFiles) == 1 && file.Content != "" {
				safeContent := sanitize(file.Content)
				builder.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", getLanguageIdentifier(file.Extension), safeContent))
			} else {
				// For multi-file batches, show summary and first chunk
				builder.WriteString(fmt.Sprintf("[Large file - %s]\n", file.Summary))
				if len(file.Chunks) > 0 && file.Chunks[0].Content != "" {
					safeContent := sanitize(file.Chunks[0].Content)
					builder.WriteString(fmt.Sprintf("\n**Preview (Chunk 1/%d):**\n```%s\n%s\n```\n",
						len(file.Chunks), getLanguageIdentifier(file.Extension), safeContent))
				}
			}
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

func (a *Analyzer) flagViolations(info *types.FileInfo, content string) {
	if a.validator == nil || info == nil || !a.config.Security.DetectSecrets {
		return
	}

	// Detect secrets and PII
	violations := a.validator.ScanForSecrets(content, info.Path)
	violations = append(violations, a.validator.ScanForPII(content, info.Path)...)

	if len(violations) > 0 {
		info.IsSensitive = true
		info.Violations = append(info.Violations, violations...)
	}
}

func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func detectLanguage(ext string) string {
	languages := map[string]string{
		".go":     "Go",
		".py":     "Python",
		".js":     "JavaScript",
		".ts":     "TypeScript",
		".java":   "Java",
		".c":      "C",
		".cpp":    "C++",
		".h":      "C/C++ Header",
		".rs":     "Rust",
		".rb":     "Ruby",
		".php":    "PHP",
		".swift":  "Swift",
		".kt":     "Kotlin",
		".scala":  "Scala",
		".sh":     "Shell",
		".sql":    "SQL",
		".pcap":   "Network Capture",
		".pcapng": "Network Capture",
		".cap":    "Network Capture",
	}

	return languages[ext]
}

func getLanguageIdentifier(ext string) string {
	identifiers := map[string]string{
		".go":     "go",
		".py":     "python",
		".js":     "javascript",
		".ts":     "typescript",
		".java":   "java",
		".c":      "c",
		".cpp":    "cpp",
		".rs":     "rust",
		".rb":     "ruby",
		".php":    "php",
		".sh":     "bash",
		".sql":    "sql",
		".md":     "markdown",
		".json":   "json",
		".yaml":   "yaml",
		".yml":    "yaml",
		".xml":    "xml",
		".pcap":   "text",
		".pcapng": "text",
		".cap":    "text",
	}

	if id, ok := identifiers[ext]; ok {
		return id
	}
	return ""
}
