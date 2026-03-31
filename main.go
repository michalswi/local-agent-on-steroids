package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/michalswi/local-agent-on-steroids/analyzer"
	"github.com/michalswi/local-agent-on-steroids/config"
	"github.com/michalswi/local-agent-on-steroids/filter"
	"github.com/michalswi/local-agent-on-steroids/llm"
	"github.com/michalswi/local-agent-on-steroids/security"
	"github.com/michalswi/local-agent-on-steroids/sessionlog"
	"github.com/michalswi/local-agent-on-steroids/types"
	"github.com/michalswi/local-agent-on-steroids/webui"
)

func main() {
	// Define CLI flags
	homeDirDefault := sessionlog.DefaultDir()
	var (
		configPath      = flag.String("config", "", "Path to configuration file")
		directory       = flag.String("dir", ".", "Directory to analyze")
		model           = flag.String("model", "", "LLM model to use (overrides config)")
		host            = flag.String("host", "localhost:11434", "Ollama instance host (e.g., localhost:11434, 192.168.1.100:8080, or ollama.example.com:11434)")
		homedir         = flag.String("homedir", homeDirDefault, "Directory for session logs and saved data")
		dryRun          = flag.Bool("dry-run", false, "Scan and list files without starting the web UI")
		noDetectSecrets = flag.Bool("no-detect-secrets", false, "Disable secret/sensitive content detection")

		showVersion = flag.Bool("version", false, "Show version")
		checkHealth = flag.Bool("health", false, "Check LLM connectivity")
		listModels  = flag.Bool("list-models", false, "List available LLM models")
	)

	flag.Parse()

	// Apply homedir (expand ~ just in case the user passed a literal ~).
	if h := *homedir; h != "" {
		if strings.HasPrefix(h, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				h = filepath.Join(home, h[2:])
			}
		}
		sessionlog.SetDir(h)
	}

	// Show version
	if *showVersion {
		fmt.Printf("local-agent version %s\n", version)
		return
	}

	// Load configuration
	cfg := config.LoadConfigWithFallback(*configPath)
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Override model if specified via flag
	if *model != "" {
		cfg.LLM.Model = *model
	}

	// Override host if specified via flag.
	// If the value already contains a scheme (http:// or https://), use it as-is;
	// otherwise prepend http:// so bare host:port values still work.
	if *host != "localhost:11434" {
		h := *host
		if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
			cfg.LLM.Endpoint = h
		} else {
			cfg.LLM.Endpoint = fmt.Sprintf("http://%s", h)
		}
	}

	// Override secret detection if disabled via flag
	if *noDetectSecrets {
		cfg.Security.DetectSecrets = false
	}

	// Initialize LLM client
	llmClient := llm.NewOllamaClient(cfg.LLM.Endpoint, cfg.LLM.Model, cfg.LLM.Timeout)

	// Handle health check
	if *checkHealth {
		checkLLMHealth(llmClient)
		return
	}

	// Handle list models
	if *listModels {
		listAvailableModels(llmClient)
		return
	}

	// Validate directory
	absDir, err := filepath.Abs(*directory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid directory: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stat(absDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Directory does not exist: %s\n", absDir)
		os.Exit(1)
	}

	// If dry-run, just scan and display — no web UI.
	if *dryRun {
		result, err := scanDirectory(absDir, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to scan directory: %v\n", err)
			os.Exit(1)
		}
		displayScanResult(result)
		return
	}

	// Default: start interactive web UI.
	startInteractiveMode(absDir, cfg, llmClient)
}

func scanDirectory(rootPath string, cfg *config.Config) (*types.ScanResult, error) {
	startTime := time.Now()

	// Initialize components
	fileFilter, err := filter.NewFilter(cfg, rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize filter: %w", err)
	}

	analyzer := analyzer.NewAnalyzer(cfg)
	validator := security.NewValidator()

	result := &types.ScanResult{
		RootPath: rootPath,
		Files:    make([]types.FileInfo, 0),
		Errors:   make([]types.ScanError, 0),
		Summary:  make(map[string]int),
	}

	visitedDirs := make(map[string]struct{})
	var filePaths []string

	var walk func(string, int)
	walk = func(current string, depth int) {
		info, err := os.Lstat(current)
		if err != nil {
			result.Errors = append(result.Errors, types.ScanError{Path: current, Error: err.Error(), Time: time.Now()})
			return
		}

		// Follow symlinks when enabled
		if info.Mode()&os.ModeSymlink != 0 {
			if !fileFilter.ShouldFollowSymlink(current) {
				return
			}

			target, err := filepath.EvalSymlinks(current)
			if err != nil {
				result.Errors = append(result.Errors, types.ScanError{Path: current, Error: err.Error(), Time: time.Now()})
				return
			}

			targetAbs, _ := filepath.Abs(target)

			// Avoid cycles
			if _, seen := visitedDirs[targetAbs]; seen {
				return
			}

			info, err = os.Stat(targetAbs)
			if err != nil {
				result.Errors = append(result.Errors, types.ScanError{Path: targetAbs, Error: err.Error(), Time: time.Now()})
				return
			}

			current = targetAbs
		}

		// Validate path traversal
		if err := validator.ValidatePath(current); err != nil {
			return
		}

		if info.IsDir() {
			// directory depth to traverse (nested folders)
			if !fileFilter.IsWithinDepthLimit(depth) {
				return
			}

			absDir, _ := filepath.Abs(current)
			visitedDirs[absDir] = struct{}{}

			entries, err := os.ReadDir(current)
			if err != nil {
				result.Errors = append(result.Errors, types.ScanError{Path: current, Error: err.Error(), Time: time.Now()})
				return
			}

			for _, entry := range entries {
				childPath := filepath.Join(current, entry.Name())
				walk(childPath, depth+1)
			}
			return
		}

		// Apply filters to files
		if !fileFilter.ShouldInclude(current, info) {
			result.FilteredFiles++
			return
		}

		filePaths = append(filePaths, current)
		result.TotalFiles++
		result.TotalSize += info.Size()
	}

	walk(rootPath, 0)

	// Analyze files
	fileInfos, errors := analyzer.AnalyzeFiles(filePaths, rootPath)

	for i, fileInfo := range fileInfos {
		if errors[i] != nil {
			result.Errors = append(result.Errors, types.ScanError{
				Path:  filePaths[i],
				Error: errors[i].Error(),
				Time:  time.Now(),
			})
			continue
		}

		if fileInfo != nil {
			result.Files = append(result.Files, *fileInfo)

			// Update summary
			result.Summary[string(fileInfo.Type)]++
			result.Summary[string(fileInfo.Category)]++
		}
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

func normalizeRelPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func displayScanResult(result *types.ScanResult) {
	fmt.Printf("📊 Scan Results\n")
	fmt.Printf("   Total files found: %d\n", result.TotalFiles)
	fmt.Printf("   Filtered files: %d\n", result.FilteredFiles)
	fmt.Printf("   Total size: %s\n", formatBytes(result.TotalSize))
	fmt.Printf("   Duration: %v\n", result.Duration)

	if len(result.Summary) > 0 {
		fmt.Printf("\n   File breakdown:\n")
		for key, count := range result.Summary {
			fmt.Printf("      %s: %d\n", key, count)
		}
	}

	if len(result.Errors) > 0 {
		fmt.Printf("\n   ⚠️  Errors: %d\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Printf("      %s: %s\n", e.Path, e.Error)
		}
	}
}

func checkLLMHealth(client *llm.OllamaClient) {
	fmt.Printf("🏥 Checking LLM health...\n")

	if client.IsAvailable() {
		fmt.Printf("✅ LLM is available at %s\n", client.GetModel())

		// Try to list models
		models, err := client.ListModels()
		if err == nil && len(models) > 0 {
			fmt.Printf("📋 Available models: %d\n", len(models))
			for _, model := range models {
				fmt.Printf("   - %s\n", model)
			}
		}
	} else {
		fmt.Printf("❌ LLM is not available\n")
		fmt.Printf("   Make sure Ollama is running: ollama serve\n")
		os.Exit(1)
	}
}

func listAvailableModels(client *llm.OllamaClient) {
	models, err := client.ListModels()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list models: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("📋 Available Models:\n")
	for _, model := range models {
		fmt.Printf("   - %s\n", model)
	}
}

func startInteractiveMode(directory string, cfg *config.Config, llmClient *llm.OllamaClient) {
	// Pre-check: verify Ollama is reachable before doing any work.
	fmt.Printf("🔍 Checking Ollama at %s...\n", cfg.LLM.Endpoint)
	if !llmClient.IsAvailable() {
		fmt.Fprintf(os.Stderr, "❌ Cannot reach Ollama at %s\n", cfg.LLM.Endpoint)
		fmt.Fprintf(os.Stderr, "   Make sure Ollama is running: ollama serve\n")
		fmt.Fprintf(os.Stderr, "   Use -host to specify a different address (e.g., -host 192.168.1.100:11434)\n")
		os.Exit(1)
	}
	fmt.Printf("✅ Ollama is reachable\n\n")

	// Perform initial scan
	scanResult, err := scanDirectory(directory, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to scan directory: %v\n", err)
		os.Exit(1)
	}

	const port = 5050
	url := fmt.Sprintf("http://localhost:%d", port)

	fmt.Printf("\n🤖 Local Agent v%s — Interactive Mode\n", version)
	fmt.Printf("📁 Directory : %s\n", directory)
	fmt.Printf("🤖 Model     : %s @ %s\n", cfg.LLM.Model, cfg.LLM.Endpoint)
	fmt.Printf("📊 Files     : %d scanned\n", scanResult.TotalFiles)
	fmt.Printf("🌐 Web UI    : %s\n\n", url)
	fmt.Printf("Press Ctrl+C to stop.\n\n")

	// Open the browser in the background (best-effort)
	openBrowser(url)

	// Start web server — this blocks until Ctrl+C or error
	webServer := webui.NewServer(directory, cfg.LLM.Model, cfg.LLM.Endpoint, scanResult, cfg, llmClient, "")
	if err := webServer.Start(port); err != nil {
		fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
		os.Exit(1)
	}
}

// openBrowser launches the default system browser for the given URL.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "linux":
		cmd, args = "xdg-open", []string{url}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", url}
	default:
		return
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		// Non-fatal: the URL was already printed to the terminal
		fmt.Fprintf(os.Stderr, "Could not open browser automatically: %v\n", err)
	}
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
