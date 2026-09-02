# payment-service

## 职责

在事务中写入支付成功记录、订单状态历史和 Outbox 事件。

## 关键状态

支付状态为 `SUCCESS`，Outbox 发布状态为 `PENDING` 或 `PUBLISHED`。

## 依赖

PostgreSQL 保存支付和 Outbox，后台发布器把事件写入 Redis Stream。
