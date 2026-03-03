-- Direct-mode schema for mnemos.
-- This is the single source of truth for the memories table used by all
-- direct-mode plugins (openclaw-plugin/schema.ts,
-- opencode-plugin/src/direct-backend.ts, claude-plugin/hooks/common.sh).
--
-- The embedding column is NOT listed here because its definition varies by
-- plugin config (dimension, optional GENERATED ALWAYS AS for auto-embedding).
-- Each plugin constructs that column itself and splices it in.
--
-- Run `make check-schema` to verify all plugin copies match this file.
--
-- Server-mode schema lives in server/schema.sql and evolves independently.

CREATE TABLE IF NOT EXISTS memories (
  id          VARCHAR(36)       PRIMARY KEY,
  space_id    VARCHAR(36)       NOT NULL,
  content     TEXT              NOT NULL,
  key_name    VARCHAR(255),
  source      VARCHAR(100),
  tags        JSON,
  metadata    JSON,
  -- embedding column inserted here by each plugin (dimension + type vary)
  version     INT               DEFAULT 1,
  updated_by  VARCHAR(100),
  created_at  TIMESTAMP         DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP         DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE INDEX idx_key    (space_id, key_name),
  INDEX idx_space         (space_id),
  INDEX idx_source        (space_id, source),
  INDEX idx_updated       (space_id, updated_at)
);
