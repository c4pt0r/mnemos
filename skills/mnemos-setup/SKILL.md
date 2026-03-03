---
name: mnemos-setup
description: |
  Use when setting up mnemos backend and plugins from scratch.
  Triggers: "set up mnemos", "deploy mnemo-server", "install mnemo plugin",
  "create mnemos database", "configure openclaw memory", "configure opencode memory",
  "configure claude code memory".
---

# mnemos Setup

End-to-end setup for mnemos backend (TiDB + mnemo-server) and agent plugins
(OpenClaw, OpenCode, Claude Code). Covers both direct mode and server mode.

## Prerequisites

- A TiDB Cloud Serverless cluster (free at [tidbcloud.com](https://tidbcloud.com))
- For server mode: a Linux host (EC2, VM, etc.) with Go 1.21+ installed
- Credentials: TiDB host, port, username, password

## Mode Decision

| Question | Direct Mode | Server Mode |
|----------|-------------|-------------|
| How many agents? | 1 (single developer) | 2+ (team, multi-agent) |
| Need space isolation? | No | Yes |
| Need CRDT conflict resolution? | No | Yes |
| Want zero deployment? | Yes | N/A |

**Direct mode**: Plugin talks to TiDB Serverless directly. No server needed.
**Server mode**: Plugin talks to mnemo-server (Go binary), which talks to TiDB.

## Phase 1: Database Setup

### 1.1 Create the database

```sql
CREATE DATABASE IF NOT EXISTS mnemos;
```

### 1.2 Create tables

Connect to the `mnemos` database, then run:

```sql
CREATE TABLE IF NOT EXISTS user_tokens (
  api_token     VARCHAR(64)   PRIMARY KEY,
  user_id       VARCHAR(36)   NOT NULL,
  user_name     VARCHAR(255)  NOT NULL,
  created_at    TIMESTAMP     DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_user (user_id)
);

CREATE TABLE IF NOT EXISTS space_tokens (
  api_token       VARCHAR(64)   PRIMARY KEY,
  space_id        VARCHAR(36)   NOT NULL,
  space_name      VARCHAR(255)  NOT NULL,
  agent_name      VARCHAR(100)  NOT NULL,
  agent_type      VARCHAR(50),
  user_id         VARCHAR(36)   NOT NULL DEFAULT '',
  workspace_key   VARCHAR(64)   NOT NULL DEFAULT '',
  created_at      TIMESTAMP     DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_space (space_id),
  INDEX idx_user_workspace (user_id, workspace_key)
);
```

### 1.3 Create memories table

**Two variants** depending on whether you want TiDB auto-embedding:

#### Variant A: Auto-embedding (recommended for TiDB Cloud Serverless)

TiDB generates embeddings server-side via `EMBED_TEXT()`. No OpenAI key needed.

```sql
CREATE TABLE IF NOT EXISTS memories (
  id          VARCHAR(36)     PRIMARY KEY,
  space_id    VARCHAR(36)     NOT NULL,
  content     TEXT            NOT NULL,
  key_name    VARCHAR(255),
  source      VARCHAR(100),
  tags        JSON,
  metadata    JSON,
  embedding   VECTOR(1024) GENERATED ALWAYS AS (
    EMBED_TEXT("tidbcloud_free/amazon/titan-embed-text-v2", content)
  ) STORED,
  version     INT             DEFAULT 1,
  updated_by  VARCHAR(100),
  created_at  TIMESTAMP       DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP       DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  vector_clock      JSON         NOT NULL,
  origin_agent      VARCHAR(64),
  tombstone         TINYINT(1)   NOT NULL DEFAULT 0,
  last_write_id     VARCHAR(36),
  last_write_snapshot JSON,
  last_write_status TINYINT,
  UNIQUE INDEX idx_key    (space_id, key_name),
  INDEX idx_space         (space_id),
  INDEX idx_source        (space_id, source),
  INDEX idx_updated       (space_id, updated_at),
  INDEX idx_tombstone     (space_id, tombstone)
);

ALTER TABLE memories SET TIFLASH REPLICA 1;
ALTER TABLE memories ADD VECTOR INDEX idx_cosine ((VEC_COSINE_DISTANCE(embedding)));
```

#### Variant B: Client-side embedding (or keyword-only)

Use when TiDB auto-embedding is unavailable, or when using OpenAI/Ollama for embeddings.

```sql
CREATE TABLE IF NOT EXISTS memories (
  id          VARCHAR(36)     PRIMARY KEY,
  space_id    VARCHAR(36)     NOT NULL,
  content     TEXT            NOT NULL,
  key_name    VARCHAR(255),
  source      VARCHAR(100),
  tags        JSON,
  metadata    JSON,
  embedding   VECTOR(1536)    NULL,
  version     INT             DEFAULT 1,
  updated_by  VARCHAR(100),
  created_at  TIMESTAMP       DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP       DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  vector_clock      JSON         NOT NULL,
  origin_agent      VARCHAR(64),
  tombstone         TINYINT(1)   NOT NULL DEFAULT 0,
  last_write_id     VARCHAR(36),
  last_write_snapshot JSON,
  last_write_status TINYINT,
  UNIQUE INDEX idx_key    (space_id, key_name),
  INDEX idx_space         (space_id),
  INDEX idx_source        (space_id, source),
  INDEX idx_updated       (space_id, updated_at),
  INDEX idx_tombstone     (space_id, tombstone)
);
```

Adjust `VECTOR(1536)` dimension to match your embedding model (1536 for OpenAI
`text-embedding-3-small`, 768 for Ollama `nomic-embed-text`, etc.).

### 1.4 Verify tables

```sql
SHOW TABLES;
-- Expected: memories, space_tokens, user_tokens

SHOW CREATE TABLE memories\G
-- Verify embedding column type and vector index
```

### Known DDL Gotchas

| Issue | Symptom | Fix |
|-------|---------|-----|
| JSON column default | `BLOB/TEXT/JSON column can't have a default value` | Omit `DEFAULT ('{}')` on `vector_clock`. The server always provides the value explicitly. |
| SSL mode | `SSL connection error: CA certificate is required` | Use `--ssl-mode=REQUIRED` (not `VERIFY_IDENTITY`) for TiDB Serverless staging. |
| EMBED_TEXT unavailable | `function EMBED_TEXT does not exist` | Use Variant B (client-side embedding). `EMBED_TEXT` is only available on TiDB Cloud Serverless. |

## Phase 2: Server Mode Setup (skip for direct mode)

### 2.1 Build the server

```bash
cd server
go build -o mnemo-server ./cmd/mnemo-server
```

Cross-compile for Linux (if building locally for a remote host):

```bash
cd server
GOOS=linux GOARCH=amd64 go build -o mnemo-server ./cmd/mnemo-server
```

### 2.2 Configure environment

Create a startup script (e.g. `/tmp/start-mnemo.sh`):

```bash
#!/bin/bash
export MNEMO_DSN='<user>:<pass>@tcp(<host>:<port>)/<dbname>?parseTime=true&tls=skip-verify'
export MNEMO_PORT='18081'

# Auto-embedding (TiDB Cloud Serverless only):
export MNEMO_EMBED_AUTO_MODEL='tidbcloud_free/amazon/titan-embed-text-v2'
export MNEMO_EMBED_AUTO_DIMS='1024'

# OR client-side embedding (OpenAI):
# export MNEMO_EMBED_API_KEY='sk-...'
# export MNEMO_EMBED_MODEL='text-embedding-3-small'
# export MNEMO_EMBED_DIMS='1536'

exec /path/to/mnemo-server
```

**DSN format**: `<user>:<pass>@tcp(<host>:<port>)/<dbname>?parseTime=true&tls=skip-verify`

### 2.3 Start the server

```bash
nohup bash /tmp/start-mnemo.sh > /tmp/mnemo-server.log 2>&1 &
```

### 2.4 Verify server health

```bash
curl -sf http://127.0.0.1:18081/healthz
# Expected: {"status":"ok"}
```

Check logs for successful startup:

```bash
cat /tmp/mnemo-server.log | head -5
# Expected:
# {"level":"INFO","msg":"auto-embedding enabled (TiDB EMBED_TEXT)","model":"..."}
# {"level":"INFO","msg":"starting mnemo server","port":"18081"}
```

### 2.5 Create a user token (server mode only)

The bootstrap endpoint requires no auth:

```bash
curl -s -X POST http://127.0.0.1:18081/api/users \
  -H "Content-Type: application/json" \
  -d '{"name":"my-agent-user"}' | jq .
# Returns: {"ok":true, "user_id":"...", "api_token":"mnemo_..."}
```

Save the `api_token` value -- this is the **user token** used by plugins.

### 2.6 End-to-end server verification

```bash
# Create a test space
curl -s -X POST http://127.0.0.1:18081/api/spaces \
  -H "Content-Type: application/json" \
  -d '{"name":"test","agent_name":"test","agent_type":"test"}' | jq .

# Store a memory (use the api_token from the space creation response)
TOKEN="mnemo_<from_above>"
curl -s -X POST http://127.0.0.1:18081/api/memories \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"content":"test memory","key":"test-key","tags":["test"]}' | jq .

# Search
curl -s "http://127.0.0.1:18081/api/memories?q=test&limit=5" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

## Phase 3: Plugin Configuration

### OpenClaw Plugin

#### Server mode (recommended for teams)

In `~/.openclaw/openclaw.json`:

```json
{
  "plugins": {
    "load": {
      "paths": ["/path/to/mnemos/openclaw-plugin"]
    },
    "slots": {
      "memory": "mnemo"
    },
    "entries": {
      "mnemo": {
        "enabled": true,
        "config": {
          "apiUrl": "http://<server-host>:18081",
          "userToken": "mnemo_<user_token_from_phase_2.5>"
        }
      }
    }
  }
}
```

**IMPORTANT**: The config field is `userToken`, NOT `apiToken`. The plugin uses
the user token to auto-provision workspace-scoped space tokens via
`POST /api/spaces/provision`.

#### Direct mode

```json
{
  "plugins": {
    "slots": { "memory": "mnemo" },
    "entries": {
      "mnemo": {
        "enabled": true,
        "config": {
          "host": "<tidb-host>",
          "username": "<tidb-user>",
          "password": "<tidb-pass>",
          "database": "mnemos",
          "autoEmbedModel": "tidbcloud_free/amazon/titan-embed-text-v2",
          "autoEmbedDims": 1024
        }
      }
    }
  }
}
```

#### Verify OpenClaw

Restart the gateway, then check logs:

```bash
openclaw daemon restart
# Check logs for:
#   [mnemo] Server mode (user token + workspace isolation)
# or:
#   [mnemo] Direct mode (auto-embedding: tidbcloud_free/amazon/titan-embed-text-v2)
```

**If you see** `[mnemo] Server mode requires apiUrl and userToken. Plugin disabled.`:
the config has `apiToken` instead of `userToken`. Fix the field name and restart.

**The warning** `plugin id mismatch (manifest uses "mnemo", entry hints "mnemo-openclaw")`
is cosmetic and harmless. The npm package name is `mnemo-openclaw`, the plugin ID is `mnemo`.

### OpenCode Plugin

In `opencode.json`:

```json
{
  "plugin": ["mnemo-opencode"]
}
```

Set environment variables:

**Direct mode**:
```bash
export MNEMO_DB_HOST="<tidb-host>"
export MNEMO_DB_USER="<tidb-user>"
export MNEMO_DB_PASS="<tidb-pass>"
```

**Server mode**:
```bash
export MNEMO_API_URL="http://<server-host>:18081"
export MNEMO_API_TOKEN="mnemo_<space_token>"
```

### Claude Code Plugin

**Marketplace install** (recommended):

```
/plugin marketplace add qiffang/mnemos
/plugin install mnemo-memory@mnemos
```

Then add credentials to `~/.claude/settings.json`:

**Direct mode**:
```json
{
  "env": {
    "MNEMO_DB_HOST": "<tidb-host>",
    "MNEMO_DB_USER": "<tidb-user>",
    "MNEMO_DB_PASS": "<tidb-pass>",
    "MNEMO_DB_NAME": "mnemos"
  }
}
```

**Server mode**:
```json
{
  "env": {
    "MNEMO_API_URL": "http://<server-host>:18081",
    "MNEMO_API_TOKEN": "mnemo_<space_token>"
  }
}
```

Restart Claude Code after configuration.

## Troubleshooting

| Problem | Cause | Fix |
|---------|-------|-----|
| `Server mode requires apiUrl and userToken` | Config uses `apiToken` instead of `userToken` | Change field name to `userToken` in plugin config |
| `BLOB/TEXT/JSON column can't have a default value` | TiDB strict mode rejects `DEFAULT` on JSON columns | Omit the DEFAULT clause; server provides values explicitly |
| `SSL connection error: CA certificate is required` | Wrong SSL mode for TiDB Serverless | Use `tls=skip-verify` in DSN or `--ssl-mode=REQUIRED` in mysql CLI |
| `function EMBED_TEXT does not exist` | Not on TiDB Cloud Serverless | Use client-side embedding (Variant B) or no embedding |
| `plugin id mismatch` warning | npm name vs plugin ID differ | Harmless. npm name is `mnemo-openclaw`, plugin ID is `mnemo` |
| Server starts but memory_store fails | Wrong DSN format or missing `parseTime=true` | DSN must include `?parseTime=true` |
| Vector search returns 0 results | TiFlash replica not ready | Wait a few minutes after `SET TIFLASH REPLICA 1`, or check `SHOW TABLE memories REGIONS` |
| Memory stored but search score is low | Content too short for meaningful embedding | Expected for short test strings. Real content gets higher scores. |
