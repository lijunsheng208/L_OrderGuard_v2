# MCP contract v0.1.0

四个 MCP Server 使用 Streamable HTTP，端点均为 `POST /mcp`，协议版本为 `2025-06-18`。支持阶段 0 验收所需的 `initialize`、`ping`、`tools/list` 和 `tools/call`。

工具名称与输入 JSON Schema 以 `internal/mcp/catalog.go` 为唯一可执行定义，Server 通过 `tools/list` 原样发布。工具分组如下：

- `business-mcp`：`get_order_snapshot`、`get_payment_status`、`get_inventory_status`、`get_order_state_history`
- `observability-mcp`：`search_service_logs`、`get_trace`、`get_metric`、`get_event_record`、`get_outbox_status`
- `knowledge-mcp`：`search_runbook`、`search_historical_incidents`、`get_service_topology`、`get_state_machine`、`get_repair_policy`
- `remediation-mcp`：`deduct_inventory_once`

协议错误使用 JSON-RPC `error`；已进入工具但执行失败时，使用 `tool-result.schema.json` 中的统一结构，并设置 MCP `isError: true`。错误码清单见 `error-codes.json`。

阶段 0 仅为 `get_order_snapshot` 提供契约冒烟响应，状态明确标记为 `UNKNOWN` 和 `CONTRACT_ONLY`。其余工具返回 `PHASE_NOT_IMPLEMENTED`，不会伪造尚未实现的业务能力。

