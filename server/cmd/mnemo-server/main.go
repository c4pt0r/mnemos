package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qiffang/mnemos/server/internal/config"
	"github.com/qiffang/mnemos/server/internal/db9zero"
	"github.com/qiffang/mnemos/server/internal/embed"
	"github.com/qiffang/mnemos/server/internal/handler"
	"github.com/qiffang/mnemos/server/internal/llm"
	"github.com/qiffang/mnemos/server/internal/middleware"
	"github.com/qiffang/mnemos/server/internal/repository"
	"github.com/qiffang/mnemos/server/internal/repository/db9"
	"github.com/qiffang/mnemos/server/internal/repository/tidb"
	"github.com/qiffang/mnemos/server/internal/service"
	"github.com/qiffang/mnemos/server/internal/tenant"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	var sqlDB *sql.DB
	var tenantRepo repository.TenantRepo

	if cfg.DBType == "db9" {
		sqlDB, err = db9.NewDB(cfg.DSN)
		if err != nil {
			logger.Error("failed to connect db9 database", "err", err)
			os.Exit(1)
		}
		// Initialize schema for db9
		if err := db9.InitSchema(sqlDB); err != nil {
			logger.Error("failed to initialize db9 schema", "err", err)
			os.Exit(1)
		}
		tenantRepo = db9.NewTenantRepo(sqlDB)
		logger.Info("connected to db9", "dsn_prefix", cfg.DSN[:min(50, len(cfg.DSN))]+"...")
	} else {
		sqlDB, err = tidb.NewDB(cfg.DSN)
		if err != nil {
			logger.Error("failed to connect tidb database", "err", err)
			os.Exit(1)
		}
		tenantRepo = tidb.NewTenantRepo(sqlDB)
		logger.Info("connected to tidb")
	}
	defer sqlDB.Close()

	// Embedder (nil if not configured → keyword-only search).
	embedder := embed.New(embed.Config{
		APIKey:  cfg.EmbedAPIKey,
		BaseURL: cfg.EmbedBaseURL,
		Model:   cfg.EmbedModel,
		Dims:    cfg.EmbedDims,
	})
	if cfg.EmbedAutoModel != "" {
		logger.Info("auto-embedding enabled (TiDB EMBED_TEXT)", "model", cfg.EmbedAutoModel, "dims", cfg.EmbedAutoDims)
	} else if embedder != nil {
		logger.Info("client-side embedding configured", "model", cfg.EmbedModel, "dims", cfg.EmbedDims)
	} else {
		logger.Info("no embedding configured, keyword-only search active")
	}
	// LLM client (nil if not configured → raw ingest mode).
	llmClient := llm.New(llm.Config{
		APIKey:      cfg.LLMAPIKey,
		BaseURL:     cfg.LLMBaseURL,
		Model:       cfg.LLMModel,
		Temperature: cfg.LLMTemperature,
	})
	if llmClient != nil {
		logger.Info("LLM configured for smart ingest", "model", cfg.LLMModel)
	} else {
		logger.Info("no LLM configured, ingest will use raw mode")
	}

	// Tenant pool.
	tenantPool := tenant.NewPool(tenant.PoolConfig{
		MaxIdle:     cfg.TenantPoolMaxIdle,
		MaxOpen:     cfg.TenantPoolMaxOpen,
		IdleTimeout: cfg.TenantPoolIdleTimeout,
		TotalLimit:  cfg.TenantPoolTotalLimit,
	})
	defer tenantPool.Close()

	// Services.
	var zeroClient *tenant.ZeroClient
	var db9Client *db9zero.Client

	if cfg.DBType == "db9" && cfg.DB9APIKey != "" {
		db9Client = db9zero.NewClient(db9zero.Config{
			BaseURL: cfg.DB9APIURL,
			APIKey:  cfg.DB9APIKey,
		})
		logger.Info("db9 provisioning enabled", "api_url", cfg.DB9APIURL)
	} else if cfg.TiDBZeroEnabled && cfg.DBType != "db9" {
		zeroClient = tenant.NewZeroClient(cfg.TiDBZeroAPIURL)
	}
	tenantSvc := service.NewTenantService(tenantRepo, zeroClient, db9Client, tenantPool, logger)

	// Middleware.
	tenantMW := middleware.ResolveTenant(tenantRepo, tenantPool)
	rl := middleware.NewRateLimiter(cfg.RateLimit, cfg.RateBurst)
	defer rl.Stop()
	rateMW := rl.Middleware()

	// Handler.
	srv := handler.NewServer(tenantSvc, embedder, llmClient, cfg.EmbedAutoModel, service.IngestMode(cfg.IngestMode), cfg.DBType, logger)
	router := srv.Router(tenantMW, rateMW)

	httpSrv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			logger.Error("shutdown error", "err", err)
		}
	}()

	logger.Info("starting mnemo server", "port", cfg.Port, "db_type", cfg.DBType)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}
