#!/usr/bin/env bash
# lib/sandbox.sh — operator functions for creating an OpenCode sandbox.
# The sandbox contains opencode.jsonc with a remote MCP entry pointing at the local server.

# sandbox_create <sandbox_dir> <kbname>
#   Writes opencode.jsonc in <sandbox_dir> with the correct MCP reference.
#   Uses E2E_MODEL (must be set by the orchestrator).
#   Also creates a minimal AGENTS.md as system context for the agent.
sandbox_create() {
    local sandbox_dir="$1"
    local kbname="$2"

    if [[ -z "$sandbox_dir" || -z "$kbname" ]]; then
        echo "[ERROR] sandbox_create: missing arguments (sandbox_dir, kbname)" >&2
        return 1
    fi

    if [[ -z "${E2E_LLM_BASE_URL:-}" ]]; then
        echo "[ERROR] E2E_LLM_BASE_URL not set (OpenAI-compatible LLM endpoint)" >&2
        return 1
    fi

    mkdir -p "$sandbox_dir"

    # opencode.jsonc — official schema format https://opencode.ai/config.json
    cat > "${sandbox_dir}/opencode.jsonc" <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "model": "${E2E_MODEL}",
  "permission": {
    "edit": "allow",
    "bash": "allow",
    "read": "allow",
    "webfetch": "allow",
    "websearch": "allow"
  },
  "provider": {
    "opencode-go": {
      "options": {
        "baseURL": "${E2E_LLM_BASE_URL}"
      }
    }
  },
  "mcp": {
    "wiki": {
      "type": "remote",
      "url": "http://127.0.0.1:${E2E_HTTP_PORT:-47821}/mcp?kb=${kbname}",
      "enabled": true
    }
  }
}
EOF

    # Minimal AGENTS.md: system context for the agent (optional but useful)
    cat > "${sandbox_dir}/AGENTS.md" <<'EOF'
# Cartographer E2E test environment

You are a test agent. You have access exclusively to the `wiki` server's MCP tools.
Do not use the local filesystem: all operations must go through the MCP tools.
EOF

    echo "[sandbox] created in ${sandbox_dir} (kb=${kbname}, model=${E2E_MODEL})" >&2
}

# sandbox_create_auth <sandbox_dir> <kbname> <token>
#   Like sandbox_create, but adds the Authorization header for a server with auth enabled.
sandbox_create_auth() {
    local sandbox_dir="$1"
    local kbname="$2"
    local token="$3"

    if [[ -z "$sandbox_dir" || -z "$kbname" || -z "$token" ]]; then
        echo "[ERROR] sandbox_create_auth: missing arguments (sandbox_dir, kbname, token)" >&2
        return 1
    fi

    if [[ -z "${E2E_LLM_BASE_URL:-}" ]]; then
        echo "[ERROR] E2E_LLM_BASE_URL not set (OpenAI-compatible LLM endpoint)" >&2
        return 1
    fi

    mkdir -p "$sandbox_dir"

    cat > "${sandbox_dir}/opencode.jsonc" <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "model": "${E2E_MODEL}",
  "permission": {
    "edit": "allow",
    "bash": "allow",
    "read": "allow",
    "webfetch": "allow",
    "websearch": "allow"
  },
  "provider": {
    "opencode-go": {
      "options": {
        "baseURL": "${E2E_LLM_BASE_URL}"
      }
    }
  },
  "mcp": {
    "wiki": {
      "type": "remote",
      "url": "http://127.0.0.1:${E2E_HTTP_PORT:-47821}/mcp?kb=${kbname}",
      "enabled": true,
      "headers": {
        "Authorization": "Bearer ${token}"
      }
    }
  }
}
EOF

    cat > "${sandbox_dir}/AGENTS.md" <<'EOF'
# Cartographer E2E test environment

You are a test agent. You have access exclusively to the `wiki` server's MCP tools.
Do not use the local filesystem: all operations must go through the MCP tools.
EOF

    echo "[sandbox] created with auth in ${sandbox_dir} (kb=${kbname}, model=${E2E_MODEL})" >&2
}
