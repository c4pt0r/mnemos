package db9

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
	"github.com/qiffang/mnemos/server/internal/domain"
)

type MemoryRepo struct {
	db           *sql.DB
	autoModel    string
	ftsAvailable atomic.Bool
}

func NewMemoryRepo(db *sql.DB, autoModel string) *MemoryRepo {
	r := &MemoryRepo{db: db, autoModel: autoModel}
	// Probe FTS availability in background
	go func() {
		if err := probeFTS(db); err != nil {
			slog.Error("FTS probe failed — full-text search will be unavailable", "err", err)
			return
		}
		slog.Info("FTS probe succeeded — full-text search is available")
		r.ftsAvailable.Store(true)
	}()
	return r
}

// probeFTS checks if jieba FTS is available
func probeFTS(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `SELECT to_tsvector('jieba', 'test')`)
	return err
}

func (r *MemoryRepo) FTSAvailable() bool { return r.ftsAvailable.Load() }

const allColumns = `id, content, source, tags, metadata, embedding, memory_type, agent_id, session_id, state, version, updated_by, created_at, updated_at, superseded_by`

func (r *MemoryRepo) Create(ctx context.Context, m *domain.Memory) error {
	tagsJSON := marshalTags(m.Tags)
	memoryType := string(m.MemoryType)
	if memoryType == "" {
		memoryType = string(domain.TypePinned)
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO memories (id, content, source, tags, metadata, embedding, memory_type, agent_id, session_id, state, version, updated_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active', $10, $11, NOW(), NOW())`,
		m.ID, m.Content, nullString(m.Source),
		tagsJSON, nullJSON(m.Metadata), vecToString(m.Embedding), memoryType,
		nullString(m.AgentID), nullString(m.SessionID),
		m.Version, nullString(m.UpdatedBy),
	)
	if err != nil {
		return fmt.Errorf("create memory: %w", err)
	}
	return nil
}

func (r *MemoryRepo) GetByID(ctx context.Context, id string) (*domain.Memory, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+allColumns+` FROM memories WHERE id = $1 AND state = 'active'`, id,
	)
	return scanMemory(row)
}

func (r *MemoryRepo) UpdateOptimistic(ctx context.Context, m *domain.Memory, expectedVersion int) error {
	tagsJSON := marshalTags(m.Tags)

	query := `UPDATE memories SET content = $1, tags = $2, metadata = $3, embedding = $4, version = version + 1, updated_by = $5, updated_at = NOW()
		 WHERE id = $6`
	args := []any{m.Content, tagsJSON, nullJSON(m.Metadata), vecToString(m.Embedding), nullString(m.UpdatedBy), m.ID}

	if expectedVersion > 0 {
		query += " AND version = $7"
		args = append(args, expectedVersion)
	}

	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		if expectedVersion > 0 {
			// Version mismatch - return conflict error
			return domain.ErrWriteConflict
		}
		return domain.ErrNotFound
	}
	return nil
}

func (r *MemoryRepo) SoftDelete(ctx context.Context, id, agentName string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("soft delete begin tx: %w", err)
	}
	defer tx.Rollback()

	var state sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM memories WHERE id = $1 FOR UPDATE`, id,
	).Scan(&state)
	if err == sql.ErrNoRows {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("soft delete lock row: %w", err)
	}

	if state.String == string(domain.StateDeleted) {
		return nil
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE memories SET state = 'deleted', updated_at = NOW() WHERE id = $1`, id,
	)
	if err != nil {
		return fmt.Errorf("soft delete update: %w", err)
	}

	return tx.Commit()
}

func (r *MemoryRepo) ArchiveMemory(ctx context.Context, id, supersededBy string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE memories SET state = 'archived', superseded_by = $1, updated_at = NOW()
		 WHERE id = $2 AND state = 'active'`,
		supersededBy, id,
	)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *MemoryRepo) ArchiveAndCreate(ctx context.Context, archiveID, supersededBy string, newMem *domain.Memory) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`UPDATE memories SET state = 'archived', superseded_by = $1, updated_at = NOW()
		 WHERE id = $2 AND state = 'active'`,
		supersededBy, archiveID,
	)
	if err != nil {
		return fmt.Errorf("archive old memory: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}

	tagsJSON := marshalTags(newMem.Tags)
	memoryType := string(newMem.MemoryType)
	if memoryType == "" {
		memoryType = string(domain.TypePinned)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories (id, content, source, tags, metadata, embedding, memory_type, agent_id, session_id, state, version, updated_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active', $10, $11, NOW(), NOW())`,
		newMem.ID, newMem.Content, nullString(newMem.Source),
		tagsJSON, nullJSON(newMem.Metadata), vecToString(newMem.Embedding), memoryType,
		nullString(newMem.AgentID), nullString(newMem.SessionID),
		newMem.Version, nullString(newMem.UpdatedBy),
	)
	if err != nil {
		return fmt.Errorf("create new memory: %w", err)
	}

	return tx.Commit()
}

func (r *MemoryRepo) SetState(ctx context.Context, id string, state domain.MemoryState) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE memories SET state = $1, updated_at = NOW() WHERE id = $2 AND state = 'active'`,
		string(state), id,
	)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *MemoryRepo) List(ctx context.Context, f domain.MemoryFilter) ([]domain.Memory, int, error) {
	where, args := r.buildWhere(f)

	// Count total matches
	var total int
	countQuery := "SELECT COUNT(*) FROM memories WHERE " + where
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count memories: %w", err)
	}

	// Fetch page
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	nextArg := len(args) + 1
	dataQuery := "SELECT " + allColumns + " FROM memories WHERE " +
		where + fmt.Sprintf(" ORDER BY updated_at DESC LIMIT $%d OFFSET $%d", nextArg, nextArg+1)
	dataArgs := append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	var memories []domain.Memory
	for rows.Next() {
		m, err := scanMemoryRows(rows)
		if err != nil {
			return nil, 0, err
		}
		memories = append(memories, *m)
	}
	return memories, total, rows.Err()
}

func (r *MemoryRepo) Count(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE state = 'active'`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return count, nil
}

func (r *MemoryRepo) ListBootstrap(ctx context.Context, limit int) ([]domain.Memory, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+allColumns+` FROM memories WHERE state = 'active' ORDER BY updated_at DESC LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list bootstrap: %w", err)
	}
	defer rows.Close()

	var memories []domain.Memory
	for rows.Next() {
		m, err := scanMemoryRows(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, *m)
	}
	return memories, rows.Err()
}

func (r *MemoryRepo) BulkCreate(ctx context.Context, memories []*domain.Memory) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO memories (id, content, source, tags, metadata, embedding, memory_type, agent_id, session_id, state, version, updated_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active', $10, $11, NOW(), NOW())`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, m := range memories {
		tagsJSON := marshalTags(m.Tags)
		memoryType := string(m.MemoryType)
		if memoryType == "" {
			memoryType = string(domain.TypePinned)
		}
		_, execErr := stmt.ExecContext(ctx,
			m.ID, m.Content, nullString(m.Source),
			tagsJSON, nullJSON(m.Metadata), vecToString(m.Embedding), memoryType,
			nullString(m.AgentID), nullString(m.SessionID),
			m.Version, nullString(m.UpdatedBy),
		)
		if execErr != nil {
			var pqErr *pq.Error
			if errors.As(execErr, &pqErr) && pqErr.Code == "23505" {
				return fmt.Errorf("bulk insert memory %s: %w", m.ID, domain.ErrDuplicateKey)
			}
			return fmt.Errorf("bulk insert memory %s: %w", m.ID, execErr)
		}
	}
	return tx.Commit()
}

// VectorSearch performs ANN search using cosine distance with db9's <=> operator.
func (r *MemoryRepo) VectorSearch(ctx context.Context, queryVec []float32, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	vecStr := vecToString(queryVec)
	if vecStr == nil {
		return nil, nil
	}

	conds, args := r.buildFilterConds(f)
	conds = append(conds, "embedding IS NOT NULL")

	where := strings.Join(conds, " AND ")
	nextArg := len(args) + 1

	query := fmt.Sprintf(`SELECT %s, (embedding <=> $%d) AS distance
		 FROM memories
		 WHERE %s
		 ORDER BY embedding <=> $%d
		 LIMIT $%d`, allColumns, nextArg, where, nextArg+1, nextArg+2)

	fullArgs := append(args, vecStr, vecStr, limit)

	rows, err := r.db.QueryContext(ctx, query, fullArgs...)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var memories []domain.Memory
	for rows.Next() {
		m, err := scanMemoryRowsWithDistance(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, *m)
	}
	return memories, rows.Err()
}

// AutoVectorSearch is not supported in db9 (no built-in embedding).
// Falls back to keyword search.
func (r *MemoryRepo) AutoVectorSearch(ctx context.Context, queryText string, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	// db9 doesn't have auto-embedding, fall back to FTS
	if r.FTSAvailable() {
		return r.FTSSearch(ctx, queryText, f, limit)
	}
	return r.KeywordSearch(ctx, queryText, f, limit)
}

// KeywordSearch performs substring search on content.
func (r *MemoryRepo) KeywordSearch(ctx context.Context, query string, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	conds, args := r.buildFilterConds(f)
	if query != "" {
		nextArg := len(args) + 1
		conds = append(conds, fmt.Sprintf("content ILIKE '%%' || $%d || '%%'", nextArg))
		args = append(args, query)
	}

	where := strings.Join(conds, " AND ")
	nextArg := len(args) + 1
	sqlQuery := fmt.Sprintf(`SELECT %s FROM memories WHERE %s ORDER BY updated_at DESC LIMIT $%d`, allColumns, where, nextArg)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()

	var memories []domain.Memory
	for rows.Next() {
		m, err := scanMemoryRows(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, *m)
	}
	return memories, rows.Err()
}

// FTSSearch performs full-text search using PostgreSQL's tsvector with jieba tokenizer.
func (r *MemoryRepo) FTSSearch(ctx context.Context, query string, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	conds, args := r.buildFilterConds(f)
	nextArg := len(args) + 1
	
	// Use jieba for Chinese FTS, with ts_rank for scoring
	where := strings.Join(conds, " AND ")
	sqlQuery := fmt.Sprintf(`SELECT %s, ts_rank(to_tsvector('jieba', content), plainto_tsquery('jieba', $%d)) AS fts_score
		 FROM memories
		 WHERE %s AND to_tsvector('jieba', content) @@ plainto_tsquery('jieba', $%d)
		 ORDER BY fts_score DESC
		 LIMIT $%d`, allColumns, nextArg, where, nextArg+1, nextArg+2)

	fullArgs := append(args, query, query, limit)

	rows, err := r.db.QueryContext(ctx, sqlQuery, fullArgs...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	var memories []domain.Memory
	for rows.Next() {
		m, err := scanMemoryRowsWithFTSScore(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, *m)
	}
	return memories, rows.Err()
}

func (r *MemoryRepo) buildWhere(f domain.MemoryFilter) (string, []any) {
	conds, args := r.buildFilterConds(f)
	if f.Query != "" {
		nextArg := len(args) + 1
		conds = append(conds, fmt.Sprintf("content ILIKE '%%' || $%d || '%%'", nextArg))
		args = append(args, f.Query)
	}
	return strings.Join(conds, " AND "), args
}

func (r *MemoryRepo) buildFilterConds(f domain.MemoryFilter) ([]string, []any) {
	conds := []string{}
	args := []any{}
	argNum := 1

	if f.State == "all" {
		// no state filter
	} else if f.State != "" {
		conds = append(conds, fmt.Sprintf("state = $%d", argNum))
		args = append(args, f.State)
		argNum++
	} else {
		conds = append(conds, "state = 'active'")
	}

	if f.MemoryType != "" {
		types := strings.Split(f.MemoryType, ",")
		if len(types) == 1 {
			conds = append(conds, fmt.Sprintf("memory_type = $%d", argNum))
			args = append(args, types[0])
			argNum++
		} else {
			placeholders := make([]string, len(types))
			for i, t := range types {
				placeholders[i] = fmt.Sprintf("$%d", argNum)
				args = append(args, strings.TrimSpace(t))
				argNum++
			}
			conds = append(conds, "memory_type IN ("+strings.Join(placeholders, ",")+")")
		}
	}

	if f.AgentID != "" {
		conds = append(conds, fmt.Sprintf("agent_id = $%d", argNum))
		args = append(args, f.AgentID)
		argNum++
	}
	if f.SessionID != "" {
		conds = append(conds, fmt.Sprintf("session_id = $%d", argNum))
		args = append(args, f.SessionID)
		argNum++
	}
	if f.Source != "" {
		conds = append(conds, fmt.Sprintf("source = $%d", argNum))
		args = append(args, f.Source)
		argNum++
	}
	for _, tag := range f.Tags {
		tagJSON, err := json.Marshal(tag)
		if err != nil {
			continue
		}
		// PostgreSQL JSONB containment: tags @> '["tag"]'::jsonb
		conds = append(conds, fmt.Sprintf("tags @> $%d::jsonb", argNum))
		args = append(args, "["+string(tagJSON)+"]")
		argNum++
	}
	if len(conds) == 0 {
		conds = append(conds, "1=1")
	}
	return conds, args
}

func scanMemory(row *sql.Row) (*domain.Memory, error) {
	var m domain.Memory
	var source, memoryType, agentID, sessionID, state, updatedBy, supersededBy sql.NullString
	var tagsJSON, metadataJSON, embeddingStr []byte

	err := row.Scan(&m.ID, &m.Content, &source,
		&tagsJSON, &metadataJSON, &embeddingStr, &memoryType, &agentID, &sessionID, &state, &m.Version, &updatedBy,
		&m.CreatedAt, &m.UpdatedAt, &supersededBy)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan memory: %w", err)
	}
	m.Source = source.String
	m.MemoryType = domain.MemoryType(memoryType.String)
	if m.MemoryType == "" {
		m.MemoryType = domain.TypePinned
	}
	m.AgentID = agentID.String
	m.SessionID = sessionID.String
	m.State = domain.MemoryState(state.String)
	if m.State == "" {
		m.State = domain.StateActive
	}
	m.UpdatedBy = updatedBy.String
	m.SupersededBy = supersededBy.String
	m.Tags = unmarshalTags(tagsJSON)
	m.Metadata = unmarshalRawJSON(metadataJSON)
	return &m, nil
}

func scanMemoryRows(rows *sql.Rows) (*domain.Memory, error) {
	var m domain.Memory
	var source, memoryType, agentID, sessionID, state, updatedBy, supersededBy sql.NullString
	var tagsJSON, metadataJSON, embeddingStr []byte

	err := rows.Scan(&m.ID, &m.Content, &source,
		&tagsJSON, &metadataJSON, &embeddingStr, &memoryType, &agentID, &sessionID, &state, &m.Version, &updatedBy,
		&m.CreatedAt, &m.UpdatedAt, &supersededBy)
	if err != nil {
		return nil, fmt.Errorf("scan memory row: %w", err)
	}
	m.Source = source.String
	m.MemoryType = domain.MemoryType(memoryType.String)
	if m.MemoryType == "" {
		m.MemoryType = domain.TypePinned
	}
	m.AgentID = agentID.String
	m.SessionID = sessionID.String
	m.State = domain.MemoryState(state.String)
	if m.State == "" {
		m.State = domain.StateActive
	}
	m.UpdatedBy = updatedBy.String
	m.SupersededBy = supersededBy.String
	m.Tags = unmarshalTags(tagsJSON)
	m.Metadata = unmarshalRawJSON(metadataJSON)
	return &m, nil
}

func scanMemoryRowsWithDistance(rows *sql.Rows) (*domain.Memory, error) {
	var m domain.Memory
	var source, memoryType, agentID, sessionID, state, updatedBy, supersededBy sql.NullString
	var tagsJSON, metadataJSON, embeddingStr []byte
	var distance float64

	err := rows.Scan(&m.ID, &m.Content, &source,
		&tagsJSON, &metadataJSON, &embeddingStr, &memoryType, &agentID, &sessionID, &state, &m.Version, &updatedBy,
		&m.CreatedAt, &m.UpdatedAt, &supersededBy,
		&distance)
	if err != nil {
		return nil, fmt.Errorf("scan memory row with distance: %w", err)
	}
	m.Source = source.String
	m.MemoryType = domain.MemoryType(memoryType.String)
	if m.MemoryType == "" {
		m.MemoryType = domain.TypePinned
	}
	m.AgentID = agentID.String
	m.SessionID = sessionID.String
	m.State = domain.MemoryState(state.String)
	if m.State == "" {
		m.State = domain.StateActive
	}
	m.UpdatedBy = updatedBy.String
	m.SupersededBy = supersededBy.String
	m.Tags = unmarshalTags(tagsJSON)
	m.Metadata = unmarshalRawJSON(metadataJSON)
	score := 1 - distance
	m.Score = &score
	return &m, nil
}

func scanMemoryRowsWithFTSScore(rows *sql.Rows) (*domain.Memory, error) {
	var m domain.Memory
	var source, memoryType, agentID, sessionID, state, updatedBy, supersededBy sql.NullString
	var tagsJSON, metadataJSON, embeddingStr []byte
	var ftsScore float64

	err := rows.Scan(&m.ID, &m.Content, &source,
		&tagsJSON, &metadataJSON, &embeddingStr, &memoryType, &agentID, &sessionID, &state, &m.Version, &updatedBy,
		&m.CreatedAt, &m.UpdatedAt, &supersededBy,
		&ftsScore)
	if err != nil {
		return nil, fmt.Errorf("scan memory row with fts score: %w", err)
	}
	m.Source = source.String
	m.MemoryType = domain.MemoryType(memoryType.String)
	if m.MemoryType == "" {
		m.MemoryType = domain.TypePinned
	}
	m.AgentID = agentID.String
	m.SessionID = sessionID.String
	m.State = domain.MemoryState(state.String)
	if m.State == "" {
		m.State = domain.StateActive
	}
	m.UpdatedBy = updatedBy.String
	m.SupersededBy = supersededBy.String
	m.Tags = unmarshalTags(tagsJSON)
	m.Metadata = unmarshalRawJSON(metadataJSON)
	m.Score = &ftsScore
	return &m, nil
}

func marshalTags(tags []string) []byte {
	if len(tags) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func unmarshalTags(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil
	}
	return tags
}

func unmarshalRawJSON(data []byte) json.RawMessage {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	return json.RawMessage(data)
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullJSON(data json.RawMessage) any {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	return []byte(data)
}

// vecToString converts a float32 slice to PostgreSQL VECTOR format: '[0.1,0.2,...]'.
func vecToString(embedding []float32) any {
	if len(embedding) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range embedding {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%g", v))
	}
	sb.WriteByte(']')
	return sb.String()
}
