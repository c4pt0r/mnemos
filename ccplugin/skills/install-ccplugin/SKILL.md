---
name: install-ccplugin
description: "Install mnemo ccplugin for Claude Code. Sets up hooks, skills, and database connection so Claude Code automatically persists and recalls memories across sessions."
context: fork
allowed-tools: Bash, Read, Write, Edit
---

You are the installer for the mnemo Claude Code plugin. Follow these steps precisely.

## Prerequisites

The user must provide TiDB Serverless connection info:
- **Host** (e.g. `gateway01.us-east-1.prod.aws.tidbcloud.com`)
- **Username** (e.g. `xxxxx.root`)
- **Password**
- **Port** (default: 4000)
- **Database** (default: `mnemos`)

If any of these are missing, ask the user before proceeding.

## Installation Steps

### Step 1: Locate the ccplugin source

The ccplugin source lives in this repository:
```
<repo-root>/ccplugin/
├── hooks/
│   ├── common.sh
│   ├── hooks.json
│   ├── session-start.sh
│   ├── stop.sh
│   ├── user-prompt-submit.sh
│   └── session-end.sh
└── skills/
    ├── memory-recall/SKILL.md
    └── memory-store/SKILL.md
```

Find the absolute path to `ccplugin/` in the current repo. Store it as `$CCPLUGIN_DIR`.

### Step 2: Copy skills to ~/.claude/skills/

```bash
cp -r "$CCPLUGIN_DIR/skills/memory-recall" ~/.claude/skills/memory-recall
cp -r "$CCPLUGIN_DIR/skills/memory-store" ~/.claude/skills/memory-store
```

### Step 3: Add hooks to ~/.claude/settings.json

Read `~/.claude/settings.json`, then merge the following into it:

**Add to `env`** (replace values with user-provided credentials):
```json
{
  "MNEMO_DB_HOST": "<host>",
  "MNEMO_DB_USER": "<username>",
  "MNEMO_DB_PASS": "<password>",
  "MNEMO_DB_NAME": "mnemos"
}
```

**Add `hooks` key** (use absolute paths to the hook scripts):
```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "<CCPLUGIN_DIR>/hooks/session-start.sh"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "<CCPLUGIN_DIR>/hooks/user-prompt-submit.sh"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "<CCPLUGIN_DIR>/hooks/stop.sh",
            "timeout": 120
          }
        ]
      }
    ]
  }
}
```

**IMPORTANT**: Do NOT add `enabledPlugins` or modify `known_marketplaces.json` / `installed_plugins.json`. Hooks go directly in `settings.json`.

### Step 4: Make hook scripts executable

```bash
chmod +x "$CCPLUGIN_DIR"/hooks/*.sh
```

### Step 5: Create database and table

Use the TiDB HTTP SQL API to create the database and table. The API endpoint is `https://http-<HOST>/v1beta/sql`.

**IMPORTANT**: This dev endpoint ignores the `database` field in the request body, so always use fully-qualified table names like `mnemos.memories`.

```bash
# Create database
curl -sf --max-time 10 \
  -u "<USER>:<PASS>" \
  -H "Content-Type: application/json" \
  -d '{"database":"test","query":"CREATE DATABASE IF NOT EXISTS mnemos"}' \
  "https://http-<HOST>/v1beta/sql"

# Create table (use fully-qualified name)
curl -sf --max-time 10 \
  -u "<USER>:<PASS>" \
  -H "Content-Type: application/json" \
  -d '{"database":"test","query":"CREATE TABLE IF NOT EXISTS mnemos.memories (id VARCHAR(36) PRIMARY KEY, space_id VARCHAR(36) NOT NULL DEFAULT '\''default'\'', content TEXT NOT NULL, key_name VARCHAR(255), source VARCHAR(128), tags JSON, metadata JSON, embedding VECTOR(1536) NULL, version INT NOT NULL DEFAULT 1, updated_by VARCHAR(128), created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE INDEX idx_key (space_id, key_name), INDEX idx_space (space_id), INDEX idx_source (space_id, source), INDEX idx_updated (space_id, updated_at))"}' \
  "https://http-<HOST>/v1beta/sql"
```

### Step 6: Verify

1. Test database connectivity:
```bash
curl -sf --max-time 10 \
  -u "<USER>:<PASS>" \
  -H "Content-Type: application/json" \
  -d '{"database":"test","query":"SELECT COUNT(*) FROM mnemos.memories"}' \
  "https://http-<HOST>/v1beta/sql"
```

2. Test that Claude Code starts:
```bash
claude -p "say hi"
```
This should return a response within 15 seconds. If it hangs, check the hooks configuration.

3. Tell the user: "Installation complete. Restart Claude Code to activate memory. Use `/memory-store` to save memories and `/memory-recall` to search them."

## Troubleshooting

- **Claude hangs on startup**: The `read_stdin` function in `common.sh` uses `read -t 2` to avoid blocking. If still hanging, check that hook script paths are correct absolute paths.
- **Memories not saving**: The Stop hook saves when a session ends. For on-demand saving, use `/memory-store`.
- **Database field ignored**: The TiDB Serverless dev endpoint ignores the `database` field in HTTP API requests. Always use fully-qualified table names like `mnemos.memories` in SQL.
