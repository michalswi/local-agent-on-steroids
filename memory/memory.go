// Package memory manages per-project persistent memory for the local agent.
//
// Memory is stored as a plain-text Markdown file alongside session logs:
//
//	<homedir>/<project-slug>/memory.md
//
// where <project-slug> is derived from the absolute path of the scanned
// directory so that each project gets independent memory.
package memory

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// maxBytes is the hard cap for memory.md.  Content beyond this is silently
// trimmed from the top (oldest entries) when writing.
const maxBytes = 32 * 1024 // 32 KB

var (
	writeMu       sync.Mutex
	entryHeaderRE = regexp.MustCompile(`(?m)^## \d{4}-\d{2}-\d{2} \d{2}:\d{2}\n`)
)

// Path returns the absolute path to the memory file for the given project
// directory, stored under homeDir.
//
// The project slug is the last path component of absProjectDir followed by a
// 6-character hash of the full path, avoiding collisions for same-named dirs.
func Path(homeDir, absProjectDir string) string {
	slug := projectSlug(absProjectDir)
	return filepath.Join(homeDir, slug, "memory.md")
}

// Load reads the memory file for the project and returns its content.
// Returns an empty string (no error) if the file does not exist yet.
func Load(homeDir, absProjectDir string) (string, error) {
	p := Path(homeDir, absProjectDir)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Append adds content to the memory file, creating it (and its parent directory)
// if needed.  If the resulting file would exceed maxBytes, the oldest content is
// trimmed from the top while preserving the last maxBytes bytes.
func Append(homeDir, absProjectDir, content string) error {
	writeMu.Lock()
	defer writeMu.Unlock()

	p := Path(homeDir, absProjectDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	existing, err := Load(homeDir, absProjectDir)
	if err != nil {
		return err
	}

	combined := existing
	if combined != "" && !strings.HasSuffix(combined, "\n") {
		combined += "\n"
	}
	combined += strings.TrimSpace(content) + "\n"
	combined = trimToMaxBytes(combined)

	return os.WriteFile(p, []byte(combined), 0o644)
}

// Save replaces the entire memory file with the provided content (capped to
// maxBytes from the end).
func Save(homeDir, absProjectDir, content string) error {
	writeMu.Lock()
	defer writeMu.Unlock()

	p := Path(homeDir, absProjectDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	content = trimToMaxBytes(content)

	return os.WriteFile(p, []byte(content), 0o644)
}

// Clear deletes the memory file for the project.  It is not an error if the
// file does not exist.
func Clear(homeDir, absProjectDir string) error {
	writeMu.Lock()
	defer writeMu.Unlock()

	p := Path(homeDir, absProjectDir)
	err := os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// trimToMaxBytes keeps the newest content under maxBytes while trying to align
// to whole timestamped entries that start with: "## YYYY-MM-DD HH:MM".
func trimToMaxBytes(content string) string {
	if len(content) <= maxBytes {
		return content
	}

	matches := entryHeaderRE.FindAllStringIndex(content, -1)
	if len(matches) > 0 {
		for _, m := range matches {
			start := m[0]
			if len(content[start:]) <= maxBytes {
				return strings.TrimLeft(content[start:], "\n")
			}
		}
	}

	trimmed := content[len(content)-maxBytes:]
	if idx := strings.Index(trimmed, "\n"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	return trimmed
}

// projectSlug returns a filesystem-safe directory name derived from the
// absolute project path: last component + first 6 hex chars of SHA-256.
func projectSlug(absPath string) string {
	base := filepath.Base(absPath)
	// Sanitise: keep only alphanumeric, hyphens, underscores, dots.
	var sb strings.Builder
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	h := sha256.Sum256([]byte(absPath))
	return fmt.Sprintf("%s-%x", sb.String(), h[:3])
}
