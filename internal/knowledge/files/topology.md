# 服务拓扑

## 订单链路

order-service 写入 PostgreSQL；payment-service 原子更新订单并写 Outbox；发布器写 Redis Streams；inventory-service 消费并写回 PostgreSQL。

## 调查链路

agent-runtime 通过 business-mcp 和 observability-mcp 读取业务证据，再通过 knowledge-mcp 查询本地知识。
