# OrderGuard

当前实现范围包括 `ORDERGUARD_MCP_AGENT_PLAN.md` 的阶段 0 至阶段 3：MCP 契约和 Server 骨架、订单正常交付链路、可审计的只读证据采集，以及基于 Eino 的 Planner/Investigator Agent Runtime。

## 本地验证

运行 Go 测试：

```bash
go test ./...
```

启动完整骨架：

```bash
docker compose -f deployments/docker-compose.yml up --build -d
docker compose -f deployments/docker-compose.yml ps
```

Compose 会先自动执行 `migrations/` 中的数据库迁移，再启动业务服务和 MCP Server。

创建并支付演示订单：

```bash
curl -X POST http://localhost:8081/demo/orders \
  -H 'Content-Type: application/json' \
  -d '{"id":"O1001","user_id":"U1001","items":[{"sku_id":"SKU1","quantity":2,"unit_price":19900}]}'

curl -X POST http://localhost:8082/demo/orders/O1001/pay \
  -H 'Content-Type: application/json' \
  -d '{"payment_id":"P1001","amount":39800}'

curl http://localhost:8081/orders/O1001/snapshot
curl http://localhost:8081/orders/O1001/history
curl http://localhost:8082/payments/O1001
curl http://localhost:8082/events/O1001/payment-succeeded
curl http://localhost:8083/inventory/O1001/status
curl http://localhost:8083/stocks/SKU1
```

运行真实 PostgreSQL 和 Redis 集成测试：

```bash
make integration-test
```

发现 `business-mcp` 工具并调用只读工具：

```bash
go run ./cmd/mcp/client -endpoint http://localhost:8091/mcp -order-id O1001
```

`business-mcp` 提供订单快照、支付状态、库存状态和订单状态历史。`observability-mcp` 提供结构化日志、Trace、白名单指标、Redis 事件记录和 Outbox 状态。可用指标为 `operations_total`、`operations_failed_total` 和 `operation_duration_ms_avg`。

阶段 2 的模型无关验收包含在集成测试中。它会调用两个 MCP Server 的全部 9 个工具，并核对证据元数据和 `mcp.tool_calls` 审计记录：

```bash
make integration-test
```

阶段 3 使用 Eino `v0.9.18`。Planner 生成结构化调查计划，Investigator 使用 Eino ReAct Agent 动态调用只读 MCP 工具。模型通过 OpenAI-compatible Eino 组件配置：

```bash
export AGENT_API_KEY=your-key
export AGENT_BASE_URL=https://your-compatible-endpoint/v1
export AGENT_MODEL=your-model
docker compose -f deployments/docker-compose.yml up --build -d
```

未配置模型时 `agent-runtime` 仍能启动并返回健康状态，但创建调查会返回 `MODEL_NOT_CONFIGURED`。创建调查并订阅进度：

```bash
curl -X POST http://localhost:8090/api/v1/investigations \
  -H 'Content-Type: application/json' \
  -d '{"message":"调查订单 O1001 的支付和库存链路","order_id":"O1001"}'

curl -N http://localhost:8090/api/v1/investigations/{run_id}/events
curl http://localhost:8090/api/v1/investigations/{run_id}/evidence
```

集成测试中的阶段 3 验收使用脚本化 Eino `ToolCallingChatModel`，不访问任何外部模型，但会真实执行 ReAct 循环和至少 4 次 MCP 调用。

停止容器：

```bash
docker compose -f deployments/docker-compose.yml down
```

宿主机端口：业务服务使用 `8081` 至 `8084`，MCP Server 使用 `8091` 至 `8094`，PostgreSQL 使用 `55432`，Redis 使用 `56379`。每个服务都暴露 `GET /healthz`。

阶段 3 尚不实现 Knowledge Agent、Diagnosis Agent、Critic Agent、策略审批或修复逻辑。
