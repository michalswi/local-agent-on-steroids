package analyzer

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/michalswi/local-agent-on-steroids/config"
	"github.com/michalswi/local-agent-on-steroids/llm"
	"github.com/michalswi/local-agent-on-steroids/types"
)

// Chunker handles chunking of files into smaller pieces
type Chunker struct {
	config    *config.ChunkingConfig
	detector  *Detector
	tokenizer *llm.Tokenizer
}

// NewChunker creates a new Chunker with the specified configuration
func NewChunker(cfg *config.ChunkingConfig) *Chunker {
	return &Chunker{
		config:    cfg,
		detector:  NewDetector(),
		tokenizer: llm.NewTokenizer(),
	}
}

// ChunkFile chunks a file according to the configured strategy
func (c *Chunker) ChunkFile(path string) ([]types.FileChunk, error) {
	switch strings.ToLower(c.config.Strategy) {
	case "lines":
		return c.chunkByLines(path)
	case "tokens":
		return c.chunkByTokens(path)
	case "smart":
		return c.chunkSmart(path)
	default:
		return c.chunkByLines(path)
	}
}

// chunkByLines splits a file into chunks by line count
func (c *Chunker) chunkByLines(path string) ([]types.FileChunk, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // allow large lines
	var chunks []types.FileChunk
	var currentLines []string
	lineNum := 1
	chunkStartLine := 1
	offset := int64(0)

	for scanner.Scan() {
		line := scanner.Text()
		currentLines = append(currentLines, line)

		// Check if we've reached chunk size
		if len(currentLines) >= c.config.ChunkSize {
			content := strings.Join(currentLines, "\n")
			chunk := types.FileChunk{
				Index:       len(chunks),
				StartLine:   chunkStartLine,
				EndLine:     lineNum,
				StartOffset: offset,
				EndOffset:   offset + int64(len(content)),
				Content:     content,
				TokenCount:  c.estimateTokens(content),
			}
			chunks = append(chunks, chunk)

			// Prepare next chunk with overlap
			overlapSize := c.config.Overlap
			if overlapSize > len(currentLines) {
				overlapSize = len(currentLines)
			}
			currentLines = currentLines[len(currentLines)-overlapSize:]
			offset = chunk.EndOffset
			chunkStartLine = lineNum - overlapSize + 1
		}

		lineNum++
	}

	// Add remaining lines as final chunk
	if len(currentLines) > 0 {
		content := strings.Join(currentLines, "\n")
		chunk := types.FileChunk{
			Index:       len(chunks),
			StartLine:   chunkStartLine,
			EndLine:     lineNum - 1,
			StartOffset: offset,
			EndOffset:   offset + int64(len(content)),
			Content:     content,
			TokenCount:  c.estimateTokens(content),
		}
		chunks = append(chunks, chunk)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan file: %w", err)
	}

	return chunks, nil
}

// chunkByTokens splits a file into chunks by estimated token count
func (c *Chunker) chunkByTokens(path string) ([]types.FileChunk, error) {
	content, err := c.detector.ReadContent(path, 0)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(content, "\n")
	var chunks []types.FileChunk
	var currentChunk []string
	currentTokens := 0
	chunkStartLine := 1
	lineNum := 1
	offset := int64(0)

	for _, line := range lines {
		lineTokens := c.estimateTokens(line)

		// If adding this line exceeds chunk size, create a new chunk
		if currentTokens+lineTokens > c.config.ChunkSize && len(currentChunk) > 0 {
			chunkContent := strings.Join(currentChunk, "\n")
			chunk := types.FileChunk{
				Index:       len(chunks),
				StartLine:   chunkStartLine,
				EndLine:     lineNum - 1,
				StartOffset: offset,
				EndOffset:   offset + int64(len(chunkContent)),
				Content:     chunkContent,
				TokenCount:  currentTokens,
			}
			chunks = append(chunks, chunk)

			// Calculate overlap
			overlapLines := c.calculateOverlapLines(currentChunk)
			currentChunk = currentChunk[len(currentChunk)-overlapLines:]
			currentTokens = c.estimateTokens(strings.Join(currentChunk, "\n"))
			chunkStartLine = lineNum - overlapLines
			offset = chunk.EndOffset
		}

		currentChunk = append(currentChunk, line)
		currentTokens += lineTokens
		lineNum++
	}

	// Add remaining content as final chunk
	if len(currentChunk) > 0 {
		chunkContent := strings.Join(currentChunk, "\n")
		chunk := types.FileChunk{
			Index:       len(chunks),
			StartLine:   chunkStartLine,
			EndLine:     lineNum - 1,
			StartOffset: offset,
			EndOffset:   offset + int64(len(chunkContent)),
			Content:     chunkContent,
			TokenCount:  currentTokens,
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// chunkSmart uses context-aware chunking (functions, classes, etc.)
func (c *Chunker) chunkSmart(path string) ([]types.FileChunk, error) {
	// For now, use token-based chunking with smarter boundaries
	// In a full implementation, this would parse the code structure
	// and chunk at logical boundaries (function/class boundaries)

	content, err := c.detector.ReadContent(path, 0)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(content, "\n")
	var chunks []types.FileChunk
	var currentChunk []string
	currentTokens := 0
	chunkStartLine := 1
	lineNum := 1
	offset := int64(0)

	for i, line := range lines {
		lineTokens := c.estimateTokens(line)

		// Check if we should create a chunk
		shouldChunk := currentTokens+lineTokens > c.config.ChunkSize && len(currentChunk) > 0

		if shouldChunk {
			// Try to chunk at logical boundaries
			// Look ahead for a good break point (empty line, function definition, etc.)
			if c.isLogicalBoundary(line) || (i < len(lines)-1 && c.isLogicalBoundary(lines[i+1])) {
				chunkContent := strings.Join(currentChunk, "\n")
				chunk := types.FileChunk{
					Index:       len(chunks),
					StartLine:   chunkStartLine,
					EndLine:     lineNum - 1,
					StartOffset: offset,
					EndOffset:   offset + int64(len(chunkContent)),
					Content:     chunkContent,
					TokenCount:  currentTokens,
				}
				chunks = append(chunks, chunk)

				currentChunk = []string{}
				currentTokens = 0
				chunkStartLine = lineNum
				offset = chunk.EndOffset
			}
		}

		currentChunk = append(currentChunk, line)
		currentTokens += lineTokens
		lineNum++
	}

	// Add remaining content
	if len(currentChunk) > 0 {
		chunkContent := strings.Join(currentChunk, "\n")
		chunk := types.FileChunk{
			Index:       len(chunks),
			StartLine:   chunkStartLine,
			EndLine:     lineNum - 1,
			StartOffset: offset,
			EndOffset:   offset + int64(len(chunkContent)),
			Content:     chunkContent,
			TokenCount:  currentTokens,
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// isLogicalBoundary checks if a line is a good place to chunk
func (c *Chunker) isLogicalBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)

	// Empty line
	if trimmed == "" {
		return true
	}

	// Function/method definitions (Go, Python, JavaScript, etc.)
	prefixes := []string{
		"func ", "def ", "function ", "class ",
		"public ", "private ", "protected ",
		"async ", "export ",
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	// Comments that look like section headers
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") {
		return true
	}

	return false
}

// calculateOverlapLines determines how many lines to overlap
func (c *Chunker) calculateOverlapLines(lines []string) int {
	overlapTokens := c.config.Overlap
	overlapLines := 0
	currentTokens := 0

	// Count from the end
	for i := len(lines) - 1; i >= 0 && currentTokens < overlapTokens; i-- {
		lineTokens := c.estimateTokens(lines[i])
		currentTokens += lineTokens
		overlapLines++
	}

	return overlapLines
}

// estimateTokens estimates the number of tokens in text
// This is a rough approximation: ~1 token per 4 characters
func (c *Chunker) estimateTokens(text string) int {
	if c.tokenizer != nil {
		return c.tokenizer.EstimateTokensSimple(text)
	}

	// Fallback heuristic
	return len(text) / 4
}

// GetChunk retrieves a specific chunk by index
func (c *Chunker) GetChunk(chunks []types.FileChunk, index int) (*types.FileChunk, error) {
	if index < 0 || index >= len(chunks) {
		return nil, fmt.Errorf("chunk index %d out of range (0-%d)", index, len(chunks)-1)
	}
	return &chunks[index], nil
}
