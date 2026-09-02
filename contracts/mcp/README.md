# MCP contract v0.2.0

四个 MCP Server 使用 Streamable HTTP，端点均为 `POST /mcp`，协议版本为 `2025-06-18`。支持 `initialize`、`ping`、`tools/list` 和 `tools/call`。

工具名称与输入 JSON Schema 以 `internal/mcp/catalog.go` 为唯一可执行定义，Server 通过 `tools/list` 原样发布。工具分组如下：

- `business-mcp`：`get_order_snapshot`、`get_payment_status`、`get_inventory_status`、`get_order_state_history`
- `observability-mcp`：`search_service_logs`、`get_trace`、`get_metric`、`get_event_record`、`get_outbox_status`
- `knowledge-mcp`：`search_runbook`、`search_historical_incidents`、`get_service_topology`、`get_state_machine`、`get_repair_policy`
- `remediation-mcp`：`deduct_inventory_once`

协议错误使用 JSON-RPC `error`；已进入工具但执行失败时，使用 `tool-result.schema.json` 中的统一结构，并设置 MCP `isError: true`。错误码清单见 `error-codes.json`。

阶段 2 已为 `business-mcp` 和 `observability-mcp` 注册真实只读 Handler。未进入当前阶段的 `knowledge-mcp` 与 `remediation-mcp` 工具仍返回 `PHASE_NOT_IMPLEMENTED`，不会伪造业务能力。

所有成功工具结果包含 `evidence_id`、`source`、`collected_at` 和原始结构化 `data`。工具执行失败使用稳定错误码并设置 `isError: true`；协议和 Schema 校验错误仍使用 JSON-RPC `error`。

阶段 3 Runtime 调用工具时额外传递 `X-Run-ID`、`X-Agent-Step-ID`、`X-Trace-ID`、`X-Tool-Call-ID` 和 `X-MCP-Caller`。这些字段只用于审计关联，不参与工具业务参数，也不会赋予额外权限。
