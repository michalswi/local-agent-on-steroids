package filter

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ignorePattern represents a single gitignore-style pattern
type ignorePattern struct {
	pattern  string
	negate   bool
	dirOnly  bool
	absolute bool
}

// IgnoreParser parses and matches .gitignore-style patterns
type IgnoreParser struct {
	patterns []ignorePattern
}

// NewIgnoreParser creates a new IgnoreParser
func NewIgnoreParser() *IgnoreParser {
	return &IgnoreParser{
		patterns: []ignorePattern{},
	}
}

// LoadFile loads patterns from a file
func (p *IgnoreParser) LoadFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Not an error if file doesn't exist
		}
		return fmt.Errorf("failed to open ignore file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		pattern := p.parsePattern(line)
		p.patterns = append(p.patterns, pattern)
	}

	return scanner.Err()
}

// AddPattern adds a single pattern
func (p *IgnoreParser) AddPattern(pattern string) {
	p.patterns = append(p.patterns, p.parsePattern(pattern))
}

// AddPatterns adds multiple patterns
func (p *IgnoreParser) AddPatterns(patterns []string) {
	for _, pattern := range patterns {
		p.AddPattern(pattern)
	}
}

// parsePattern parses a gitignore pattern string
func (p *IgnoreParser) parsePattern(pattern string) ignorePattern {
	ip := ignorePattern{
		pattern: pattern,
	}

	// Check for negation
	if strings.HasPrefix(pattern, "!") {
		ip.negate = true
		pattern = pattern[1:]
	}

	// Check for directory-only pattern
	if strings.HasSuffix(pattern, "/") {
		ip.dirOnly = true
		pattern = strings.TrimSuffix(pattern, "/")
	}

	// Check for absolute pattern
	if strings.HasPrefix(pattern, "/") {
		ip.absolute = true
		pattern = strings.TrimPrefix(pattern, "/")
	}

	ip.pattern = pattern
	return ip
}

// Match checks if a path matches any of the patterns
func (p *IgnoreParser) Match(path string, isDir bool) bool {
	// Normalize path
	path = filepath.Clean(path)
	path = filepath.ToSlash(path)

	matched := false

	for _, pattern := range p.patterns {
		// Skip directory-only patterns for files
		if pattern.dirOnly && !isDir {
			continue
		}

		if p.matchPattern(path, pattern) {
			matched = !pattern.negate
		}
	}

	return matched
}

// matchPattern checks if a path matches a specific pattern
func (p *IgnoreParser) matchPattern(path string, pattern ignorePattern) bool {
	patternStr := pattern.pattern

	// Handle wildcard patterns
	if strings.Contains(patternStr, "**") {
		parts := strings.Split(patternStr, "**")
		if len(parts) == 2 {
			prefix := parts[0]
			suffix := parts[1]
			// Remove leading/trailing slashes
			prefix = strings.Trim(prefix, "/")
			suffix = strings.Trim(suffix, "/")

			if prefix != "" && !strings.HasPrefix(path, prefix) {
				return false
			}
			if suffix != "" {
				match, _ := filepath.Match(suffix, filepath.Base(path))
				return match
			}
			return true
		}
	}

	// Simple glob matching
	if pattern.absolute {
		// Match from root
		match, _ := filepath.Match(patternStr, path)
		return match
	}

	// Match basename or full path
	baseName := filepath.Base(path)
	baseMatch, _ := filepath.Match(patternStr, baseName)
	fullMatch, _ := filepath.Match(patternStr, path)

	return baseMatch || fullMatch
}

// GetPatterns returns all loaded patterns as strings
func (p *IgnoreParser) GetPatterns() []string {
	patterns := make([]string, len(p.patterns))
	for i, p := range p.patterns {
		patterns[i] = p.pattern
	}
	return patterns
}

// LoadGitignore loads .gitignore file from a directory
func LoadGitignore(dir string) (*IgnoreParser, error) {
	parser := NewIgnoreParser()
	gitignorePath := filepath.Join(dir, ".gitignore")

	if err := parser.LoadFile(gitignorePath); err != nil {
		return nil, err
	}

	return parser, nil
}

// LoadCustomIgnoreFile loads a custom ignore file
func LoadCustomIgnoreFile(dir, filename string) (*IgnoreParser, error) {
	parser := NewIgnoreParser()
	ignorePath := filepath.Join(dir, filename)

	if err := parser.LoadFile(ignorePath); err != nil {
		return nil, err
	}

	return parser, nil
}
