package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/michalswi/local-agent-on-steroids/types"
	"gopkg.in/yaml.v3"
)

// Config represents the complete agent configuration
type Config struct {
	Agent    AgentConfig    `yaml:"agent" json:"agent"`
	LLM      LLMConfig      `yaml:"llm" json:"llm"`
	Filters  FilterConfig   `yaml:"filters" json:"filters"`
	Security SecurityConfig `yaml:"security" json:"security"`
	Chunking ChunkingConfig `yaml:"chunking" json:"chunking"`
}

// AgentConfig contains general agent settings
type AgentConfig struct {
	MaxFileSizeBytes int `yaml:"max_file_size_bytes" json:"max_file_size_bytes"`
	ConcurrentFiles  int `yaml:"concurrent_files" json:"concurrent_files"`
	// AutoApply controls whether chat-mode code blocks are written to disk
	// automatically without user confirmation. Defaults to false (off) so
	// every write requires an explicit "⚡ Apply" click.
	AutoApply bool `yaml:"auto_apply" json:"auto_apply"`
}

// LLMConfig contains LLM provider settings
type LLMConfig struct {
	Provider    string  `yaml:"provider" json:"provider"`
	Endpoint    string  `yaml:"endpoint" json:"endpoint"`
	Model       string  `yaml:"model" json:"model"`
	APIKey      string  `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Temperature float64 `yaml:"temperature" json:"temperature"`
	Timeout     int     `yaml:"timeout" json:"timeout"` // seconds
}

// FilterConfig contains file filtering rules
type FilterConfig struct {
	RespectGitignore bool     `yaml:"respect_gitignore" json:"respect_gitignore"`
	CustomIgnoreFile string   `yaml:"custom_ignore_file" json:"custom_ignore_file"`
	DenyPatterns     []string `yaml:"deny_patterns" json:"deny_patterns"`
	AllowPatterns    []string `yaml:"allow_patterns" json:"allow_patterns"`
}

// SecurityConfig contains security and privacy settings
type SecurityConfig struct {
	DetectSecrets  bool `yaml:"detect_secrets" json:"detect_secrets"`
	SkipBinaries   bool `yaml:"skip_binaries" json:"skip_binaries"`
	FollowSymlinks bool `yaml:"follow_symlinks" json:"follow_symlinks"`
	MaxDepth       int  `yaml:"max_depth" json:"max_depth"`
}

// ChunkingConfig contains file chunking settings
type ChunkingConfig struct {
	Strategy  string `yaml:"strategy" json:"strategy"`     // smart, lines, tokens
	ChunkSize int    `yaml:"chunk_size" json:"chunk_size"` // tokens or lines
	Overlap   int    `yaml:"overlap" json:"overlap"`       // overlap between chunks
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	// Read from environment variables with defaults
	concurrentFiles := 1
	if val := os.Getenv("AGENT_CONCURRENT_FILES"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			concurrentFiles = parsed
		}
	}

	return &Config{
		Agent: AgentConfig{
			MaxFileSizeBytes: 1048576, // 1MB
			ConcurrentFiles:  concurrentFiles,
		},
		LLM: LLMConfig{
			Provider: "ollama",
			Endpoint: "http://localhost:11434",
			Model:    "wizardlm2:7b", // https://ollama.com/library/wizardlm2
			// Model:       "gemma3:4b", // https://ollama.com/library/gemma3
			Temperature: 0.3, // 0.1 might be too conservative..
			Timeout:     300, // 5 minutes for large batches
		},
		Filters: FilterConfig{
			RespectGitignore: true,
			CustomIgnoreFile: ".agentignore",
			DenyPatterns: []string{
				"node_modules/**",
				".git/**",
				"*.tmp",
				"dist/**",
				"build/**",
				"vendor/**",
			},
			// Build AllowPatterns from the central language registry (code/config
			// extensions) plus non-language patterns that are always included.
			AllowPatterns: func() []string {
				var out []string
				for _, e := range types.LangRegistry {
					if e.AllowGlob != "" {
						out = append(out, e.AllowGlob)
					}
				}
				// Go module files
				out = append(out, "*.mod", "*.sum")
				// Infrastructure
				out = append(out, "*.tf", "*.tfvars")
				// Data / analysis targets
				out = append(out, "*.log", "*.pdf", "*.pcap")
				// Secrets / credentials (included for scanning; detection handled separately)
				out = append(out, "*.env.*", "*.key", "*.pem", "*.crt")
				// Container / build system
				out = append(out, "Dockerfile", "Makefile", "*.dockerfile", "*.conf", "*.service")
				// Common dotfiles (no extension — need explicit names)
				out = append(out,
					".gitignore", ".dockerignore", ".editorconfig",
					".eslintrc", ".prettierrc", ".babelrc", ".nvmrc", ".node-version",
				)
				return out
			}(),
		},
		Security: SecurityConfig{
			DetectSecrets:  false, // Disabled by default
			SkipBinaries:   true,
			FollowSymlinks: false,
			MaxDepth:       20, // Maximum directory depth to traverse (nested folders)
		},
		Chunking: ChunkingConfig{
			Strategy:  "smart",
			ChunkSize: 1000,
			Overlap:   100,
		},
	}
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

// LoadConfigWithFallback tries to load config from file, falls back to default
func LoadConfigWithFallback(path string) *Config {
	if path != "" {
		if cfg, err := LoadConfig(path); err == nil {
			return cfg
		}
	}

	// Try to load from standard locations
	standardPaths := []string{
		".agent/config.yaml",
		".agent/config.yml",
		"agent-config.yaml",
		"agent-config.yml",
	}

	for _, p := range standardPaths {
		if cfg, err := LoadConfig(p); err == nil {
			return cfg
		}
	}

	return DefaultConfig()
}

// Save saves the configuration to a file
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Agent.MaxFileSizeBytes <= 0 {
		return fmt.Errorf("max_file_size_bytes must be positive")
	}

	if c.Agent.ConcurrentFiles <= 0 {
		return fmt.Errorf("concurrent_files must be positive")
	}

	if c.LLM.Endpoint == "" {
		return fmt.Errorf("llm endpoint is required")
	}

	if c.LLM.Model == "" {
		return fmt.Errorf("llm model is required")
	}

	if c.Security.MaxDepth <= 0 {
		return fmt.Errorf("max_depth must be positive")
	}

	if c.Chunking.ChunkSize <= 0 {
		return fmt.Errorf("chunk_size must be positive")
	}

	return nil
}

// ToJSON converts config to JSON string
func (c *Config) ToJSON() (string, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
