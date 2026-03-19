package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/michalswi/local-agent-on-steroids/analyzer"
	"github.com/michalswi/local-agent-on-steroids/config"
	"github.com/michalswi/local-agent-on-steroids/filter"
	"github.com/michalswi/local-agent-on-steroids/llm"
	"github.com/michalswi/local-agent-on-steroids/sessionlog"
	"github.com/michalswi/local-agent-on-steroids/types"
)

// mustPrompt reads an embedded prompt .md file by name and panics if missing.
func mustPrompt(name string) string {
	data, err := PromptFiles.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("embedded prompt %q not found: %v", name, err))
	}
	return strings.TrimSpace(string(data))
}

var (
	promptChat        = mustPrompt("prompts/chat.md")
	promptAgentEdit   = mustPrompt("prompts/agent_edit.md")
	promptAgentCreate = mustPrompt("prompts/agent_create.md")
)

// AgentLogEntry records a single file operation performed by the agent.
// The log is injected into subsequent agent prompts so the LLM knows what
// has already been done in this session.
type AgentLogEntry struct {
	Operation string // "created" | "modified"
	File      string
	Task      string
}

// Server represents the web UI server
type Server struct {
	directory    string
	model        string
	endpoint     string
	indexTmpl    *template.Template
	scanResult   *types.ScanResult
	focusedPath  string
	cfg          *config.Config
	llmClient    *llm.OllamaClient
	messages     []Message
	agentLog     []AgentLogEntry     // structured changelog injected into agent prompts
	taskCancel   context.CancelFunc  // cancels the current in-flight LLM request (Stop button)
	taskToken    *struct{}           // unique identity token to avoid cross-task cancel races
	taskRunning  bool                // true while chat/agent work is executing
	taskKind     string              // "chat" | "agent"
	scanFilter   *filter.Filter      // filter snapshot from startup; reused by rescan so agent-created files (e.g. .gitignore) don't hide themselves
	writtenFiles map[string]struct{} // rel paths written via /api/file/write; always shown on rescan
	mu           sync.RWMutex
}

// Message represents a chat message
type Message struct {
	Role         string            `json:"role"`
	Content      string            `json:"content"`
	Timestamp    time.Time         `json:"timestamp"`
	DurationMs   int64             `json:"duration_ms,omitempty"`
	AgentResults []AgentFileResult `json:"agentResults,omitempty"`
}

// ChatRequest represents an incoming chat message
type ChatRequest struct {
	Message string `json:"message"`
}

// ChatResponse represents a chat response
type ChatResponse struct {
	Success  bool      `json:"success"`
	Message  *Message  `json:"message,omitempty"`
	Error    string    `json:"error,omitempty"`
	Messages []Message `json:"messages,omitempty"`
	Cleared  bool      `json:"cleared,omitempty"`
}

// StatusResponse represents the current status
type StatusResponse struct {
	Directory   string `json:"directory"`
	Model       string `json:"model"`
	TotalFiles  int    `json:"totalFiles"`
	FocusedPath string `json:"focusedPath,omitempty"`
	Processing  bool   `json:"processing"`
	TaskKind    string `json:"taskKind,omitempty"`
}

// NewServer creates a new web UI server
func NewServer(directory, model, endpoint string, scanResult *types.ScanResult, cfg *config.Config, llmClient *llm.OllamaClient, focusedPath string) *Server {
	s := &Server{
		directory:    directory,
		model:        model,
		endpoint:     endpoint,
		indexTmpl:    template.Must(template.New("index").Parse(htmlTemplate)),
		scanResult:   scanResult,
		focusedPath:  focusedPath,
		cfg:          cfg,
		llmClient:    llmClient,
		messages:     make([]Message, 0),
		writtenFiles: make(map[string]struct{}),
	}

	// Capture the filter state at startup. Re-using this on every rescan
	// ensures that a .gitignore written by the agent during the session does
	// not retroactively hide other files it just created.
	if f, err := filter.NewFilter(cfg, directory); err == nil {
		s.scanFilter = f
	}

	s.messages = append(s.messages, Message{
		Role: "assistant",
		Content: fmt.Sprintf("🤖 Interactive mode started!\n\nScanned: %s\nFiles found: %d\nModel: %s\n\nConcurrent Files: %d\nTemperature: %.2f\n\nType your questions or commands.",
			directory, scanResult.TotalFiles, model, cfg.Agent.ConcurrentFiles, cfg.LLM.Temperature),
		Timestamp: time.Now(),
	})

	return s
}

func (s *Server) Start(port int) error {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(StaticFiles, "webstatic")
	if err != nil {
		log.Printf("Warning: failed to access embedded static files: %v", err)
	} else {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/messages", s.handleMessages)
	mux.HandleFunc("/api/rescan", s.handleRescan)
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/file", s.handleFileContent)
	mux.HandleFunc("/api/file/write", s.handleFileWrite)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/agent/run", s.handleAgentRun)
	mux.HandleFunc("/api/agent/stream", s.handleAgentStream)
	mux.HandleFunc("/api/agent/commit", s.handleAgentCommit)
	mux.HandleFunc("/api/ext/send", s.handleExtSend)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("🌐 Web UI available at http://localhost%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if err := s.indexTmpl.Execute(w, nil); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := StatusResponse{
		Directory:   s.directory,
		Model:       s.model,
		TotalFiles:  s.scanResult.TotalFiles,
		FocusedPath: s.focusedPath,
		Processing:  s.taskRunning,
		TaskKind:    s.taskKind,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.messages)
}

// handleStop cancels the currently running LLM request, if any.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	if s.taskCancel != nil {
		s.taskCancel()
		s.taskCancel = nil
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request")
		return
	}

	userInput := strings.TrimSpace(req.Message)
	if userInput == "" {
		sendError(w, "Empty message")
		return
	}

	// Add user message
	s.mu.Lock()
	s.messages = append(s.messages, Message{
		Role:      "user",
		Content:   userInput,
		Timestamp: time.Now(),
	})
	s.mu.Unlock()

	// Handle special commands
	if response := s.handleCommand(userInput); response != "" {
		if response == "__CLEAR__" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ChatResponse{Success: true, Cleared: true})
			return
		}
		s.mu.Lock()
		msg := Message{
			Role:      "assistant",
			Content:   response,
			Timestamp: time.Now(),
		}
		s.messages = append(s.messages, msg)
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			Success: true,
			Message: &msg,
		})
		return
	}

	// Get active files, scoped to any filenames mentioned in the question.
	// If there are no files at all, still send the request — the user may be
	// asking the LLM to generate something from scratch.
	activeFiles := s.scopeFilesToQuestion(userInput, s.getActiveFilesSnapshot())

	// Use a background context so a page refresh does NOT cancel the Ollama
	// request. Only an explicit Stop press (via /api/stop) cancels it.
	taskCtx, cancel := context.WithCancel(context.Background())
	taskToken := new(struct{})
	s.mu.Lock()
	if s.taskCancel != nil {
		s.taskCancel() // cancel any previous stale task
	}
	s.taskCancel = cancel
	s.taskToken = taskToken
	s.taskRunning = true
	s.taskKind = "chat"
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.taskToken == taskToken {
			s.taskCancel = nil
			s.taskToken = nil
			s.taskRunning = false
			s.taskKind = ""
		}
		s.mu.Unlock()
		cancel()
	}()

	resp, answer, duration, err := s.processQuestion(taskCtx, userInput, activeFiles)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return // stopped — keep the user message, no reply to store
		}
		sendError(w, err.Error())
		return
	}

	msg := Message{
		Role:       "assistant",
		Content:    answer,
		Timestamp:  time.Now(),
		DurationMs: duration.Milliseconds(),
	}

	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	s.saveSession(userInput, answer, resp, duration)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ChatResponse{
		Success: true,
		Message: &msg,
	})
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	scanResult, err := s.performRescan()
	if err != nil {
		sendError(w, fmt.Sprintf("Rescan failed: %v", err))
		return
	}

	s.mu.Lock()
	s.scanResult = scanResult
	s.mu.Unlock()

	msg := Message{
		Role:      "assistant",
		Content:   fmt.Sprintf("✅ Rescan complete!\n\nFiles found: %d\nFiltered: %d", scanResult.TotalFiles, scanResult.FilteredFiles),
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ChatResponse{
		Success: true,
		Message: &msg,
	})
}

func (s *Server) handleCommand(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))

	switch {
	case lower == "help":
		return `📚 Available commands:
• help - Show this help message
• clear - Clear conversation history
• model <name> - Switch to a different LLM model
• rescan - Rescan the directory for changes
• stats - Show current statistics
• files - List all files in scope`

	case lower == "stats":
		s.mu.RLock()
		defer s.mu.RUnlock()
		activeFiles := s.getActiveFilesLocked()
		return fmt.Sprintf(`📊 Statistics:
• Directory: %s
• Total files scanned: %d
• Active files: %d
• Model: %s`, s.directory, s.scanResult.TotalFiles, len(activeFiles), s.model)

	case lower == "files":
		s.mu.RLock()
		defer s.mu.RUnlock()
		activeFiles := s.getActiveFilesLocked()
		var builder strings.Builder
		builder.WriteString(fmt.Sprintf("📁 Files (%d total):\n", len(activeFiles)))
		for _, file := range activeFiles {
			builder.WriteString(fmt.Sprintf("• %s\n", file.RelPath))
		}
		return builder.String()

	case lower == "clear":
		s.mu.Lock()
		s.messages = nil
		s.agentLog = nil
		s.mu.Unlock()
		return "__CLEAR__"

	case strings.HasPrefix(lower, "model "):
		newModel := strings.TrimSpace(strings.TrimPrefix(lower, "model "))
		if newModel == "" {
			return fmt.Sprintf("⚠️  Please specify a model name.\nCurrent model: %s", s.model)
		}
		s.mu.Lock()
		oldModel := s.model
		s.model = newModel
		s.cfg.LLM.Model = newModel
		s.llmClient = llm.NewOllamaClient(s.cfg.LLM.Endpoint, newModel, s.cfg.LLM.Timeout)
		s.mu.Unlock()
		return fmt.Sprintf("✅ Model switched: %s → %s\n\nYou can now continue asking questions.", oldModel, newModel)

	case lower == "rescan":
		scanResult, err := s.performRescan()
		if err != nil {
			return fmt.Sprintf("❌ Rescan failed: %v", err)
		}
		s.mu.Lock()
		s.scanResult = scanResult
		s.mu.Unlock()
		return fmt.Sprintf("✅ Rescan complete!\n\nFiles found: %d\nFiltered: %d\nTotal size: %s",
			scanResult.TotalFiles, scanResult.FilteredFiles, formatBytes(scanResult.TotalSize))

	}

	return "" // Not a command
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

// getActiveFilesLocked returns active files while assuming s.mu is already held
// by the caller (read or write lock). Returned file pointers point to copies,
// so they remain safe after the lock is released.
func (s *Server) getActiveFilesLocked() []*types.FileInfo {
	if s.scanResult == nil {
		return nil
	}

	if s.focusedPath == "" {
		files := make([]*types.FileInfo, 0, len(s.scanResult.Files))
		for i := range s.scanResult.Files {
			copied := s.scanResult.Files[i]
			files = append(files, &copied)
		}
		return files
	}

	for i := range s.scanResult.Files {
		if s.scanResult.Files[i].RelPath == s.focusedPath {
			copied := s.scanResult.Files[i]
			return []*types.FileInfo{&copied}
		}
	}

	return nil
}

// getActiveFilesSnapshot safely snapshots active files under a read lock.
// Use this from call sites that do not already hold s.mu.
func (s *Server) getActiveFilesSnapshot() []*types.FileInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getActiveFilesLocked()
}

// scopeFilesToQuestion narrows the file list to files relevant to the prompt.
// If explicit filenames are mentioned, those win. Otherwise use conservative
// heuristics to avoid fanning out edits across unrelated files.
func (s *Server) scopeFilesToQuestion(question string, all []*types.FileInfo) []*types.FileInfo {
	lower := strings.ToLower(question)
	if len(all) <= 1 {
		return all
	}

	// Explicit broad intent means "touch many files".
	if strings.Contains(lower, "all files") ||
		strings.Contains(lower, "entire project") ||
		strings.Contains(lower, "whole project") ||
		strings.Contains(lower, "across project") ||
		strings.Contains(lower, "across codebase") {
		return all
	}

	var matched []*types.FileInfo
	for _, f := range all {
		name := strings.ToLower(filepath.Base(f.RelPath))
		rel := strings.ToLower(f.RelPath)
		if strings.Contains(lower, name) || strings.Contains(lower, rel) {
			matched = append(matched, f)
		}
	}
	if len(matched) > 0 {
		return matched
	}

	// No explicit file mentions: pick a conservative, likely-relevant subset.
	// This prevents generic follow-up prompts (e.g. "add auth") from modifying
	// README/config/generated artifacts unintentionally.

	// 1) Task-specific file families.
	if strings.Contains(lower, "readme") || strings.Contains(lower, "documentation") || strings.Contains(lower, "docs") {
		return pickByExtensions(all, map[string]bool{".md": true}, 3)
	}
	if strings.Contains(lower, "terraform") || strings.Contains(lower, "gke") || strings.Contains(lower, "tf") {
		picked := pickByExtensions(all, map[string]bool{".tf": true, ".tfvars": true}, 6)
		if len(picked) > 0 {
			return picked
		}
	}

	// 2) Technology mismatch check: if the task explicitly requests a language
	// or framework that is NOT present in the workspace, return nil so the
	// caller routes to runAgentCreate rather than overwriting unrelated files.
	techMap := map[string][]string{
		".ts":    {"typescript", " ts ", ".ts "},
		".tsx":   {"react", ".tsx"},
		".vue":   {"vue"},
		".js":    {"javascript", " js ", ".js ", "nodejs", "node.js"},
		".py":    {"python", ".py "},
		".rs":    {"rust", ".rs "},
		".rb":    {"ruby", ".rb "},
		".java":  {"java "},
		".kt":    {"kotlin"},
		".cs":    {"c# ", "csharp", ".net"},
		".swift": {"swift"},
		".php":   {"php"},
	}
	// Collect extensions present in the workspace.
	workspaceExts := map[string]bool{}
	for _, f := range all {
		if f == nil {
			continue
		}
		workspaceExts[strings.ToLower(filepath.Ext(f.RelPath))] = true
	}
	for ext, keywords := range techMap {
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				// Task mentions this technology; if the workspace has none of
				// these files, signal that new files must be created.
				if !workspaceExts[ext] {
					return nil
				}
			}
		}
	}

	// 3) Prefer dominant source extension among code files (e.g. .py in a
	// generated Python app), capped to a small set.
	codeExts := map[string]bool{
		".py": true, ".go": true, ".js": true, ".ts": true, ".tsx": true,
		".java": true, ".rs": true, ".rb": true, ".php": true, ".cs": true,
		".cpp": true, ".c": true, ".h": true, ".kt": true, ".scala": true,
		".swift": true,
	}
	extFreq := map[string]int{}
	for _, f := range all {
		if f == nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.RelPath))
		if codeExts[ext] {
			extFreq[ext]++
		}
	}
	bestExt := ""
	bestCount := 0
	for ext, n := range extFreq {
		if n > bestCount {
			bestExt = ext
			bestCount = n
		}
	}
	if bestExt != "" {
		picked := pickByExtensions(all, map[string]bool{bestExt: true}, 3)
		if len(picked) > 0 {
			return picked
		}
	}

	// 3) Last fallback: include only likely code files (still capped).
	picked := pickByExtensions(all, codeExts, 3)
	if len(picked) > 0 {
		return picked
	}

	// If everything else fails, keep previous behavior.
	return all
}

func pickByExtensions(all []*types.FileInfo, allowed map[string]bool, limit int) []*types.FileInfo {
	if limit <= 0 {
		limit = 1
	}
	mainLike := make([]*types.FileInfo, 0)
	others := make([]*types.FileInfo, 0)
	for _, f := range all {
		if f == nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.RelPath))
		if !allowed[ext] {
			continue
		}
		base := strings.ToLower(filepath.Base(f.RelPath))
		if strings.HasPrefix(base, "main.") || strings.HasPrefix(base, "app.") || strings.HasPrefix(base, "server.") || strings.HasPrefix(base, "index.") {
			mainLike = append(mainLike, f)
		} else {
			others = append(others, f)
		}
	}

	out := make([]*types.FileInfo, 0, limit)
	for _, f := range mainLike {
		if len(out) >= limit {
			return out
		}
		out = append(out, f)
	}
	for _, f := range others {
		if len(out) >= limit {
			break
		}
		out = append(out, f)
	}
	return out
}

func (s *Server) processQuestion(ctx context.Context, question string, files []*types.FileInfo) (*llm.ChatResponse, string, time.Duration, error) {
	// Build file context
	var prompt strings.Builder
	prompt.WriteString(fmt.Sprintf("Task/Question: %s\n\n", question))
	prompt.WriteString("Files:\n\n")

	for _, file := range files {
		if file != nil && file.IsReadable && len(file.Content) > 0 {
			prompt.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", file.RelPath, file.Content))
		}
	}

	// Build the list of file paths so the LLM knows the exact names to use.
	var filePaths []string
	for _, f := range files {
		if f != nil {
			filePaths = append(filePaths, f.RelPath)
		}
	}

	systemPrompt := promptChat + "\nAvailable files: " + strings.Join(filePaths, ", ")

	chatReq := &llm.ChatRequest{
		Model: s.cfg.LLM.Model,
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt.String()},
		},
		Temperature: s.cfg.LLM.Temperature,
	}

	start := time.Now()
	resp, err := s.llmClient.Chat(ctx, chatReq)
	if err != nil {
		return nil, "", 0, fmt.Errorf("LLM request failed: %w", err)
	}

	return resp, resp.Message.Content, time.Since(start), nil
}

func (s *Server) saveSession(question, answer string, resp *llm.ChatResponse, duration time.Duration) {
	if resp == nil {
		return
	}

	// Snapshot mutable server state under lock to avoid racing with rescans
	// and runtime model/focus changes while building the record.
	s.mu.RLock()
	focus := s.focusedPath
	model := s.model
	var scanSummary *sessionlog.ScanSummary
	if s.scanResult != nil {
		scanSummary = &sessionlog.ScanSummary{
			TotalFiles:    s.scanResult.TotalFiles,
			FilteredFiles: s.scanResult.FilteredFiles,
			TotalSize:     s.scanResult.TotalSize,
			Duration:      s.scanResult.Duration,
		}
	}
	s.mu.RUnlock()

	record := &sessionlog.Record{
		Timestamp:   time.Now(),
		Mode:        "webui",
		Directory:   s.directory,
		Task:        question,
		Focus:       focus,
		Model:       model,
		TokensUsed:  resp.PromptEvalCount + resp.EvalCount,
		Duration:    duration,
		Files:       sessionlog.FilesFromTokens(nil, focus),
		Response:    answer,
		ScanSummary: scanSummary,
	}

	if _, err := sessionlog.Save(record); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save web session JSON: %v\n", err)
	}
}

func (s *Server) performRescan() (*types.ScanResult, error) {
	startTime := time.Now()

	// Reuse the filter that was built at startup so that files created by
	// the agent during this session (e.g. .gitignore) do not accidentally
	// hide other agent-created files on subsequent rescans.
	s.mu.RLock()
	f := s.scanFilter
	writtenFiles := make(map[string]struct{}, len(s.writtenFiles))
	for relPath := range s.writtenFiles {
		writtenFiles[relPath] = struct{}{}
	}
	s.mu.RUnlock()
	if f == nil {
		// Fallback: build a fresh filter if the startup one is unavailable.
		var err error
		f, err = filter.NewFilter(s.cfg, s.directory)
		if err != nil {
			return nil, err
		}
	}

	analyzerEngine := analyzer.NewAnalyzer(s.cfg)

	result := &types.ScanResult{
		RootPath: s.directory,
		Files:    make([]types.FileInfo, 0),
		Errors:   make([]types.ScanError, 0),
		Summary:  make(map[string]int),
	}

	// Simple file walker
	err := filepath.Walk(s.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if info.IsDir() {
			return nil
		}

		relPath, relErr := filepath.Rel(s.directory, path)
		if relErr != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(filepath.Clean(relPath))

		// Check if file should be included
		if !f.ShouldInclude(path, info) {
			if _, keep := writtenFiles[relPath]; !keep {
				result.FilteredFiles++
				return nil
			}
		}

		// Analyze file
		fileInfo, err := analyzerEngine.AnalyzeFile(path, s.directory)
		if err != nil {
			return nil // Skip errors
		}

		result.Files = append(result.Files, *fileInfo)
		result.TotalFiles++
		result.TotalSize += fileInfo.Size

		// Update summary
		ext := filepath.Ext(path)
		if ext == "" {
			ext = "(no ext)"
		}
		result.Summary[ext]++

		return nil
	})

	if err != nil {
		return nil, err
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

func (s *Server) markWrittenFile(relPath string) {
	cleanRel := filepath.ToSlash(filepath.Clean(relPath))
	s.mu.Lock()
	s.writtenFiles[cleanRel] = struct{}{}
	s.mu.Unlock()
}

func sendError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(ChatResponse{
		Success: false,
		Error:   message,
	})
}

// ── Agent ────────────────────────────────────────────────────────────────────

// AgentRunRequest is the JSON body for POST /api/agent/run.
type AgentRunRequest struct {
	Task string `json:"task"`
}

// ExtSendRequest is the JSON body for POST /api/ext/send.
// It allows external apps to send messages or agent tasks directly without
// going through the web UI chat box.
//
// Fields:
//
//	Message – the prompt / task text (required)
//	Mode    – "chat" (default) or "agent"
//	Auto    – for agent mode: write files immediately (default true)
type ExtSendRequest struct {
	Message string `json:"message"`
	Mode    string `json:"mode"` // "chat" | "agent"
	Auto    *bool  `json:"auto"` // nil → true for agent, ignored for chat
}

// AgentFileResult describes what the agent did with one file.
type AgentFileResult struct {
	File    string `json:"file"`
	Changed bool   `json:"changed"`
	Created bool   `json:"created"`
	// Deleted is true when the agent determined this file should be removed.
	Deleted bool `json:"deleted,omitempty"`
	// Pending is true when the result is a proposed change that has NOT yet
	// been written to disk — the user must confirm via /api/agent/commit.
	Pending    bool   `json:"pending,omitempty"`
	OldContent string `json:"oldContent,omitempty"`
	NewContent string `json:"newContent,omitempty"`
	Error      string `json:"error,omitempty"`
}

// AgentCommitRequest is the JSON body for POST /api/agent/commit.
type AgentCommitRequest struct {
	Files []AgentCommitFile `json:"files"`
}

// AgentCommitFile is one approved file entry inside an AgentCommitRequest.
type AgentCommitFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Delete, when true, removes the file instead of writing Content.
	Delete bool `json:"delete,omitempty"`
}

// AgentRunResponse is the JSON response for POST /api/agent/run.
type AgentRunResponse struct {
	Success      bool              `json:"success"`
	Message      *Message          `json:"message,omitempty"`
	Error        string            `json:"error,omitempty"`
	AgentResults []AgentFileResult `json:"agentResults,omitempty"`
	Cleared      bool              `json:"cleared,omitempty"`
}

// handleAgentRun processes each relevant file independently: for every file it
// sends task + file content to the LLM and asks for the updated content or
// NO_CHANGE. Changes are written to disk by the backend; the frontend only
// renders diffs from the structured AgentFileResult list.
func (s *Server) handleAgentRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(AgentRunResponse{Success: false, Error: "Invalid request body"})
		return
	}

	task := strings.TrimSpace(req.Task)
	if task == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(AgentRunResponse{Success: false, Error: "task is required"})
		return
	}

	s.mu.Lock()
	s.messages = append(s.messages, Message{Role: "user", Content: task, Timestamp: time.Now()})
	s.mu.Unlock()

	// Commands (clear, help, stats, …) should work regardless of which button
	// the user pressed. Handle them before touching the LLM.
	if response := s.handleCommand(task); response != "" {
		if response == "__CLEAR__" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(AgentRunResponse{Success: true, Cleared: true})
			return
		}
		s.mu.Lock()
		msg := Message{Role: "assistant", Content: response, Timestamp: time.Now()}
		s.messages = append(s.messages, msg)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(AgentRunResponse{Success: true, Message: &msg})
		return
	}

	// When auto-apply is off (the default) the agent only proposes changes
	// and the user must confirm each file via POST /api/agent/commit.
	// When auto-apply is on, changes are written to disk immediately.
	s.mu.RLock()
	dryRun := !s.cfg.Agent.AutoApply
	s.mu.RUnlock()

	// Use a background context so browser refreshes do not cancel the agent task.
	taskCtx, cancel := context.WithCancel(context.Background())
	taskToken := new(struct{})
	s.mu.Lock()
	if s.taskCancel != nil {
		s.taskCancel()
	}
	s.taskCancel = cancel
	s.taskToken = taskToken
	s.taskRunning = true
	s.taskKind = "agent"
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.taskToken == taskToken {
			s.taskCancel = nil
			s.taskToken = nil
			s.taskRunning = false
			s.taskKind = ""
		}
		s.mu.Unlock()
		cancel()
	}()

	agentStart := time.Now()
	summary, results, err := s.runAgentTask(taskCtx, task, func(string) {}, dryRun)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(AgentRunResponse{Success: false, Error: err.Error()})
		return
	}

	if !dryRun {
		for _, res := range results {
			if res.Changed {
				if scanned, scanErr := s.performRescan(); scanErr == nil {
					s.mu.Lock()
					s.scanResult = scanned
					s.mu.Unlock()
				}
				break
			}
		}
	}

	msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(agentStart).Milliseconds(), AgentResults: results}
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AgentRunResponse{
		Success:      true,
		Message:      &msg,
		AgentResults: results,
	})
}

// handleAgentStream is the streaming variant of handleAgentRun.
// It writes Server-Sent Events so the browser can update the typing bubble
// in real time as each file is processed.
//
// Event types:
//
//	{"type":"status","text":"..."}   — progress update
//	{"type":"done","success":true,"summary":"...","agentResults":[...]}
//	{"type":"done","success":true,"cleared":true}
//	{"type":"done","success":false,"error":"..."}
func (s *Server) handleAgentStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	var req AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": "invalid request body"})
		return
	}

	task := strings.TrimSpace(req.Task)
	if task == "" {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": "task is required"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	s.mu.Lock()
	s.messages = append(s.messages, Message{Role: "user", Content: task, Timestamp: time.Now()})
	s.mu.Unlock()

	// Handle built-in commands (clear, help, …) before touching the LLM.
	if response := s.handleCommand(task); response != "" {
		if response == "__CLEAR__" {
			sseWrite(w, flusher, map[string]any{"type": "done", "success": true, "cleared": true})
			return
		}
		s.mu.Lock()
		msg := Message{Role: "assistant", Content: response, Timestamp: time.Now()}
		s.messages = append(s.messages, msg)
		s.mu.Unlock()
		sseWrite(w, flusher, map[string]any{
			"type":    "done",
			"success": true,
			"message": msg,
		})
		return
	}

	// Progress callback — sends a status SSE event for each step.
	// Protected by a mutex because runAgentTask calls this from multiple
	// goroutines concurrently when ConcurrentFiles > 1, and http.ResponseWriter
	// is not safe for concurrent use.
	var sseMu sync.Mutex
	progress := func(text string) {
		sseMu.Lock()
		defer sseMu.Unlock()
		sseWrite(w, flusher, map[string]any{"type": "status", "text": text})
	}

	// When auto-apply is off (the default) the agent only proposes changes
	// and the user must confirm each file via POST /api/agent/commit.
	// When auto-apply is on, changes are written to disk immediately.
	s.mu.RLock()
	dryRun := !s.cfg.Agent.AutoApply
	s.mu.RUnlock()

	// Use a background context so a page refresh does NOT cancel the Ollama
	// requests. Only an explicit Stop press (via /api/stop) cancels them.
	taskCtx, cancel := context.WithCancel(context.Background())
	taskToken := new(struct{})
	s.mu.Lock()
	if s.taskCancel != nil {
		s.taskCancel()
	}
	s.taskCancel = cancel
	s.taskToken = taskToken
	s.taskRunning = true
	s.taskKind = "agent"
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.taskToken == taskToken {
			s.taskCancel = nil
			s.taskToken = nil
			s.taskRunning = false
			s.taskKind = ""
		}
		s.mu.Unlock()
		cancel()
	}()

	streamAgentStart := time.Now()
	summary, results, err := s.runAgentTask(taskCtx, task, progress, dryRun)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return // stopped — keep the user message, no reply to store
		}
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": err.Error()})
		return
	}

	if !dryRun {
		for _, res := range results {
			if res.Changed {
				if scanned, scanErr := s.performRescan(); scanErr == nil {
					s.mu.Lock()
					s.scanResult = scanned
					s.mu.Unlock()
				}
				break
			}
		}
	}

	msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(streamAgentStart).Milliseconds(), AgentResults: results}
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	sseWrite(w, flusher, map[string]any{
		"type":         "done",
		"success":      true,
		"message":      msg,
		"agentResults": results,
	})
}

// sseWrite marshals data as JSON and writes a single SSE data event, then
// flushes the response buffer so the browser receives it immediately.
func sseWrite(w http.ResponseWriter, f http.Flusher, data map[string]any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}

// handleAgentCommit writes the user-approved file proposals to disk.
// The frontend sends the subset of pending AgentFileResults that the user
// clicked "Apply" on; this endpoint performs the actual writes.
func (s *Server) handleAgentCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AgentCommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body")
		return
	}
	if len(req.Files) == 0 {
		sendError(w, "no files provided")
		return
	}

	type commitResult struct {
		File    string `json:"file"`
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}

	cleanDir := filepath.Clean(s.directory)
	var results []commitResult
	anyWritten := false

	for _, f := range req.Files {
		if f.Path == "" {
			results = append(results, commitResult{File: f.Path, Success: false, Error: "empty path"})
			continue
		}
		absPath := filepath.Clean(filepath.Join(s.directory, f.Path))
		rel, err := filepath.Rel(cleanDir, absPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			results = append(results, commitResult{File: f.Path, Success: false, Error: "path outside working directory"})
			continue
		}

		// Delete operation: remove the file instead of writing.
		if f.Delete {
			if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
				results = append(results, commitResult{File: f.Path, Success: false, Error: err.Error()})
				continue
			}
			anyWritten = true
			results = append(results, commitResult{File: f.Path, Success: true})
			s.mu.Lock()
			s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "deleted", File: f.Path, Task: "(confirmed via Apply)"})
			s.mu.Unlock()
			continue
		}

		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			results = append(results, commitResult{File: f.Path, Success: false, Error: err.Error()})
			continue
		}

		// Determine operation from on-disk state BEFORE writing.
		_, statErr := os.Stat(absPath)
		existedBefore := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			results = append(results, commitResult{File: f.Path, Success: false, Error: statErr.Error()})
			continue
		}

		if err := os.WriteFile(absPath, []byte(f.Content), 0o644); err != nil {
			results = append(results, commitResult{File: f.Path, Success: false, Error: err.Error()})
			continue
		}
		s.markWrittenFile(f.Path)
		anyWritten = true
		results = append(results, commitResult{File: f.Path, Success: true})
		// Record in agent changelog so future agent runs know about this file.
		// The commit endpoint doesn't have access to the original task string,
		// so we mark it generically; the file and operation are what matter.
		s.mu.Lock()
		op := "created"
		if existedBefore {
			op = "modified"
		}
		s.agentLog = append(s.agentLog, AgentLogEntry{Operation: op, File: f.Path, Task: "(confirmed via Apply)"})
		s.mu.Unlock()
	}

	// Rescan so the sidebar and future agent runs see the new/changed files.
	if anyWritten {
		if scanned, err := s.performRescan(); err == nil {
			s.mu.Lock()
			s.scanResult = scanned
			s.mu.Unlock()
		}
	}

	// Clear pending flags in stored messages for successfully committed files.
	written := map[string]bool{}
	for _, r := range results {
		if r.Success {
			written[r.File] = true
		}
	}
	if len(written) > 0 {
		s.mu.Lock()
		for mi := range s.messages {
			for ri := range s.messages[mi].AgentResults {
				if written[s.messages[mi].AgentResults[ri].File] {
					s.messages[mi].AgentResults[ri].Pending = false
				}
			}
		}
		s.mu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"results": results,
	})
}

// handleExtSend is the external API endpoint POST /api/ext/send.
// It lets any application send a prompt directly to the local model without
// using the browser chat UI.  The message (and the model's reply) are stored
// in s.messages so they appear in the chat panel on the next poll / refresh.
//
// Request body (JSON):
//
//	{
//	  "message": "...",   // required – the user prompt or agent task
//	  "mode":    "chat",  // optional – "chat" (default) | "agent"
//	  "auto":    true     // optional – agent only; write files immediately (default true)
//	}
//
// Response body (JSON):
//
//	{
//	  "success":      true,
//	  "message":      { role, content, timestamp },
//	  "agentResults": [...]    // only present for agent mode
//	  "error":        "..."    // only present on failure
//	}
func (s *Server) handleExtSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExtSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "invalid request body"})
		return
	}

	text := strings.TrimSpace(req.Message)
	if text == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "message is required"})
		return
	}

	// Normalise mode; default to "chat".
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "chat"
	}
	if mode != "chat" && mode != "agent" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "mode must be \"chat\" or \"agent\""})
		return
	}

	// Store the user message so it shows up in the chat UI immediately.
	s.mu.Lock()
	s.messages = append(s.messages, Message{Role: "user", Content: text, Timestamp: time.Now()})
	s.mu.Unlock()

	// Handle built-in commands (clear, stats, …) regardless of mode.
	if response := s.handleCommand(text); response != "" {
		if response == "__CLEAR__" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "cleared": true})
			return
		}
		msg := Message{Role: "assistant", Content: response, Timestamp: time.Now()}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": msg})
		return
	}

	// Acquire a task slot (cancels any previous stale task).
	taskCtx, cancel := context.WithCancel(context.Background())
	taskToken := new(struct{})
	s.mu.Lock()
	if s.taskCancel != nil {
		s.taskCancel()
	}
	s.taskCancel = cancel
	s.taskToken = taskToken
	s.taskRunning = true
	s.taskKind = mode
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.taskToken == taskToken {
			s.taskCancel = nil
			s.taskToken = nil
			s.taskRunning = false
			s.taskKind = ""
		}
		s.mu.Unlock()
		cancel()
	}()

	w.Header().Set("Content-Type", "application/json")

	switch mode {
	case "agent":
		// Default auto=true for external API calls (caller wants changes applied).
		autoApply := true
		if req.Auto != nil {
			autoApply = *req.Auto
		}
		dryRun := !autoApply

		extAgentStart := time.Now()
		summary, results, err := s.runAgentTask(taskCtx, text, func(string) {}, dryRun)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "stopped"})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		if !dryRun {
			for _, res := range results {
				if res.Changed {
					if scanned, scanErr := s.performRescan(); scanErr == nil {
						s.mu.Lock()
						s.scanResult = scanned
						s.mu.Unlock()
					}
					break
				}
			}
		}

		msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(extAgentStart).Milliseconds(), AgentResults: results}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"message":      msg,
			"agentResults": results,
		})

	default: // "chat"
		activeFiles := s.scopeFilesToQuestion(text, s.getActiveFilesSnapshot())
		_, answer, chatDuration, err := s.processQuestion(taskCtx, text, activeFiles)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "stopped"})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		msg := Message{Role: "assistant", Content: answer, Timestamp: time.Now(), DurationMs: chatDuration.Milliseconds()}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()

		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": msg})
	}
}

// agentFilenameRe matches a token that looks like a real filename: word chars,
// hyphens, dots, at least one dot with an extension of 2+ chars, no spaces.
// Requires a 2-char minimum extension to avoid matching abbreviations like "e.g".
var agentFilenameRe = regexp.MustCompile(`[\w\-][\w\-\.]*\.\w{2,}`)

// agentAbbreviationRe catches common prose abbreviations that superficially
// look like filenames but are not (e.g → e.g, i.e → i.e, vs → v.s…).
var knownNonFilenames = map[string]bool{
	"e.g": true, "i.e": true, "etc": true, "vs": true,
	"fig": true, "ref": true, "no": true, "op": true,
}

// agentSanitizeFilename strips common LLM response formatting artifacts from
// a filename string. Models frequently add numbered-list prefixes, markdown
// bold markers, backticks, code-fence markers, or prose around the filename.
func agentSanitizeFilename(name string) string {
	name = strings.TrimSpace(name)

	// Strip "FILE:" / "file:" label wherever it appears (handles prefix and
	// embedded positions like "```plaintext FILE: main.tf").
	if idx := strings.Index(strings.ToLower(name), "file:"); idx >= 0 {
		name = strings.TrimSpace(name[idx+5:])
	}

	// Strip numbered-list prefixes like "1. ", "2) ", "(3) "
	if idx := strings.Index(name, ". "); idx >= 0 && idx <= 3 {
		if _, err := strconv.Atoi(strings.TrimSpace(name[:idx])); err == nil {
			name = strings.TrimSpace(name[idx+2:])
		}
	}
	if idx := strings.Index(name, ") "); idx >= 0 && idx <= 3 {
		candidate := strings.TrimLeft(name[:idx], "(")
		if _, err := strconv.Atoi(strings.TrimSpace(candidate)); err == nil {
			name = strings.TrimSpace(name[idx+2:])
		}
	}
	// Strip markdown bold (**name**) or italic (*name*)
	name = strings.TrimPrefix(name, "**")
	name = strings.TrimSuffix(name, "**")
	name = strings.TrimPrefix(name, "*")
	name = strings.TrimSuffix(name, "*")
	// Strip backtick / code-fence characters
	name = strings.ReplaceAll(name, "`", "")
	// Strip leading language hints that code fences emit, e.g. "plaintext " or "hcl "
	if sp := strings.Index(name, " "); sp > 0 && sp <= 12 {
		rest := strings.TrimSpace(name[sp+1:])
		if rest != "" && !strings.Contains(rest, " ") {
			name = rest
		}
	}
	name = strings.TrimSpace(name)

	// Last resort: if the name still contains spaces it's clearly not a plain
	// filename — extract the last token that looks like "name.ext".
	if strings.Contains(name, " ") {
		if m := agentFilenameRe.FindString(name); m != "" {
			name = m
		}
	}

	return strings.TrimSpace(name)
}

// agentImpliedExtension returns the file extension (including dot) that a task
// implies creating, e.g. ".md" for "prepare md file". Returns "" when the task
// does not clearly request a new specific file type.
func agentImpliedExtension(task string) string {
	lower := strings.ToLower(task)
	pairs := [][2]string{
		{".md", ".md"}, {"md file", ".md"}, {"markdown", ".md"}, {"readme", ".md"},
		{".yaml", ".yaml"}, {"yaml file", ".yaml"}, {".yml", ".yml"}, {"yml file", ".yml"},
		{".json", ".json"}, {"json file", ".json"},
		{".toml", ".toml"}, {"toml file", ".toml"},
		{".txt", ".txt"}, {"txt file", ".txt"}, {"text file", ".txt"},
		{".sh", ".sh"}, {"shell script", ".sh"}, {"bash script", ".sh"},
		{".html", ".html"}, {"html file", ".html"},
		{".css", ".css"}, {"css file", ".css"},
		{".env", ".env"}, {"env file", ".env"},
		{".dockerfile", ""}, {"dockerfile", ""},
		// TypeScript / JavaScript families
		{"typescript", ".ts"}, {".ts ", ".ts"}, {".tsx", ".tsx"},
		{"react", ".tsx"}, {"vue", ".vue"}, {"angular", ".ts"},
		{"javascript", ".js"}, {".js ", ".js"}, {"node", ".js"},
		// Python
		{"python", ".py"}, {".py ", ".py"},
		// Go
		{"golang", ".go"}, {" go app", ".go"}, {" go server", ".go"},
		// Rust / Java / other common languages
		{"rust", ".rs"}, {"java ", ".java"}, {"kotlin", ".kt"},
		{"c# ", ".cs"}, {"csharp", ".cs"}, {".net", ".cs"},
		{"ruby", ".rb"}, {"php", ".php"}, {"swift", ".swift"},
	}
	for _, p := range pairs {
		if strings.Contains(lower, p[0]) {
			return p[1]
		}
	}
	return ""
}

// runAgentTask sends one LLM request per relevant file.
// For each file the LLM is asked to return the complete updated content.
// Changes are written to disk immediately.
// When there are no existing files (empty directory or no match), the LLM is
// asked to create a new file from scratch.
// agentChangelogPrompt returns a compact, token-efficient summary of past
// agent actions to inject into the system prompt for context continuity.
func (s *Server) agentChangelogPrompt() string {
	s.mu.RLock()
	log := s.agentLog
	s.mu.RUnlock()
	if len(log) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Previous actions in this session:\n")
	for _, e := range log {
		switch e.Operation {
		case "deleted":
			fmt.Fprintf(&b, "- DELETED %s (task: %q) — this file no longer exists\n", e.File, e.Task)
		default:
			fmt.Fprintf(&b, "- %s %s (task: %q)\n", e.Operation, e.File, e.Task)
		}
	}
	return b.String()
}

// agentIsDeleteTask returns true when the task is clearly asking to delete or
// remove one or more files entirely (not to delete code inside a file).
func agentIsDeleteTask(task string) bool {
	lower := strings.ToLower(task)
	deleteWords := []string{"delete", "remove", "erase"}
	fileWords := []string{"file", ".go", ".py", ".js", ".ts", ".yaml", ".yml", ".json",
		".tf", ".sh", ".md", ".txt", ".html", ".css", ".toml", ".env"}
	for _, d := range deleteWords {
		if strings.Contains(lower, d) {
			for _, f := range fileWords {
				if strings.Contains(lower, f) {
					return true
				}
			}
		}
	}
	return false
}

func promptTokens(s string) []string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) >= 3 {
			out = append(out, p)
		}
	}
	return out
}

func promptTokenSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, t := range promptTokens(s) {
		set[t] = struct{}{}
	}
	return set
}

func truncatePromptContent(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n\n...[truncated for context]"
}

func (s *Server) runAgentTask(ctx context.Context, task string, progress func(string), dryRun bool) (string, []AgentFileResult, error) {
	// Scope to files mentioned in the task; fall back to all files.
	allFiles := s.getActiveFilesSnapshot()
	targetFiles := s.scopeFilesToQuestion(task, allFiles)

	// nil means a technology mismatch was detected: the task requests a
	// language/framework absent from the workspace — route directly to create.
	if targetFiles == nil {
		progress("🔍 Technology mismatch detected — routing to file creation…")
		return s.runAgentCreate(ctx, task, allFiles, progress, true)
	}

	progress(fmt.Sprintf("Scoping: %d file(s) in range…", len(targetFiles)))

	// ── Detect "delete file" intent ─────────────────────────────────────────
	// When the task is asking to delete files, bypass the LLM entirely and
	// directly propose deletions for the scoped files.  This prevents the LLM
	// from misinterpreting a delete request as a rewrite instruction and
	// overwriting files that were never supposed to change.
	//
	// Only act when scopeFilesToQuestion found explicit matches; if it fell back
	// to "all files" we refuse rather than blindly deleting everything.
	if agentIsDeleteTask(task) {
		allCount := len(allFiles)
		scopedCount := len(targetFiles)
		if scopedCount == allCount {
			// No specific file was identified — refuse to avoid mass-deletion.
			errMsg := "⚠️ Could not identify a specific file to delete. Please mention the exact filename (e.g. \"delete main.go\")."
			return errMsg, nil, nil
		}
		var results []AgentFileResult
		for _, f := range targetFiles {
			if f == nil {
				continue
			}
			absPath := filepath.Clean(filepath.Join(s.directory, f.RelPath))
			oldContent := ""
			if data, err := os.ReadFile(absPath); err == nil {
				oldContent = string(data)
			}
			if !dryRun {
				if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
					results = append(results, AgentFileResult{File: f.RelPath, Error: err.Error()})
					continue
				}
				progress(fmt.Sprintf("🗑️  %s — deleted", f.RelPath))
				s.mu.Lock()
				s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "deleted", File: f.RelPath, Task: task})
				s.mu.Unlock()
			} else {
				progress(fmt.Sprintf("📋 %s — delete proposed (awaiting confirmation)", f.RelPath))
			}
			results = append(results, AgentFileResult{
				File:       f.RelPath,
				Changed:    true,
				Deleted:    true,
				Pending:    dryRun,
				OldContent: oldContent,
			})
		}
		var sb strings.Builder
		if dryRun {
			fmt.Fprintf(&sb, "🤖 **Agent**: %d file(s) proposed for deletion — confirm or deny below.\n", len(results))
		} else {
			fmt.Fprintf(&sb, "🤖 **Agent**: deleted %d file(s).\n", len(results))
		}
		for _, r := range results {
			if r.Error != "" {
				fmt.Fprintf(&sb, "• ❌ %s — %s\n", r.File, r.Error)
			} else if r.Pending {
				fmt.Fprintf(&sb, "• 📋 %s — pending confirmation\n", r.File)
			} else {
				fmt.Fprintf(&sb, "• 🗑️  %s — deleted\n", r.File)
			}
		}
		if needsRescan := !dryRun && len(results) > 0; needsRescan {
			if scanned, err := s.performRescan(); err == nil {
				s.mu.Lock()
				s.scanResult = scanned
				s.mu.Unlock()
			}
		}
		return sb.String(), results, nil
	}

	// ── Detect "create new file" intent ─────────────────────────────────────
	// If the task implies a specific new file type (e.g. ".md") and none of the
	// targeted files have that extension, route to runAgentCreate so we don't
	// overwrite an unrelated existing file with the new content.
	if impliedExt := agentImpliedExtension(task); impliedExt != "" {
		hasMatch := false
		for _, f := range targetFiles {
			if f != nil && filepath.Ext(f.RelPath) == impliedExt {
				hasMatch = true
				break
			}
		}
		if !hasMatch {
			// File-creation flows should always be confirm-first in UI so users can
			// select/tick which proposed files to create.
			return s.runAgentCreate(ctx, task, allFiles, progress, true)
		}
	}

	// ── No existing files: ask LLM to create a new one ──────────────────────
	if len(targetFiles) == 0 {
		// Force confirm-first for initial scaffold generation.
		return s.runAgentCreate(ctx, task, nil, progress, true)
	}

	var results []AgentFileResult
	changedCount := 0

	// ── Concurrent per-file LLM calls ────────────────────────────────────────
	// Use a semaphore sized to cfg.Agent.ConcurrentFiles so we saturate
	// OLLAMA_NUM_PARALLEL slots without over-subscribing.
	s.mu.RLock()
	concurrency := s.cfg.Agent.ConcurrentFiles
	s.mu.RUnlock()
	if concurrency <= 0 {
		concurrency = 1
	}

	type indexedResult struct {
		idx int
		r   AgentFileResult
	}

	resultsCh := make(chan indexedResult, len(targetFiles))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	// Pre-fetch changelog once; it's read-only during this loop.
	changelog := s.agentChangelogPrompt()
	systemPrompt := promptAgentEdit
	if changelog != "" {
		systemPrompt += "\n\n" + changelog
	}

	// crossFileContextLimit caps the total bytes of cross-file context injected
	// into each per-file prompt. Additional caps keep fanout bounded.
	const crossFileContextLimit = 24 * 1024
	const crossFileMaxFiles = 3
	const crossFileSnippetBytes = 6 * 1024
	taskLower := strings.ToLower(task)
	taskTokenSet := promptTokenSet(task)

	// Pre-read all target files once so that each goroutine can build its
	// cross-file context from memory rather than issuing O(n²) disk reads.
	fileCache := make(map[string]string, len(targetFiles))
	for _, f := range targetFiles {
		if f == nil || !f.IsReadable {
			continue
		}
		absPath := filepath.Clean(filepath.Join(s.directory, f.RelPath))
		if data, err := os.ReadFile(absPath); err == nil {
			fileCache[f.RelPath] = string(data)
		}
	}

	for fileIdx, f := range targetFiles {
		if f == nil {
			resultsCh <- indexedResult{fileIdx, AgentFileResult{}}
			continue
		}
		if !f.IsReadable {
			resultsCh <- indexedResult{fileIdx, AgentFileResult{File: f.RelPath}}
			continue
		}

		wg.Add(1)
		go func(idx int, f *types.FileInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			progress(fmt.Sprintf("⚡ %s (%d/%d)…", f.RelPath, idx+1, len(targetFiles)))

			// Use the pre-read cache; fall back to a direct read if the file was
			// not cached (e.g. it became readable after the pre-read pass).
			absPath := filepath.Clean(filepath.Join(s.directory, f.RelPath))
			oldContent, inCache := fileCache[f.RelPath]
			if !inCache {
				rawData, err := os.ReadFile(absPath)
				if err != nil {
					resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Error: err.Error()}}
					return
				}
				oldContent = string(rawData)
			}

			// Build bounded cross-file context from in-memory cache (no extra disk I/O).
			// Rank candidates and include only a few relevant files/snippets.
			type related struct {
				relPath string
				content string
				score   int
			}
			currentRelLower := strings.ToLower(f.RelPath)
			currentDir := strings.ToLower(filepath.Dir(currentRelLower))
			currentExt := strings.ToLower(filepath.Ext(currentRelLower))
			candidates := make([]related, 0, len(targetFiles))

			var ctxBuf strings.Builder
			for _, other := range targetFiles {
				if other == nil || other.RelPath == f.RelPath || !other.IsReadable {
					continue
				}
				if content, ok := fileCache[other.RelPath]; ok {
					otherRelLower := strings.ToLower(other.RelPath)
					otherBaseLower := strings.ToLower(filepath.Base(otherRelLower))
					otherDir := strings.ToLower(filepath.Dir(otherRelLower))
					otherExt := strings.ToLower(filepath.Ext(otherRelLower))

					score := 0
					if strings.Contains(taskLower, otherRelLower) {
						score += 12
					}
					if strings.Contains(taskLower, otherBaseLower) {
						score += 8
					}
					if otherDir == currentDir {
						score += 4
					}
					if otherExt != "" && otherExt == currentExt {
						score += 3
					}
					for _, tok := range promptTokens(otherBaseLower) {
						if _, ok := taskTokenSet[tok]; ok {
							score++
						}
					}

					candidates = append(candidates, related{
						relPath: other.RelPath,
						content: content,
						score:   score,
					})
				}
			}

			sort.Slice(candidates, func(i, j int) bool {
				if candidates[i].score == candidates[j].score {
					return candidates[i].relPath < candidates[j].relPath
				}
				return candidates[i].score > candidates[j].score
			})

			included := 0
			for _, cand := range candidates {
				if included >= crossFileMaxFiles {
					break
				}
				if cand.score <= 0 {
					continue
				}
				snippet := truncatePromptContent(cand.content, crossFileSnippetBytes)
				if ctxBuf.Len()+len(snippet) > crossFileContextLimit {
					continue
				}
				fmt.Fprintf(&ctxBuf, "=== %s ===\n%s\n\n", cand.relPath, snippet)
				included++
			}

			var userPrompt string
			if ctxBuf.Len() > 0 {
				userPrompt = fmt.Sprintf(
					"Related files for context (do NOT output these — only output the file you are asked to edit):\n\n%s\nFile to edit: %s\n\n%s\n\nTask: %s",
					ctxBuf.String(), f.RelPath, oldContent, task,
				)
			} else {
				userPrompt = fmt.Sprintf("File: %s\n\n%s\n\nTask: %s", f.RelPath, oldContent, task)
			}
			chatReq := &llm.ChatRequest{
				Model: s.cfg.LLM.Model,
				Messages: []llm.Message{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: userPrompt},
				},
				Temperature: agentTemperature(s.cfg.LLM.Temperature),
			}

			progress(fmt.Sprintf("⚙️  Calling LLM for %s…", f.RelPath))
			resp, err := s.llmClient.Chat(ctx, chatReq)
			if err != nil {
				progress(fmt.Sprintf("❌ %s — error", f.RelPath))
				resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Error: err.Error()}}
				return
			}

			newContent := agentStripFences(resp.Message.Content)

			// Handle explicit no-change sentinel.
			if strings.TrimSpace(newContent) == "NO_CHANGE" {
				progress(fmt.Sprintf("— %s — no change needed", f.RelPath))
				resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Changed: false}}
				return
			}

			// Handle explicit delete sentinel.
			if strings.TrimSpace(newContent) == "DELETE_FILE" {
				if !dryRun {
					if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
						progress(fmt.Sprintf("❌ %s — delete error", f.RelPath))
						resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Error: err.Error()}}
						return
					}
					progress(fmt.Sprintf("🗑️ %s — deleted", f.RelPath))
					s.mu.Lock()
					s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "deleted", File: f.RelPath, Task: task})
					s.mu.Unlock()
				} else {
					progress(fmt.Sprintf("📋 %s — delete proposed (awaiting confirmation)", f.RelPath))
				}
				resultsCh <- indexedResult{idx, AgentFileResult{
					File:       f.RelPath,
					Changed:    true,
					Deleted:    true,
					Pending:    dryRun,
					OldContent: oldContent,
				}}
				return
			}

			if newContent == "" || strings.TrimSpace(newContent) == strings.TrimSpace(oldContent) {
				progress(fmt.Sprintf("— %s — no change needed", f.RelPath))
				resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Changed: false}}
				return
			}

			if !dryRun {
				if err := os.WriteFile(absPath, []byte(newContent), 0o644); err != nil {
					progress(fmt.Sprintf("❌ %s — write error", f.RelPath))
					resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Error: err.Error()}}
					return
				}
				s.markWrittenFile(f.RelPath)
				progress(fmt.Sprintf("✅ %s — modified", f.RelPath))
				s.mu.Lock()
				s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "modified", File: f.RelPath, Task: task})
				s.mu.Unlock()
			} else {
				progress(fmt.Sprintf("📋 %s — proposed (awaiting confirmation)", f.RelPath))
			}
			resultsCh <- indexedResult{idx, AgentFileResult{
				File:       f.RelPath,
				Changed:    true,
				Pending:    dryRun,
				OldContent: oldContent,
				NewContent: newContent,
			}}
		}(fileIdx, f)
	}

	// Close channel once all goroutines finish.
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	// Collect into an ordered slice.
	ordered := make([]AgentFileResult, len(targetFiles))
	for ir := range resultsCh {
		ordered[ir.idx] = ir.r
	}
	for _, r := range ordered {
		if r.File == "" {
			continue
		}
		results = append(results, r)
		if r.Changed {
			changedCount++
		}
	}

	// Build summary line for the chat message.
	var sb strings.Builder
	if dryRun {
		fmt.Fprintf(&sb, "🤖 **Agent**: reviewed %d file(s), %d proposed — confirm or deny below.\n", len(results), changedCount)
	} else {
		fmt.Fprintf(&sb, "🤖 **Agent**: processed %d file(s), modified %d.\n", len(results), changedCount)
	}
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(&sb, "• ❌ %s — %s\n", r.File, r.Error)
		} else if r.Pending {
			fmt.Fprintf(&sb, "• 📋 %s — pending confirmation\n", r.File)
		} else if r.Created {
			fmt.Fprintf(&sb, "• ✨ %s — created\n", r.File)
		} else if r.Changed {
			fmt.Fprintf(&sb, "• ✅ %s — modified\n", r.File)
		} else {
			fmt.Fprintf(&sb, "• — %s — no change needed\n", r.File)
		}
	}

	return sb.String(), results, nil
}

// agentFileBlock holds a parsed filename + content pair from the LLM create response.
type agentFileBlock struct {
	name    string
	content string
}

// parseAgentCreateResponse parses the LLM response for create tasks.
//
// Supported formats:
//
//	Single file (legacy):
//	  main.go
//	  ---
//	  <content>
//
//	Multiple files:
//	  FILE: main.go
//	  ---
//	  <content>
//	  ===
//	  FILE: variables.tf
//	  ---
//	  <content>
//
// Both separators are normalised so tolerate leading/trailing whitespace.
func parseAgentCreateResponse(raw string) []agentFileBlock {
	const fileSep = "\n===\n"
	// Split on the multi-file separator first.
	sections := strings.Split(raw, fileSep)
	var blocks []agentFileBlock
	for _, sec := range sections {
		sec = strings.TrimSpace(sec)
		if sec == "" {
			continue
		}
		// Find the first \n---\n separator within this section.
		sepIdx := strings.Index(sec, "\n---\n")
		if sepIdx < 0 {
			continue
		}
		rawName := strings.TrimSpace(sec[:sepIdx])
		// Strip optional "FILE:" prefix (case-insensitive) the model might add.
		if len(rawName) > 5 && strings.EqualFold(rawName[:5], "file:") {
			rawName = strings.TrimSpace(rawName[5:])
		}
		content := agentStripFences(sec[sepIdx+5:])
		if rawName != "" && content != "" {
			blocks = append(blocks, agentFileBlock{name: rawName, content: content})
		}
	}
	return blocks
}

// agentExplicitFilenameFromTask returns a single explicitly mentioned filename
// from the task, e.g. "README.md" in "prepare README.md ...".
// It is intentionally conservative: long or multi-sentence tasks are
// treated as multi-file creation requests so they go through runAgentPlan.
// If none is clearly present, it returns "".
func agentExplicitFilenameFromTask(task string) string {
	// Long tasks describe multi-file projects — don't try to extract a single
	// filename from prose ("e.g., React, Vue..." would otherwise match "e.g").
	if len(task) > 200 {
		return ""
	}
	match := agentFilenameRe.FindString(task)
	if match == "" {
		return ""
	}
	name := agentSanitizeFilename(match)
	ext := filepath.Ext(name)
	if name == "" || strings.Contains(name, " ") || ext == "" {
		return ""
	}
	// Require at least a 2-character extension (e.g. ".go", not ".g").
	if len(ext) < 3 {
		return ""
	}
	// Block known prose abbreviations that match the filename pattern.
	if knownNonFilenames[strings.ToLower(name)] {
		return ""
	}
	return name
}

// runAgentPlan asks the LLM which files need to be created for the task.
// It returns a deduplicated, sanitized list of filenames. This is a cheap
// "thinking" call — the model only returns names, not content.
func (s *Server) runAgentPlan(ctx context.Context, task string, contextFiles []*types.FileInfo) ([]string, error) {
	// Build a brief context summary (just filenames, not content — keep tokens low).
	var ctxNames []string
	for _, cf := range contextFiles {
		if cf != nil && cf.IsReadable {
			ctxNames = append(ctxNames, cf.RelPath)
		}
	}
	userMsg := "Task: " + task
	if len(ctxNames) > 0 {
		userMsg += "\n\nExisting files in the project:\n" + strings.Join(ctxNames, "\n")
	}

	planPrompt := "You are a project planning agent. " +
		"Given a task, list EVERY file that must be created to fully complete it. " +
		"Output ONLY the filenames, one per line — no explanations, no numbers, no markdown, no extra text. " +
		"Include all files needed (e.g. for a standalone Go app: main.go and go.mod; " +
		"for Terraform: main.tf, variables.tf, providers.tf; " +
		"for a Python service: main.py, requirements.txt). " +
		"Always include config/dependency files required to make the project runnable."

	chatReq := &llm.ChatRequest{
		Model: s.cfg.LLM.Model,
		Messages: []llm.Message{
			{Role: "system", Content: planPrompt},
			{Role: "user", Content: userMsg},
		},
		Temperature: 0.2, // low temp: we want a deterministic, factual list
	}

	resp, err := s.llmClient.Chat(ctx, chatReq)
	if err != nil {
		return nil, err
	}

	// Parse the response: one filename per line.
	seen := map[string]bool{}
	var files []string
	for _, line := range strings.Split(resp.Message.Content, "\n") {
		name := agentSanitizeFilename(strings.TrimSpace(line))
		// Skip empty lines, lines that look like prose (contain spaces after sanitizing)
		if name == "" || strings.Contains(name, " ") {
			continue
		}
		ext := filepath.Ext(name)
		// Must have an extension of at least 2 chars (e.g. ".go", not ".g")
		// to reject abbreviations like "e.g" that the LLM may echo from the prompt.
		if len(ext) < 3 {
			continue
		}
		if knownNonFilenames[strings.ToLower(name)] {
			continue
		}
		if !seen[name] {
			seen[name] = true
			files = append(files, name)
		}
	}
	return files, nil
}

// runAgentCreate handles the case where one or more new files must be created
// from scratch. It first calls runAgentPlan to collect the full file list, then
// generates each file with a focused, isolated LLM prompt.
func (s *Server) runAgentCreate(ctx context.Context, task string, contextFiles []*types.FileInfo, progress func(string), dryRun bool) (string, []AgentFileResult, error) {
	// ── Step 1: planning call (or explicit single-file shortcut) ───────────
	var plannedFiles []string
	if explicit := agentExplicitFilenameFromTask(task); explicit != "" {
		// The user explicitly named a file, so avoid broad multi-file planning.
		plannedFiles = []string{explicit}
		progress(fmt.Sprintf("📋 Plan: explicit target %s (planning skipped)", explicit))
	} else {
		// Ask the model which files are needed. This small focused call is far more
		// reliable than asking a small LLM to plan AND generate in one shot.
		progress("🗺️  Planning: asking LLM which files to create…")
		var err error
		plannedFiles, err = s.runAgentPlan(ctx, task, contextFiles)
		if err != nil {
			return "", nil, fmt.Errorf("agent plan step failed: %w", err)
		}
		if len(plannedFiles) == 0 {
			// Planning failed silently (model returned only prose). Fall back to
			// the legacy single-call path.
			progress("⚠️  Planning returned no files — falling back to single-file mode…")
			plannedFiles = nil
		} else {
			var planBuf strings.Builder
			fmt.Fprintf(&planBuf, "📋 Plan: %d file(s) to create\n", len(plannedFiles))
			for _, f := range plannedFiles {
				fmt.Fprintf(&planBuf, "  • %s\n", f)
			}
			progress(planBuf.String())
		}
	}

	// ── Step 2: build context block ─────────────────────────────────────────
	var ctxBuf strings.Builder
	for _, cf := range contextFiles {
		if cf == nil || !cf.IsReadable {
			continue
		}
		absPath := filepath.Clean(filepath.Join(s.directory, cf.RelPath))
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		fmt.Fprintf(&ctxBuf, "=== %s ===\n%s\n\n", cf.RelPath, string(data))
	}

	changelog := s.agentChangelogPrompt()
	cleanDir := filepath.Clean(s.directory)
	var results []AgentFileResult
	var summaryLines []string

	// ── Step 3a: per-file generation (planning succeeded) ───────────────────
	if len(plannedFiles) > 0 {
		alreadyGenerated := map[string]string{} // relPath → content, for cross-file context

		for i, fileName := range plannedFiles {
			progress(fmt.Sprintf("⚙️  Generating %s (%d/%d)…", fileName, i+1, len(plannedFiles)))

			// Build a focused prompt for this single file.
			var userMsg strings.Builder
			fmt.Fprintf(&userMsg, "Task: %s\n\nGenerate ONLY the file: %s\n", task, fileName)
			if ctxBuf.Len() > 0 {
				fmt.Fprintf(&userMsg, "\nExisting project files for context:\n%s", ctxBuf.String())
			}
			// Inject already-generated files so the model can stay consistent
			// (e.g. go.mod module name matches main.go package path).
			if len(alreadyGenerated) > 0 {
				userMsg.WriteString("\nFiles already created in this session:\n")
				for name, content := range alreadyGenerated {
					fmt.Fprintf(&userMsg, "=== %s ===\n%s\n\n", name, content)
				}
			}

			sysPrompt := "You are a coding agent. " +
				"Generate ONLY the complete content of the single file specified. " +
				"Output raw file content — no markdown fences, no commentary, no filename header."
			if changelog != "" {
				sysPrompt += "\n\n" + changelog
			}

			chatReq := &llm.ChatRequest{
				Model: s.cfg.LLM.Model,
				Messages: []llm.Message{
					{Role: "system", Content: sysPrompt},
					{Role: "user", Content: userMsg.String()},
				},
				Temperature: s.cfg.LLM.Temperature,
			}
			resp, err := s.llmClient.Chat(ctx, chatReq)
			if err != nil {
				progress(fmt.Sprintf("❌ %s — LLM error: %s", fileName, err))
				continue
			}
			fileContent := agentStripFences(strings.TrimSpace(resp.Message.Content))
			if fileContent == "" {
				progress(fmt.Sprintf("⚠️  %s — empty response, skipping", fileName))
				continue
			}

			// Sanitize and validate the filename.
			fileName = agentSanitizeFilename(fileName)
			// Only strip to basename when the filename itself has no directory
			// component. If the LLM proposes "src/game.ts" we preserve the
			// subdirectory; if it proposes just "game.ts" we keep it as-is.
			if !strings.ContainsAny(fileName, "/\\") {
				fileName = filepath.Base(fileName)
			}
			if filepath.Ext(fileName) == "" {
				fileName = agentInferExtension(fileName, fileContent)
			}
			if fileName == "" {
				continue
			}

			absPath := filepath.Clean(filepath.Join(s.directory, fileName))
			rel, err := filepath.Rel(cleanDir, absPath)
			if err != nil || strings.HasPrefix(rel, "..") {
				return "", nil, fmt.Errorf("agent tried to write outside working directory: %s", fileName)
			}
			relSlash := filepath.ToSlash(rel)
			alreadyGenerated[relSlash] = fileContent

			if !dryRun {
				if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
					return "", nil, fmt.Errorf("failed to create directory for %s: %w", relSlash, err)
				}
				if err := os.WriteFile(absPath, []byte(fileContent), 0o644); err != nil {
					return "", nil, fmt.Errorf("failed to write %s: %w", relSlash, err)
				}
				s.markWrittenFile(relSlash)
				progress(fmt.Sprintf("✨ %s — created", relSlash))
				s.mu.Lock()
				s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "created", File: relSlash, Task: task})
				s.mu.Unlock()
				summaryLines = append(summaryLines, fmt.Sprintf("• ✨ %s — created", relSlash))
			} else {
				progress(fmt.Sprintf("📋 %s — proposed (awaiting confirmation)", relSlash))
				summaryLines = append(summaryLines, fmt.Sprintf("• 📋 %s — pending confirmation", relSlash))
			}
			results = append(results, AgentFileResult{
				File: relSlash, Created: true, Changed: true,
				Pending: dryRun, OldContent: "", NewContent: fileContent,
			})
		}
	}

	// ── Step 3b: legacy single-call path (planning returned nothing) ─────────
	if len(results) == 0 {
		progress("⚙️  Calling LLM to create file(s)…")

		userContent := task
		if ctxBuf.Len() > 0 {
			progress("Reading context files…")
			userContent = fmt.Sprintf("Existing files for context:\n\n%s\nTask: %s", ctxBuf.String(), task)
		}

		createSystemPrompt := promptAgentCreate
		if changelog != "" {
			createSystemPrompt += "\n\n" + changelog
		}

		chatReq := &llm.ChatRequest{
			Model: s.cfg.LLM.Model,
			Messages: []llm.Message{
				{Role: "system", Content: createSystemPrompt},
				{Role: "user", Content: userContent},
			},
			Temperature: s.cfg.LLM.Temperature,
		}
		resp, err := s.llmClient.Chat(ctx, chatReq)
		if err != nil {
			return "", nil, fmt.Errorf("agent create step failed: %w", err)
		}
		raw := strings.TrimSpace(resp.Message.Content)
		blocks := parseAgentCreateResponse(raw)
		if len(blocks) == 0 {
			return "", nil, fmt.Errorf("agent could not determine filename — try rephrasing, e.g. \"create main.go with a simple web server\"")
		}

		for _, blk := range blocks {
			fileName := blk.name
			fileContent := blk.content
			// Preserve subdirectory components the LLM proposes (e.g. src/game.ts);
			// only strip to basename when there is no directory separator.
			if !strings.ContainsAny(fileName, "/\\") {
				fileName = filepath.Base(fileName)
			}
			fileName = agentSanitizeFilename(fileName)
			if filepath.Ext(fileName) == "" {
				fileName = agentInferExtension(fileName, fileContent)
			}
			if fileName == "" || fileContent == "" {
				continue
			}
			absPath := filepath.Clean(filepath.Join(s.directory, fileName))
			rel, err := filepath.Rel(cleanDir, absPath)
			if err != nil || strings.HasPrefix(rel, "..") {
				return "", nil, fmt.Errorf("agent tried to write outside working directory: %s", fileName)
			}
			relSlash := filepath.ToSlash(rel)
			if !dryRun {
				if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
					return "", nil, fmt.Errorf("failed to create directory for %s: %w", relSlash, err)
				}
				if err := os.WriteFile(absPath, []byte(fileContent), 0o644); err != nil {
					return "", nil, fmt.Errorf("failed to write %s: %w", relSlash, err)
				}
				s.markWrittenFile(relSlash)
				progress(fmt.Sprintf("✨ %s — created", relSlash))
				s.mu.Lock()
				s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "created", File: relSlash, Task: task})
				s.mu.Unlock()
				summaryLines = append(summaryLines, fmt.Sprintf("• ✨ %s — created", relSlash))
			} else {
				progress(fmt.Sprintf("📋 %s — proposed (awaiting confirmation)", relSlash))
				summaryLines = append(summaryLines, fmt.Sprintf("• 📋 %s — pending confirmation", relSlash))
			}
			results = append(results, AgentFileResult{
				File: relSlash, Created: true, Changed: true,
				Pending: dryRun, OldContent: "", NewContent: fileContent,
			})
		}
	}

	if len(results) == 0 {
		return "", nil, fmt.Errorf("agent returned no valid files")
	}

	var summary string
	if dryRun {
		summary = fmt.Sprintf("🤖 **Agent**: proposed %d new file(s) — confirm or deny below.\n%s",
			len(results), strings.Join(summaryLines, "\n"))
	} else {
		summary = fmt.Sprintf("🤖 **Agent**: no existing files found — created %d file(s)\n%s",
			len(results), strings.Join(summaryLines, "\n"))
	}
	return summary, results, nil
}

// agentTemperature returns a temperature suitable for agent code-editing tasks.
// The configured temperature is often tuned low for Q&A (e.g. 0.10) which makes
// the model too conservative to actually apply changes. We floor it at 0.4.
func agentTemperature(configured float64) float64 {
	const minAgentTemp = 0.4
	if configured < minAgentTemp {
		return minAgentTemp
	}
	return configured
}

// agentInferExtension appends a file extension to name based on content
// heuristics. Called when the LLM returns a filename with no extension.
func agentInferExtension(name, content string) string {
	c := strings.TrimSpace(content)
	switch {
	case strings.HasPrefix(c, "package ") || strings.Contains(c, "\npackage "):
		return name + ".go"
	case strings.HasPrefix(c, "#!/usr/bin/env python") || strings.HasPrefix(c, "#!/usr/bin/python"):
		return name + ".py"
	case strings.Contains(c, "\ndef ") || (strings.Contains(c, "import ") && strings.Contains(c, ":\n")):
		return name + ".py"
	case strings.HasPrefix(c, "fn main") || (strings.Contains(c, "\nfn ") && strings.Contains(c, "use std::")):
		return name + ".rs"
	case strings.Contains(c, "module.exports") || strings.Contains(c, "require("):
		return name + ".js"
	case strings.Contains(c, "export default") || strings.Contains(c, "import React"):
		return name + ".tsx"
	}
	return name
}

// agentStripFences extracts raw file content from a model response that may
// contain explanation text and/or markdown code fences.
//
// Cases handled:
//  1. Response starts with ``` — drop the opening fence line and closing fence.
//  2. Response contains ``` somewhere (model wrote explanation then a fence) —
//     extract only the content of the *first* fenced block.
//  3. No fences — return the response as-is (model obeyed raw-content instruction).
func agentStripFences(s string) string {
	s = strings.TrimSpace(s)

	fenceIdx := strings.Index(s, "```")
	if fenceIdx < 0 {
		// No fences at all — treat the whole response as file content.
		return s
	}

	// Find the newline that ends the opening fence line (e.g. ```go).
	blockStart := strings.Index(s[fenceIdx:], "\n")
	if blockStart < 0 {
		return s
	}
	blockStart += fenceIdx + 1 // first content line

	// Find the closing fence.
	closeIdx := strings.Index(s[blockStart:], "```")
	if closeIdx < 0 {
		// Unclosed fence — return everything after the opening line.
		return strings.TrimSpace(s[blockStart:])
	}

	return strings.TrimSpace(s[blockStart : blockStart+closeIdx])
}

// FileEntry is the JSON shape returned by /api/files
type FileEntry struct {
	RelPath string `json:"relPath"`
	Size    int64  `json:"size"`
	Type    string `json:"type"`
}

// handleFiles returns the list of all scanned files.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]FileEntry, 0, len(s.scanResult.Files))
	for _, f := range s.scanResult.Files {
		entries = append(entries, FileEntry{
			RelPath: f.RelPath,
			Size:    f.Size,
			Type:    string(f.Type),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// handleFileContent returns the raw content of a file inside the scanned directory.
func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		sendError(w, "path is required")
		return
	}

	cleanDir := filepath.Clean(s.directory)
	absPath := filepath.Clean(filepath.Join(s.directory, relPath))

	// Prevent path traversal: the resolved path must stay inside the directory.
	rel, err := filepath.Rel(cleanDir, absPath)
	if err != nil || len(rel) >= 2 && rel[:2] == ".." {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to read file: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"path":    relPath,
		"content": string(data),
	})
}

// handleFileWrite writes new content to a file inside the scanned directory.
func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body")
		return
	}
	if req.Path == "" {
		sendError(w, "path is required")
		return
	}

	cleanDir := filepath.Clean(s.directory)
	absPath := filepath.Clean(filepath.Join(s.directory, req.Path))

	// Prevent path traversal.
	rel, err := filepath.Rel(cleanDir, absPath)
	if err != nil || len(rel) >= 2 && rel[:2] == ".." {
		http.Error(w, "Access denied: path outside working directory", http.StatusForbidden)
		return
	}

	// Ensure parent directory exists (for new files suggested by the LLM).
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		sendError(w, fmt.Sprintf("Failed to create directory: %v", err))
		return
	}

	if err := os.WriteFile(absPath, []byte(req.Content), 0o644); err != nil {
		sendError(w, fmt.Sprintf("Failed to write file: %v", err))
		return
	}

	// Append a confirmation message into the chat history.
	s.mu.Lock()
	s.writtenFiles[filepath.ToSlash(filepath.Clean(req.Path))] = struct{}{}
	msg := Message{
		Role:      "assistant",
		Content:   fmt.Sprintf("✅ Applied changes to **%s**", req.Path),
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": msg,
	})
}

// handleSettings returns or updates runtime-configurable settings.
// GET  /api/settings  → {"auto_apply": bool}
// POST /api/settings  ← {"auto_apply": bool}
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		autoApply := s.cfg.Agent.AutoApply
		s.mu.RUnlock()
		json.NewEncoder(w).Encode(map[string]interface{}{"auto_apply": autoApply})

	case http.MethodPost:
		var req struct {
			AutoApply bool `json:"auto_apply"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendError(w, "Invalid request body")
			return
		}
		s.mu.Lock()
		s.cfg.Agent.AutoApply = req.AutoApply
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]interface{}{"auto_apply": req.AutoApply})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
