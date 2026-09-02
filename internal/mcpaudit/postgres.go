package mcpaudit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

// PostgresAuditor 把 MCP 工具调用写入 PostgreSQL。
type PostgresAuditor struct {
	db *pgxpool.Pool
}

// NewPostgresAuditor 创建 PostgreSQL MCP 审计器。
func NewPostgresAuditor(db *pgxpool.Pool) *PostgresAuditor {
	return &PostgresAuditor{db: db}
}

// Record 保存一次 MCP 工具执行及其完整结构化结果。
func (a *PostgresAuditor) Record(ctx context.Context, record mcp.ToolCallAudit) error {
	arguments, err := json.Marshal(record.Arguments)
	if err != nil {
		return fmt.Errorf("encode audit arguments: %w", err)
	}
	result, err := json.Marshal(record.Envelope)
	if err != nil {
		return fmt.Errorf("encode audit result: %w", err)
	}
	var errorCode *string
	if record.Envelope.Error != nil {
		errorCode = &record.Envelope.Error.Code
	}
	_, err = a.db.Exec(ctx, `
		INSERT INTO mcp.tool_calls (
			request_id, server_name, tool_name, arguments, success,
			evidence_id, source, result, error_code, duration_ms, called_at
		) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11)`,
		record.RequestID, record.ServerName, record.ToolName, arguments,
		record.Envelope.Success, record.Envelope.EvidenceID,
		record.Envelope.Source, result, errorCode,
		record.Duration.Milliseconds(), record.CalledAt,
	)
	if err != nil {
		return fmt.Errorf("insert mcp tool audit: %w", err)
	}
	return nil
}
