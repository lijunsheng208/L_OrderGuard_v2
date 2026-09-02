package mcp

// ServerProfile 保存一个 MCP Server 的名称和工具集合。
type ServerProfile struct {
	Name    string
	Version string
	Tools   []Tool
}

// Profiles 返回阶段 0 冻结的四组 MCP 工具契约。
func Profiles() map[string]ServerProfile {
	return map[string]ServerProfile{
		"business": {
			Name:    "business-mcp",
			Version: "0.2.0",
			Tools: []Tool{
				readTool("get_order_snapshot", "查询订单快照", objectSchema(
					fields{"order_id": stringField("订单 ID")}, "order_id",
				)),
				readTool("get_payment_status", "查询订单对应的支付状态", objectSchema(
					fields{"order_id": stringField("订单 ID")}, "order_id",
				)),
				readTool("get_inventory_status", "查询订单对应的库存状态", objectSchema(
					fields{"order_id": stringField("订单 ID")}, "order_id",
				)),
				readTool("get_order_state_history", "查询订单状态变更历史", objectSchema(
					fields{"order_id": stringField("订单 ID")}, "order_id",
				)),
			},
		},
		"observability": {
			Name:    "observability-mcp",
			Version: "0.2.0",
			Tools: []Tool{
				readTool("search_service_logs", "按服务和订单检索日志", objectSchema(fields{
					"service": stringField("服务名"), "order_id": stringField("订单 ID"),
					"keyword": stringField("可选关键词"),
				}, "service", "order_id")),
				readTool("get_trace", "查询 Trace", objectSchema(
					fields{"trace_id": stringField("Trace ID")}, "trace_id",
				)),
				readTool("get_metric", "查询服务指标", objectSchema(fields{
					"service": stringField("服务名"), "metric": enumStringField(
						"指标名", "operations_total", "operations_failed_total", "operation_duration_ms_avg",
					),
					"from": dateTimeField("开始时间"), "to": dateTimeField("结束时间"),
				}, "service", "metric", "from", "to")),
				readTool("get_event_record", "查询订单事件记录", objectSchema(fields{
					"order_id": stringField("订单 ID"), "event_type": stringField("事件类型"),
				}, "order_id", "event_type")),
				readTool("get_outbox_status", "查询支付 Outbox 状态", objectSchema(
					fields{"order_id": stringField("订单 ID")}, "order_id",
				)),
			},
		},
		"knowledge": {
			Name:    "knowledge-mcp",
			Version: "0.1.0",
			Tools: []Tool{
				readTool("search_runbook", "检索故障手册", objectSchema(
					fields{"query": stringField("检索词")}, "query",
				)),
				readTool("search_historical_incidents", "检索历史故障", objectSchema(
					fields{"query": stringField("检索词")}, "query",
				)),
				readTool("get_service_topology", "查询服务拓扑", objectSchema(
					fields{"service": stringField("服务名")}, "service",
				)),
				readTool("get_state_machine", "查询领域状态机", objectSchema(
					fields{"domain": stringField("领域名")}, "domain",
				)),
				readTool("get_repair_policy", "查询修复策略", objectSchema(
					fields{"action": stringField("修复动作")}, "action",
				)),
			},
		},
		"remediation": {
			Name:    "remediation-mcp",
			Version: "0.1.0",
			Tools: []Tool{
				writeTool("deduct_inventory_once", "经 Runtime 授权后执行一次库存扣减", objectSchema(fields{
					"order_id": stringField("订单 ID"),
					"items": arrayField("待扣减商品", map[string]any{
						"type": "object",
						"properties": fields{
							"sku_id":   stringField("SKU ID"),
							"quantity": map[string]any{"type": "integer", "minimum": 1, "description": "扣减数量"},
						},
						"required":             []string{"sku_id", "quantity"},
						"additionalProperties": false,
					}),
					"idempotency_key":  stringField("幂等键"),
					"evidence_version": stringField("证据版本"),
				}, "order_id", "items", "idempotency_key", "evidence_version")),
			},
		},
	}
}

type fields map[string]any

// readTool 创建只读工具定义。
func readTool(name, description string, schema map[string]any) Tool {
	return Tool{
		Name: name, Description: description, InputSchema: schema,
		Annotations: ToolAnnotations{ReadOnlyHint: true},
	}
}

// writeTool 创建幂等写工具定义。
func writeTool(name, description string, schema map[string]any) Tool {
	return Tool{
		Name: name, Description: description, InputSchema: schema,
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: true},
	}
}

// objectSchema 创建禁止额外字段的对象 Schema。
func objectSchema(properties fields, required ...string) map[string]any {
	return map[string]any{
		"type": "object", "properties": properties,
		"required": required, "additionalProperties": false,
	}
}

// stringField 创建非空字符串字段。
func stringField(description string) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "description": description}
}

// enumStringField 创建限定枚举值的字符串字段。
func enumStringField(description string, values ...string) map[string]any {
	return map[string]any{
		"type": "string", "minLength": 1, "description": description, "enum": values,
	}
}

// dateTimeField 创建 RFC 3339 时间字段。
func dateTimeField(description string) map[string]any {
	return map[string]any{
		"type": "string", "format": "date-time", "description": description,
	}
}

// arrayField 创建至少包含一项的数组字段。
func arrayField(description string, items map[string]any) map[string]any {
	return map[string]any{
		"type": "array", "minItems": 1, "items": items, "description": description,
	}
}
