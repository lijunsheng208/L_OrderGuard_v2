# OrderGuard：MCP 化多 Agent 微服务故障调查与安全修复平台

## 1. 项目定位

OrderGuard 是一个本地可运行的 Agent 工程项目。用户用自然语言描述订单异常，系统通过多个 Agent 动态制定调查计划、调用 MCP 工具收集证据、检索项目知识、分析根因，并在 Go 策略引擎和人工审批约束下执行受控修复，最后由 Verify Agent 验证结果。

首版只实现一种可稳定复现的故障：

> 支付事务已经成功，但支付成功事件没有发布，导致库存未扣减。

订单、支付、库存服务只是演示环境；项目的核心是 MCP 工具体系、Agent 协作、证据约束和安全执行闭环。

## 2. 目标与非目标

### 2.1 首版目标

- 用户可以输入自然语言调查问题。
- Planner Agent 能生成结构化调查计划。
- Investigator Agent 能通过 MCP 多轮调用业务和观测工具。
- Knowledge Agent 能从本地故障手册和历史案例中检索知识。
- Diagnosis Agent 能输出带证据 ID 的根因结论。
- Critic Agent 能审查证据是否充分、结论是否越界。
- Go 策略引擎拥有最终修复否决权。
- 修复操作必须经过权限、审批、状态和幂等校验。
- Verify Agent 能重新收集证据并判断是否恢复。
- 前端可以查看任务进度、工具调用、证据、审批和验证结果。

### 2.2 首版不做

- Kubernetes 和多机高可用。
- 真实支付或真实生产数据。
- 自动退款、金额调整等高风险资金操作。
- 任意业务系统的通用自动修复。
- 让模型直接访问数据库、Redis 或执行任意 SQL。

## 3. 总体架构

```text
React 控制台
    │ REST / SSE
    ▼
Go Agent Runtime
    ├── Planner Agent
    ├── Investigator Agent
    ├── Knowledge Agent
    ├── Diagnosis Agent
    ├── Critic Agent
    └── Verify Agent
    │ MCP Client
    ▼
MCP Server 层
    ├── business-mcp       订单、支付、库存查询
    ├── observability-mcp  日志、事件、Trace、指标
    ├── knowledge-mcp      故障手册、拓扑、历史案例
    └── remediation-mcp    受控写操作
    ▼
order-service / payment-service / inventory-service
    ▼
PostgreSQL + Redis Streams
```

MCP 从第一版开始使用。所有 Agent 能力都通过 MCP 工具暴露，业务服务不直接暴露给模型。

## 4. 职责边界

### 4.1 Agent 负责

- 理解自然语言任务。
- 制定和调整调查计划。
- 选择下一步要调用的 MCP 工具。
- 综合多源证据并提出根因假设。
- 检索和使用领域知识。
- 审查其他 Agent 的结论。
- 生成结构化调查和验证结论。

### 4.2 Go Agent Runtime 负责

- 创建任务和推进工作流状态。
- 维护 Agent 上下文和消息记录。
- 连接 MCP Server，发现和调用工具。
- 限制 Agent 可用工具、调用次数和超时时间。
- 保存不可变证据快照。
- 做 JSON Schema、证据 ID 和枚举校验。
- 执行权限、策略、审批、幂等和审计。
- 向前端推送实时事件。

### 4.3 MCP Server 负责

- 暴露标准化的工具、资源和参数 Schema。
- 将工具请求转换为内部服务调用。
- 屏蔽数据库表结构和基础设施细节。
- 对工具输入做第一层校验。
- 返回统一的结构化结果和 `evidence_id`。

模型只能提出调查或修复建议，不能直接改变工作流状态，也不能直接连接数据库。

## 5. MCP Server 设计

### 5.1 business-mcp

只读工具：

```text
get_order_snapshot(order_id)
get_payment_status(order_id)
get_inventory_status(order_id)
get_order_state_history(order_id)
```

工具返回统一格式：

```json
{
  "success": true,
  "data": {
    "order_id": "O1001",
    "status": "PAID",
    "amount": 19900
  },
  "evidence_id": "ev-order-001",
  "source": "order-service",
  "collected_at": "2026-09-02T10:00:00Z"
}
```

### 5.2 observability-mcp

只读工具：

```text
search_service_logs(service, order_id, keyword)
get_trace(trace_id)
get_metric(service, metric, from, to)
get_event_record(order_id, event_type)
get_outbox_status(order_id)
```

首版重点查询：支付事件是否存在、Outbox 是否发布失败、库存消费者是否处理过事件。

### 5.3 knowledge-mcp

资源和工具：

```text
search_runbook(query)
search_historical_incidents(query)
get_service_topology(service)
get_state_machine(domain)
get_repair_policy(action)
```

知识库首版使用 Markdown 文件和 SQLite 全文检索，不依赖云端向量数据库：

```text
knowledge/
├── services/order-service.md
├── services/payment-service.md
├── services/inventory-service.md
├── runbooks/payment-event-lost.md
├── runbooks/duplicate-consumption.md
└── incidents/incident-001.md
```

### 5.4 remediation-mcp

首版只开放一个写工具：

```text
deduct_inventory_once(order_id, items, idempotency_key, evidence_version)
```

后续才考虑：

```text
replay_payment_event(order_id)
create_compensation_task(order_id)
```

该 Server 必须与只读 Server 分离，并且所有请求都要经过 Go Runtime 的最终校验。

## 6. Agent 设计

### 6.1 Planner Agent

输入：用户自然语言、已知订单号、可用 MCP 工具摘要。

输出：结构化调查计划。

```json
{
  "goal": "调查订单 O1001 的支付成功但库存未扣减问题",
  "steps": [
    {"tool": "get_order_snapshot", "args": {"order_id": "O1001"}},
    {"tool": "get_payment_status", "args": {"order_id": "O1001"}},
    {"tool": "get_inventory_status", "args": {"order_id": "O1001"}},
    {"tool": "get_event_record", "args": {"order_id": "O1001", "event_type": "payment.succeeded"}},
    {"tool": "search_runbook", "args": {"query": "支付成功库存未扣减"}}
  ]
}
```

Go 校验工具名、参数、步骤数量和只读权限后，才允许执行。

### 6.2 Investigator Agent

按照计划调用 MCP 工具，但可以根据中间结果追加调查。例如事件不存在时继续查询 Outbox 和支付日志；如果发现库存消费者宕机，则查询消费者指标和 Pending 消息。

输出不可变证据集合：

```json
{
  "facts": [
    {"evidence_id": "ev-payment-001", "fact": "payment.status = SUCCESS"},
    {"evidence_id": "ev-inventory-001", "fact": "inventory reservation is missing"},
    {"evidence_id": "ev-event-001", "fact": "payment.succeeded event not found"}
  ],
  "remaining_questions": ["是否是 Outbox 发布失败？"]
}
```

### 6.3 Knowledge Agent

根据当前证据检索故障手册、服务拓扑和历史案例，不直接修改知识库。

输出：

```json
{
  "matches": [
    {
      "source": "incidents/incident-001.md",
      "title": "支付事件发布超时",
      "relevance": 0.91,
      "content_summary": "支付事务提交成功，但事件发布失败会导致库存未扣减。"
    }
  ]
}
```

### 6.4 Diagnosis Agent

输入调查事实、知识检索结果、服务拓扑和允许的根因枚举。

输出必须区分事实与假设，并引用证据：

```json
{
  "root_cause": "PAYMENT_EVENT_NOT_PUBLISHED",
  "confidence": 0.91,
  "evidence_ids": ["ev-payment-001", "ev-inventory-001", "ev-event-001"],
  "alternatives": [
    {"cause": "INVENTORY_CONSUMER_UNAVAILABLE", "confidence": 0.28}
  ],
  "recommended_action": "DEDUCT_INVENTORY_ONCE"
}
```

证据不足时必须返回 `INCONCLUSIVE`，不得猜测。

### 6.5 Critic Agent

审查 Diagnosis Agent 的结论：

- 证据 ID 是否真实存在。
- 结论是否超出证据范围。
- 是否遗漏关键工具查询。
- 替代原因是否被验证或明确标记为未验证。
- 推荐修复是否可能造成重复扣减。

审查失败时回退 Investigator Agent 补充调查，而不是直接修复。

### 6.6 Verify Agent

修复完成后重新调用 MCP 查询工具，创建新证据快照。首版断言：

```text
order.status == PAID
payment.status == SUCCESS
payment.amount == order.amount
inventory.status == DEDUCTED
successful_deduction_count == 1
```

所有断言通过才标记 `REPAIRED`。

## 7. Go Runtime 设计

Go Runtime 是用 Go 实现的 Agent 编排和安全控制层，不是 Go 语言底层的 `runtime` 包。

推荐模块：

```text
internal/runtime/
├── orchestrator.go       # Agent 工作流
├── state_machine.go      # 状态迁移和乐观锁
├── mcp_client.go         # MCP 连接、发现、调用
├── tool_guard.go         # 工具白名单和调用限制
├── evidence_store.go     # 证据快照
├── policy_engine.go      # 确定性策略
├── approval_service.go   # 人工审批
├── audit_service.go      # 审计日志
└── event_publisher.go    # SSE 事件
```

核心流程：

```text
接收自然语言任务
→ Planner
→ Investigator 多轮 MCP 调用
→ Knowledge 检索
→ Diagnosis
→ Critic
→ Go Policy Engine
→ 审批（如需要）
→ remediation-mcp
→ Verify
```

所有步骤都带 `trace_id`、`run_id`、`agent_step_id` 和 `evidence_id`。

## 8. 修复安全设计

Go 策略引擎拥有最终否决权。以下条件全部满足，才允许调用写工具：

```text
支付状态为 SUCCESS
订单和支付金额一致
订单未发货、未取消
库存足够
不存在成功扣减记录
诊断置信度达到阈值
证据版本未过期
任务已获得所需审批
幂等键未被其他参数使用
```

修复工具执行前要二次读取业务状态，防止审批等待期间状态发生变化。库存服务使用数据库唯一约束和幂等键保证重复调用只产生一次成功扣减。

## 9. 工作流状态机

```text
CREATED
  → PLANNING
  → INVESTIGATING
  → KNOWLEDGE_LOOKUP
  → DIAGNOSING
  → CRITIC_REVIEW
  → POLICY_CHECK
  → AWAITING_APPROVAL
  → EXECUTING
  → VERIFYING
  → REPAIRED
```

异常分支：

```text
NO_ANOMALY
INCONCLUSIVE
REJECTED
EXECUTION_FAILED
VERIFICATION_FAILED
EVIDENCE_EXPIRED
```

状态迁移必须在事务中完成，并追加审计日志。

## 10. 数据模型

保留原 OrderGuard 的订单、支付、库存、Outbox 和消费记录表，新增 Agent 和 MCP 相关表：

```text
investigation_runs       一次自然语言调查任务
agent_steps              每个 Agent 的输入、输出和耗时
mcp_tool_calls           MCP 工具调用、参数摘要和结果
evidence_snapshots       不可变证据快照
knowledge_retrievals     知识检索记录
diagnoses                根因、置信度和证据引用
critic_reviews           审查结果和补查建议
repair_plans             修复方案和策略版本
repair_executions        幂等键、执行状态和错误
verification_results     修复后断言结果
audit_logs               用户、Agent、工具和状态审计
```

模型输出不直接覆盖旧数据；修复前后证据必须分别保存。

## 11. API 设计

```http
POST /api/v1/investigations
```

请求：

```json
{
  "message": "帮我调查订单 O1001 为什么支付成功但库存没有扣减",
  "order_id": "O1001"
}
```

返回：

```json
{
  "run_id": "run-001",
  "status": "PLANNING",
  "trace_id": "trace-001"
}
```

实时进度：

```http
GET /api/v1/investigations/{run_id}/events
```

其他接口：

```text
GET  /api/v1/investigations/{run_id}
GET  /api/v1/investigations/{run_id}/evidence
POST /api/v1/anomalies/{id}/approve
POST /api/v1/anomalies/{id}/reject
POST /api/v1/anomalies/{id}/execute
POST /api/v1/anomalies/{id}/verify
PUT  /api/v1/demo/faults/payment-event-drop
```

## 12. 前端控制台

首屏不是聊天窗口，而是“自然语言调查 + 证据工作台”：

```text
输入框：订单 O1001 支付成功但库存没扣，帮我查原因
[开始调查]
```

页面展示：

- Agent 时间线：Planner、Investigator、Knowledge、Diagnosis、Critic、Verify。
- MCP 工具调用：工具名、参数摘要、耗时、成功/失败。
- 证据链：来源服务、采集时间、原始 JSON、证据 ID。
- 根因候选：主原因、替代原因、置信度和依据。
- 修复审批：影响范围、风险、策略命中规则。
- 修复结果：幂等键、执行结果和验证断言。

## 13. 本地演示流程

```text
1. docker compose up
2. 开启 payment-event-drop 故障开关
3. 创建订单 O1001
4. 模拟支付成功
5. 确认订单 PAID、支付 SUCCESS、库存 NOT_DEDUCTED
6. 在前端输入自然语言调查问题
7. Planner 生成调查计划
8. Investigator 通过 MCP 查询业务、日志和事件
9. Knowledge 检索历史案例
10. Diagnosis 输出带证据的根因
11. Critic 审查并通过
12. Go 策略引擎要求人工审批
13. 用户批准，调用 remediation-mcp
14. Verify 重新查询并确认只扣减一次
```

## 14. 仓库结构

```text
orderGuard/
├── cmd/
│   ├── order-service/
│   ├── payment-service/
│   ├── inventory-service/
│   ├── reconciliation-service/
│   ├── business-mcp/
│   ├── observability-mcp/
│   ├── knowledge-mcp/
│   └── remediation-mcp/
├── internal/
│   ├── runtime/
│   ├── agents/
│   ├── mcp/
│   ├── policy/
│   ├── evidence/
│   ├── approval/
│   └── audit/
├── knowledge/
├── migrations/
├── web/
├── deployments/docker-compose.yml
├── test/e2e/
└── README.md
```

## 15. 分阶段开发计划

### 阶段 0：MCP 契约和项目骨架

- 冻结事件丢失这一种故障。
- 定义 MCP 工具命名、输入 Schema、错误格式和统一返回结构。
- 建立四个业务服务和四个 MCP Server 入口。
- Docker Compose 启动 PostgreSQL、Redis 和服务。

验收：所有 MCP Server 能启动；MCP Client 能发现工具并调用一个只读工具。

### 阶段 1：业务正常链路

- 实现创建订单、支付、Outbox、Redis Streams 和库存消费。
- 实现 `event_id`、`order_id + sku_id` 和幂等键约束。
- 完成正常链路和重复消息集成测试。

验收：支付成功后库存只扣减一次；同一事件重复投递 10 次仍只有一条成功扣减记录。

### 阶段 2：只读 MCP Server

- 完成 business-mcp 和 observability-mcp。
- 所有查询结果包含 `evidence_id`、来源、时间和原始数据。
- 实现工具超时、错误码和调用审计。

验收：不接模型时，通过测试程序可以完成完整证据采集。

### 阶段 3：Go Runtime 和 Planner/Investigator

- 实现任务状态机、MCP Client、工具白名单和证据存储。
- 接入 Planner Agent 和 Investigator Agent。
- 实现多轮工具调用和 SSE 进度。

验收：用户输入自然语言后，Agent 能动态调用至少四个 MCP 工具并保存调用轨迹。

### 阶段 4：Knowledge/Diagnosis/Critic

- 实现 knowledge-mcp 和本地知识库。
- 接入 Diagnosis Agent 和 Critic Agent。
- 加入 JSON Schema、证据 ID 和根因枚举校验。

验收：根因结论必须引用真实证据；证据不足时进入 `INCONCLUSIVE`，不能生成修复任务。

### 阶段 5：安全修复和 Verify

- 实现 Go Policy Engine、人工审批和 remediation-mcp。
- 实现 `deduct_inventory_once` 和状态二次确认。
- 接入 Verify Agent，完成修复后断言。

验收：事件丢失故障可以完成“调查 → 审批 → 修复 → 验证”；重复执行不会重复扣库存。

### 阶段 6：评测和可观测性

- 建立固定故障样本和标准答案。
- 对比规则、单 Agent 和多 Agent 的诊断准确率、证据完整率、错误修复率和耗时。
- 增加 Agent/MCP/工具链路的 OpenTelemetry 指标。

验收：能用真实实验数据说明多 Agent 相比单 Agent 的收益和代价。

## 16. 测试策略

### 单元测试

- Agent 输出 Schema、枚举和证据引用校验。
- MCP 工具参数校验和错误映射。
- 策略矩阵和状态机非法迁移。
- 幂等键重复调用和并发调用。

### 集成测试

- MCP Server 到业务服务的调用。
- Outbox 发布和 Redis Streams 消费。
- 消费者崩溃后的 Pending 消息恢复。
- 审批期间状态变化导致修复被拒绝。

### 端到端测试

```text
注入事件丢失
→ 创建并支付订单
→ 自然语言发起调查
→ Agent 通过 MCP 收集证据
→ 输出正确根因
→ Critic 审查
→ 审批并修复
→ Verify 通过
→ 重复执行修复
→ 断言库存只扣减一次
```

## 17. 简历和面试重点

项目不要强调“有几个 Agent”，应强调：

> 使用 Go 实现 MCP 化的 Agent Runtime，将订单、支付、库存、日志、Trace、指标和故障知识封装为可发现、可调用的工具。用户通过自然语言发起调查，Planner 和 Investigator Agent 动态规划并执行多轮工具调用，Knowledge、Diagnosis 和 Critic Agent 基于证据完成知识检索、根因推理和结论审查；Go 层负责策略否决、人工审批、幂等修复、审计和 Verify 闭环，模型不能直接访问业务数据或执行写操作。

项目真正的验收标准不是“模型能回答”，而是：

```text
故障能复现
工具能发现和调用
证据可追踪
结论可审查
修复有权限和幂等保护
结果可以再次验证
```



# 开发遇到的问题

