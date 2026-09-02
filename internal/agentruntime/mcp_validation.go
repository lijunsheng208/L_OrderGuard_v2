package agentruntime

import "github.com/lijunsheng/orderguard/internal/mcp"

// mcpValidate 使用 MCP Server 同源 Schema 校验 Planner 参数。
func mcpValidate(registered RegisteredTool, arguments map[string]any) error {
	return mcp.ValidateToolArguments(registered.Definition, arguments)
}
