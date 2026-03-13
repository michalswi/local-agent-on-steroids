<div align="center">

<img src="./img/local-agent-on-steroids.png" alt="logo" width="120">

# ai-powered code analysis tool

[![stars](https://img.shields.io/github/stars/michalswi/local-agent-on-steroids?style=for-the-badge&color=353535)](https://github.com/michalswi/local-agent-on-steroids)
[![forks](https://img.shields.io/github/forks/michalswi/local-agent-on-steroids?style=for-the-badge&color=353535)](https://github.com/michalswi/local-agent-on-steroids/fork)
[![releases](https://img.shields.io/github/v/release/michalswi/local-agent-on-steroids?style=for-the-badge&color=353535)](https://github.com/michalswi/local-agent-on-steroids/releases)

**local-agent-on-steroids** is a privacy-first coding agent that scans, analyzes, generates and edits your codebase from a browser UI — 100% local, powered by your own LLMs through Ollama. No cloud. No telemetry. Your code stays yours.

</div>

> **Default LLM:** `wizardlm2:7b`.
> Change it in `config/config.go` under `LLMConfig.Model`, or switch live in chat with `model <name>` (for example `model gemma3:4b`).

## \# Quick Start

```bash
make build

# Web UI at http://localhost:5050
./local-agent-on-steroids --interactive --dir ./myproject

# Remote Ollama
./local-agent-on-steroids --interactive --dir ./myproject --host 192.168.1.100:11434

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

## \# UI Buttons

| Button | Action |
|---|---|
| **⚡ Agent** | Agent mode — scans all files, plans changes, and applies them autonomously. Triggered by pressing `Enter`. |
| **Send** | Chat-only mode — sends your message as a plain conversation without modifying any files. |
| **Help** | Opens the in-app help modal listing all available chat commands and keyboard shortcuts. |
| **🔒 Auto** | Toggles auto-apply mode. When **OFF** (default), each file change requires an explicit **⚡ Apply** confirmation. When **ON**, changes are applied immediately. |

## \# Chat Commands

| Command | Action |
|---|---|
| `focus <path>` | Limit LLM context to one file |
| `model <name>` | Switch model |
| `rescan` | Pick up new/changed files |
| `clear` | Clear chat history |
| `help` | Show all commands |

