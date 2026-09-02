# OrderGuard

当前实现范围包括 `ORDERGUARD_MCP_AGENT_PLAN.md` 的阶段 0 至阶段 2：MCP 契约和 Server 骨架、订单正常交付链路，以及可审计的只读业务与可观测性证据采集。

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

停止容器：

```bash
docker compose -f deployments/docker-compose.yml down
```

宿主机端口：业务服务使用 `8081` 至 `8084`，MCP Server 使用 `8091` 至 `8094`，PostgreSQL 使用 `55432`，Redis 使用 `56379`。每个服务都暴露 `GET /healthz`。

阶段 2 尚不实现知识检索、Agent Runtime、不可变证据仓库、诊断、审查或修复逻辑。
