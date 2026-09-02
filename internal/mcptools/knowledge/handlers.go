package knowledge

import (
	"context"
	"strings"

	"github.com/lijunsheng/orderguard/internal/knowledge"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

// StoreHandlers 将本地知识库适配为 MCP 工具 Handler。
func StoreHandlers(store *knowledge.Store) map[string]mcp.ToolHandler {
	search := func(ctx context.Context, args map[string]any) (mcp.ToolOutput, error) {
		_ = ctx
		query, _ := args["query"].(string)
		return mcp.ToolOutput{Data: map[string]any{"query": query, "matches": store.Search(query, 8)}, Source: "local-markdown", EvidencePrefix: "knowledge"}, nil
	}
	return map[string]mcp.ToolHandler{
		"search_runbook":              search,
		"search_historical_incidents": search,
		"get_service_topology": func(ctx context.Context, args map[string]any) (mcp.ToolOutput, error) {
			service, _ := args["service"].(string)
			return mcp.ToolOutput{Data: map[string]any{"service": service, "matches": store.Search(strings.TrimSpace(service)+" topology", 8)}, Source: "local-markdown", EvidencePrefix: "topology"}, nil
		},
		"get_state_machine": func(ctx context.Context, args map[string]any) (mcp.ToolOutput, error) {
			domain, _ := args["domain"].(string)
			return mcp.ToolOutput{Data: map[string]any{"domain": domain, "matches": store.Search(domain+" state status", 8)}, Source: "local-markdown", EvidencePrefix: "state"}, nil
		},
		"get_repair_policy": func(ctx context.Context, args map[string]any) (mcp.ToolOutput, error) {
			action, _ := args["action"].(string)
			return mcp.ToolOutput{Data: map[string]any{"action": action, "matches": store.Search(action+" policy", 8)}, Source: "local-markdown", EvidencePrefix: "policy"}, nil
		},
	}
}
