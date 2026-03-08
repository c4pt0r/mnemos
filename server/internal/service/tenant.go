package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/qiffang/mnemos/server/internal/db9zero"
	"github.com/qiffang/mnemos/server/internal/domain"
	"github.com/qiffang/mnemos/server/internal/repository"
	"github.com/qiffang/mnemos/server/internal/tenant"
)

const (
	tenantMemorySchema = `CREATE TABLE IF NOT EXISTS memories (
	    id              VARCHAR(36)     PRIMARY KEY,
	    content         TEXT            NOT NULL,
	    source          VARCHAR(100),
	    tags            JSON,
	    metadata        JSON,
	    embedding       VECTOR(1536)    NULL,
	    memory_type     VARCHAR(20)     NOT NULL DEFAULT 'pinned',
	    agent_id        VARCHAR(100)    NULL,
	    session_id      VARCHAR(100)    NULL,
	    state           VARCHAR(20)     NOT NULL DEFAULT 'active',
	    version         INT             DEFAULT 1,
	    updated_by      VARCHAR(100),
	    superseded_by   VARCHAR(36)     NULL,
	    created_at      TIMESTAMP       DEFAULT CURRENT_TIMESTAMP,
	    updated_at      TIMESTAMP       DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	    INDEX idx_memory_type         (memory_type),
	    INDEX idx_source              (source),
	    INDEX idx_state               (state),
	    INDEX idx_agent               (agent_id),
	    INDEX idx_session             (session_id),
	    INDEX idx_updated             (updated_at)
	)`
)

type TenantService struct {
	tenants repository.TenantRepo
	zero    *tenant.ZeroClient
	db9     *db9zero.Client
	pool    *tenant.TenantPool
	logger  *slog.Logger
}

func NewTenantService(
	tenants repository.TenantRepo,
	zero *tenant.ZeroClient,
	db9Client *db9zero.Client,
	pool *tenant.TenantPool,
	logger *slog.Logger,
) *TenantService {
	return &TenantService{tenants: tenants, zero: zero, db9: db9Client, pool: pool, logger: logger}
}

// ProvisionResult is the output of Provision.
type ProvisionResult struct {
	ID       string `json:"id"`
	ClaimURL string `json:"claim_url,omitempty"`
}

// Provision creates a new database instance and registers it as a tenant.
// Supports both TiDB Zero and db9 backends.
func (s *TenantService) Provision(ctx context.Context) (*ProvisionResult, error) {
	// Try db9 first if configured
	if s.db9 != nil {
		return s.provisionDB9(ctx)
	}

	// Fall back to TiDB Zero
	if s.zero != nil {
		return s.provisionTiDBZero(ctx)
	}

	return nil, &domain.ValidationError{Message: "provisioning disabled (no backend configured)"}
}

// provisionDB9 creates a new db9 database instance.
func (s *TenantService) provisionDB9(ctx context.Context) (*ProvisionResult, error) {
	// Generate a unique database name
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	dbName := fmt.Sprintf("mem9s-%s", hex.EncodeToString(randomBytes))

	db, err := s.db9.CreateDatabase(ctx, dbName)
	if err != nil {
		return nil, fmt.Errorf("provision db9 database: %w", err)
	}

	// Parse connection string to extract host, port, user, password
	// Format: postgresql://dbid.admin:password@pg.db9.io:5433/postgres
	host, port, user, password := parseDB9ConnectionString(db.ConnectionString)

	t := &domain.Tenant{
		ID:            db.ID,
		Name:          dbName,
		DBHost:        host,
		DBPort:        port,
		DBUser:        user,
		DBPassword:    password,
		DBName:        "postgres",
		DBTLS:         true,
		Provider:      "db9",
		ClusterID:     db.ID,
		ClaimURL:      "", // db9 databases are claimed server-side
		Status:        domain.TenantProvisioning,
		SchemaVersion: 0,
	}
	if err := s.tenants.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create tenant record: %w", err)
	}

	// Initialize schema via db9 SQL API
	if err := s.initDB9Schema(ctx, db.ID); err != nil {
		if s.logger != nil {
			s.logger.Error("tenant schema init failed", "tenant_id", db.ID, "err", err)
		}
		return nil, fmt.Errorf("init tenant schema: %w", err)
	}

	if err := s.tenants.UpdateStatus(ctx, db.ID, domain.TenantActive); err != nil {
		return nil, fmt.Errorf("activate tenant: %w", err)
	}
	if err := s.tenants.UpdateSchemaVersion(ctx, db.ID, 1); err != nil {
		return nil, fmt.Errorf("update schema version: %w", err)
	}

	return &ProvisionResult{
		ID:       db.ID,
		ClaimURL: "", // No claim needed for db9
	}, nil
}

// initDB9Schema initializes the memory table schema via db9 SQL API.
func (s *TenantService) initDB9Schema(ctx context.Context, dbID string) error {
	schema := `CREATE TABLE IF NOT EXISTS memories (
		id              TEXT            PRIMARY KEY,
		content         TEXT            NOT NULL,
		source          TEXT,
		tags            JSONB,
		metadata        JSONB,
		embedding       VECTOR(768),
		memory_type     TEXT            NOT NULL DEFAULT 'pinned',
		agent_id        TEXT,
		session_id      TEXT,
		state           TEXT            NOT NULL DEFAULT 'active',
		version         INT             DEFAULT 1,
		updated_by      TEXT,
		superseded_by   TEXT,
		created_at      TIMESTAMPTZ     DEFAULT NOW(),
		updated_at      TIMESTAMPTZ     DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_memory_type ON memories(memory_type);
	CREATE INDEX IF NOT EXISTS idx_source ON memories(source);
	CREATE INDEX IF NOT EXISTS idx_state ON memories(state);
	CREATE INDEX IF NOT EXISTS idx_agent ON memories(agent_id);
	CREATE INDEX IF NOT EXISTS idx_session ON memories(session_id);
	CREATE INDEX IF NOT EXISTS idx_updated ON memories(updated_at DESC);
	CREATE INDEX IF NOT EXISTS idx_tags ON memories USING GIN(tags);`

	return s.db9.ExecuteSQL(ctx, dbID, schema)
}

// parseDB9ConnectionString extracts components from a db9 connection string.
// Format: postgresql://dbid.admin:password@pg.db9.io:5433/postgres
func parseDB9ConnectionString(connStr string) (host string, port int, user, password string) {
	// Default values
	host = "pg.db9.io"
	port = 5433
	user = "admin"
	password = ""

	// Remove protocol prefix
	connStr = strings.TrimPrefix(connStr, "postgresql://")
	connStr = strings.TrimPrefix(connStr, "postgres://")

	// Split user:pass@host:port/db
	if atIdx := strings.Index(connStr, "@"); atIdx > 0 {
		userPass := connStr[:atIdx]
		hostPart := connStr[atIdx+1:]

		// Parse user:password
		if colonIdx := strings.Index(userPass, ":"); colonIdx > 0 {
			user = userPass[:colonIdx]
			password = userPass[colonIdx+1:]
		} else {
			user = userPass
		}

		// Parse host:port/db
		if slashIdx := strings.Index(hostPart, "/"); slashIdx > 0 {
			hostPort := hostPart[:slashIdx]
			if colonIdx := strings.LastIndex(hostPort, ":"); colonIdx > 0 {
				host = hostPort[:colonIdx]
				fmt.Sscanf(hostPort[colonIdx+1:], "%d", &port)
			} else {
				host = hostPort
			}
		}
	}

	return host, port, user, password
}

// provisionTiDBZero creates a new TiDB Zero instance.
func (s *TenantService) provisionTiDBZero(ctx context.Context) (*ProvisionResult, error) {
	instance, err := s.zero.CreateInstance(ctx, "mem9s")
	if err != nil {
		return nil, fmt.Errorf("provision TiDB Zero instance: %w", err)
	}

	tenantID := instance.ID

	t := &domain.Tenant{
		ID:            tenantID,
		Name:          tenantID,
		DBHost:        instance.Host,
		DBPort:        instance.Port,
		DBUser:        instance.Username,
		DBPassword:    instance.Password,
		DBName:        "test",
		DBTLS:         true,
		Provider:      "tidb_zero",
		ClusterID:     instance.ID,
		ClaimURL:      instance.ClaimURL,
		Status:        domain.TenantProvisioning,
		SchemaVersion: 0,
	}
	if err := s.tenants.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create tenant record: %w", err)
	}

	if err := s.initSchema(ctx, t); err != nil {
		if s.logger != nil {
			s.logger.Error("tenant schema init failed", "tenant_id", tenantID, "err", err)
		}
		return nil, fmt.Errorf("init tenant schema: %w", err)
	}

	if err := s.tenants.UpdateStatus(ctx, tenantID, domain.TenantActive); err != nil {
		return nil, fmt.Errorf("activate tenant: %w", err)
	}
	if err := s.tenants.UpdateSchemaVersion(ctx, tenantID, 1); err != nil {
		return nil, fmt.Errorf("update schema version: %w", err)
	}

	return &ProvisionResult{
		ID:       tenantID,
		ClaimURL: instance.ClaimURL,
	}, nil
}

// GetInfo returns tenant info including agent and memory counts.
func (s *TenantService) GetInfo(ctx context.Context, tenantID string) (*domain.TenantInfo, error) {
	t, err := s.tenants.GetByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	if s.pool == nil {
		return nil, fmt.Errorf("tenant pool not configured")
	}
	db, err := s.pool.Get(ctx, tenantID, t.DSN())
	if err != nil {
		return nil, err
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories").Scan(&count); err != nil {
		return nil, err
	}

	return &domain.TenantInfo{
		TenantID:    t.ID,
		Name:        t.Name,
		Status:      t.Status,
		Provider:    t.Provider,
		ClaimURL:    t.ClaimURL,
		MemoryCount: count,
		CreatedAt:   t.CreatedAt,
	}, nil
}

func (s *TenantService) initSchema(ctx context.Context, t *domain.Tenant) error {
	if s.pool == nil {
		return fmt.Errorf("tenant pool not configured")
	}
	db, err := s.pool.Get(ctx, t.ID, t.DSN())
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, tenantMemorySchema); err != nil {
		return fmt.Errorf("init tenant schema: memories: %w", err)
	}
	return nil
}
