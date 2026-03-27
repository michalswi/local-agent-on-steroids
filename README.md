<div align="center">

<img src="./img/local-agent-on-steroids.png" alt="logo" width="120">

# ai-powered code analysis tool

[![stars](https://img.shields.io/github/stars/michalswi/local-agent-on-steroids?style=for-the-badge&color=353535)](https://github.com/michalswi/local-agent-on-steroids)
[![forks](https://img.shields.io/github/forks/michalswi/local-agent-on-steroids?style=for-the-badge&color=353535)](https://github.com/michalswi/local-agent-on-steroids/fork)
[![releases](https://img.shields.io/github/v/release/michalswi/local-agent-on-steroids?style=for-the-badge&color=353535)](https://github.com/michalswi/local-agent-on-steroids/releases)

**local-agent-on-steroids** is a privacy-first coding agent that scans, analyzes, generates and edits your codebase from a browser UI — 100% local, powered by your own LLMs through Ollama. Your code stays yours. Exposes a REST API for programmatic access from any external tool or script.

</div>

> **Default LLM:** `wizardlm2:7b`.
> Change it in `config/config.go` under `LLMConfig.Model`, or run app with `--model <model>` flag or switch on app's chat with `model <model>`.

## \# Quick Start

```bash
make build

# Verify files (if any) in the directory
./local-agent-on-steroids --interactive --dir ./myproject --dry-run

# Web UI at http://localhost:5050
./local-agent-on-steroids --interactive --dir ./myproject

# Remote Ollama
./local-agent-on-steroids --interactive --dir ./myproject --host 192.168.1.100:11434

# Custom session-log directory (default: ~/Downloads/local-agent-on-steroids)
./local-agent-on-steroids --dir ./myproject --homedir /tmp/local-agent-on-steroids

# Utility
./local-agent-on-steroids --health
./local-agent-on-steroids --list-models
```

## \# Ollama Setup

```bash
# Recommended: match OLLAMA_NUM_PARALLEL with AGENT_CONCURRENT_FILES,
#              match OLLAMA_CONTEXT_LENGTH with AGENT_TOKEN_LIMIT
OLLAMA_CONTEXT_LENGTH=8192 OLLAMA_NUM_PARALLEL=5 ollama serve

AGENT_TOKEN_LIMIT=8000 AGENT_CONCURRENT_FILES=5 ./local-agent-on-steroids --dir . --interactive

# Defaults (context=4096, parallel=1)
ollama serve
./local-agent-on-steroids --dir . --interactive
```

> **`AGENT_CONCURRENT_FILES`** controls parallel LLM calls when **editing/analyzing existing files** (agent task mode).
> It does **not** apply when **generating new files** from scratch — those are produced sequentially.

## \# Session Logs

Every chat and agent interaction is saved as a JSON record under `~/Downloads/local-agent-on-steroids/` (default).
Override the location with the `--homedir` flag:

```bash
# default location
ls ~/Downloads/local-agent-on-steroids/
# local-agent-20260317-123456.json  local-agent-20260317-130012.json  ...

# custom location
./local-agent-on-steroids --dir . --homedir /tmp/local-agent-on-steroids
ls /tmp/local-agent-on-steroids/
```

## \# UI Buttons

| Button | Action |
|---|---|
| **⚡ Agent** | Agent mode — scans all files, plans changes, and applies them autonomously. Triggered by pressing `Enter`. |
| **🔧 Run & Fix** | Runs the project, feeds build errors to the LLM, applies fixes, and retries — up to 3 attempts. |
| **Send** | Chat-only mode — sends your message as a plain conversation without modifying any files. |
| **Clear** | Clears the current chat conversation history (same behavior as typing `clear` in chat). |
| **Help** | Opens the in-app help modal listing all available chat commands and keyboard shortcuts. |
| **⏹ Stop** | Aborts the current Agent or Send operation mid-stream. Only visible while a request is in progress. |
| **🔒 Auto Off** | Toggles auto-apply mode. When **OFF** (default), each file change requires an explicit **⚡ Apply** confirmation. When **ON**, changes are applied immediately. Does **not** affect the Agent's planning phase — planning always runs regardless. |


## \# Chat Commands

| Command | Action |
|---|---|
| `model <name>` | Switch model |
| `rescan` | Pick up new/changed files |
| `clear` | Clear chat history |
| `help` | Show all commands |

## \# System Prompts

There are three distinct system prompts used internally, each targeting a different operation mode. They are stored as embedded `.md` files under `webui/prompts/` and compiled into the binary at build time.

| Prompt file | Triggered by |
|---|---|
| `webui/prompts/chat.md` | **Send** button — plain conversation, no file writes |
| `webui/prompts/agent_edit.md` | **⚡ Agent** button — applied per-file in a parallel loop |
| `webui/prompts/agent_create.md` | Agent sub-step when a new file needs to be created from scratch |
| `webui/prompts/agent_fix.md` | **🔧 Run & Fix** button — language-agnostic fix prompt used in each repair iteration |

All three prompts are the static base. At runtime the server appends dynamic context (file tree, file contents, and session changelog) before sending to the LLM. Edit the `.md` files directly to tune the behaviour and rebuild — no Go string hunting required.

## \# External API

Two endpoints are available for programmatic access. Both accept the same request body and store the exchange in the chat history so it appears in the UI on the next refresh.

| Endpoint | Transport | Use when |
|---|---|---|
| `POST /api/ext/send` | JSON (blocking) | Short tasks; simple scripts |
| `POST /api/ext/stream` | Server-Sent Events | Long agent tasks; real-time progress |

**Request body** (same for both endpoints):

| Field | Type | Default | Description |
|---|---|---|---|
| `message` | string | required | The prompt or agent task |
| `mode` | `"chat"` \| `"agent"` | `"chat"` | Chat (Send) or Agent mode |
| `model` | string | *(server default)* | Optional model override for this request only (e.g. `"llama3:8b"`). Reverts to the server default after the call. |

> Agent mode via the external API always writes files to disk immediately. The pending/confirm workflow is a browser UI feature only.

**Verify app**:

```bash
curl -s http://localhost:5050/api/status | jq
```

**Chat mode example** (equivalent to pressing **Send**):

```bash
curl -s -X POST http://localhost:5050/api/ext/send \
  -H 'Content-Type: application/json' \
  -d '{"message": "explain the main.go file", "mode": "chat"}' | jq .
```

**Agent mode example** (equivalent to pressing **⚡ Agent**):

```bash
curl -s -X POST http://localhost:5050/api/ext/send \
  -H 'Content-Type: application/json' \
  -d '{"message": "add error handling to all functions in utils.go", "mode": "agent"}' | jq .
```

**Per-request model override** (reverts to server default after the call):

```bash
curl -s -X POST http://localhost:5050/api/ext/send \
  -H 'Content-Type: application/json' \
  -d '{"message": "explain the main.go file", "mode": "chat", "model": "gemma3:4b"}' | jq .
```

**Streaming example** — `/api/ext/stream` emits SSE events so you see progress as it happens:

```bash
curl -s -X POST http://localhost:5050/api/ext/stream \
  -H 'Content-Type: application/json' \
  -d '{"message": "refactor main.go", "mode": "agent"}'
```

SSE events:

```
data: {"type":"status","text":"⚙ Processing main.go…"}

data: {"type":"done","success":true,"message":{…},"agentResults":[…]}
```

> `status` events are only emitted in agent mode. The final `done` event has the same shape as the `/api/ext/send` JSON response.

**Response** (`/api/ext/send` only):

```json
{
  "success": true,
  "message": {
    "role": "assistant",
    "content": "...",
    "timestamp": "2026-03-17T12:00:00Z",
    "duration_ms": 3210,
    "agentResults": [...]
  },
  "agentResults": [
    {
      "file": "main.go",
      "changed": true,
      "created": false,
      "oldContent": "...",
      "newContent": "..."
    }
  ]
}
```

> `agentResults` is only present in agent mode responses. It is included at the top level for convenience and also mirrored inside `message.agentResults`.
>
> `AgentFileResult` fields: `file` (path), `changed` (bool), `created` (bool — true when the file was newly created), `deleted` (bool, omitempty — true when the file was removed), `oldContent`/`newContent` (strings, omitempty), `error` (string, omitempty — set when that file's LLM call failed).

## \# Configuration

Create `.agent/config.yaml` (see [examples/config.yaml](examples/config.yaml)):

```yaml
agent:
  token_limit: 8000       # match OLLAMA_CONTEXT_LENGTH (default: 4000)
  concurrent_files: 5     # match OLLAMA_NUM_PARALLEL (default: 1)

llm:
  model: "wizardlm2:7b"
  temperature: 0.1        # 0.0–0.3 for code, 0.4–0.7 for docs

filters:
  respect_gitignore: true
  deny_patterns: ["node_modules/**", ".git/**"]
  allow_patterns: ["*.go", "*.js", "*.md"]  # if set, only these are included

security:
  skip_binaries: true
  max_depth: 20
```

## \# AI chat

If you are looking for Ollama based AI chat check this app [scoutai](https://github.com/michalswi/scoutai) or visit [home page](https://scoutai.azurewebsites.net/).