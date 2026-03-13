package webui

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>LAonSteroids</title>
    <link rel="icon" type="image/png" href="/static/favicon.png" sizes="150x150">
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github-dark-dimmed.min.css">
    <style>
        :root {
            --bg:       #1a1b26;
            --bg2:      #24283b;
            --bg3:      #1f2335;
            --border:   #292e42;
            --text:     #c0caf5;
            --text2:    #787c99;
            --accent:   #7aa2f7;
            --accent2:  #bb9af7;
            --green:    #9ece6a;
            --red:      #f7768e;
            --yellow:   #e0af68;
        }
        body.light {
            --bg:       #f5f6fa;
            --bg2:      #ffffff;
            --bg3:      #eef0f4;
            --border:   #d0d4e8;
            --text:     #343a6e;
            --text2:    #666c8f;
            --accent:   #2563eb;
            --accent2:  #7c3aed;
            --green:    #16a34a;
            --red:      #dc2626;
            --yellow:   #ca8a04;
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        html, body { height: 100%; overflow: hidden; }
        body { font-family: system-ui, -apple-system, sans-serif; background: var(--bg); color: var(--text); display: flex; flex-direction: column; }

        /* ── Header ─────────────────────────────────────────────────── */
        .app-header {
            height: 52px; flex-shrink: 0;
            background: var(--bg2); border-bottom: 1px solid var(--border);
            display: flex; align-items: center; gap: 0.75rem; padding: 0 1rem;
        }
        .logo { font-weight: 700; color: var(--accent); white-space: nowrap; }
        .header-badges { flex: 1; display: flex; gap: 0.4rem; overflow: hidden; flex-wrap: nowrap; }
        .badge {
            font-size: 0.73rem; color: var(--text2);
            background: var(--bg3); border: 1px solid var(--border);
            border-radius: 4px; padding: 0.2rem 0.5rem;
            white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 240px;
        }
        .badge.focus { color: var(--yellow); border-color: var(--yellow); }

        /* ── Workspace ───────────────────────────────────────────────── */
        .workspace { flex: 1; min-height: 0; display: flex; overflow: hidden; }

        /* ── Sidebar ─────────────────────────────────────────────────── */
        .sidebar {
            width: 252px; min-width: 252px; flex-shrink: 0;
            background: var(--bg2); border-right: 1px solid var(--border);
            display: flex; flex-direction: column; transition: width 0.2s, min-width 0.2s; overflow: hidden;
        }
        .sidebar.collapsed { width: 44px; min-width: 44px; }
        .sidebar-hdr {
            padding: 0.5rem 0.5rem 0.5rem 0.75rem; display: flex; align-items: center;
            justify-content: space-between; border-bottom: 1px solid var(--border);
            font-size: 0.78rem; font-weight: 600; color: var(--text2); flex-shrink: 0;
            text-transform: uppercase; letter-spacing: 0.04em; overflow: hidden;
        }
        .sidebar.collapsed .sidebar-hdr { justify-content: center; padding: 0.5rem 0.5rem 0.5rem 0.5rem; border-bottom: none; }
        .sidebar-hdr-left { display: flex; align-items: center; gap: 0.4rem; overflow: hidden; }
        .sidebar.collapsed .sidebar-hdr-left { display: none; }
        .file-search {
            padding: 0.45rem 0.75rem; background: var(--bg3); border: none;
            border-bottom: 1px solid var(--border); color: var(--text);
            font-size: 0.8rem; width: 100%; outline: none; flex-shrink: 0;
        }
        .file-search::placeholder { color: var(--text2); }
        .sidebar.collapsed .file-search,
        .sidebar.collapsed .file-tree { display: none; }
        .file-tree { flex: 1; overflow-y: auto; padding: 0.35rem 0; font-size: 0.8rem; }
        .tree-dir details > summary {
            display: flex; align-items: center; gap: 0.35rem;
            padding: 0.28rem 0.75rem; cursor: pointer;
            color: var(--text2); font-weight: 600; list-style: none; user-select: none;
        }
        .tree-dir details > summary:hover { background: var(--bg3); }
        .tree-dir details > summary::before { content: "▶"; font-size: 0.6rem; transition: transform 0.15s; }
        .tree-dir details[open] > summary::before { transform: rotate(90deg); }
        .tree-children { padding-left: 0.9rem; }
        .tree-file {
            display: flex; align-items: center; gap: 0.35rem;
            padding: 0.28rem 0.75rem; cursor: pointer; color: var(--text);
            border-left: 2px solid transparent; overflow: hidden;
            white-space: nowrap; text-overflow: ellipsis;
        }
        .tree-file:hover { background: var(--bg3); border-left-color: var(--border); }
        .tree-file.active { background: var(--bg3); border-left-color: var(--accent); color: var(--accent); }
        .tree-file .fname { overflow: hidden; text-overflow: ellipsis; min-width: 0; }

        /* ── Content ─────────────────────────────────────────────────── */
        .content { flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: hidden; min-width: 0; }
        .tab-bar {
            display: flex; align-items: flex-end; gap: 2px;
            background: var(--bg2); border-bottom: 1px solid var(--border);
            padding: 0 1rem; flex-shrink: 0; overflow-x: auto;
        }
        .tab {
            padding: 0.55rem 0.9rem; font-size: 0.82rem; background: none; border: none;
            border-bottom: 2px solid transparent; color: var(--text2); cursor: pointer;
            white-space: nowrap; display: flex; align-items: center; gap: 0.3rem;
            transition: color 0.1s, border-color 0.1s;
        }
        .tab:hover { color: var(--text); }
        .tab.active { color: var(--accent); border-bottom-color: var(--accent); }
        .tab.modified::after { content: '●'; font-size: 0.6rem; color: var(--yellow,#e0af68); margin-left: 0.25rem; vertical-align: middle; }
        .tab-x { font-size: 0.7rem; opacity: 0.5; margin-left: 0.2rem; }
        .tab-x:hover { opacity: 1; color: var(--red); }
        .panel { flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: hidden; }
        .panel.hidden { display: none !important; }

        /* ── Chat panel ──────────────────────────────────────────────── */
        .messages { flex: 1; overflow-y: auto; padding: 1.25rem; display: flex; flex-direction: column; gap: 0.9rem; }
        .msg { display: flex; flex-direction: column; max-width: 92%; }
        .msg.user { align-self: flex-end; align-items: flex-end; }
        .msg.assistant { align-self: flex-start; align-items: flex-start; }
        .msg-meta { font-size: 0.72rem; color: var(--text2); margin-bottom: 0.25rem; }
        .bubble {
            padding: 0.75rem 1rem; border-radius: 10px; line-height: 1.65;
            max-width: 100%; word-break: break-word; overflow-wrap: anywhere;
        }
        .msg.user .bubble { background: var(--accent); color: #fff; border-bottom-right-radius: 3px; }
        .msg.assistant .bubble { background: var(--bg2); border: 1px solid var(--border); border-bottom-left-radius: 3px; }
        /* Markdown inside bubbles */
        .bubble h1 { font-size: 1.1rem; color: var(--accent); margin: 0.6rem 0 0.3rem; }
        .bubble h2 { font-size: 0.97rem; color: var(--accent); margin: 0.5rem 0 0.25rem; }
        .bubble h3 { font-size: 0.88rem; color: var(--accent2); margin: 0.4rem 0 0.2rem; }
        .bubble p { margin: 0.3rem 0; }
        .bubble p:first-child { margin-top: 0; }
        .bubble p:last-child { margin-bottom: 0; }
        .bubble ul, .bubble ol { padding-left: 1.4rem; margin: 0.35rem 0; }
        .bubble li { margin: 0.15rem 0; }
        .bubble strong { font-weight: 600; }
        .bubble em { font-style: italic; opacity: 0.9; }
        .bubble hr { border-color: var(--border); margin: 0.6rem 0; }
        .bubble .ic { background: var(--bg3); padding: 0.1rem 0.35rem; border-radius: 3px; font-family: monospace; font-size: 0.84em; color: var(--accent2); }
        /* Code blocks */
        .cb { margin: 0.65rem 0; border: 1px solid var(--border); border-radius: 7px; overflow: hidden; background: var(--bg); }
        .cb-hdr { display: flex; align-items: center; gap: 0.45rem; padding: 0.4rem 0.7rem; background: var(--bg3); border-bottom: 1px solid var(--border); font-size: 0.77rem; }
        .cb-lang { color: var(--accent); font-family: monospace; font-weight: 600; }
        .cb-file { color: var(--green); font-family: monospace; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .cb-actions { display: flex; gap: 0.4rem; margin-left: auto; }
        .btn-apply { padding: 0.18rem 0.55rem; background: var(--green); color: #1a1b26; border: none; border-radius: 3px; cursor: pointer; font-size: 0.74rem; font-weight: 700; }
        .btn-apply:hover { opacity: 0.85; }
        .btn-deny  { padding: 0.18rem 0.55rem; background: var(--bg3); color: var(--text2); border: 1px solid var(--border); border-radius: 3px; cursor: pointer; font-size: 0.74rem; font-weight: 700; }
        .btn-deny:hover  { border-color: var(--red); color: var(--red); }
        .btn-copy { padding: 0.18rem 0.55rem; background: var(--bg2); color: var(--text2); border: 1px solid var(--border); border-radius: 3px; cursor: pointer; font-size: 0.74rem; }
        .btn-copy:hover { color: var(--text); border-color: var(--accent); }
        .cb pre { margin: 0; padding: 0.8rem 1rem; overflow-x: auto; font-size: 0.82rem; line-height: 1.55; }
        .cb code { font-family: "Cascadia Code", "JetBrains Mono", "Fira Code", monospace; }
        /* Input area */
        .input-area { padding: 0.85rem 1.25rem; background: var(--bg2); border-top: 1px solid var(--border); flex-shrink: 0; }
        .input-row { display: flex; gap: 0.65rem; align-items: flex-end; }
        .msg-input {
            flex: 1; padding: 0.65rem 0.9rem; background: var(--bg3); border: 1px solid var(--border);
            border-radius: 8px; color: var(--text); font-size: 0.88rem; font-family: inherit;
            resize: none; outline: none; transition: border-color 0.2s; min-height: 40px; max-height: 110px;
        }
        .msg-input:focus { border-color: var(--accent); }
        .send-btn {
            padding: 0.65rem 1.35rem; background: var(--accent); color: #fff;
            border: none; border-radius: 8px; cursor: pointer; font-weight: 600;
            font-size: 0.88rem; height: 40px; transition: opacity 0.15s;
        }
        .send-btn:hover:not(:disabled) { opacity: 0.85; }
        .send-btn:disabled { opacity: 0.4; cursor: not-allowed; }
        .stop-btn {
            padding: 0.65rem 1.1rem; background: var(--red); color: #fff;
            border: none; border-radius: 8px; cursor: pointer; font-weight: 600;
            font-size: 0.88rem; height: 40px; transition: opacity 0.15s; white-space: nowrap;
        }
        .stop-btn:hover { opacity: 0.85; }
        .btn-agent {
            padding: 0.65rem 1.1rem; background: var(--accent2); color: #fff;
            border: none; border-radius: 8px; cursor: pointer; font-weight: 600;
            font-size: 0.88rem; height: 40px; transition: opacity 0.15s; white-space: nowrap;
        }
        .btn-agent:hover:not(:disabled) { opacity: 0.85; }
        .btn-agent:disabled { opacity: 0.4; cursor: not-allowed; }
        .btn-help {
            padding: 0.65rem 1.1rem; background: var(--green); color: #fff;
            border: none; border-radius: 8px; cursor: pointer; font-weight: 600;
            font-size: 0.88rem; height: 40px; transition: opacity 0.15s; white-space: nowrap;
        }
        .btn-help:hover { opacity: 0.85; }
        .input-hint { font-size: 0.73rem; color: var(--text2); margin-top: 0.35rem; }
        .input-hint code { background: var(--bg3); padding: 0.1rem 0.3rem; border-radius: 3px; color: var(--accent); font-size: 0.9em; }
        /* Typing indicator */
        .typing { display: flex; align-items: center; gap: 0.35rem; color: var(--text2); font-size: 0.83rem; }
        .dot { width: 5px; height: 5px; background: var(--accent2); border-radius: 50%; animation: blink 1.2s infinite; }
        .dot:nth-child(2) { animation-delay: 0.2s; }
        .dot:nth-child(3) { animation-delay: 0.4s; }
        @keyframes blink { 0%,60%,100%{opacity:1} 30%{opacity:0.2} }

        /* ── Viewer panel ────────────────────────────────────────────── */
        .viewer-toolbar {
            display: flex; align-items: center; gap: 0.75rem; padding: 0.45rem 1rem;
            background: var(--bg2); border-bottom: 1px solid var(--border); flex-shrink: 0;
        }
        .viewer-path { font-family: monospace; font-size: 0.82rem; color: var(--text2); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .viewer-btns { display: flex; gap: 0.45rem; }
        .viewer-body { flex: 1; overflow: hidden; display: flex; flex-direction: column; }
        #codeView { flex: 1; min-height: 0; margin: 0; padding: 1rem 0; font-size: 0.83rem; line-height: 1.6; background: var(--bg); overflow: auto; }
        .code-line { display: block; padding: 0 1.5rem; white-space: pre; }
        .code-line.line-changed { background: rgba(158,206,106,0.10); border-left: 3px solid var(--green); padding-left: calc(1.5rem - 3px); }
        .code-line.line-added   { background: rgba(158,206,106,0.15); border-left: 3px solid var(--green); padding-left: calc(1.5rem - 3px); }
        .code-editor {
            flex: 1; min-height: 0; padding: 1rem 1.5rem; font-family: "Cascadia Code", "JetBrains Mono", "Fira Code", monospace;
            font-size: 0.83rem; line-height: 1.6; background: var(--bg); color: var(--text);
            border: none; outline: none; resize: none; width: 100%;
        }

        /* ── Apply modal ─────────────────────────────────────────────── */
        .modal-bg {
            position: fixed; inset: 0; background: rgba(0,0,0,0.6);
            display: flex; align-items: center; justify-content: center;
            z-index: 999; backdrop-filter: blur(3px);
        }
        .modal-bg.hidden { display: none !important; }
        .modal-box {
            background: var(--bg2); border: 1px solid var(--border); border-radius: 10px;
            width: 92%; max-width: 820px; max-height: 80vh;
            display: flex; flex-direction: column; overflow: hidden;
        }
        .modal-hdr { display: flex; align-items: center; justify-content: space-between; padding: 0.85rem 1rem; border-bottom: 1px solid var(--border); }
        .modal-hdr h3 { font-size: 0.95rem; }
        .modal-hdr h3 code { color: var(--accent); background: var(--bg3); padding: 0.1rem 0.35rem; border-radius: 3px; }
        .modal-body { flex: 1; overflow: auto; padding: 0.85rem; }
        .diff-pre { font-family: monospace; font-size: 0.78rem; line-height: 1.5; white-space: pre; background: var(--bg); padding: 0.75rem; border-radius: 5px; overflow: auto; }
        .da { color: var(--green); background: rgba(158,206,106,0.08); display: block; }
        .dr { color: var(--red);   background: rgba(247,118,142,0.08); display: block; }
        .dc { color: var(--text2); display: block; }
        /* ── Inline diff card ───────────────────────────────────────── */
        .inline-diff { border: 1px solid var(--border); border-radius: 6px; overflow: hidden; margin-top: 0.3rem; }
        .inline-diff-hdr { display: flex; align-items: center; justify-content: space-between;
            padding: 0.4rem 0.7rem; background: var(--bg3); cursor: pointer; user-select: none; }
        .inline-diff-hdr:hover { background: var(--bg2); }
        .inline-diff-title { font-size: 0.8rem; color: var(--text); }
        .inline-diff-toggle { font-size: 0.75rem; color: var(--accent); }
        .inline-diff-body { margin: 0; border-radius: 0; border: none; max-height: 420px; overflow: auto; }
        .modal-ftr { display: flex; gap: 0.65rem; justify-content: flex-end; padding: 0.85rem 1rem; border-top: 1px solid var(--border); }
        .btn-green { padding: 0.55rem 1.1rem; background: var(--green); color: #1a1b26; font-weight: 700; border: none; border-radius: 6px; cursor: pointer; font-size: 0.85rem; }
        .btn-green:hover { opacity: 0.85; }
        .btn-grey  { padding: 0.55rem 1.1rem; background: var(--bg3); color: var(--text2); border: 1px solid var(--border); border-radius: 6px; cursor: pointer; font-size: 0.85rem; }
        .btn-grey:hover { border-color: var(--red); color: var(--red); }

        /* ── Shared button ───────────────────────────────────────────── */
        .ibtn {
            padding: 0.28rem 0.6rem; background: none; border: 1px solid var(--border);
            border-radius: 4px; color: var(--text2); cursor: pointer; font-size: 0.78rem;
            white-space: nowrap; transition: all 0.12s;
        }
        .ibtn:hover { background: var(--bg3); color: var(--text); border-color: var(--accent); }
        .ibtn.green:hover { border-color: var(--green); color: var(--green); }
        .ibtn.red:hover   { border-color: var(--red);   color: var(--red);   }
        .ibtn.hidden { display: none !important; }
        .hidden { display: none !important; }

        /* ── Misc ────────────────────────────────────────────────────── */
        .muted { color: var(--text2); font-size: 0.82rem; padding: 0.75rem; text-align: center; }
        ::-webkit-scrollbar { width: 5px; height: 5px; }
        ::-webkit-scrollbar-track { background: transparent; }
        ::-webkit-scrollbar-thumb { background: var(--border); border-radius: 3px; }
        ::-webkit-scrollbar-thumb:hover { background: var(--text2); }
    </style>
</head>
<body>

    <!-- ── Header ───────────────────────────────────────────────── -->
    <header class="app-header">
        <img src="/static/favicon.png" alt="" style="width:26px;height:26px;flex-shrink:0;">
        <span class="logo">local-agent-onsteroids</span>
        <div class="header-badges">
            <span class="badge" id="dirBadge">📁 —</span>
            <span class="badge" id="modelBadge">🤖 —</span>
            <span class="badge" id="filesBadge">📄 0 files</span>
            <span class="badge focus" id="focusBadge" style="display:none;">🎯 —</span>
        </div>
        <button class="ibtn" id="themeBtn" title="Toggle theme">🌙</button>
    </header>

    <!-- ── Workspace ────────────────────────────────────────────── -->
    <div class="workspace">

        <!-- Sidebar -->
        <aside class="sidebar" id="sidebar">
            <div class="sidebar-hdr">
                <div class="sidebar-hdr-left">
                    <span>Explorer</span>
                    <button class="ibtn" id="rescanBtn" title="Rescan">🔄</button>
                </div>
                <button class="ibtn" id="sidebarToggle" title="Close sidebar" style="display:flex;align-items:center;justify-content:center;flex-shrink:0;"><svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg"><rect x="1" y="1" width="14" height="14" rx="2" stroke="currentColor" stroke-width="1.4"/><line x1="5" y1="1.7" x2="5" y2="14.3" stroke="currentColor" stroke-width="1.4"/></svg></button>
            </div>
            <input type="search" class="file-search" id="fileSearch" placeholder="Search files…">
            <div class="file-tree" id="fileTree"><span class="muted">Loading…</span></div>
        </aside>

        <!-- Main content -->
        <div class="content">
            <div class="tab-bar" id="tabBar">
                <button class="tab active" id="tabChat" onclick="switchTab('chat')">💬 Chat</button>
            </div>

            <!-- Chat panel -->
            <div class="panel" id="chatPanel">
                <div class="messages" id="messages"></div>
                <div class="input-area">
                    <div class="input-row">
                        <textarea class="msg-input" id="msgInput" rows="2" placeholder="Describe a task and press Enter to run Agent, or Shift+Enter for a new line…"></textarea>
                        <button class="btn-agent" id="agentBtn" title="Agent mode: reviews all files then applies changes autonomously">⚡ Agent</button>
                        <button class="send-btn" id="sendBtn">Send</button>
                        <button class="stop-btn hidden" id="stopBtn">⏹ Stop</button>
                        <button class="btn-help" id="helpBtn" onclick="openHelpModal()">Help</button>
                        <button class="btn-help" id="autoApplyBtn" title="Auto-apply is OFF — explicit ⚡ Apply required. Click to enable (use with caution).">🔒 Auto</button>
                    </div>
                    <div class="input-hint">💡 <code>Enter</code> = ⚡ Agent &nbsp;•&nbsp; <code>Shift+Enter</code> = new line &nbsp;•&nbsp; <code>Send</code> = chat only</div>
                </div>
            </div>

            <!-- File viewer panel (hidden by default) -->
            <div class="panel hidden" id="viewerPanel">
                <div class="viewer-toolbar">
                    <span class="viewer-path" id="viewerPath">No file selected</span>
                    <div class="viewer-btns">
                        <button class="ibtn hidden" id="diffBtn" onclick="toggleViewerDiff()">Diff</button>
                        <button class="ibtn" id="editBtn" onclick="enterEditMode()">✏️ Edit</button>
                        <button class="ibtn green hidden" id="saveBtn" onclick="saveFile()">💾 Save</button>
                        <button class="ibtn red  hidden" id="cancelBtn" onclick="cancelEdit()">✕ Cancel</button>
                    </div>
                </div>
                <div class="viewer-body">
                    <pre id="codeView"><code id="codeContent" class="hljs"></code></pre>
                    <textarea id="codeEditor" class="code-editor hidden" spellcheck="false"></textarea>
                </div>
            </div>
        </div>
    </div><!-- /workspace -->

<!-- ── Help modal ───────────────────────────────────────────── -->
<div class="modal-bg hidden" id="helpModal" onclick="if(event.target===this)closeHelpModal()">
    <div class="modal-box" style="max-width:480px">
        <div class="modal-hdr">
            <h3>📚 Available commands</h3>
            <button class="ibtn" onclick="closeHelpModal()">✕</button>
        </div>
        <div class="modal-body">
            <table style="width:100%;border-collapse:collapse;font-size:0.88rem;line-height:1.7">
                <tr><td style="padding:0.2rem 0.6rem 0.2rem 0;white-space:nowrap"><code style="color:var(--accent)">help</code></td><td>Show this help</td></tr>
                <tr><td style="padding:0.2rem 0.6rem 0.2rem 0;white-space:nowrap"><code style="color:var(--accent)">clear</code></td><td>Clear conversation history</td></tr>
                <tr><td style="padding:0.2rem 0.6rem 0.2rem 0;white-space:nowrap"><code style="color:var(--accent)">model &lt;name&gt;</code></td><td>Switch to a different LLM model</td></tr>
                <tr><td style="padding:0.2rem 0.6rem 0.2rem 0;white-space:nowrap"><code style="color:var(--accent)">rescan</code></td><td>Rescan the directory for changes</td></tr>
                <tr><td style="padding:0.2rem 0.6rem 0.2rem 0;white-space:nowrap"><code style="color:var(--accent)">stats</code></td><td>Show current statistics</td></tr>
                <tr><td style="padding:0.2rem 0.6rem 0.2rem 0;white-space:nowrap"><code style="color:var(--accent)">files</code></td><td>List all files in scope</td></tr>
            </table>
            <hr style="border:none;border-top:1px solid var(--border);margin:0.8rem 0">
            <p style="font-size:0.85rem;color:var(--muted);margin:0">💡 <strong>Enter</strong> = ⚡ Agent &nbsp;•&nbsp; <strong>Shift+Enter</strong> = new line &nbsp;•&nbsp; <strong>Send</strong> = chat only</p>
        </div>
        <div class="modal-ftr">
            <button class="btn-grey" onclick="closeHelpModal()">Close</button>
        </div>
    </div>
</div>

<!-- ── Apply modal ──────────────────────────────────────────── -->
<div class="modal-bg hidden" id="applyModal">
    <div class="modal-box">
        <div class="modal-hdr">
            <h3>Apply changes to <code id="applyFileName"></code>?</h3>
            <button class="ibtn" onclick="closeApplyModal()">✕</button>
        </div>
        <div class="modal-body">
            <pre class="diff-pre" id="diffPre"></pre>
        </div>
        <div class="modal-ftr">
            <button class="btn-green" onclick="confirmApply()">✅ Apply Changes</button>
            <button class="btn-grey"  onclick="closeApplyModal()">Cancel</button>
        </div>
    </div>
</div>

<script src="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js"></script>
<script>
// ═══════════════════════════════════════════════════════════════
// State
// ═══════════════════════════════════════════════════════════════
var allFiles       = [];      // FileEntry[] from /api/files
var codeStore      = {};      // blockId -> {lang, file, code}
var currentFile    = null;    // relPath of open file
// autoApply: when false (default) chat code blocks are never
// written automatically — the user must click ⚡ Apply explicitly.
var autoApply      = false;
var agentPendingStore = {};   // cardId -> {file, oldContent, newContent}
var agentBulkGroups   = {};   // barId  -> [cardId, ...]
var origContent    = null;    // content when file was loaded
var pendingApply   = null;    // {file, code} waiting for confirm
var serverProcessing = false; // server reports an in-flight chat/agent task
var serverTaskKind   = '';    // "chat" | "agent"

// ═══════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════
function esc(t) {
    return String(t)
        .replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function uid() {
    return 'b' + Date.now().toString(36) + Math.random().toString(36).slice(2,7);
}

function clearResumeIndicator() {
    var existing = document.getElementById('resume_processing_hint');
    if (existing) existing.remove();
}

function ensureResumeIndicator() {
    if (!serverProcessing || window._activeAbortController) return;
    var c = document.getElementById('messages');
    if (!c || document.getElementById('resume_processing_hint')) return;
    var kind = serverTaskKind === 'agent' ? 'Agent' : 'Request';
    var hint = document.createElement('div');
    hint.className = 'msg assistant';
    hint.id = 'resume_processing_hint';
    hint.innerHTML = '<div class="bubble"><div class="typing"><div class="dot"></div><div class="dot"></div><div class="dot"></div><span>' + kind + ' is still running on the server…</span></div></div>';
    c.appendChild(hint);
    c.scrollTop = c.scrollHeight;
}

function syncBusyUI() {
    var busy = !!window._activeAbortController || serverProcessing;
    var inp = document.getElementById('msgInput');
    var sendBtn = document.getElementById('sendBtn');
    var agentBtn = document.getElementById('agentBtn');
    var stopBtn = document.getElementById('stopBtn');
    if (!inp || !sendBtn || !agentBtn || !stopBtn) return;

    inp.disabled = busy;
    sendBtn.disabled = busy;
    agentBtn.disabled = busy;
    if (busy) {
        stopBtn.classList.remove('hidden');
        if (serverProcessing && !window._activeAbortController) {
            agentBtn.textContent = '⏳';
            ensureResumeIndicator();
        }
    } else {
        stopBtn.classList.add('hidden');
        agentBtn.textContent = '⚡ Agent';
        clearResumeIndicator();
    }
}

// ═══════════════════════════════════════════════════════════════
// Initialisation
// ═══════════════════════════════════════════════════════════════
loadStatus();
loadMessages();
loadFiles();
applyTheme();
loadSettings();

document.getElementById('sendBtn').addEventListener('click', sendMsg);
document.getElementById('agentBtn').addEventListener('click', sendAgentTask);
document.getElementById('stopBtn').addEventListener('click', function() {
    // Cancel server-side LLM request first, then abort the browser fetch.
    fetch('/api/stop', { method: 'POST' }).catch(function(){});
    if (window._activeAbortController) { window._activeAbortController.abort(); }
});
document.getElementById('msgInput').addEventListener('keydown', function(e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendAgentTask(); }
});
document.getElementById('sidebarToggle').addEventListener('click', toggleSidebar);
document.getElementById('rescanBtn').addEventListener('click', rescan);
document.getElementById('themeBtn').addEventListener('click', toggleTheme);
document.getElementById('autoApplyBtn').addEventListener('click', toggleAutoApply);
document.getElementById('fileSearch').addEventListener('input', filterFiles);
document.getElementById('applyModal').addEventListener('click', function(e) {
    if (e.target === this) { closeApplyModal(); }
});

// ═══════════════════════════════════════════════════════════════
// Status & polling
// ═══════════════════════════════════════════════════════════════
function loadStatus() {
    fetch('/api/status').then(function(r){ return r.json(); }).then(function(d) {
        document.getElementById('dirBadge').textContent   = '📁 ' + (d.directory || '—');
        document.getElementById('modelBadge').textContent = '🤖 ' + (d.model || '—');
        document.getElementById('filesBadge').textContent = '📄 ' + (d.totalFiles || 0) + ' files';
        serverProcessing = !!d.processing;
        serverTaskKind = d.taskKind || '';
        var fb = document.getElementById('focusBadge');
        if (d.focusedPath) { fb.textContent = '🎯 ' + d.focusedPath; fb.style.display = ''; }
        else               { fb.style.display = 'none'; }
        syncBusyUI();
    }).catch(function(){});
}
setInterval(loadStatus, 6000);

// Poll messages every 3 s so that replies which finish after a page refresh
// appear automatically without requiring a manual reload.
var _lastMsgCount = 0;
function pollMessages() {
    if (window._activeAbortController) return; // request in flight — live updates handle it
    fetch('/api/messages').then(function(r){ return r.json(); }).then(function(msgs) {
        if (!msgs) return;
        if (msgs.length !== _lastMsgCount) {
            _lastMsgCount = msgs.length;
            var c = document.getElementById('messages');
            c.innerHTML = '';
            msgs.forEach(function(m) {
                c.appendChild(makeMsgEl(m.role, m.content, m.timestamp));
                if (m.agentResults && m.agentResults.length) {
                    var hasPending = m.agentResults.some(function(r){ return r.pending; });
                    if (hasPending) { renderAgentPendingResults(m.agentResults, c); }
                    else            { renderAgentResults(m.agentResults, c); }
                }
            });
            c.scrollTop = c.scrollHeight;
        }
        if (serverProcessing) ensureResumeIndicator();
        else clearResumeIndicator();
    }).catch(function(){});
}
setInterval(pollMessages, 3000);

// ═══════════════════════════════════════════════════════════════
// Markdown renderer
// ═══════════════════════════════════════════════════════════════
function renderMarkdown(text) {
    var codeBlocks  = [];
    var inlineCodes = [];
    var BT = '` + "`" + `';
    var BT3 = BT + BT + BT;

    // 1. Extract fenced code blocks
    var cbRe = new RegExp(BT3 + '(\\w+)?(?::([^\\s' + BT + ']+))?\\n?([\\s\\S]*?)' + BT3, 'g');
    text = text.replace(cbRe, function(_, lang, file, code) {
        var idx = codeBlocks.length;
        codeBlocks.push({ lang: (lang||'').trim(), file: (file||'').trim(), code: code.replace(/\n$/,'') });
        return '\x00CB' + idx + '\x00';
    });

    // 2. Extract inline code
    text = text.replace(/` + "`" + `([^` + "`" + `\n]+)` + "`" + `/g, function(_, c) {
        var idx = inlineCodes.length;
        inlineCodes.push(c);
        return '\x00IC' + idx + '\x00';
    });

    // 3. HTML-escape plain text
    text = esc(text);

    // 4. Block elements
    text = text.replace(/^### (.+)$/gm, '<h3>$1</h3>');
    text = text.replace(/^## (.+)$/gm,  '<h2>$1</h2>');
    text = text.replace(/^# (.+)$/gm,   '<h1>$1</h1>');
    text = text.replace(/^(-{3,}|\*{3,}|_{3,})$/gm, '<hr>');

    // 5. Inline emphasis
    text = text.replace(/\*\*\*(.+?)\*\*\*/gs, '<strong><em>$1</em></strong>');
    text = text.replace(/\*\*(.+?)\*\*/gs,     '<strong>$1</strong>');
    text = text.replace(/\*(.+?)\*/g,           '<em>$1</em>');

    // 6. Lists → wrap consecutive <li>
    text = text.replace(/^[*\-•] (.+)$/gm,     '<li>$1</li>');
    text = text.replace(/^\d+\. (.+)$/gm,       '<li>$1</li>');
    var lines = text.split('\n'), out = '', inList = false;
    for (var i = 0; i < lines.length; i++) {
        var l = lines[i];
        if (l.startsWith('<li>')) {
            if (!inList) { out += '<ul>'; inList = true; }
            out += l;
        } else {
            if (inList) { out += '</ul>'; inList = false; }
            out += l + '\n';
        }
    }
    if (inList) out += '</ul>';
    text = out;

    // 7. Paragraphs
    var paras = text.split('\n\n');
    text = paras.map(function(p) {
        p = p.trim();
        if (!p) return '';
        var isBlock = /^<(h[1-6]|ul|ol|li|hr|pre|div)/.test(p) || p.startsWith('\x00CB');
        return isBlock ? p : '<p>' + p.replace(/\n/g, '<br>') + '</p>';
    }).join('\n');

    // 8. Restore inline code
    text = text.replace(/\x00IC(\d+)\x00/g, function(_, i) {
        return '<code class="ic">' + esc(inlineCodes[+i]) + '</code>';
    });

    // 9. Restore code blocks
    text = text.replace(/\x00CB(\d+)\x00/g, function(_, i) {
        var b = codeBlocks[+i];
        return renderCodeBlock(b.lang, b.file, b.code);
    });

    return text;
}

function renderCodeBlock(lang, file, code) {
    var id = uid();
    codeStore[id] = { lang: lang, file: file, code: code };

    var highlighted = '';
    if (window.hljs) {
        try {
            var l = (lang && hljs.getLanguage(lang)) ? lang : 'plaintext';
            highlighted = hljs.highlight(code, { language: l }).value;
        } catch(e) { highlighted = esc(code); }
    } else { highlighted = esc(code); }

    var h = '<div class="cb"><div class="cb-hdr">';
    if (lang) h += '<span class="cb-lang">' + esc(lang) + '</span>';
    if (file) h += '<span class="cb-file">📄 ' + esc(file) + '</span>';
    h += '<div class="cb-actions">';
    if (file) {
        h += '<button class="btn-apply" onclick="showApplyModal(\'' + id + '\')">⚡ Apply</button>';
    }
    h += '<button class="btn-copy" onclick="copyBlock(\'' + id + '\')">📋 Copy</button>';
    h += '</div></div>';
    h += '<pre><code class="hljs">' + highlighted + '</code></pre></div>';
    return h;
}

// ═══════════════════════════════════════════════════════════════
// Chat
// ═══════════════════════════════════════════════════════════════
function loadMessages() {
    fetch('/api/messages').then(function(r){ return r.json(); }).then(function(msgs) {
        var c = document.getElementById('messages');
        c.innerHTML = '';
        var list = msgs || [];
        _lastMsgCount = list.length;
        list.forEach(function(m) {
            c.appendChild(makeMsgEl(m.role, m.content, m.timestamp));
            if (m.agentResults && m.agentResults.length) {
                var hasPending = m.agentResults.some(function(r){ return r.pending; });
                if (hasPending) {
                    renderAgentPendingResults(m.agentResults, c);
                } else {
                    renderAgentResults(m.agentResults, c);
                }
            }
        });
        if (serverProcessing) ensureResumeIndicator();
        c.scrollTop = c.scrollHeight;
    }).catch(function(){});
}

function makeMsgEl(role, content, ts) {
    var wrap   = document.createElement('div');
    wrap.className = 'msg ' + role;
    var meta   = document.createElement('div');
    meta.className = 'msg-meta';
    meta.textContent = (role === 'user' ? 'You' : '🤖 Assistant') + ' · ' + (ts ? new Date(ts).toLocaleTimeString() : '');
    var bubble = document.createElement('div');
    bubble.className = 'bubble';
    if (role === 'assistant') {
        bubble.innerHTML = renderMarkdown(content);
    } else {
        bubble.textContent = content;
    }
    wrap.appendChild(meta);
    wrap.appendChild(bubble);
    return wrap;
}

function sendMsg() {
    var inp = document.getElementById('msgInput');
    var txt = inp.value.trim();
    if (!txt) return;

    var c = document.getElementById('messages');
    c.appendChild(makeMsgEl('user', txt, new Date().toISOString()));
    c.scrollTop = c.scrollHeight;
    inp.value = '';

    var controller = new AbortController();
    window._activeAbortController = controller;
    serverProcessing = true;
    serverTaskKind = 'chat';
    syncBusyUI();

    var tid = 'typing_' + Date.now();
    var typing = document.createElement('div');
    typing.className = 'msg assistant';
    typing.id = tid;
    typing.innerHTML = '<div class="bubble"><div class="typing"><div class="dot"></div><div class="dot"></div><div class="dot"></div><span>Processing…</span></div></div>';
    c.appendChild(typing);
    c.scrollTop = c.scrollHeight;

    fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: txt }),
        signal: controller.signal
    }).then(function(r){ return r.json(); }).then(function(d) {
        var t = document.getElementById(tid); if (t) t.remove();
        if (d.cleared) {
            document.getElementById('messages').innerHTML = '';
        } else if (d.success && d.message) {
            c.appendChild(makeMsgEl(d.message.role, d.message.content, d.message.timestamp));
            c.scrollTop = c.scrollHeight;
            autoApplyCodeBlocks(d.message.content, c, txt);
        } else {
            c.appendChild(makeMsgEl('assistant', '❌ ' + (d.error || 'Unknown error'), new Date().toISOString()));
            c.scrollTop = c.scrollHeight;
        }
        loadStatus();
    }).catch(function(e) {
        var t = document.getElementById(tid); if (t) t.remove();
        if (e.name === 'AbortError') {
            c.appendChild(makeMsgEl('assistant', '⏹ Request cancelled.', new Date().toISOString()));
        } else {
            c.appendChild(makeMsgEl('assistant', '❌ Network error: ' + e.message, new Date().toISOString()));
        }
        c.scrollTop = c.scrollHeight;
    }).finally(function() {
        window._activeAbortController = null;
        serverProcessing = false;
        serverTaskKind = '';
        syncBusyUI();
        inp.focus();
        loadStatus();
    });
}

// ═══════════════════════════════════════════════════════════════
// Agent mode
// ═══════════════════════════════════════════════════════════════
function sendAgentTask() {
    var inp = document.getElementById('msgInput');
    var txt = inp.value.trim();
    if (!txt) return;

    var c = document.getElementById('messages');
    c.appendChild(makeMsgEl('user', txt, new Date().toISOString()));
    c.scrollTop = c.scrollHeight;
    inp.value = '';

    var agentBtn = document.getElementById('agentBtn');

    var controller = new AbortController();
    window._activeAbortController = controller;
    serverProcessing = true;
    serverTaskKind = 'agent';
    syncBusyUI();

    var tid = 'typing_agent_' + Date.now();
    var typing = document.createElement('div');
    typing.className = 'msg assistant';
    typing.id = tid;
    typing.innerHTML = '<div class="bubble"><div class="typing"><div class="dot"></div><div class="dot"></div><div class="dot"></div><span id="agent_status_span">Agent: starting…</span></div></div>';
    c.appendChild(typing);
    c.scrollTop = c.scrollHeight;

    function setStatus(text) {
        var span = document.getElementById('agent_status_span');
        if (span) span.textContent = text;
        c.scrollTop = c.scrollHeight;
    }

    function finishAgent(d) {
        var t = document.getElementById(tid); if (t) t.remove();
        if (d.cleared) {
            document.getElementById('messages').innerHTML = '';
        } else if (d.success && d.message) {
            c.appendChild(makeMsgEl(d.message.role, d.message.content, d.message.timestamp));
            c.scrollTop = c.scrollHeight;
            if (d.agentResults && d.agentResults.length) {
                var hasPending = d.agentResults.some(function(r){ return r.pending; });
                if (hasPending) {
                    renderAgentPendingResults(d.agentResults, c);
                } else {
                    renderAgentResults(d.agentResults, c);
                }
            }
        } else {
            c.appendChild(makeMsgEl('assistant', '❌ Agent error: ' + (d.error || 'unknown'), new Date().toISOString()));
            c.scrollTop = c.scrollHeight;
        }
        loadStatus();
        loadFiles();
        window._activeAbortController = null;
        serverProcessing = false;
        serverTaskKind = '';
        syncBusyUI();
        inp.focus();
    }

    // Use streaming fetch so we can update the status bubble live.
    fetch('/api/agent/stream', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task: txt }),
        signal: controller.signal
    }).then(function(resp) {
        if (!resp.ok || !resp.body) {
            return resp.text().then(function(t) { finishAgent({ success: false, error: t || resp.statusText }); });
        }
        var reader = resp.body.getReader();
        var decoder = new TextDecoder();
        var buf = '';
        function pump() {
            return reader.read().then(function(chunk) {
                if (chunk.done) return;
                buf += decoder.decode(chunk.value, { stream: true });
                // SSE lines look like: "data: {...}\n\n"
                var lines = buf.split('\n');
                buf = lines.pop(); // keep any incomplete tail
                lines.forEach(function(line) {
                    if (!line.startsWith('data: ')) return;
                    try {
                        var ev = JSON.parse(line.slice(6));
                        if (ev.type === 'status') {
                            setStatus(ev.text);
                        } else if (ev.type === 'done') {
                            finishAgent(ev);
                        }
                    } catch(e) {}
                });
                return pump();
            });
        }
        return pump();
    }).catch(function(e) {
        var t = document.getElementById(tid); if (t) t.remove();
        if (e.name === 'AbortError') {
            c.appendChild(makeMsgEl('assistant', '⏹ Agent task cancelled.', new Date().toISOString()));
        } else {
            c.appendChild(makeMsgEl('assistant', '❌ Agent network error: ' + e.message, new Date().toISOString()));
        }
        c.scrollTop = c.scrollHeight;
        window._activeAbortController = null;
        serverProcessing = false;
        serverTaskKind = '';
        syncBusyUI();
        loadStatus();
        inp.focus();
    });
}

// Render one inline diff card per file result returned by the agent backend.
function renderAgentResults(results, container) {
    results.forEach(function(r) {
        var el = document.createElement('div');
        el.className = 'msg assistant';

        var inner = '<div class="bubble" style="padding:0.6rem 0.85rem;font-size:0.82rem;">';

        if (r.error) {
            inner += '❌ <code style="color:var(--red)">' + esc(r.file) + '</code> — ' + esc(r.error);
        } else if (!r.changed) {
            inner += '<span style="color:var(--text2)">— <code>' + esc(r.file) + '</code> — no change needed</span>';
        } else {
            var icon  = r.created ? '✨ Created' : '✅ Modified';
            var color = r.created ? 'var(--accent2)' : 'var(--accent)';
            var diffHtml = buildDiffHtml(r.oldContent || '', r.newContent || '');
            var uid = 'idiff_' + Date.now() + '_' + Math.random().toString(36).slice(2,7);
            inner += icon + ' <code style="color:' + color + '">' + esc(r.file) + '</code>' +
                '<div class="inline-diff" style="margin-top:0.45rem;">' +
                    '<div class="inline-diff-hdr" onclick="toggleDiff(\'' + uid + '\')">' +
                        '<span class="inline-diff-title">📄 ' + esc(r.file) + '</span>' +
                        '<span class="inline-diff-toggle" id="tog_' + uid + '">▼ show diff</span>' +
                    '</div>' +
                    '<pre class="diff-pre inline-diff-body hidden" id="' + uid + '">' + diffHtml + '</pre>' +
                '</div>';

            // If this file is open in the viewer, refresh it with diff highlights.
            if (currentFile === r.file) {
                origContent = r.newContent;
                showFileDiff(r.oldContent, r.newContent, r.file);
            }
        }

        inner += '</div>';
        el.innerHTML = inner;
        container.appendChild(el);
    });
    container.scrollTop = container.scrollHeight;
}

// ═══════════════════════════════════════════════════════════════
// Agent pending-result confirmation UI
// ═══════════════════════════════════════════════════════════════

// renderAgentPendingResults shows each proposed file change with
// individual Apply / Deny buttons plus an Apply-All / Deny-All bar.
function renderAgentPendingResults(results, container) {
    var pendingCardIds = [];
    var barId = 'agentBar_' + Date.now();

    results.forEach(function(r, i) {
        var el      = document.createElement('div');
        el.className = 'msg assistant';
        var cardId   = 'apc_' + Date.now() + '_' + i;
        el.id        = cardId;
        var inner;

        if (r.error) {
            inner = '<div class="bubble" style="padding:0.6rem 0.85rem;font-size:0.82rem;">' +
                '\u274c <code style="color:var(--red)">' + esc(r.file) + '</code> \u2014 ' + esc(r.error) +
                '</div>';
        } else if (!r.changed) {
            inner = '<div class="bubble" style="padding:0.6rem 0.85rem;font-size:0.82rem;">' +
                '<span style="color:var(--text2)">\u2014 <code>' + esc(r.file) + '</code> \u2014 no change needed</span>' +
                '</div>';
        } else {
            agentPendingStore[cardId] = { file: r.file, oldContent: r.oldContent||'', newContent: r.newContent||'' };
            pendingCardIds.push(cardId);
            var icon  = r.created ? '\u2728 New file' : '\ud83d\udcdd Proposed';
            var color = r.created ? 'var(--accent2)' : 'var(--yellow)';
            var diffHtml = buildDiffHtml(r.oldContent||'', r.newContent||'');
            var uid2 = 'idiff_p_' + Date.now() + '_' + i;
            inner =
                '<div class="bubble" style="padding:0.6rem 0.85rem;font-size:0.82rem;">' +
                icon + ' <code style="color:' + color + '">' + esc(r.file) + '</code>' +
                '<div class="inline-diff" style="margin-top:0.45rem;">' +
                    '<div class="inline-diff-hdr" onclick="toggleDiff(\'' + uid2 + '\')">' +
                        '<span class="inline-diff-title">\ud83d\udcc4 ' + esc(r.file) + '</span>' +
                        '<span class="inline-diff-toggle" id="tog_' + uid2 + '">\u25bc show diff</span>' +
                    '</div>' +
                    '<pre class="diff-pre inline-diff-body hidden" id="' + uid2 + '">' + diffHtml + '</pre>' +
                '</div>' +
                '<div style="display:flex;gap:0.5rem;margin-top:0.55rem;">' +
                    '<button class="btn-apply" id="applyBtn_' + cardId + '" onclick="agentApplyFile(\'' + cardId + '\')">' +
                        '\u2705 Apply</button>' +
                    '<button class="btn-deny"  id="denyBtn_'  + cardId + '" onclick="agentDenyFile(\'' + cardId  + '\')">' +
                        '\ud83d\udeab Deny</button>' +
                '</div>' +
                '</div>';
        }
        el.innerHTML = inner;
        container.appendChild(el);
    });

    // Bulk bar — only when at least one file needs confirmation.
    if (pendingCardIds.length > 0) {
        agentBulkGroups[barId] = pendingCardIds;
        var bar = document.createElement('div');
        bar.className = 'msg assistant';
        bar.id = barId;
        bar.innerHTML =
            '<div class="bubble" style="padding:0.5rem 0.85rem;font-size:0.82rem;' +
            'display:flex;gap:0.6rem;align-items:center;flex-wrap:wrap;">' +
            '<span style="color:var(--text2)">' + pendingCardIds.length + ' proposed change(s) \u2014 review and confirm:</span>' +
            '<button class="btn-green" onclick="agentApplyAll(\'' + barId + '\')" id="applyAllBtn_' + barId + '">\u2705 Apply All</button>' +
            '<button class="btn-grey"  onclick="agentDenyAll(\''  + barId + '\')" id="denyAllBtn_'  + barId + '">\ud83d\udeab Deny All</button>' +
            '</div>';
        // Insert the bulk bar BEFORE the file cards (prepend before first card).
        var firstChild = container.lastChild;
        while (firstChild && firstChild.id && firstChild.id.startsWith('apc_')) {
            firstChild = firstChild.previousSibling;
        }
        if (firstChild && firstChild.nextSibling) {
            container.insertBefore(bar, firstChild.nextSibling);
        } else {
            container.appendChild(bar);
        }
    }
    container.scrollTop = container.scrollHeight;
}

function agentApplyFile(cardId) {
    var pending = agentPendingStore[cardId];
    if (!pending) return;
    var applyBtn = document.getElementById('applyBtn_' + cardId);
    var denyBtn  = document.getElementById('denyBtn_'  + cardId);
    if (applyBtn) { applyBtn.disabled = true; applyBtn.textContent = '\u23f3'; }
    if (denyBtn)  { denyBtn.disabled  = true; }

    fetch('/api/agent/commit', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ files: [{ path: pending.file, content: pending.newContent }] })
    }).then(function(r){ return r.json(); }).then(function(d) {
        var res = d.results && d.results[0];
        var el  = document.getElementById(cardId);
        if (!el) return;
        if (res && res.success) {
            var diffHtml = buildDiffHtml(pending.oldContent, pending.newContent);
            var uid3 = 'idiff_c_' + Date.now();
            var icon  = pending.oldContent ? '\u2705 Applied' : '\u2705 Created';
            var color = pending.oldContent ? 'var(--accent)' : 'var(--accent2)';
            el.innerHTML =
                '<div class="bubble" style="padding:0.6rem 0.85rem;font-size:0.82rem;">' +
                icon + ' <code style="color:' + color + '">' + esc(pending.file) + '</code>' +
                '<div class="inline-diff" style="margin-top:0.45rem;">' +
                    '<div class="inline-diff-hdr" onclick="toggleDiff(\'' + uid3 + '\')">' +
                        '<span class="inline-diff-title">\ud83d\udcc4 ' + esc(pending.file) + '</span>' +
                        '<span class="inline-diff-toggle" id="tog_' + uid3 + '">\u25bc show diff</span>' +
                    '</div>' +
                    '<pre class="diff-pre inline-diff-body hidden" id="' + uid3 + '">' + diffHtml + '</pre>' +
                '</div></div>';
            if (currentFile === pending.file) {
                origContent = pending.newContent;
                renderFileContent(pending.newContent, pending.file);
                showFileDiff(pending.oldContent, pending.newContent, pending.file);
            }
            delete agentPendingStore[cardId];
            checkBulkBarDone();
            loadFiles();
        } else {
            if (applyBtn) { applyBtn.disabled = false; applyBtn.textContent = '\u2705 Apply'; }
            if (denyBtn)  { denyBtn.disabled  = false; }
            var errMsg = (res && res.error) || 'Unknown error';
            var bubble = el.querySelector('.bubble');
            if (bubble) bubble.insertAdjacentHTML('beforeend',
                '<div style="color:var(--red);font-size:0.8rem;margin-top:0.3rem;">\u274c ' + esc(errMsg) + '</div>');
        }
    }).catch(function(e) {
        if (applyBtn) { applyBtn.disabled = false; applyBtn.textContent = '\u2705 Apply'; }
        if (denyBtn)  { denyBtn.disabled  = false; }
    });
}

function agentDenyFile(cardId) {
    var pending = agentPendingStore[cardId];
    if (!pending) return;
    var el = document.getElementById(cardId);
    if (el) {
        el.innerHTML =
            '<div class="bubble" style="padding:0.5rem 0.85rem;font-size:0.82rem;color:var(--text2);">' +
            '\ud83d\udeab Skipped <code>' + esc(pending.file) + '</code>' +
            '</div>';
    }
    delete agentPendingStore[cardId];
    checkBulkBarDone();
}

function checkBulkBarDone() {
    Object.keys(agentBulkGroups).forEach(function(barId) {
        var cards = agentBulkGroups[barId];
        var anyPending = cards.some(function(cid) { return !!agentPendingStore[cid]; });
        if (!anyPending) {
            delete agentBulkGroups[barId];
            var bar = document.getElementById(barId);
            if (bar) { bar.style.opacity = '0.35'; bar.style.pointerEvents = 'none'; }
        }
    });
}

function agentApplyAll(barId) {
    var cardIds = agentBulkGroups[barId] || [];
    var applyAllBtn = document.getElementById('applyAllBtn_' + barId);
    var denyAllBtn  = document.getElementById('denyAllBtn_'  + barId);
    if (applyAllBtn) { applyAllBtn.disabled = true; applyAllBtn.textContent = '\u23f3'; }
    if (denyAllBtn)  { denyAllBtn.disabled  = true; }
    cardIds.forEach(function(cardId) { agentApplyFile(cardId); });
    delete agentBulkGroups[barId];
    var bar = document.getElementById(barId);
    if (bar) { bar.style.opacity = '0.5'; bar.style.pointerEvents = 'none'; }
}

function agentDenyAll(barId) {
    var cardIds = (agentBulkGroups[barId] || []).slice();
    delete agentBulkGroups[barId];
    cardIds.forEach(function(cardId) {
        var pending = agentPendingStore[cardId];
        if (!pending) return;
        var el = document.getElementById(cardId);
        if (el) {
            el.innerHTML =
                '<div class="bubble" style="padding:0.5rem 0.85rem;font-size:0.82rem;color:var(--text2);">' +
                '\ud83d\udeab Skipped <code>' + esc(pending.file) + '</code>' +
                '</div>';
        }
        delete agentPendingStore[cardId];
    });
    var bar = document.getElementById(barId);
    if (bar) { bar.style.opacity = '0.35'; bar.style.pointerEvents = 'none'; }
}

// ═══════════════════════════════════════════════════════════════
// Auto-apply code blocks from LLM response
// ═══════════════════════════════════════════════════════════════
function autoApplyCodeBlocks(content, msgContainer, userQuestion) {
    // Safety gate: if auto-apply is disabled (the default), never write to
    // disk without explicit user confirmation.  Every rendered code block
    // already has an ⚡ Apply button that goes through the confirmation modal
    // — just surface a brief reminder and stop here.
    if (!autoApply) {
        var BT3check = '` + "```" + `';
        var reCheck = new RegExp(BT3check + '(\\w+):([^\\s` + "`" + `]+)', 'g');
        if (reCheck.test(content)) {
            var hint = document.createElement('div');
            hint.className = 'msg assistant';
            hint.innerHTML = '<div class="bubble" style="padding:0.5rem 0.85rem;font-size:0.82rem;color:var(--text2);">' +
                '🔒 Auto-apply is <strong>off</strong>. Click <strong>⚡ Apply</strong> on any code block above to apply it, ' +
                'or enable auto-apply with the <strong>⚡ Auto</strong> toggle in the header.' +
                '</div>';
            msgContainer.appendChild(hint);
            msgContainer.scrollTop = msgContainer.scrollHeight;
        }
        return;
    }

    var BT3 = '` + "```" + `';
    // First try: named blocks with lang:filename header (allow trailing spaces after lang:file)
    var reNamed = new RegExp(BT3 + '(\\w+):([^\\s` + "`" + `]+)[^\\n]*\\n([\\s\\S]*?)' + BT3, 'g');
    var matches = [];
    var m;
    while ((m = reNamed.exec(content)) !== null) {
        matches.push({ lang: m[1], file: m[2], code: m[3].replace(/\n$/, '') });
    }
    // Fallback: plain lang blocks - infer target from user question first, then currentFile
    if (matches.length === 0) {
        var inferredFile = null;
        if (userQuestion) {
            var q = userQuestion.toLowerCase();
            var bestIdx = -1;
            allFiles.forEach(function(f) {
                var p = f.relPath || f.RelPath || f.path || f;
                if (typeof p !== 'string') return;
                var name = p.split('/').pop().toLowerCase();
                var idx = q.lastIndexOf(name);
                if (idx !== -1 && idx > bestIdx) { bestIdx = idx; inferredFile = p; }
            });
        }
        var targetFile = inferredFile || currentFile;
        if (targetFile) {
            var ext = targetFile.split('.').pop().toLowerCase();
            // [^\S\n]* allows trailing spaces after the lang identifier
            var rePlain = new RegExp(BT3 + '(\\w+)[^\\S\\n]*\\n([\\s\\S]*?)' + BT3, 'g');
            var langExt = {
                go:'go', js:'js', javascript:'js', ts:'ts', typescript:'ts',
                py:'py', python:'py', rb:'rb', ruby:'rb', rs:'rs', rust:'rs',
                java:'java', cs:'cs', cpp:'cpp', c:'c', sh:'sh', bash:'sh'
            };
            while ((m = rePlain.exec(content)) !== null) {
                var bl = m[1].toLowerCase();
                if ((langExt[bl] || bl) === ext) {
                    matches.push({ lang: m[1], file: targetFile, code: m[2].replace(/\n$/, ''), inferred: true });
                }
            }
        }
    }
    if (matches.length === 0) {
        // Warn the user so they know to ask more explicitly
        var notice = document.createElement('div');
        notice.className = 'msg assistant';
        notice.innerHTML = '<div class="bubble" style="padding:0.5rem 0.85rem;font-size:0.82rem;color:var(--text2);">' +
            '⚠️ Could not auto-apply: no code block with a filename was found.<br>' +
            'Ask the LLM to specify the file, e.g. <code>edit vars.go: ...</code>' +
            '</div>';
        msgContainer.appendChild(notice);
        msgContainer.scrollTop = msgContainer.scrollHeight;
        return;
    }

    var results  = []; // { file, old, code, success, error }
    var pending  = matches.length;

    function done() {
        pending--;
        if (pending > 0) return;

        // Render one inline diff card per applied file
        results.forEach(function(r) {
            var header, body;
            if (r.success) {
                var diffHtml = buildDiffHtml(r.old, r.code);
                var uid = 'idiff_' + Date.now() + '_' + Math.random().toString(36).slice(2);
                header = '✅ Applied changes to <code style="color:var(--accent)">' + esc(r.file) + '</code>';
                body = '<div class="inline-diff">' +
                    '<div class="inline-diff-hdr" onclick="toggleDiff(\'' + uid + '\')">' +
                        '<span class="inline-diff-title">📄 ' + esc(r.file) + '</span>' +
                        '<span class="inline-diff-toggle" id="tog_' + uid + '">▼ show diff</span>' +
                    '</div>' +
                    '<pre class="diff-pre inline-diff-body hidden" id="' + uid + '">' + diffHtml + '</pre>' +
                '</div>';
            } else {
                header = '❌ Failed to apply <code style="color:var(--red)">' + esc(r.file) + '</code>: ' + esc(r.error||'error');
                body = '';
            }
            var notice = document.createElement('div');
            notice.className = 'msg assistant';
            notice.innerHTML = '<div class="bubble" style="padding:0.6rem 0.85rem;font-size:0.82rem;">' +
                '<div style="margin-bottom:' + (body?'0.5rem':'0') + '">' + header + '</div>' + body + '</div>';
            msgContainer.appendChild(notice);
        });
        msgContainer.scrollTop = msgContainer.scrollHeight;

        // Re-render open file tab if it was updated and show diff
        results.filter(function(r){ return r.success && currentFile === r.file; }).forEach(function(r) {
            var oldContent = r.old;
            origContent = r.code;
            renderFileContent(r.code, r.file);
            showFileDiff(oldContent, r.code, r.file);
        });
    }

    matches.forEach(function(block) {
        // Fetch old content first, then write
        fetch('/api/file?path=' + encodeURIComponent(block.file))
            .then(function(r){ return r.ok ? r.json() : { content: '' }; })
            .catch(function(){ return { content: '' }; })
            .then(function(old) {
                var oldContent = old.content || '';
                return fetch('/api/file/write', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ path: block.file, content: block.code })
                }).then(function(r){ return r.json(); }).then(function(d) {
                    results.push({ file: block.file, old: oldContent, code: block.code, success: !!d.success, error: d.error });
                    done();
                });
            }).catch(function(e) {
                results.push({ file: block.file, old: '', code: block.code, success: false, error: e.message });
                done();
            });
    });
}

// ═══════════════════════════════════════════════════════════════
// File tree
// ═══════════════════════════════════════════════════════════════
function loadFiles() {
    fetch('/api/files').then(function(r){ return r.json(); }).then(function(files) {
        allFiles = files || [];
        renderTree(allFiles);
    }).catch(function() {
        document.getElementById('fileTree').innerHTML = '<span class="muted">Failed to load files</span>';
    });
}

var FILE_ICONS = {
    go:'🐹',js:'📜',ts:'📘',py:'🐍',rs:'🦀',rb:'💎',php:'🐘',java:'☕',
    c:'🔧',cpp:'🔧',h:'🔧',cs:'🔷',swift:'🍎',kt:'🔶',scala:'🔴',
    md:'📝',txt:'📄',yaml:'⚙️',yml:'⚙️',json:'📋',toml:'📋',xml:'📋',
    html:'🌐',css:'🎨',scss:'🎨',sh:'💻',bash:'💻',zsh:'💻',
    dockerfile:'🐳',tf:'🏗️',proto:'🔌',sql:'🗃️',pdf:'📑',pcap:'📡',
};
function fileIcon(path) {
    var ext = (path.split('.').pop()||'').toLowerCase();
    return FILE_ICONS[ext] || '📄';
}

function renderTree(files) {
    var tree = document.getElementById('fileTree');
    if (!files || files.length === 0) {
        tree.innerHTML = '<span class="muted">No files</span>';
        return;
    }
    var dirs = {}, rootFiles = [];
    files.forEach(function(f) {
        var parts = f.relPath.split('/');
        if (parts.length === 1) { rootFiles.push(f); }
        else {
            var dir = parts[0];
            if (!dirs[dir]) dirs[dir] = [];
            dirs[dir].push(f);
        }
    });
    var html = '';
    rootFiles.forEach(function(f) { html += treeFileHTML(f.relPath); });
    Object.keys(dirs).sort().forEach(function(dir) {
        html += '<div class="tree-dir"><details open><summary>' + esc(dir) + '</summary>';
        html += '<div class="tree-children">';
        dirs[dir].forEach(function(f) { html += treeFileHTML(f.relPath); });
        html += '</div></details></div>';
    });
    tree.innerHTML = html;
    // Mark active file
    if (currentFile) highlightTreeFile(currentFile);
}

function treeFileHTML(relPath) {
    var name  = relPath.split('/').pop();
    var safe  = relPath.replace(/\\/g,'\\\\').replace(/'/g,"\\'");
    return '<div class="tree-file" onclick="openFile(\'' + safe + '\')" data-path="' + esc(relPath) + '">' +
           '<span>' + fileIcon(relPath) + '</span><span class="fname">' + esc(name) + '</span></div>';
}

function highlightTreeFile(relPath) {
    document.querySelectorAll('.tree-file').forEach(function(el) {
        el.classList.toggle('active', el.getAttribute('data-path') === relPath);
    });
}

function filterFiles() {
    var q = document.getElementById('fileSearch').value.toLowerCase();
    renderTree(q ? allFiles.filter(function(f){ return f.relPath.toLowerCase().includes(q); }) : allFiles);
}

// ═══════════════════════════════════════════════════════════════
// File viewer / editor
// ═══════════════════════════════════════════════════════════════
function openFile(relPath) {
    fetch('/api/file?path=' + encodeURIComponent(relPath))
        .then(function(r){ return r.json(); })
        .then(function(d) {
            currentFile  = relPath;
            origContent  = d.content;
            addFileTab(relPath);
            switchTab('viewer:' + relPath);
            renderFileContent(d.content, relPath);
            highlightTreeFile(relPath);
        })
        .catch(function(e){ alert('Cannot open file: ' + e.message); });
}

function renderFileContent(content, relPath, changedLines) {
    var ext  = (relPath.split('.').pop()||'').toLowerCase();
    var code = document.getElementById('codeContent');
    var changed = changedLines || {};
    var hasChanges = Object.keys(changed).length > 0;

    if (hasChanges) {
        // Split raw text by line and mark each with gutter class.
        // Do NOT use hljs here: splitting hljs HTML by \n breaks multi-line tokens.
        var lines = content.split('\n');
        if (lines[lines.length-1] === '') lines.pop();
        var html = lines.map(function(l, i) {
            var cls = 'code-line';
            if (changed[i+1] === 'added')    cls += ' line-added';
            else if (changed[i+1] === 'changed') cls += ' line-changed';
            return '<span class="' + cls + '">' + esc(l) + '</span>';
        }).join('\n');
        code.className = 'hljs';
        code.innerHTML = html;
    } else {
        code.className = 'hljs language-' + ext;
        code.textContent = content;
        if (window.hljs) { try { hljs.highlightElement(code); } catch(e){} }
    }
    document.getElementById('viewerPath').textContent = relPath;
    document.getElementById('codeView').style.display    = '';
    document.getElementById('codeEditor').classList.add('hidden');
    document.getElementById('editBtn').classList.remove('hidden');
    document.getElementById('saveBtn').classList.add('hidden');
    document.getElementById('cancelBtn').classList.add('hidden');
    document.getElementById('diffBtn').classList.add('hidden');
    document.getElementById('diffBtn').textContent = '± Diff';
}

// Returns a map of { lineNumber: 'added'|'changed' } for lines in newText.
// A '+' immediately preceded by a '-' is a 'changed' line; a '+' alone is 'added'.
function computeChangedLines(oldText, newText) {
    var ops = computeDiff((oldText||'').split('\n'), (newText||'').split('\n'));
    var result = {};
    var newLine = 0;
    for (var i = 0; i < ops.length; i++) {
        var op = ops[i];
        if (op.t === ' ') {
            newLine++;
        } else if (op.t === '-') {
            // Look ahead: if next op is '+', it's a replacement (changed line)
            if (i+1 < ops.length && ops[i+1].t === '+') {
                newLine++;
                result[newLine] = 'changed';
                i++; // consume the '+' too
            }
            // pure deletion - no new line consumed
        } else if (op.t === '+') {
            newLine++;
            result[newLine] = 'added';
        }
    }
    return result;
}

function showFileDiff(oldContent, newContent, relPath) {
    var changed = computeChangedLines(oldContent, newContent);
    renderFileContent(newContent, relPath, changed);
    document.getElementById('diffBtn').classList.remove('hidden');
    document.getElementById('diffBtn').textContent = '✕ Clear';
    // Mark tab as modified
    var tid = 'ftab_' + relPath.replace(/[^a-zA-Z0-9]/g, '_');
    var tab = document.getElementById(tid);
    if (tab) tab.classList.add('modified');
}

function toggleViewerDiff() {
    var btn = document.getElementById('diffBtn');
    // Clear highlights — re-render without changedLines
    if (currentFile) {
        renderFileContent(origContent, currentFile);
        var tid = 'ftab_' + currentFile.replace(/[^a-zA-Z0-9]/g, '_');
        var tab = document.getElementById(tid);
        if (tab) tab.classList.remove('modified');
    }
}

function enterEditMode() {
    var editor = document.getElementById('codeEditor');
    editor.value = origContent;
    editor.classList.remove('hidden');
    document.getElementById('codeView').style.display    = 'none';
    document.getElementById('editBtn').classList.add('hidden');
    document.getElementById('saveBtn').classList.remove('hidden');
    document.getElementById('cancelBtn').classList.remove('hidden');
}

function cancelEdit() {
    document.getElementById('codeEditor').classList.add('hidden');
    document.getElementById('codeView').style.display    = '';
    document.getElementById('editBtn').classList.remove('hidden');
    document.getElementById('saveBtn').classList.add('hidden');
    document.getElementById('cancelBtn').classList.add('hidden');
}

function saveFile() {
    var newContent = document.getElementById('codeEditor').value;
    writeFile(currentFile, newContent, function() {
        origContent = newContent;
        renderFileContent(newContent, currentFile);
        loadMessages();
    });
}

function writeFile(relPath, content, onSuccess) {
    fetch('/api/file/write', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: relPath, content: content })
    }).then(function(r){ return r.json(); }).then(function(d) {
        if (d.success) { if (onSuccess) onSuccess(); }
        else { alert('Write failed: ' + (d.error || 'unknown error')); }
    }).catch(function(e){ alert('Write failed: ' + e.message); });
}

// ═══════════════════════════════════════════════════════════════
// Tabs
// ═══════════════════════════════════════════════════════════════
function addFileTab(relPath) {
    var id = 'ftab_' + relPath.replace(/[^a-zA-Z0-9]/g, '_');
    if (document.getElementById(id)) return;
    var name = relPath.split('/').pop();
    var safe = relPath.replace(/\\/g,'\\\\').replace(/'/g, "\\'");
    var btn  = document.createElement('button');
    btn.className = 'tab';
    btn.id        = id;
    btn.setAttribute('data-path', relPath);
    btn.innerHTML = fileIcon(relPath) + ' ' + esc(name) +
        ' <span class="tab-x" onclick="closeFileTab(\'' + safe + '\',event)">×</span>';
    btn.onclick   = function() { openFile(relPath); };
    document.getElementById('tabBar').appendChild(btn);
}

function closeFileTab(relPath, e) {
    e.stopPropagation();
    var id  = 'ftab_' + relPath.replace(/[^a-zA-Z0-9]/g, '_');
    var tab = document.getElementById(id);
    if (tab) tab.remove();
    if (currentFile === relPath) { currentFile = null; switchTab('chat'); }
}

function switchTab(name) {
    document.querySelectorAll('.tab').forEach(function(t) {
        var tp = t.getAttribute('data-path');
        t.classList.toggle('active',
            (name === 'chat' && t.id === 'tabChat') ||
            (name.startsWith('viewer:') && tp === name.slice(7))
        );
    });
    if (name === 'chat') {
        document.getElementById('chatPanel').classList.remove('hidden');
        document.getElementById('viewerPanel').classList.add('hidden');
    } else {
        document.getElementById('chatPanel').classList.add('hidden');
        document.getElementById('viewerPanel').classList.remove('hidden');
    }
}

// ═══════════════════════════════════════════════════════════════
// Apply changes
// ═══════════════════════════════════════════════════════════════
function showApplyModal(blockId) {
    var block = codeStore[blockId];
    if (!block) return;
    pendingApply = { file: block.file, code: block.code };
    document.getElementById('applyFileName').textContent = block.file;
    document.getElementById('diffPre').innerHTML = '<span class="dc">Loading diff…</span>';
    document.getElementById('applyModal').classList.remove('hidden');

    fetch('/api/file?path=' + encodeURIComponent(block.file))
        .then(function(r){ return r.json(); })
        .then(function(d){ buildDiff(d.content, block.code); })
        .catch(function(){ buildDiff('', block.code); }); // new file
}

function closeApplyModal() {
    document.getElementById('applyModal').classList.add('hidden');
    pendingApply = null;
}

function openHelpModal() {
    document.getElementById('helpModal').classList.remove('hidden');
}
function closeHelpModal() {
    document.getElementById('helpModal').classList.add('hidden');
}
document.addEventListener('keydown', function(e) {
    if (e.key === 'Escape') { closeHelpModal(); }
});

function toggleDiff(uid) {
    var pre = document.getElementById(uid);
    var tog = document.getElementById('tog_' + uid);
    if (!pre) return;
    var hidden = pre.classList.toggle('hidden');
    if (tog) tog.textContent = hidden ? '▼ show diff' : '▲ hide diff';
}

function confirmApply() {
    if (!pendingApply) return;
    writeFile(pendingApply.file, pendingApply.code, function() {
        closeApplyModal();
        loadMessages();
        if (currentFile === pendingApply.file) {
            origContent = pendingApply.code;
            renderFileContent(pendingApply.code, pendingApply.file);
        }
    });
}

function buildDiffHtml(oldText, newText) {
    var oldLines = (oldText||'').split('\n');
    var newLines = (newText||'').split('\n');
    var ops      = computeDiff(oldLines, newLines);
    var html     = '';
    ops.forEach(function(op) {
        if      (op.t === '+') html += '<span class="da">+ ' + esc(op.l) + '\n</span>';
        else if (op.t === '-') html += '<span class="dr">- ' + esc(op.l) + '\n</span>';
        else                   html += '<span class="dc">  ' + esc(op.l) + '\n</span>';
    });
    return html;
}

function buildDiff(oldText, newText) {
    document.getElementById('diffPre').innerHTML = buildDiffHtml(oldText, newText);
}

function computeDiff(oldLines, newLines) {
    // LCS-based diff for reasonably small files; simplified for larger ones.
    if (oldLines.length + newLines.length > 600) {
        return newLines.map(function(l){ return {t:'+',l:l}; });
    }
    var n = oldLines.length, m = newLines.length;
    var dp = [];
    for (var i = 0; i <= n; i++) { dp[i] = new Array(m+1).fill(0); }
    for (var i = 1; i <= n; i++)
        for (var j = 1; j <= m; j++)
            dp[i][j] = oldLines[i-1] === newLines[j-1]
                ? dp[i-1][j-1]+1
                : Math.max(dp[i-1][j], dp[i][j-1]);

    var ops = []; var i = n, j = m;
    while (i > 0 || j > 0) {
        if (i > 0 && j > 0 && oldLines[i-1] === newLines[j-1]) {
            ops.push({t:'=',l:oldLines[i-1]}); i--; j--;
        } else if (j > 0 && (i === 0 || dp[i][j-1] >= dp[i-1][j])) {
            ops.push({t:'+',l:newLines[j-1]}); j--;
        } else {
            ops.push({t:'-',l:oldLines[i-1]}); i--;
        }
    }
    return ops.reverse();
}

// ═══════════════════════════════════════════════════════════════
// Utilities
// ═══════════════════════════════════════════════════════════════
function copyBlock(id) {
    var b = codeStore[id];
    if (b && navigator.clipboard) navigator.clipboard.writeText(b.code).catch(function(){});
}

function toggleSidebar() {
    var sidebar = document.getElementById('sidebar');
    var collapsed = sidebar.classList.toggle('collapsed');
    var btn = document.getElementById('sidebarToggle');
    btn.title = collapsed ? 'Open sidebar' : 'Close sidebar';
}

function rescan() {
    fetch('/api/rescan', { method:'POST' })
        .then(function(r){ return r.json(); })
        .then(function() { loadFiles(); loadStatus(); loadMessages(); })
        .catch(function(){});
}

function applyTheme() {
    if (localStorage.getItem('theme') === 'light') {
        document.body.classList.add('light');
        document.getElementById('themeBtn').textContent = '☀️';
    }
}
function toggleTheme() {
    document.body.classList.toggle('light');
    var light = document.body.classList.contains('light');
    document.getElementById('themeBtn').textContent = light ? '☀️' : '🌙';
    localStorage.setItem('theme', light ? 'light' : 'dark');
}

// ═══════════════════════════════════════════════════════════════
// Settings (auto-apply toggle)
// ═══════════════════════════════════════════════════════════════
function applyAutoApplyUI() {
    var btn = document.getElementById('autoApplyBtn');
    if (!btn) return;
    if (autoApply) {
        btn.textContent = '⚡ Auto';
        btn.title = 'Auto-apply is ON — code blocks are written to disk automatically. Click to disable.';
        btn.style.color = 'var(--yellow)';
    } else {
        btn.textContent = '🔒 Auto';
        btn.title = 'Auto-apply is OFF — explicit ⚡ Apply required. Click to enable (use with caution).';
        btn.style.color = '';
    }
}

function loadSettings() {
    fetch('/api/settings').then(function(r){ return r.json(); }).then(function(d) {
        autoApply = !!d.auto_apply;
        applyAutoApplyUI();
    }).catch(function(){});
}

function toggleAutoApply() {
    autoApply = !autoApply;
    fetch('/api/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ auto_apply: autoApply })
    }).catch(function(){});
    applyAutoApplyUI();
}
</script>
</body>
</html>
`
