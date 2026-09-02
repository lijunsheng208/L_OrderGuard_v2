# OrderGuard

当前实现范围为 `ORDERGUARD_MCP_AGENT_PLAN.md` 的阶段 0：MCP 契约、四个业务服务骨架、四个 MCP Server、PostgreSQL 和 Redis 编排。

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

发现 `business-mcp` 工具并调用只读工具：

```bash
go run ./cmd/mcp/client -endpoint http://localhost:8091/mcp -order-id O1001
```

停止容器：

```bash
docker compose -f deployments/docker-compose.yml down
```

宿主机端口：业务服务使用 `8081` 至 `8084`，MCP Server 使用 `8091` 至 `8094`。每个服务都暴露 `GET /healthz`。

阶段 0 不实现订单交易、Outbox、Redis Streams 消费、知识检索、Agent Runtime 或修复逻辑。
