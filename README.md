# OrderGuard

OrderGuard 是一个面向订单异常的调查与安全修复平台，针对支付成功但库存未扣减、异步事件异常和业务状态不一致等问题，使用多个 Agent 完成故障取证、知识检索、根因分析、结论校验和结果验证。

系统通过 MCP 提供订单、支付、库存、日志、Trace、事件和故障知识等领域工具。Agent 通过受控工具获取数据，诊断结论关联 Evidence ID；涉及业务变更时，必须经过 Go 规则校验和人工审批，修复后重新读取业务状态确认结果。

## Features

- **多 Agent 调查**：Planner 制定调查计划，Investigator 基于 Eino ReAct 动态取证，Knowledge Agent 检索故障知识，Diagnosis 输出结构化根因结论。
- **证据驱动诊断**：保存工具、参数、来源、返回数据和 Evidence ID，使诊断事实能够追溯到具体查询。
- **MCP 领域工具**：提供业务查询、可观测性查询、知识检索和受控修复工具，支持工具发现、JSON Schema 参数校验和调用审计。
- **安全修复闭环**：通过 Go 规则、任务状态流转和人工审批限制修复，完成后由 Verify Agent 根据实际业务状态判断是否恢复。
- **可靠异步链路**：使用 PostgreSQL 事务和 Outbox 记录支付事件，通过 Redis Streams 完成投递和库存消费，并使用事件 ID 与数据库唯一约束保证消费幂等。
- **实时进度**：持久化任务、Agent 步骤、工具调用、证据和状态变化，并通过 SSE 推送调查进度。

## Architecture

```text
React Console (REST + SSE)
              │
              ▼
Agent Runtime (Go + Eino)
  Planner / Investigator / Knowledge
  Diagnosis / Critic / Verify
              │
              ├── MCP domain tools
              │   business / observability
              │   knowledge / remediation
              ├── Go policy and state machine
              ├── PostgreSQL: business data / evidence / audit
              └── Redis Streams: event delivery / inventory consumption
```

### Investigation flow

```text
用户提交异常
  -> Planner 制定调查计划
  -> Investigator 使用 ReAct 查询 MCP 工具
  -> 保存工具结果和 Evidence ID
  -> Knowledge 检索故障手册与历史案例
  -> Diagnosis 输出根因结论
  -> Go 规则校验与人工审批
  -> 受控修复
  -> 重新查询业务状态
  -> Verify 确认是否恢复
```

## Tech Stack

- **Backend**：Go 1.26、Eino
- **Agent integration**：MCP、Eino ReAct、OpenAI-compatible ChatModel
- **Storage**：PostgreSQL、Redis Streams
- **Frontend**：React、TypeScript、Vite、SSE
- **Deployment**：Docker Compose

## Project Structure

```text
cmd/                         # 业务服务、Agent Runtime 和 MCP Server
internal/agentruntime/       # Agent 编排、状态、策略、证据和修复流程
internal/mcp/                # MCP Client、Server 和工具契约
internal/mcptools/           # 各领域 MCP 工具 Handler
internal/order/              # 订单服务
internal/payment/            # 支付服务和 Outbox
internal/inventory/          # 库存服务和 Redis Streams 消费
internal/knowledge/          # 本地 Markdown 知识库
migrations/                  # PostgreSQL 数据库迁移
deployments/                 # Docker Compose 配置
frontend/                    # React 管理控制台
test/integration/            # 集成测试
evaluations/                 # Agent 对比评测
```

## Quick Start

### Prepare environment

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

### Start the frontend

```bash
cd frontend
npm install
npm run dev
```

### Create and pay an order

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

### Discover MCP tools

```bash
go run ./cmd/mcp/client -endpoint http://localhost:8091/mcp -order-id O1001
```

### Configure the model

```bash
export AGENT_API_KEY=your-key
export AGENT_BASE_URL=https://your-compatible-endpoint/v1
export AGENT_MODEL=your-model
docker compose -f deployments/docker-compose.yml up --build -d
```

未配置模型时，业务服务和 MCP Server 仍可启动，但 Agent 调查无法执行模型推理。

### Start an investigation

```bash
curl -X POST http://localhost:8090/api/v1/investigations \
  -H 'Content-Type: application/json' \
  -d '{"message":"调查订单 O1001 的支付和库存链路","order_id":"O1001"}'

curl -N http://localhost:8090/api/v1/investigations/{run_id}/events
curl http://localhost:8090/api/v1/investigations/{run_id}/evidence
```

涉及修复的任务需要先审批，再执行：

```bash
curl -X POST http://localhost:8090/api/v1/investigations/{run_id}/approve
curl -X POST http://localhost:8090/api/v1/investigations/{run_id}/execute \
  -H 'Content-Type: application/json' \
  -d '{"items":[],"idempotency_key":"repair-O1001-001"}'
```

创建演示故障：

```bash
curl -X POST http://localhost:8090/api/v1/demo/faults \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"O1001","fault_type":"OUTBOX_PUBLISH_FAILED"}'
```

也可以运行：

```bash
bash scripts/demo_incidents.sh
```

## MCP Tools

所有 MCP Server 使用 Streamable HTTP，端点为 `POST /mcp`，支持 `initialize`、`tools/list` 和 `tools/call`。

| Server | 提供能力 |
|---|---|
| `business-mcp` | 订单快照、支付状态、库存状态、订单状态历史 |
| `observability-mcp` | 服务日志、Trace、指标、事件记录、Outbox 状态 |
| `knowledge-mcp` | 故障手册、历史案例、服务拓扑、状态机、修复策略 |
| `remediation-mcp` | 受控库存补偿操作 |

成功工具结果包含 `evidence_id`、`source`、`collected_at` 和结构化 `data`。Runtime 将工具调用与任务、Agent 步骤、Trace 和 Evidence 关联，并把审计信息写入 PostgreSQL。

## Testing

```bash
go test ./...
make integration-test
```

集成测试需要先启动 Docker Compose 服务。

运行单 Agent / 多 Agent 对比评测：

```bash
go run ./evaluations/agent-comparison \
  -architecture both \
  -repetitions 3 \
  -case-file evaluations/agent-comparison/eight-scenarios.json \
  -output evaluations/agent-comparison-rerun-20260906/results.json
```

评测使用相同的模型、故障案例、MCP 工具、修复接口和 Verify 条件，比较诊断准确率、诊断完成率、证据工具覆盖率、无效工具调用率、修复成功率和端到端延迟。

## Service Ports

```text
order-service          8081
payment-service        8082
inventory-service      8083
reconciliation-service 8084
agent-runtime          8090
business-mcp           8091
observability-mcp      8092
knowledge-mcp          8093
remediation-mcp        8094
PostgreSQL             55432
Redis                  56379
```

## Stop Services

```bash
docker compose --env-file .env -f deployments/docker-compose.yml down
```

如需删除 Docker 持久化卷，请谨慎执行：

```bash
docker compose --env-file .env -f deployments/docker-compose.yml down -v
```
