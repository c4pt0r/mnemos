package db9

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// NewDB creates a configured *sql.DB connection pool for db9 (PostgreSQL).
func NewDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// InitSchema creates required tables and indexes if they don't exist.
func InitSchema(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create vector extension
	_, _ = db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`)

	// Create memories table
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			source TEXT,
			tags JSONB DEFAULT '[]'::jsonb,
			metadata JSONB,
			embedding VECTOR(1536),
			memory_type TEXT DEFAULT 'pinned',
			agent_id TEXT,
			session_id TEXT,
			state TEXT DEFAULT 'active',
			version INTEGER DEFAULT 1,
			updated_by TEXT,
			superseded_by TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("create memories table: %w", err)
	}

	// Create indexes
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_state ON memories(state)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_tags ON memories USING gin(tags)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_embedding ON memories USING hnsw(embedding vector_cosine_ops)`,
	}
	for _, idx := range indexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			// Ignore index creation errors (may already exist or not supported)
		}
	}

	// Create tenants table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			db_host TEXT,
			db_port INTEGER,
			db_user TEXT,
			db_password TEXT,
			db_name TEXT,
			db_tls BOOLEAN DEFAULT false,
			provider TEXT,
			cluster_id TEXT,
			claim_url TEXT,
			status TEXT DEFAULT 'provisioning',
			schema_version INTEGER DEFAULT 1,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)
	`)
	if err != nil {
		return fmt.Errorf("create tenants table: %w", err)
	}

	// Create tenant_tokens table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tenant_tokens (
			api_token TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL REFERENCES tenants(id),
			created_at TIMESTAMPTZ DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("create tenant_tokens table: %w", err)
	}

	return nil
}
