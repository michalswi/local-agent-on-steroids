package filter

import (
	"path/filepath"
	"strings"

	"github.com/michalswi/local-agent-on-steroids/config"
)

// Filter manages file filtering based on patterns and rules
type Filter struct {
	config          *config.Config
	rootDir         string
	gitignoreParser *IgnoreParser
	denyParser      *IgnoreParser
	allowParser     *IgnoreParser
}

// NewFilter creates a new Filter with the specified configuration
func NewFilter(cfg *config.Config, rootDir string) (*Filter, error) {
	f := &Filter{
		config:      cfg,
		rootDir:     rootDir,
		denyParser:  NewIgnoreParser(),
		allowParser: NewIgnoreParser(),
	}

	// Load .gitignore if configured
	if cfg.Filters.RespectGitignore {
		gitignoreParser, err := LoadGitignore(rootDir)
		if err == nil {
			f.gitignoreParser = gitignoreParser
		}
	}

	// Load custom ignore file if specified
	if cfg.Filters.CustomIgnoreFile != "" {
		customParser, err := LoadCustomIgnoreFile(rootDir, cfg.Filters.CustomIgnoreFile)
		if err == nil {
			// Merge custom patterns into deny parser
			for _, pattern := range customParser.GetPatterns() {
				f.denyParser.AddPattern(pattern)
			}
		}
	}

	// Load deny patterns
	f.denyParser.AddPatterns(cfg.Filters.DenyPatterns)

	// Load allow patterns
	f.allowParser.AddPatterns(cfg.Filters.AllowPatterns)

	return f, nil
}

// ShouldInclude determines if a file should be included based on filters
func (f *Filter) ShouldInclude(path string, info interface{}) bool {
	// Prefer matching on workspace-relative paths for predictable glob behavior
	relPath, err := filepath.Rel(f.rootDir, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	// Keep critical workspace control files visible in the UI even if
	// .gitignore/allow rules would otherwise hide them.
	if f.isAlwaysVisibleFile(relPath) {
		return !f.isSensitiveFile(relPath)
	}

	// Determine if it's a directory (assumed false for files)
	isDir := false

	// 1. Check gitignore first
	if f.gitignoreParser != nil && f.gitignoreParser.Match(relPath, isDir) {
		return false
	}

	// 2. Check deny patterns
	if f.denyParser.Match(relPath, isDir) {
		return false
	}

	// 3. Check allow patterns (if specified)
	if len(f.config.Filters.AllowPatterns) > 0 {
		// If allow patterns are specified, file must match at least one
		if !f.allowParser.Match(relPath, isDir) {
			// Check if file extension is in allow patterns
			ext := filepath.Ext(relPath)
			if ext == "" {
				// Dotfiles (e.g. .gitignore, .dockerignore, .editorconfig) have
				// no extension in Go's filepath.Ext but are valid config files.
				// Let them pass through to the sensitive-file check rather than
				// hard-blocking them.
				baseName := filepath.Base(relPath)
				if !strings.HasPrefix(baseName, ".") {
					// Truly extensionless non-dotfile (e.g. a binary) — exclude.
					return false
				}
				// Dotfile: fall through to the sensitive-file check below.
				return !f.isSensitiveFile(relPath)
			}

			extPattern := "*" + ext
			// Check if extension matches any allow pattern
			matched := false
			for _, pattern := range f.config.Filters.AllowPatterns {
				if pattern == extPattern {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}

	// 4. Check for sensitive patterns
	if f.isSensitiveFile(relPath) {
		return false
	}

	return true
}

func (f *Filter) isAlwaysVisibleFile(path string) bool {
	baseName := strings.ToLower(filepath.Base(path))
	switch baseName {
	case ".gitignore", "docker-compose.yml", "docker-compose.yaml":
		return true
	default:
		return false
	}
}

// isSensitiveFile checks if a file appears to contain sensitive data
func (f *Filter) isSensitiveFile(path string) bool {
	if !f.config.Security.DetectSecrets {
		return false
	}

	lowerPath := strings.ToLower(path)
	baseName := filepath.Base(lowerPath)

	// Common sensitive file patterns
	sensitivePatterns := []string{
		".env",
		".secret",
		"secret",
		"password",
		"credentials",
		".pem",
		".key",
		".p12",
		".pfx",
		"id_rsa",
		"id_dsa",
		"aws_credentials",
		".aws/credentials",
		".ssh/",
	}

	for _, pattern := range sensitivePatterns {
		if strings.Contains(lowerPath, pattern) || strings.Contains(baseName, pattern) {
			return true
		}
	}

	return false
}

// ShouldFollowSymlink determines if a symlink should be followed
func (f *Filter) ShouldFollowSymlink(path string) bool {
	return f.config.Security.FollowSymlinks
}

// IsWithinDepthLimit checks if the current depth (nested folders) is within limits
func (f *Filter) IsWithinDepthLimit(depth int) bool {
	return depth <= f.config.Security.MaxDepth
}

// GetDenyPatterns returns all deny patterns
func (f *Filter) GetDenyPatterns() []string {
	return f.denyParser.GetPatterns()
}

// GetAllowPatterns returns all allow patterns
func (f *Filter) GetAllowPatterns() []string {
	return f.allowParser.GetPatterns()
}
