<p align="center">
  <img src="assets/logo.png" alt="mnemos" width="180" />
</p>

<h1 align="center">mnemos</h1>

<p align="center">
  <strong>Persistent Memory for AI Agents.</strong><br/>
  Your agents forget everything between sessions. mnemos fixes that.
</p>

<p align="center">
  <a href="https://db9.ai"><img src="https://img.shields.io/badge/Powered%20by-db9-4A90E2?style=flat" alt="Powered by db9"></a>
  <a href="https://goreportcard.com/report/github.com/qiffang/mnemos/server"><img src="https://goreportcard.com/badge/github.com/qiffang/mnemos/server" alt="Go Report Card"></a>
  <a href="https://github.com/qiffang/mnemos/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/qiffang/mnemos"><img src="https://img.shields.io/github/stars/qiffang/mnemos?style=social" alt="Stars"></a>
</p>

---

## 🚀 Quick Start

### Option A: Hosted (Recommended)

Use the hosted service at `https://mem-api.db9.ai` — no server setup required.

```bash
# 1. Provision a tenant
curl -s -X POST https://mem-api.db9.ai/v1alpha1/mem9s
# → {"id":"...", "claim_url":"..."}

# 2. Configure your agent
export MNEMO_API_URL="https://mem-api.db9.ai"
export MNEMO_TENANT_ID="..."
```

### Option B: Self-Hosted

```bash
# 1. Deploy mnemo-server with db9
cd server
export MNEMO_DSN="postgresql://user:pass@pg.db9.io:5433/postgres"
export MNEMO_DB_TYPE="db9"
go run ./cmd/mnemo-server
```

**2. Install plugin for your agent (pick one):**

| Platform | Install |
|----------|---------|
| **Claude Code** | `/plugin marketplace add qiffang/mnemos` then `/plugin install mnemo-memory@mnemos` |
| **OpenCode** | Add `"plugin": ["mnemo-opencode"]` to `opencode.json` |
| **OpenClaw** | Add `mnemo` to `openclaw.json` plugins (see [openclaw-plugin/README](openclaw-plugin/README.md)) |

All agents pointing at the same tenant ID share one memory pool.

---

## The Problem

AI coding agents — Claude Code, OpenCode, OpenClaw, and others — often maintain separate local memory files. The result:

- 🧠 **Amnesia** — Agent forgets everything when a session ends
- 🏝️ **Silos** — One agent can't access what another learned yesterday
- 📁 **Local files** — Memory is tied to a single machine, lost when you switch devices
- 🚫 **No team sharing** — Your teammate's agent can't benefit from your agent's discoveries

**mnemos** gives every agent a shared, cloud-persistent memory with hybrid vector + keyword search — powered by [db9](https://db9.ai).

## Why db9?

mnemos uses [db9](https://db9.ai) as the backing store for mnemo-server:

| Feature | What it means for you |
|---|---|
| **Free tier** | Generous free quota for individual and small team use |
| **PostgreSQL compatible** | Use familiar SQL, tools, and drivers |
| **Native VECTOR type** | Hybrid search (vector + keyword) without a separate vector database |
| **Chinese FTS (jieba)** | Full-text search with Chinese tokenization out of the box |
| **JSONB support** | Flexible metadata and tag storage |
| **Zero ops** | No servers to manage, no scaling to worry about |
| **fs9 extension** | Import CSV/Parquet files directly via SQL |

This architecture keeps agent plugins **stateless** — all state lives in mnemo-server, backed by db9.

## Supported Agents

mnemos provides native plugins for major AI coding agent platforms:

| Platform | Plugin | How It Works | Install Guide |
|---|---|---|---|
| **Claude Code** | Hooks + Skills | Auto-loads memories on session start, auto-saves on stop | [`claude-plugin/README.md`](claude-plugin/README.md) |
| **OpenCode** | Plugin SDK | `system.transform` injects memories, `session.idle` auto-captures | [`opencode-plugin/README.md`](opencode-plugin/README.md) |
| **OpenClaw** | Memory Plugin | Replaces built-in memory slot (`kind: "memory"`), framework manages lifecycle | [`openclaw-plugin/README.md`](openclaw-plugin/README.md) |
| **Any HTTP client** | REST API | `curl` to mnemo-server | [API Reference](#api-reference) |

All plugins expose the same 5 tools: `memory_store`, `memory_search`, `memory_get`, `memory_update`, `memory_delete`.

> **🤖 For AI Agents**: Use the Quick Start above to deploy mnemo-server and provision a tenant ID, then follow the platform-specific README for configuration details.

## Stateless Agents, Cloud Memory

A key design principle: **agent plugins carry zero state.** All memory lives in mnemo-server, backed by db9. This means:

- **Agent plugins stay stateless** — deploy any number of agent instances freely; they all share the same memory pool via mnemo-server
- **Switch machines freely** — your agent's memory follows you, not your laptop
- **Multi-agent collaboration** — Claude Code, OpenCode, OpenClaw, and any HTTP client share memories when pointed at the same server
- **Centralized control** — rate limits and audit live in one place

## API Reference

Agent identity: `X-Mnemo-Agent-Id` header.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1alpha1/mem9s` | Provision tenant (no auth). Returns `{ "id", "claim_url" }`. |
| `POST` | `/v1alpha1/mem9s/{tenantID}/memories` | Create/upsert. Server generates embedding if configured. |
| `GET` | `/v1alpha1/mem9s/{tenantID}/memories` | Search: `?q=`, `?tags=`, `?source=`, `?key=`, `?limit=`, `?offset=` |
| `GET` | `/v1alpha1/mem9s/{tenantID}/memories/:id` | Get single memory |
| `PUT` | `/v1alpha1/mem9s/{tenantID}/memories/:id` | Update. Optional `If-Match` for version check. |
| `DELETE` | `/v1alpha1/mem9s/{tenantID}/memories/:id` | Delete |
| `POST` | `/v1alpha1/mem9s/{tenantID}/memories/ingest` | Ingest content for embedding + storage |
| `POST` | `/v1alpha1/mem9s/{tenantID}/memories/bulk` | Bulk create (max 100) |
| `GET` | `/v1alpha1/mem9s/{tenantID}/memories/bootstrap` | Bootstrap memories for agent startup |
| `GET` | `/v1alpha1/mem9s/{tenantID}/info` | Tenant metadata |

## Self-Hosting

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MNEMO_DSN` | Yes | — | Database connection string (PostgreSQL format for db9) |
| `MNEMO_DB_TYPE` | No | `tidb` | Database type: `db9` or `tidb` |
| `MNEMO_PORT` | No | `8080` | HTTP listen port |
| `MNEMO_RATE_LIMIT` | No | `100` | Requests/sec per IP |
| `MNEMO_RATE_BURST` | No | `200` | Burst size |
| `MNEMO_EMBED_API_KEY` | No | — | Embedding provider API key |
| `MNEMO_EMBED_BASE_URL` | No | OpenAI | Custom embedding endpoint (e.g., Ollama) |
| `MNEMO_EMBED_MODEL` | No | `text-embedding-3-small` | Model name |
| `MNEMO_EMBED_DIMS` | No | `1536` | Vector dimensions |

### Build & Run

```bash
cd server
go build -o mnemo-server ./cmd/mnemo-server

# With db9
export MNEMO_DSN="postgresql://user:pass@pg.db9.io:5433/postgres"
export MNEMO_DB_TYPE="db9"
./mnemo-server

# With local Ollama for embeddings
export MNEMO_EMBED_BASE_URL="http://localhost:11434/v1"
export MNEMO_EMBED_MODEL="nomic-embed-text"
export MNEMO_EMBED_DIMS="768"
./mnemo-server
```

### Docker

```bash
docker build -t mnemo-server ./server
docker run -e MNEMO_DSN="postgresql://..." -e MNEMO_DB_TYPE="db9" -p 8080:8080 mnemo-server
```

## Project Structure

```
mnemos/
├── server/                     # Go API server
│   ├── cmd/mnemo-server/       # Entry point
│   ├── internal/
│   │   ├── config/             # Env var config loading
│   │   ├── domain/             # Core types, errors, token generation
│   │   ├── embed/              # Embedding provider (OpenAI/Ollama/any)
│   │   ├── handler/            # HTTP handlers + chi router
│   │   ├── middleware/         # Auth + rate limiter
│   │   ├── repository/         # Interface + db9/TiDB implementations
│   │   │   ├── db9/            # PostgreSQL (db9) backend
│   │   │   └── tidb/           # TiDB/MySQL backend
│   │   └── service/            # Business logic (upsert, LWW, hybrid search)
│   ├── schema.sql
│   └── Dockerfile
│
├── opencode-plugin/            # OpenCode agent plugin (TypeScript)
│   └── src/                    # Plugin SDK tools + hooks + server backend
│
├── openclaw-plugin/            # OpenClaw agent plugin (TypeScript)
│   ├── index.ts                # Tool registration
│   └── server-backend.ts       # Server: fetch → mnemo API
│
├── claude-plugin/              # Claude Code plugin (Hooks + Skills)
│   ├── hooks/                  # Lifecycle hooks (bash + curl)
│   └── skills/                 # memory-recall + memory-store + mnemos-setup
│
├── skills/                     # Shared skills (OpenClaw ClawHub format)
│   └── mnemos-setup/           # Setup skill
│
└── docs/DESIGN.md              # Full design document
```

## Roadmap

| Phase | What | Status |
|-------|------|--------|
| **Phase 1** | Core server + CRUD + auth + hybrid search + upsert + plugins | ✅ Done |
| **Phase 2** | db9 backend support | ✅ Done |
| **Phase 3** | LLM-assisted conflict merge, auto-tagging | 🔜 Planned |
| **Phase 4** | Web dashboard, bulk import/export, CLI wizard | 📋 Planned |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

[Apache-2.0](LICENSE)

---

<p align="center">
  <a href="https://db9.ai"><img src="https://db9.ai/logo.svg" alt="db9" height="36" /></a>
  <br/>
  <sub>Built with <a href="https://db9.ai">db9</a> — PostgreSQL with native vector search and Chinese FTS.</sub>
</p>
