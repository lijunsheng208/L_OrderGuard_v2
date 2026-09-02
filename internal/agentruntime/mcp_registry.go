package agentruntime

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/lijunsheng/orderguard/internal/mcp"
)

// RegisteredTool 保存 MCP 工具定义和所属客户端。
type RegisteredTool struct {
	Definition mcp.Tool
	Client     *mcp.Client
}

// MCPRegistry 管理 Runtime 允许访问的 MCP Server 和动态发现工具。
type MCPRegistry struct {
	clients map[string]*mcp.Client
	tools   map[string]RegisteredTool
}

// NewMCPRegistry 创建业务与可观测性 MCP 客户端注册表。
func NewMCPRegistry(
	businessEndpoint string,
	observabilityEndpoint string,
	httpClient *http.Client,
	optionalKnowledge ...string,
) *MCPRegistry {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	clients := map[string]*mcp.Client{
		"business":      mcp.NewClient(businessEndpoint, httpClient),
		"observability": mcp.NewClient(observabilityEndpoint, httpClient),
	}
	if len(optionalKnowledge) > 0 && optionalKnowledge[0] != "" {
		clients["knowledge"] = mcp.NewClient(optionalKnowledge[0], httpClient)
	}
	return &MCPRegistry{
		clients: clients,
		tools:   make(map[string]RegisteredTool),
	}
}

// Discover 初始化 MCP Server 并注册全部只读工具。
func (r *MCPRegistry) Discover(ctx context.Context) error {
	discovered := make(map[string]RegisteredTool)
	for serverName, client := range r.clients {
		if err := client.Initialize(ctx); err != nil {
			return fmt.Errorf("initialize %s mcp: %w", serverName, err)
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			return fmt.Errorf("list %s mcp tools: %w", serverName, err)
		}
		for _, definition := range tools {
			if !definition.Annotations.ReadOnlyHint || definition.Annotations.DestructiveHint {
				continue
			}
			if _, exists := discovered[definition.Name]; exists {
				return fmt.Errorf("duplicate mcp tool: %s", definition.Name)
			}
			discovered[definition.Name] = RegisteredTool{
				Definition: definition, Client: client,
			}
		}
	}
	r.tools = discovered
	return nil
}

// Get 返回已发现的只读工具。
func (r *MCPRegistry) Get(name string) (RegisteredTool, bool) {
	registered, ok := r.tools[name]
	return registered, ok
}

// Definitions 返回按名称排序的只读工具定义。
func (r *MCPRegistry) Definitions() []mcp.Tool {
	result := make([]mcp.Tool, 0, len(r.tools))
	for _, registered := range r.tools {
		result = append(result, registered.Definition)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}
