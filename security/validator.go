package security

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/michalswi/local-agent-on-steroids/types"
)

// Validator handles security validation and sanitization
type Validator struct {
	secretPatterns []*regexp.Regexp
}

// NewValidator creates a new security validator
func NewValidator() *Validator {
	v := &Validator{
		secretPatterns: make([]*regexp.Regexp, 0),
	}

	// Initialize common secret patterns
	v.initializeSecretPatterns()

	return v
}

// initializeSecretPatterns sets up regex patterns for detecting secrets
func (v *Validator) initializeSecretPatterns() {
	patterns := []string{
		// API Keys
		`(?i)api[_-]?key[_-]?[=:]\s*['""]?([a-zA-Z0-9_\-]{32,})`,
		`(?i)apikey[_-]?[=:]\s*['""]?([a-zA-Z0-9_\-]{32,})`,

		// AWS
		`AKIA[0-9A-Z]{16}`,
		`(?i)aws[_-]?secret[_-]?access[_-]?key[_-]?[=:]\s*['""]?([a-zA-Z0-9/+=]{40})`,

		// Private keys
		`-----BEGIN\s+(RSA|DSA|EC|OPENSSH)\s+PRIVATE\s+KEY-----`,

		// Tokens
		`(?i)token[_-]?[=:]\s*['""]?([a-zA-Z0-9_\-\.]{32,})`,
		`(?i)bearer\s+([a-zA-Z0-9_\-\.=]+)`,

		// Passwords
		`(?i)password[_-]?[=:]\s*['""]?([^\s'"]+)`,
		`(?i)passwd[_-]?[=:]\s*['""]?([^\s'"]+)`,

		// Database connection strings
		`(?i)(mongodb|mysql|postgresql|postgres)://[^:]+:[^@]+@`,

		// Generic secrets
		`(?i)secret[_-]?[=:]\s*['""]?([a-zA-Z0-9_\-]{16,})`,

		// GitHub tokens
		`ghp_[a-zA-Z0-9]{36}`,
		`gho_[a-zA-Z0-9]{36}`,
		`ghu_[a-zA-Z0-9]{36}`,
		`ghs_[a-zA-Z0-9]{36}`,
		`ghr_[a-zA-Z0-9]{36}`,

		// Slack tokens
		`xox[baprs]-[0-9a-zA-Z\-]+`,

		// JWT
		`eyJ[a-zA-Z0-9_-]+\.eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`,
	}

	for _, pattern := range patterns {
		if re, err := regexp.Compile(pattern); err == nil {
			v.secretPatterns = append(v.secretPatterns, re)
		}
	}
}

// ValidatePath validates a file path for security issues
func (v *Validator) ValidatePath(path string) error {
	// Check for directory traversal
	cleanPath := filepath.Clean(path)
	if strings.Contains(cleanPath, string(filepath.Separator)+".."+string(filepath.Separator)) || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path traversal detected: %s", path)
	}

	// Check for absolute path attempts that go outside allowed directories
	// This would need to be configured based on allowed roots

	return nil
}

// ScanForSecrets scans content for potential secrets
func (v *Validator) ScanForSecrets(content string, filePath string) []types.SecurityViolation {
	violations := make([]types.SecurityViolation, 0)

	lines := strings.Split(content, "\n")

	for lineNum, line := range lines {
		for _, pattern := range v.secretPatterns {
			if pattern.MatchString(line) {
				violation := types.SecurityViolation{
					File:        filePath,
					Line:        lineNum + 1,
					Type:        "secret",
					Pattern:     pattern.String(),
					Description: "Potential secret or credential detected",
					Confidence:  0.8,
				}
				violations = append(violations, violation)
			}
		}
	}

	return violations
}

// SanitizeContent sanitizes content before sending to LLM
func (v *Validator) SanitizeContent(content string) string {
	// Remove or mask potential secrets
	sanitized := content

	for _, pattern := range v.secretPatterns {
		sanitized = pattern.ReplaceAllString(sanitized, "[REDACTED]")
	}

	return sanitized
}

// IsPathSafe checks if a path is safe to access
func (v *Validator) IsPathSafe(path string, allowedRoots []string) bool {
	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return false
	}

	// Check if path is within allowed roots
	for _, root := range allowedRoots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}

		// Check if path is under this root
		rel, err := filepath.Rel(absRoot, absPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}

	return false
}

// DetectSensitiveFile checks if a file appears to be sensitive
func (v *Validator) DetectSensitiveFile(path string) bool {
	lowerPath := strings.ToLower(path)
	baseName := filepath.Base(lowerPath)

	sensitiveNames := []string{
		".env",
		".secret",
		"secret",
		"password",
		"credentials",
		"credential",
		"token",
		"apikey",
		"api_key",
		"private",
		"id_rsa",
		"id_dsa",
		"id_ecdsa",
		"id_ed25519",
	}

	for _, name := range sensitiveNames {
		if strings.Contains(baseName, name) || strings.Contains(lowerPath, name) {
			return true
		}
	}

	// Check extensions
	sensitiveExts := []string{
		".key",
		".pem",
		".p12",
		".pfx",
		".cer",
		".crt",
		".der",
		".jks",
		".keystore",
	}

	ext := filepath.Ext(lowerPath)
	for _, sensExt := range sensitiveExts {
		if ext == sensExt {
			return true
		}
	}

	return false
}

// ValidateFileSize checks if file size is within limits
func (v *Validator) ValidateFileSize(size int64, maxSize int64) error {
	if size > maxSize {
		return fmt.Errorf("file size %d exceeds maximum %d", size, maxSize)
	}
	return nil
}

// ScanForPII scans content for personally identifiable information
func (v *Validator) ScanForPII(content string, filePath string) []types.SecurityViolation {
	violations := make([]types.SecurityViolation, 0)

	// Email addresses
	emailPattern := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// Social Security Numbers (US format)
	ssnPattern := regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)

	// Credit card numbers (simple pattern)
	ccPattern := regexp.MustCompile(`\b\d{4}[\s\-]?\d{4}[\s\-]?\d{4}[\s\-]?\d{4}\b`)

	// Phone numbers (US format)
	phonePattern := regexp.MustCompile(`\b\d{3}[-.]?\d{3}[-.]?\d{4}\b`)

	lines := strings.Split(content, "\n")

	for lineNum, line := range lines {
		if emailPattern.MatchString(line) {
			violations = append(violations, types.SecurityViolation{
				File:        filePath,
				Line:        lineNum + 1,
				Type:        "pii",
				Pattern:     "email",
				Description: "Email address detected",
				Confidence:  0.9,
			})
		}

		if ssnPattern.MatchString(line) {
			violations = append(violations, types.SecurityViolation{
				File:        filePath,
				Line:        lineNum + 1,
				Type:        "pii",
				Pattern:     "ssn",
				Description: "Potential Social Security Number detected",
				Confidence:  0.7,
			})
		}

		if ccPattern.MatchString(line) {
			violations = append(violations, types.SecurityViolation{
				File:        filePath,
				Line:        lineNum + 1,
				Type:        "pii",
				Pattern:     "credit_card",
				Description: "Potential credit card number detected",
				Confidence:  0.6,
			})
		}

		if phonePattern.MatchString(line) {
			violations = append(violations, types.SecurityViolation{
				File:        filePath,
				Line:        lineNum + 1,
				Type:        "pii",
				Pattern:     "phone",
				Description: "Phone number detected",
				Confidence:  0.8,
			})
		}
	}

	return violations
}
