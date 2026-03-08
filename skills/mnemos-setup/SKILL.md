---
name: mnemos-setup
description: |
  Setup mnemos persistent memory with mnemo-server.
  Triggers: "set up mnemos", "install mnemo plugin", "configure memory plugin",
  "configure openclaw memory", "configure opencode memory",
  "configure claude code memory".
---

# mnemos Setup

**Persistent memory for AI agents.** This skill helps you set up mnemos with any agent platform.

## Prerequisites

You need a mnemo-server instance. Options:

1. **Hosted (recommended):** Use `https://mem-api.db9.ai` — no setup required
2. **Self-hosted:** Deploy your own server (see below)

## Option A: Use Hosted Server (mem-api.db9.ai)

Skip to **Step 2** and use `https://mem-api.db9.ai` as your `apiUrl`.

## Option B: Self-hosted — Deploy mnemo-server

```bash
cd mnemos/server
MNEMO_DSN="user:pass@tcp(host:4000)/mnemos?parseTime=true" go run ./cmd/mnemo-server
```

## Step 2: Provision a tenant

```bash
curl -s -X POST http://localhost:8080/v1alpha1/mem9s | jq .
# → { "id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", "claim_url": "..." }
```

Save the returned `id` — this is your tenant ID used in all subsequent API calls.

## Step 3: Configure your agent platform

Pick your platform and follow the instructions:

---

#### OpenClaw

Add to `openclaw.json`:

```json
{
  "plugins": {
    "slots": { "memory": "mnemo" },
    "entries": {
      "mnemo": {
        "enabled": true,
        "config": {
          "apiUrl": "https://mem-api.db9.ai",
          "tenantID": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
        }
      }
    }
  }
}
```

Restart OpenClaw. You should see:
```
[mnemo] Server mode (tenant-scoped mem9 API)
```

---

#### OpenCode

Set environment variables (add to shell profile or `.env`):

```bash
export MNEMO_API_URL="https://mem-api.db9.ai"
export MNEMO_TENANT_ID="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
```

Add to `opencode.json`:
```json
{
  "plugin": ["mnemo-opencode"]
}
```

Restart OpenCode. You should see:
```
[mnemo] Server mode (mnemo-server REST API)
```

---

#### Claude Code

Add to `~/.claude/settings.json`:

```json
{
  "env": {
    "MNEMO_API_URL": "https://mem-api.db9.ai",
    "MNEMO_TENANT_ID": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
  }
}
```

Install plugin:
```
/plugin marketplace add qiffang/mnemos
/plugin install mnemo-memory@mnemos
```

Restart Claude Code.

---

## Verification

After setup, test memory:

1. Ask your agent: "Remember that the project uses PostgreSQL 15"
2. Start a new session
3. Ask: "What database does this project use?"

The agent should recall the information from memory.

---

## Troubleshooting

| Problem | Fix |
|---------|-----|
| `No MNEMO_API_URL configured` | Set `MNEMO_API_URL` env var or `apiUrl` in plugin config |
| `MNEMO_TENANT_ID is not set` | Set `MNEMO_TENANT_ID` env var or `tenantID` in plugin config |
| Plugin not loading | Check platform-specific config format |
