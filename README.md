# OrderGuard

当前实现范围包括 `ORDERGUARD_MCP_AGENT_PLAN.md` 的阶段 0 和阶段 1：MCP 契约、四个 MCP Server 骨架，以及订单、支付、Outbox、Redis Streams 和库存幂等消费链路。

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
curl http://localhost:8083/inventory/O1001/deductions
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

停止容器：

```bash
docker compose -f deployments/docker-compose.yml down
```

宿主机端口：业务服务使用 `8081` 至 `8084`，MCP Server 使用 `8091` 至 `8094`，PostgreSQL 使用 `55432`，Redis 使用 `56379`。每个服务都暴露 `GET /healthz`。

阶段 1 尚不实现故障注入、MCP 业务查询、知识检索、Agent Runtime 或修复逻辑。
