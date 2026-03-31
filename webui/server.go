package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
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
	promptAgentFix    = mustPrompt("prompts/agent_fix.md")
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
	directory        string
	model            string
	endpoint         string
	indexTmpl        *template.Template
	scanResult       *types.ScanResult
	focusedPath      string
	cfg              *config.Config
	llmClient        *llm.OllamaClient
	messages         []Message
	agentLog         []AgentLogEntry     // structured changelog injected into agent prompts
	taskCancel       context.CancelFunc  // cancels the current in-flight LLM request (Stop button)
	taskToken        *struct{}           // unique identity token to avoid cross-task cancel races
	taskRunning      bool                // true while chat/agent work is executing
	taskKind         string              // "chat" | "agent"
	lastProgressText string              // last progress message from the running agent task
	planProgressText string              // sticky plan line (kept while a task runs)
	scanFilter       *filter.Filter      // filter snapshot from startup; reused by rescan so agent-created files (e.g. .gitignore) don't hide themselves
	writtenFiles     map[string]struct{} // rel paths written via /api/file/write; always shown on rescan
	mu               sync.RWMutex
}

// Message represents a chat message
type Message struct {
	Role            string            `json:"role"`
	Content         string            `json:"content"`
	Timestamp       time.Time         `json:"timestamp"`
	DurationMs      int64             `json:"duration_ms,omitempty"`
	PromptEvalCount int               `json:"prompt_eval_count,omitempty"`
	AgentResults    []AgentFileResult `json:"agentResults,omitempty"`
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
	Directory    string `json:"directory"`
	Model        string `json:"model"`
	TotalFiles   int    `json:"totalFiles"`
	FocusedPath  string `json:"focusedPath,omitempty"`
	Processing   bool   `json:"processing"`
	TaskKind     string `json:"taskKind,omitempty"`
	LastProgress string `json:"lastProgress,omitempty"`
	PlanProgress string `json:"planProgress,omitempty"`
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
	mux.HandleFunc("/api/agent/fixstream", s.handleFixStream)
	mux.HandleFunc("/api/ext/send", s.handleExtSend)
	mux.HandleFunc("/api/ext/stream", s.handleExtStream)

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
		Directory:    s.directory,
		Model:        s.model,
		TotalFiles:   s.scanResult.TotalFiles,
		FocusedPath:  s.focusedPath,
		Processing:   s.taskRunning,
		TaskKind:     s.taskKind,
		LastProgress: s.lastProgressText,
		PlanProgress: s.planProgressText,
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
		Role:            "assistant",
		Content:         answer,
		Timestamp:       time.Now(),
		DurationMs:      duration.Milliseconds(),
		PromptEvalCount: resp.PromptEvalCount,
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

	var systemPrompt string
	if len(filePaths) == 0 {
		systemPrompt = promptChat + "\nAvailable files: none — the working directory has not been scanned or contains no readable files. Do NOT invent, guess, or fabricate any file contents or code. If the user asks about a specific file, tell them no files are available."
	} else {
		systemPrompt = promptChat + "\nAvailable files: " + strings.Join(filePaths, ", ")
	}

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

// ExtSendRequest is the JSON body for POST /api/ext/send and POST /api/ext/stream.
// It allows external apps to send messages or agent tasks directly without
// going through the web UI chat box.
//
// Fields:
//
//	Message – the prompt / task text (required)
//	Mode    – "chat" (default) or "agent"
//	Model   – optional model override for this request only (e.g. "llama3:8b")
//
// Note: agent mode always writes files immediately via the external API.
// The pending/commit review workflow is exclusively for the browser UI.
type ExtSendRequest struct {
	Message string `json:"message"`
	Mode    string `json:"mode"`  // "chat" | "agent"
	Model   string `json:"model"` // optional per-request model override
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
	Pending         bool   `json:"pending,omitempty"`
	OldContent      string `json:"oldContent,omitempty"`
	NewContent      string `json:"newContent,omitempty"`
	Error           string `json:"error,omitempty"`
	PromptEvalCount int    `json:"prompt_eval_count,omitempty"`
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

	var agentPromptEvalCount int
	for _, r := range results {
		agentPromptEvalCount += r.PromptEvalCount
	}
	msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(agentStart).Milliseconds(), AgentResults: results, PromptEvalCount: agentPromptEvalCount}
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
		s.mu.Lock()
		s.lastProgressText = text
		if strings.Contains(text, "\U0001F4CB Plan:") {
			s.planProgressText = text
		}
		s.mu.Unlock()
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
			s.lastProgressText = ""
			s.planProgressText = ""
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

	var streamAgentPromptEvalCount int
	for _, r := range results {
		streamAgentPromptEvalCount += r.PromptEvalCount
	}
	msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(streamAgentStart).Milliseconds(), AgentResults: results, PromptEvalCount: streamAgentPromptEvalCount}
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

	// Apply a per-request model override if provided, then restore on exit.
	if req.Model != "" {
		reqModel := strings.TrimSpace(req.Model)
		s.mu.Lock()
		prevModel := s.model
		prevCfgModel := s.cfg.LLM.Model
		prevClient := s.llmClient
		s.model = reqModel
		s.cfg.LLM.Model = reqModel
		s.llmClient = llm.NewOllamaClient(s.cfg.LLM.Endpoint, reqModel, s.cfg.LLM.Timeout)
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.model = prevModel
			s.cfg.LLM.Model = prevCfgModel
			s.llmClient = prevClient
			s.mu.Unlock()
		}()
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
		// External API always writes files immediately — no pending/commit workflow.
		extAgentStart := time.Now()
		summary, results, err := s.runAgentTask(taskCtx, text, func(string) {}, false)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "stopped"})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		// Always rescan after writing — dryRun is always false for the external API.
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

		var extAgentPromptEvalCount int
		for _, r := range results {
			extAgentPromptEvalCount += r.PromptEvalCount
		}
		msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(extAgentStart).Milliseconds(), AgentResults: results, PromptEvalCount: extAgentPromptEvalCount}
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
		resp, answer, chatDuration, err := s.processQuestion(taskCtx, text, activeFiles)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "stopped"})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		var promptEvalCount int
		if resp != nil {
			promptEvalCount = resp.PromptEvalCount
		}
		msg := Message{Role: "assistant", Content: answer, Timestamp: time.Now(), DurationMs: chatDuration.Milliseconds(), PromptEvalCount: promptEvalCount}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()

		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": msg})
	}
}

// handleExtStream is the streaming (SSE) variant of handleExtSend.
// It accepts the same ExtSendRequest body and emits Server-Sent Events so
// external callers can stream progress in real time without polling.
//
// Event types:
//
//	{"type":"status","text":"..."}                         — agent progress update (agent mode only)
//	{"type":"done","success":true,"message":{...},"agentResults":[...]}  — final result
//	{"type":"done","success":true,"cleared":true}          — after a clear command
//	{"type":"done","success":false,"error":"..."}          — on failure
func (s *Server) handleExtStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	var req ExtSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": "invalid request body"})
		return
	}

	text := strings.TrimSpace(req.Message)
	if text == "" {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": "message is required"})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "chat"
	}
	if mode != "chat" && mode != "agent" {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": "mode must be \"chat\" or \"agent\""})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	// Apply a per-request model override if provided, then restore on exit.
	if req.Model != "" {
		reqModel := strings.TrimSpace(req.Model)
		s.mu.Lock()
		prevModel := s.model
		prevCfgModel := s.cfg.LLM.Model
		prevClient := s.llmClient
		s.model = reqModel
		s.cfg.LLM.Model = reqModel
		s.llmClient = llm.NewOllamaClient(s.cfg.LLM.Endpoint, reqModel, s.cfg.LLM.Timeout)
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.model = prevModel
			s.cfg.LLM.Model = prevCfgModel
			s.llmClient = prevClient
			s.mu.Unlock()
		}()
	}

	s.mu.Lock()
	s.messages = append(s.messages, Message{Role: "user", Content: text, Timestamp: time.Now()})
	s.mu.Unlock()

	// Handle built-in commands (clear, stats, …) before touching the LLM.
	if response := s.handleCommand(text); response != "" {
		if response == "__CLEAR__" {
			sseWrite(w, flusher, map[string]any{"type": "done", "success": true, "cleared": true})
			return
		}
		msg := Message{Role: "assistant", Content: response, Timestamp: time.Now()}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()
		sseWrite(w, flusher, map[string]any{"type": "done", "success": true, "message": msg})
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
			s.lastProgressText = ""
			s.planProgressText = ""
		}
		s.mu.Unlock()
		cancel()
	}()

	// sseMu guards concurrent writes to w from the progress callback.
	var sseMu sync.Mutex

	switch mode {
	case "agent":
		// External API always writes files immediately — no pending/commit workflow.
		progress := func(text string) {
			sseMu.Lock()
			defer sseMu.Unlock()
			s.mu.Lock()
			s.lastProgressText = text
			if strings.Contains(text, "\U0001F4CB Plan:") {
				s.planProgressText = text
			}
			s.mu.Unlock()
			sseWrite(w, flusher, map[string]any{"type": "status", "text": text})
		}

		agentStart := time.Now()
		summary, results, err := s.runAgentTask(taskCtx, text, progress, false)
		if err != nil {
			if errors.Is(err, taskCtx.Err()) {
				return // stopped
			}
			sseMu.Lock()
			sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": err.Error()})
			sseMu.Unlock()
			return
		}

		// Always rescan after writing — dryRun is always false for the external API.
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

		var extStreamAgentPromptEvalCount int
		for _, r := range results {
			extStreamAgentPromptEvalCount += r.PromptEvalCount
		}
		msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(agentStart).Milliseconds(), AgentResults: results, PromptEvalCount: extStreamAgentPromptEvalCount}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()

		sseMu.Lock()
		sseWrite(w, flusher, map[string]any{
			"type":         "done",
			"success":      true,
			"message":      msg,
			"agentResults": results,
		})
		sseMu.Unlock()

	default: // "chat"
		activeFiles := s.scopeFilesToQuestion(text, s.getActiveFilesSnapshot())
		resp, answer, chatDuration, err := s.processQuestion(taskCtx, text, activeFiles)
		if err != nil {
			if errors.Is(err, taskCtx.Err()) {
				return // stopped
			}
			sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": err.Error()})
			return
		}

		var promptEvalCount int
		if resp != nil {
			promptEvalCount = resp.PromptEvalCount
		}
		msg := Message{Role: "assistant", Content: answer, Timestamp: time.Now(), DurationMs: chatDuration.Milliseconds(), PromptEvalCount: promptEvalCount}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()

		sseWrite(w, flusher, map[string]any{"type": "done", "success": true, "message": msg})
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
		return s.runAgentCreate(ctx, task, allFiles, progress, dryRun)
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
			return s.runAgentCreate(ctx, task, allFiles, progress, dryRun)
		}
	}

	// ── No existing files: ask LLM to create a new one ──────────────────────
	if len(targetFiles) == 0 {
		return s.runAgentCreate(ctx, task, nil, progress, dryRun)
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
				resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Changed: false, PromptEvalCount: resp.PromptEvalCount}}
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
					File:            f.RelPath,
					Changed:         true,
					Deleted:         true,
					Pending:         dryRun,
					OldContent:      oldContent,
					PromptEvalCount: resp.PromptEvalCount,
				}}
				return
			}

			if newContent == "" || strings.TrimSpace(newContent) == strings.TrimSpace(oldContent) {
				progress(fmt.Sprintf("— %s — no change needed", f.RelPath))
				resultsCh <- indexedResult{idx, AgentFileResult{File: f.RelPath, Changed: false, PromptEvalCount: resp.PromptEvalCount}}
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
				File:            f.RelPath,
				Changed:         true,
				Pending:         dryRun,
				OldContent:      oldContent,
				NewContent:      newContent,
				PromptEvalCount: resp.PromptEvalCount,
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

	// If every file returned NO_CHANGE (no errors), the LLM determined that no
	// existing file needs modification for this task — this usually means the
	// task is asking for something new that doesn't exist yet (e.g. "write a
	// key/value database"). Fall back to runAgentCreate so the LLM can generate
	// new files instead of silently producing zero changes.
	if changedCount == 0 && len(results) > 0 {
		noErrors := true
		for _, r := range results {
			if r.Error != "" {
				noErrors = false
				break
			}
		}
		if noErrors {
			progress("🔄 No existing files need changes — routing to file creation…")
			return s.runAgentCreate(ctx, task, allFiles, progress, dryRun)
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
				PromptEvalCount: resp.PromptEvalCount,
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

		legacyPromptEvalCount := resp.PromptEvalCount
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
				PromptEvalCount: legacyPromptEvalCount,
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

// fixTemperature returns the temperature to use for a fix attempt.
// When the same error keeps recurring we raise the temperature to push the model
// off its local minimum and force genuinely different solutions.
func fixTemperature(configured float64, attempt int, sameError bool) float64 {
	base := agentTemperature(configured)
	if sameError && attempt > 1 {
		// Each repeated failure nudges temperature up by 0.15, capped at 1.0.
		bump := 0.15 * float64(attempt-1)
		if t := base + bump; t < 1.0 {
			return t
		}
		return 1.0
	}
	return base
}

// extractErrorSnippets parses compiler/interpreter error output for
// "file:line: message" references and returns a formatted string that shows
// the exact source lines together with their error annotations.
// workDir is the project root used to resolve relative paths.
func extractErrorSnippets(workDir, errorOutput string) string {
	// Matches: ./path/file.go:42:5: some error message
	//          path/file.go:42: some error
	rx := regexp.MustCompile(`(?m)^(?:\./)?([^\s:]+\.\w+):(\d+)(?::\d+)?:\s+(.+)$`)
	type annotation struct {
		file    string
		lineNum int
		msg     string
	}
	var annotations []annotation
	for _, m := range rx.FindAllStringSubmatch(errorOutput, -1) {
		ln, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		annotations = append(annotations, annotation{file: m[1], lineNum: ln, msg: m[3]})
	}
	if len(annotations) == 0 {
		return ""
	}

	// Group annotations by file and load each file once.
	type fileEntry struct {
		lines []string
		annos []annotation
	}
	fileMap := map[string]*fileEntry{}
	for _, a := range annotations {
		fe, ok := fileMap[a.file]
		if !ok {
			abs := filepath.Clean(filepath.Join(workDir, a.file))
			rel, relErr := filepath.Rel(filepath.Clean(workDir), abs)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				continue // security: skip paths outside workDir
			}
			data, readErr := os.ReadFile(abs)
			if readErr != nil {
				continue
			}
			fe = &fileEntry{lines: strings.Split(string(data), "\n")}
			fileMap[a.file] = fe
		}
		fe.annos = append(fe.annos, a)
	}

	var sb strings.Builder
	sb.WriteString("Highlighted error locations (context ±3 lines):\n")
	for fname, fe := range fileMap {
		fmt.Fprintf(&sb, "\n--- %s ---\n", fname)
		for _, a := range fe.annos {
			start := a.lineNum - 4
			if start < 0 {
				start = 0
			}
			end := a.lineNum + 3
			if end > len(fe.lines) {
				end = len(fe.lines)
			}
			for i := start; i < end; i++ {
				lineMarker := " "
				if i+1 == a.lineNum {
					lineMarker = ">"
				}
				fmt.Fprintf(&sb, "%s %4d | %s\n", lineMarker, i+1, fe.lines[i])
			}
			fmt.Fprintf(&sb, "       ^ ERROR: %s\n\n", a.msg)
		}
	}
	return sb.String()
}

// ── Search/Replace patch types ─────────────────────────────────────────────

// fixHunk is a single search→replace pair within a file.
type fixHunk struct {
	search  string
	replace string
}

// fixPatch describes all hunks to apply to one file.
type fixPatch struct {
	name  string
	hunks []fixHunk
}

// parseFixPatchResponse parses SEARCH/REPLACE blocks from the LLM fix response.
//
// Expected format (one or more per response):
//
//	FILE: path/to/file.go
//	<<<<<<< SEARCH
//	old code
//	=======
//	new code
//	>>>>>>> REPLACE
func parseFixPatchResponse(text string) []fixPatch {
	const (
		stateTop = iota
		stateSearch
		stateReplace
	)
	var patches []fixPatch
	var current *fixPatch
	state := stateTop
	var buf []string
	var currentSearch string

	flushPatch := func() {
		if current != nil && len(current.hunks) > 0 {
			patches = append(patches, *current)
		}
	}

	for _, line := range strings.Split(text, "\n") {
		switch state {
		case stateTop:
			if strings.HasPrefix(line, "FILE:") {
				flushPatch()
				name := strings.TrimSpace(strings.TrimPrefix(line, "FILE:"))
				current = &fixPatch{name: name}
			} else if strings.HasPrefix(line, "<<<<<<< SEARCH") || line == "<<<<<<<" {
				if current != nil {
					state = stateSearch
					buf = nil
				}
			}
		case stateSearch:
			if line == "=======" {
				currentSearch = strings.Join(buf, "\n")
				buf = nil
				state = stateReplace
			} else {
				buf = append(buf, line)
			}
		case stateReplace:
			if strings.HasPrefix(line, ">>>>>>> REPLACE") || line == ">>>>>>>" {
				if current != nil {
					current.hunks = append(current.hunks, fixHunk{
						search:  currentSearch,
						replace: strings.Join(buf, "\n"),
					})
				}
				buf = nil
				state = stateTop
			} else {
				buf = append(buf, line)
			}
		}
	}
	flushPatch()
	return patches
}

// applyFixPatch applies all hunks in patch to the file at workDir/patch.name.
// Each hunk replaces the first occurrence of search with replace.
// If search is empty the replace content is written as the entire file.
// Returns per-hunk warnings (non-fatal) plus any fatal error.
func applyFixPatch(workDir string, patch fixPatch) (warnings []string, err error) {
	cleanDir := filepath.Clean(workDir)
	absPath := filepath.Clean(filepath.Join(workDir, patch.name))
	rel, relErr := filepath.Rel(cleanDir, absPath)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("path %q is outside working directory", patch.name)
	}

	var content string
	existing, readErr := os.ReadFile(absPath)
	hadExisting := readErr == nil
	if readErr == nil {
		content = string(existing)
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read %s: %w", patch.name, readErr)
	}

	originalContent := content
	appliedAny := false

	for i, h := range patch.hunks {
		if h.search == "" {
			if content != h.replace {
				content = h.replace
				appliedAny = true
			}
			continue
		}
		// Strip display metadata the model might have accidentally included.
		cleanSearch := stripDisplayPrefixes(h.search)
		applied, ok := fuzzyReplaceHunk(content, cleanSearch, h.replace)
		if !ok {
			// Show the first line of what the model searched for to aid debugging.
			preview := cleanSearch
			if nl := strings.IndexByte(preview, '\n'); nl != -1 {
				preview = preview[:nl]
			}
			if len(preview) > 80 {
				preview = preview[:80] + "…"
			}
			warnings = append(warnings, fmt.Sprintf("hunk %d: search text not found in %s (searched for: %q)", i+1, patch.name, preview))
			continue
		}
		if applied != content {
			appliedAny = true
		}
		content = applied
	}

	if !appliedAny {
		return warnings, fmt.Errorf("patch made no effective changes to %s", patch.name)
	}

	if content == originalContent {
		return warnings, fmt.Errorf("patch made no effective changes to %s", patch.name)
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return warnings, fmt.Errorf("mkdir for %s: %w", patch.name, err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return warnings, fmt.Errorf("write %s: %w", patch.name, err)
	}

	if validateErr := validatePatchedFile(workDir, rel, content); validateErr != nil {
		if hadExisting {
			if rollbackErr := os.WriteFile(absPath, existing, 0o644); rollbackErr != nil {
				return warnings, fmt.Errorf("%v (rollback failed: %w)", validateErr, rollbackErr)
			}
		} else {
			if rollbackErr := os.Remove(absPath); rollbackErr != nil && !os.IsNotExist(rollbackErr) {
				return warnings, fmt.Errorf("%v (rollback failed: %w)", validateErr, rollbackErr)
			}
		}
		return warnings, validateErr
	}

	return warnings, nil
}

// validatePatchedFile runs lightweight syntax checks for common manifest files.
// This prevents the fix loop from keeping obviously invalid replacements.
func validatePatchedFile(workDir, relPath, content string) error {
	if strings.EqualFold(filepath.Base(relPath), "go.mod") {
		vctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		stdout, stderr, exitCode := execCommand(vctx, workDir, "go", "mod", "edit", "-json")
		if exitCode != 0 {
			diag := strings.TrimSpace(stderr)
			if diag == "" {
				diag = strings.TrimSpace(stdout)
			}
			if diag == "" {
				diag = "go mod edit -json failed"
			}
			return fmt.Errorf("go.mod validation failed: %s", diag)
		}
	}

	if strings.EqualFold(filepath.Ext(relPath), ".json") {
		var parsed any
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			return fmt.Errorf("JSON validation failed: %w", err)
		}
	}

	return nil
}

// stripDisplayPrefixes removes display-only metadata that the LLM may have
// accidentally included in a SEARCH block when copying from the context output.
// Removes lines matching `line N | ` prefixes and `^ ERROR:` annotations.
func stripDisplayPrefixes(s string) string {
	rxPrefix := regexp.MustCompile(`^line\s+\d+\s+\|\s?`)
	rxError := regexp.MustCompile(`^\s*\^\s*ERROR:.*$`)
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if rxError.MatchString(line) {
			continue // drop error annotation lines entirely
		}
		out = append(out, rxPrefix.ReplaceAllString(line, ""))
	}
	return strings.Join(out, "\n")
}

// fuzzyReplaceHunk tries to replace the first occurrence of search in content
// with replace. It first tries an exact match; if that fails it falls back to
// a line-by-line sliding window that ignores trailing whitespace differences.
func fuzzyReplaceHunk(content, search, replace string) (result string, ok bool) {
	// Fast path: exact match.
	if strings.Contains(content, search) {
		return strings.Replace(content, search, replace, 1), true
	}

	// Slow path: normalize trailing whitespace per line and search line-by-line.
	normLine := func(s string) string { return strings.TrimRight(s, " \t\r") }

	contentLines := strings.Split(content, "\n")
	searchLines := strings.Split(strings.TrimRight(search, "\n"), "\n")
	replaceLines := strings.Split(replace, "\n")

	nc := make([]string, len(contentLines))
	for i, l := range contentLines {
		nc[i] = normLine(l)
	}
	ns := make([]string, len(searchLines))
	for i, l := range searchLines {
		ns[i] = normLine(l)
	}

	for i := 0; i <= len(contentLines)-len(searchLines); i++ {
		matched := true
		for j, sl := range ns {
			if nc[i+j] != sl {
				matched = false
				break
			}
		}
		if matched {
			out := make([]string, 0, len(contentLines)-len(searchLines)+len(replaceLines))
			out = append(out, contentLines[:i]...)
			out = append(out, replaceLines...)
			out = append(out, contentLines[i+len(searchLines):]...)
			return strings.Join(out, "\n"), true
		}
	}
	return content, false
}

// buildFixContext builds a focused prompt section for the LLM.
// For each file referenced in errorOutput it includes:
//   - the first 30 lines (package declaration + imports)
//   - ±10 lines around each error location, with error annotations inline
//
// This replaces sending full file contents, keeping the prompt small.
func buildFixContext(workDir, errorOutput string) string {
	rx := regexp.MustCompile(`(?m)^(?:\./)?([^\s:]+):(\d+)(?::\d+)?:\s+(.+)$`)
	type anno struct {
		lineNum int
		msg     string
	}
	fileAnnos := map[string][]anno{}
	var fileOrder []string
	for _, m := range rx.FindAllStringSubmatch(errorOutput, -1) {
		ln, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		if _, seen := fileAnnos[m[1]]; !seen {
			fileOrder = append(fileOrder, m[1])
		}
		fileAnnos[m[1]] = append(fileAnnos[m[1]], anno{lineNum: ln, msg: m[3]})
	}
	if len(fileAnnos) == 0 {
		return ""
	}

	type lineRange struct{ start, end int }

	var sb strings.Builder
	for _, fname := range fileOrder {
		annos := fileAnnos[fname]
		abs := filepath.Clean(filepath.Join(workDir, fname))
		rel, relErr := filepath.Rel(filepath.Clean(workDir), abs)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		srcLines := strings.Split(string(data), "\n")
		total := len(srcLines)

		// Build ranges: first 30 lines (imports) + ±10 around each error.
		var ranges []lineRange
		headerEnd := 30
		if headerEnd > total {
			headerEnd = total
		}
		ranges = append(ranges, lineRange{0, headerEnd})
		for _, a := range annos {
			s := a.lineNum - 11
			if s < 0 {
				s = 0
			}
			e := a.lineNum + 10
			if e > total {
				e = total
			}
			ranges = append(ranges, lineRange{s, e})
		}

		// Sort and merge overlapping ranges.
		sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
		merged := []lineRange{ranges[0]}
		for _, r := range ranges[1:] {
			last := &merged[len(merged)-1]
			if r.start <= last.end+1 {
				if r.end > last.end {
					last.end = r.end
				}
			} else {
				merged = append(merged, r)
			}
		}

		// Header note: make it unambiguous that "line N |" is display metadata,
		// NOT part of the file. The LLM must omit it from SEARCH blocks.
		fmt.Fprintf(&sb, "FILE: %s  (format: 'line N | RAW_CODE' — use only RAW_CODE in SEARCH blocks)\n", fname)
		lastEnd := 0
		for _, r := range merged {
			if r.start > lastEnd {
				fmt.Fprintf(&sb, "  ...\n")
			}
			for i := r.start; i < r.end; i++ {
				// Print the raw code line with a line-number prefix.
				fmt.Fprintf(&sb, "line %d | %s\n", i+1, srcLines[i])
				// Print error annotation on its own line so it is never
				// confused with file content.
				for _, a := range annos {
					if a.lineNum == i+1 {
						fmt.Fprintf(&sb, "         ^ ERROR: %s\n", a.msg)
						break
					}
				}
			}
			lastEnd = r.end
		}
		if lastEnd < total {
			fmt.Fprintf(&sb, "  ...\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// errorReferencedFiles returns unique file paths mentioned in error output.
// It accepts any file-like token before :line so it works across ecosystems
// (e.g. go.mod, Makefile, *.py, *.ts, Cargo.toml).
func errorReferencedFiles(workDir, errorOutput string) []string {
	rx := regexp.MustCompile(`(?m)^(?:\./)?([^\s:]+):(\d+)(?::\d+)?:`)
	seen := map[string]bool{}
	var out []string
	cleanDir := filepath.Clean(workDir)

	for _, m := range rx.FindAllStringSubmatch(errorOutput, -1) {
		relPath := filepath.ToSlash(filepath.Clean(m[1]))
		if seen[relPath] {
			continue
		}
		abs := filepath.Clean(filepath.Join(workDir, relPath))
		rel, relErr := filepath.Rel(cleanDir, abs)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		st, statErr := os.Stat(abs)
		if statErr != nil || st.IsDir() {
			continue
		}
		seen[relPath] = true
		out = append(out, relPath)
	}

	return out
}

func detectDeterministicManifestRepairTarget(errorOutput string) (string, bool) {
	if strings.Contains(errorOutput, "errors parsing go.mod") {
		return "go.mod", true
	}
	return "", false
}

func attemptDeterministicManifestRepair(workDir, manifestFile string) (bool, string) {
	switch manifestFile {
	case "go.mod":
		return attemptAutoRepairGoMod(workDir)
	default:
		return false, fmt.Sprintf("Auto-repair skipped: deterministic strategy for %s is not implemented", manifestFile)
	}
}

func buildManifestErrorGuidance(errorOutput string, relatedFiles []string) string {
	var b strings.Builder

	hasGoMod := strings.Contains(errorOutput, "errors parsing go.mod")
	if !hasGoMod {
		for _, rel := range relatedFiles {
			if strings.EqualFold(filepath.Base(rel), "go.mod") {
				hasGoMod = true
				break
			}
		}
	}
	if hasGoMod {
		b.WriteString("Manifest parser-error handling (go.mod):\n")
		b.WriteString("- Return a FULL replacement for go.mod in a FILE/---/=== block (not partial prose).\n")
		b.WriteString("- The go.mod content must be DIFFERENT from the current file and syntactically valid for `go mod edit -json`.\n")
		b.WriteString("- Do not repeat any previous rejected or no-op go.mod content shown above.\n")
	}

	return b.String()
}

// attemptAutoRepairGoMod performs a deterministic rewrite of go.mod when it is
// syntactically broken. It keeps only valid directives and validates the result
// with `go mod edit -json` before accepting it.
func attemptAutoRepairGoMod(workDir string) (bool, string) {
	absPath := filepath.Join(workDir, "go.mod")
	originalBytes, err := os.ReadFile(absPath)
	if err != nil {
		return false, fmt.Sprintf("Auto-repair skipped: could not read go.mod (%v)", err)
	}

	rebuilt := rebuildGoModContent(workDir, string(originalBytes))
	if strings.TrimSpace(rebuilt) == "" {
		return false, "Auto-repair skipped: could not derive a valid go.mod structure"
	}
	if rebuilt == string(originalBytes) {
		return false, "Auto-repair skipped: deterministic rewrite produced no changes"
	}

	if err := os.WriteFile(absPath, []byte(rebuilt), 0o644); err != nil {
		return false, fmt.Sprintf("Auto-repair failed while writing go.mod (%v)", err)
	}

	if err := validatePatchedFile(workDir, "go.mod", rebuilt); err != nil {
		_ = os.WriteFile(absPath, originalBytes, 0o644)
		return false, fmt.Sprintf("Auto-repair rejected: %v", err)
	}

	return true, "Auto-repaired go.mod using deterministic parser-safe rewrite"
}

func rebuildGoModContent(workDir, content string) string {
	var modulePath string
	var goVersion string
	var requireOrder []string
	requireLines := map[string]string{}
	var replaceOrder []string
	replaceSeen := map[string]bool{}

	inRequireBlock := false
	inReplaceBlock := false

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "module "):
			fields := strings.Fields(line)
			if len(fields) >= 2 && isLikelyModulePath(fields[1]) {
				modulePath = fields[1]
			}
			inRequireBlock = false
			inReplaceBlock = false
			continue

		case strings.HasPrefix(line, "go "):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if v := normalizeGoVersion(fields[1]); v != "" {
					goVersion = v
				}
			}
			inRequireBlock = false
			inReplaceBlock = false
			continue

		case line == "require (":
			inRequireBlock = true
			inReplaceBlock = false
			continue

		case line == "replace (":
			inReplaceBlock = true
			inRequireBlock = false
			continue

		case line == ")":
			inRequireBlock = false
			inReplaceBlock = false
			continue

		case strings.HasPrefix(line, "require "):
			entry := strings.TrimSpace(strings.TrimPrefix(line, "require"))
			addRequireEntry(entry, requireLines, &requireOrder)
			inRequireBlock = false
			inReplaceBlock = false
			continue

		case strings.HasPrefix(line, "replace "):
			entry := strings.TrimSpace(strings.TrimPrefix(line, "replace"))
			addReplaceEntry(entry, replaceSeen, &replaceOrder)
			inRequireBlock = false
			inReplaceBlock = false
			continue
		}

		if inRequireBlock {
			addRequireEntry(line, requireLines, &requireOrder)
			continue
		}
		if inReplaceBlock {
			addReplaceEntry(line, replaceSeen, &replaceOrder)
			continue
		}
	}

	if modulePath == "" {
		modulePath = fallbackModulePath(workDir)
	}
	if goVersion == "" {
		goVersion = detectDefaultGoVersion(workDir)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\n", modulePath)
	fmt.Fprintf(&b, "go %s\n", goVersion)

	if len(requireOrder) > 0 {
		b.WriteString("\nrequire (\n")
		for _, mod := range requireOrder {
			fmt.Fprintf(&b, "\t%s\n", requireLines[mod])
		}
		b.WriteString(")\n")
	}

	if len(replaceOrder) > 0 {
		b.WriteString("\nreplace (\n")
		for _, line := range replaceOrder {
			fmt.Fprintf(&b, "\t%s\n", line)
		}
		b.WriteString(")\n")
	}

	return b.String()
}

func addRequireEntry(line string, requireLines map[string]string, requireOrder *[]string) {
	comment := ""
	if idx := strings.Index(line, "//"); idx >= 0 {
		rawComment := strings.TrimSpace(line[idx:])
		if strings.Contains(strings.ToLower(rawComment), "indirect") {
			comment = " // indirect"
		}
		line = strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return
	}
	if !isLikelyModulePath(fields[0]) || !isLikelyVersion(fields[1]) {
		return
	}
	if _, exists := requireLines[fields[0]]; !exists {
		*requireOrder = append(*requireOrder, fields[0])
	}
	requireLines[fields[0]] = fields[0] + " " + fields[1] + comment
}

func addReplaceEntry(line string, replaceSeen map[string]bool, replaceOrder *[]string) {
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	parts := strings.SplitN(line, "=>", 2)
	if len(parts) != 2 {
		return
	}
	left := strings.Fields(strings.TrimSpace(parts[0]))
	right := strings.Fields(strings.TrimSpace(parts[1]))

	if !(len(left) == 1 || len(left) == 2) {
		return
	}
	if !(len(right) == 1 || len(right) == 2) {
		return
	}
	if !isLikelyModulePath(left[0]) {
		return
	}
	if len(left) == 2 && !isLikelyVersion(left[1]) {
		return
	}
	if len(right) == 2 && !isLikelyVersion(right[1]) {
		return
	}

	normalized := strings.Join(left, " ") + " => " + strings.Join(right, " ")
	if replaceSeen[normalized] {
		return
	}
	replaceSeen[normalized] = true
	*replaceOrder = append(*replaceOrder, normalized)
}

func isLikelyModulePath(s string) bool {
	if s == "" {
		return false
	}
	rx := regexp.MustCompile(`^[A-Za-z0-9._~\-/]+$`)
	return rx.MatchString(s)
}

func isLikelyVersion(s string) bool {
	if s == "" {
		return false
	}
	return strings.HasPrefix(s, "v")
}

func normalizeGoVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "go")
	rx := regexp.MustCompile(`^\d+\.\d+(?:\.\d+)?$`)
	if rx.MatchString(v) {
		return v
	}
	return ""
}

func detectDefaultGoVersion(workDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, stderr, exitCode := execCommand(ctx, workDir, "go", "env", "GOVERSION")
	if exitCode == 0 {
		combined := strings.TrimSpace(stdout + stderr)
		rx := regexp.MustCompile(`go(\d+\.\d+(?:\.\d+)?)`)
		if m := rx.FindStringSubmatch(combined); len(m) == 2 {
			return m[1]
		}
	}
	return "1.22.0"
}

func fallbackModulePath(workDir string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(workDir)))
	rx := regexp.MustCompile(`[^a-z0-9._-]+`)
	base = rx.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		base = "app"
	}
	return "example.com/" + base
}

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
//
// agentTrimSeparator removes a trailing === separator that models sometimes
// append at the end of the last file block (leaking the multi-file format).
func agentTrimSeparator(s string) string {
	t := strings.TrimRight(s, " \t\r\n")
	if strings.HasSuffix(t, "===") {
		t = strings.TrimRight(t[:len(t)-3], " \t\r\n")
	}
	return t
}

func agentStripFences(s string) string {
	s = strings.TrimSpace(s)

	fenceIdx := strings.Index(s, "```")
	if fenceIdx < 0 {
		// No fences at all — treat the whole response as file content.
		return agentTrimSeparator(s)
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
		return agentTrimSeparator(strings.TrimSpace(s[blockStart:]))
	}

	return agentTrimSeparator(strings.TrimSpace(s[blockStart : blockStart+closeIdx]))
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

// ── Run & Fix ─────────────────────────────────────────────────────────────────

const maxFixAttempts = 3

// FixStreamRequest is the JSON body for POST /api/agent/fixstream.
type FixStreamRequest struct {
	Task string `json:"task"`
}

// handleFixStream runs the project, captures errors, asks the LLM to fix them,
// and iterates up to maxFixAttempts times. Progress is streamed as SSE events.
func (s *Server) handleFixStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	var req FixStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": "invalid request body"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	task := strings.TrimSpace(req.Task)

	// Store user message so it appears in the chat history.
	userLabel := "🔧 Run & Fix"
	if task != "" {
		userLabel = "🔧 Run & Fix: " + task
	}
	s.mu.Lock()
	s.messages = append(s.messages, Message{Role: "user", Content: userLabel, Timestamp: time.Now()})
	s.mu.Unlock()

	var sseMu sync.Mutex
	var logLines []string
	progress := func(text string) {
		sseMu.Lock()
		defer sseMu.Unlock()
		logLines = append(logLines, text)
		sseWrite(w, flusher, map[string]any{"type": "status", "text": text})
	}

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

	fixStart := time.Now()
	summary, err := s.runFixLoop(taskCtx, task, progress)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		sseWrite(w, flusher, map[string]any{"type": "done", "success": false, "error": err.Error()})
		return
	}

	if len(logLines) > 0 {
		summary += "\n\n**Attempt log:**\n```\n" + strings.Join(logLines, "\n") + "\n```"
	}
	msg := Message{Role: "assistant", Content: summary, Timestamp: time.Now(), DurationMs: time.Since(fixStart).Milliseconds()}
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	sseWrite(w, flusher, map[string]any{"type": "done", "success": true, "message": msg})
}

// prevFixAttempt records what the LLM wrote and what error followed, so subsequent
// attempts can see exactly what was tried and why it did not help.
type prevFixAttempt struct {
	attempt     int
	errorOutput string
	patches     []fixPatch
	applied     bool
}

// runFixLoop detects the project's run/build command, executes it, and if it
// fails sends the error to the LLM for a fix. Iterates up to maxFixAttempts.
func (s *Server) runFixLoop(ctx context.Context, task string, progress func(string)) (string, error) {
	files := s.getActiveFilesSnapshot()

	runCmd, runArgs, detectErr := s.detectRunCommand(files)
	if detectErr != nil {
		return fmt.Sprintf("❌ **Run & Fix**: %v\n\nMake sure the project has a recognisable entry point (go.mod, package.json, main.py, Makefile, etc.).", detectErr), nil
	}

	cmdLabel := runCmd + " " + strings.Join(runArgs, " ")
	progress(fmt.Sprintf("🔍 Detected runner: `%s`", cmdLabel))

	var prevErrSnippet string
	var history []prevFixAttempt
	autoManifestRepairTried := map[string]bool{}

	for attempt := 1; attempt <= maxFixAttempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		progress(fmt.Sprintf("▶️  Attempt %d/%d — running `%s`…", attempt, maxFixAttempts, cmdLabel))

		buildCtx, buildCancel := context.WithTimeout(ctx, 2*time.Minute)
		stdout, stderr, exitCode := execCommand(buildCtx, s.directory, runCmd, runArgs...)
		buildCancel()

		combined := strings.TrimSpace(stdout + "\n" + stderr)

		if exitCode == 0 {
			progress("✅ No errors — build/run succeeded!")
			snippet := combined
			if len(snippet) > 2000 {
				snippet = snippet[:2000] + "\n…[truncated]"
			}
			result := fmt.Sprintf("✅ **Run & Fix**: succeeded after %d attempt(s).", attempt)
			if snippet != "" {
				result += "\n\n```\n" + snippet + "\n```"
			}
			return result, nil
		}

		// Truncate error output sent to user/LLM.
		errSnippet := combined
		if len(errSnippet) > 3000 {
			errSnippet = errSnippet[:3000] + "\n…[truncated]"
		}

		// Show the error clearly.
		progress(fmt.Sprintf("❌ Error:\n%s", errSnippet))

		sameError := errSnippet == prevErrSnippet && prevErrSnippet != ""
		if sameError {
			progress("⚠️  Same error as previous attempt.")
		}
		prevErrSnippet = errSnippet

		if manifestFile, shouldRepair := detectDeterministicManifestRepairTarget(errSnippet); shouldRepair && !autoManifestRepairTried[manifestFile] {
			autoManifestRepairTried[manifestFile] = true
			repaired, repairMsg := attemptDeterministicManifestRepair(s.directory, manifestFile)
			if repairMsg != "" {
				progress("🛠️  " + repairMsg)
			}
			if repaired {
				s.markWrittenFile(manifestFile)
				s.mu.Lock()
				s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "modified", File: manifestFile, Task: "(auto-fix deterministic)"})
				s.mu.Unlock()
				if scanned, scanErr := s.performRescan(); scanErr == nil {
					s.mu.Lock()
					s.scanResult = scanned
					s.mu.Unlock()
				}
				progress(fmt.Sprintf("♻️  Retrying build after deterministic %s repair…", manifestFile))
				continue
			}
		}

		if attempt == maxFixAttempts {
			return fmt.Sprintf("❌ **Run & Fix**: still failing after %d attempt(s).\n\nLast error:\n```\n%s\n```", maxFixAttempts, errSnippet), nil
		}

		progress(fmt.Sprintf("🤖 Asking LLM to propose a fix (attempt %d/%d)…", attempt, maxFixAttempts))

		patches, llmErr := s.runFixLLM(ctx, task, errSnippet, attempt, sameError, history)
		if llmErr != nil {
			if errors.Is(llmErr, context.Canceled) {
				return "", llmErr
			}
			// Non-fatal: surface the message so the user can see the raw LLM output,
			// then continue to the next attempt.
			progress(fmt.Sprintf("⚠️  %v", llmErr))
			history = append(history, prevFixAttempt{attempt: attempt, errorOutput: errSnippet, applied: false})
			continue
		}

		if len(patches) == 0 {
			if sameError {
				return fmt.Sprintf("❌ **Run & Fix**: same error persists and LLM could not find a fix.\n\nError:\n```\n%s\n```", errSnippet), nil
			}
			progress(fmt.Sprintf("⚠️  LLM found no changes to make on attempt %d/%d — retrying…", attempt, maxFixAttempts))
			continue
		}

		// Report and apply patches.
		var patchedNames []string
		for _, p := range patches {
			if p.name != "" {
				patchedNames = append(patchedNames, p.name)
			}
		}
		progress(fmt.Sprintf("📝 Proposed fix: %s", strings.Join(patchedNames, ", ")))

		var appliedPatches []fixPatch
		var appliedNames []string

		for _, p := range patches {
			if p.name == "" {
				continue
			}
			warnings, applyErr := applyFixPatch(s.directory, p)
			for _, w := range warnings {
				progress("⚠️  " + w)
			}
			if applyErr != nil {
				progress(fmt.Sprintf("⚠️  Could not apply patch to %s: %v", p.name, applyErr))
				continue
			}
			appliedPatches = append(appliedPatches, p)
			appliedNames = append(appliedNames, p.name)
			s.markWrittenFile(p.name)
			s.mu.Lock()
			s.agentLog = append(s.agentLog, AgentLogEntry{Operation: "modified", File: p.name, Task: "(auto-fix)"})
			s.mu.Unlock()
		}
		if len(appliedNames) == 0 {
			progress("⚠️  No proposed changes were applied (all patches were rejected or invalid).")
		} else {
			progress(fmt.Sprintf("✏️  Applied fix to: %s", strings.Join(appliedNames, ", ")))
		}

		recordedPatches := appliedPatches
		wereApplied := len(appliedPatches) > 0
		if !wereApplied {
			// Preserve rejected/no-op proposals so the next prompt can avoid repeating them.
			recordedPatches = patches
		}

		// Record this attempt so the next iteration can see what was tried.
		history = append(history, prevFixAttempt{
			attempt:     attempt,
			errorOutput: errSnippet,
			patches:     recordedPatches,
			applied:     wereApplied,
		})

		// Rescan so the next iteration sees up-to-date file contents.
		if scanned, scanErr := s.performRescan(); scanErr == nil {
			s.mu.Lock()
			s.scanResult = scanned
			s.mu.Unlock()
		}
	}

	return "❌ Max fix attempts reached.", nil
}

// runFixLLM calls the LLM with focused context (imports + error locations) and
// error output. It expects SEARCH/REPLACE blocks back, parsed as []fixPatch.
func (s *Server) runFixLLM(ctx context.Context, task string, errorOutput string, attempt int, sameError bool, history []prevFixAttempt) ([]fixPatch, error) {
	var prompt strings.Builder
	if task != "" {
		fmt.Fprintf(&prompt, "Original task: %s\n\n", task)
	}

	// Show history of previous failed attempts.
	if len(history) > 0 {
		prompt.WriteString("=== PREVIOUS FAILED ATTEMPTS ===\n")
		for _, h := range history {
			fmt.Fprintf(&prompt, "--- Attempt %d error ---\n```\n%s\n```\n", h.attempt, h.errorOutput)
			if len(h.patches) > 0 {
				if h.applied {
					prompt.WriteString("Files written (which did NOT fix the error):\n")
				} else {
					prompt.WriteString("Files proposed in previous attempt but rejected/no-op (did NOT fix the error):\n")
				}
				for _, p := range h.patches {
					for _, hunk := range p.hunks {
						// Reconstruct as FILE/---/=== to match the output format.
						fmt.Fprintf(&prompt, "FILE: %s\n---\n%s\n===\n", p.name, hunk.replace)
					}
				}
			} else {
				prompt.WriteString("(no files produced)\n")
			}
			prompt.WriteString("\n")
		}
		if sameError {
			fmt.Fprintf(&prompt, "CRITICAL: Attempt %d produced the SAME error. The files above had NO effect. Take a completely different approach.\n\n", attempt)
		} else {
			prompt.WriteString("IMPORTANT: The files above did not fix the error. Take a structurally different approach.\n\n")
		}
		prompt.WriteString("=== END OF PREVIOUS ATTEMPTS ===\n\n")
	}

	// Keep context tight: only send files directly referenced by the error.
	relatedFiles := errorReferencedFiles(s.directory, errorOutput)
	if len(relatedFiles) > 0 {
		prompt.WriteString("Relevant source files referenced by the error:\n\n")
		for _, relPath := range relatedFiles {
			abs := filepath.Clean(filepath.Join(s.directory, relPath))
			data, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			fmt.Fprintf(&prompt, "FILE: %s\n```\n%s\n```\n\n", relPath, string(data))
		}
	}

	if guidance := buildManifestErrorGuidance(errorOutput, relatedFiles); guidance != "" {
		prompt.WriteString(guidance)
		prompt.WriteString("\n")
	}

	// Append a focused error-location summary.
	if fixCtx := buildFixContext(s.directory, errorOutput); fixCtx != "" {
		prompt.WriteString("Error locations (line numbers shown for reference only — do not include them in file output):\n\n")
		prompt.WriteString(fixCtx)
	}

	fmt.Fprintf(&prompt, "Error output:\n```\n%s\n```\n\nFix the error(s) above. Return only the files that need to change.", errorOutput)

	chatReq := &llm.ChatRequest{
		Model: s.cfg.LLM.Model,
		Messages: []llm.Message{
			{Role: "system", Content: promptAgentFix},
			{Role: "user", Content: prompt.String()},
		},
		Temperature: fixTemperature(s.cfg.LLM.Temperature, attempt, sameError),
	}

	resp, err := s.llmClient.Chat(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("LLM fix request failed: %w", err)
	}

	// Primary: FILE/---/=== full-file format (matches the system prompt).
	if blocks := parseAgentCreateResponse(resp.Message.Content); len(blocks) > 0 {
		var patches []fixPatch
		for _, b := range blocks {
			if b.name != "" {
				patches = append(patches, fixPatch{
					name:  b.name,
					hunks: []fixHunk{{search: "", replace: b.content}},
				})
			}
		}
		return patches, nil
	}

	// Fallback: SEARCH/REPLACE format.
	if patches := parseFixPatchResponse(resp.Message.Content); len(patches) > 0 {
		return patches, nil
	}

	// Neither format matched — surface the raw response so the progress log
	// shows what the model actually said, making failures diagnosable.
	if strings.TrimSpace(resp.Message.Content) != "" {
		return nil, fmt.Errorf("LLM response did not contain any fix blocks.\nRaw response:\n%s", resp.Message.Content)
	}
	return nil, nil
}

// detectRunCommand inspects the project files and returns the appropriate
// build/run command. Returns an error when no recognisable entry point is found.
func (s *Server) detectRunCommand(files []*types.FileInfo) (cmd string, args []string, err error) {
	// Check the workspace directory directly for manifest files so that
	// filtered-out files (e.g. vendor/) don't confuse detection.
	check := func(relPath string) bool {
		_, statErr := os.Stat(filepath.Join(s.directory, relPath))
		return statErr == nil
	}

	// Go module
	if check("go.mod") {
		return "go", []string{"build", "./..."}, nil
	}
	// Rust
	if check("Cargo.toml") {
		return "cargo", []string{"build"}, nil
	}
	// Node.js / TypeScript — prefer npm run build, fall back to npm install
	if check("package.json") {
		if check("tsconfig.json") {
			return "npx", []string{"tsc", "--noEmit"}, nil
		}
		return "npm", []string{"run", "build", "--if-present"}, nil
	}
	// Python: look for a clear entry-point file
	for _, candidate := range []string{"main.py", "app.py", "run.py", "server.py"} {
		if check(candidate) {
			return "python3", []string{"-m", "py_compile", candidate}, nil
		}
	}
	// Java (Maven or Gradle)
	if check("pom.xml") {
		return "mvn", []string{"compile", "-q"}, nil
	}
	if check("build.gradle") || check("build.gradle.kts") {
		return "gradle", []string{"build"}, nil
	}
	// Makefile
	if check("Makefile") || check("makefile") {
		return "make", nil, nil
	}
	// Shell scripts — any .sh in the root
	for _, f := range files {
		if f != nil && filepath.Dir(f.RelPath) == "." && filepath.Ext(f.RelPath) == ".sh" {
			return "bash", []string{"-n", f.RelPath}, nil
		}
	}
	// Generic Python fallback: any .py file
	for _, f := range files {
		if f != nil && filepath.Ext(f.RelPath) == ".py" {
			return "python3", []string{"-m", "py_compile", f.RelPath}, nil
		}
	}

	return "", nil, fmt.Errorf("no recognisable entry point found (go.mod, Cargo.toml, package.json, main.py, Makefile, etc.)")
}

// execCommand runs name with args inside dir, captures combined stdout+stderr,
// and returns the exit code. A non-zero code always means failure.
func execCommand(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, exitCode int) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if runErr := cmd.Run(); runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
		return outBuf.String(), errBuf.String() + "\n" + runErr.Error(), 1
	}
	return outBuf.String(), errBuf.String(), 0
}
