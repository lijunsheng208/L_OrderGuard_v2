# 事故 001：支付事件发布延迟

## 结论

支付事务已提交，但 Outbox 仍为 `PENDING` 或 Redis 中不存在对应事件，会导致库存消费者没有机会扣减库存。

## 证据模式

通常同时观察到订单 `PAID`、支付 `SUCCESS`、库存 `NOT_DEDUCTED`，以及缺少 `payment.succeeded` 事件。

## 相关服务

涉及 order-service、payment-service、inventory-service、PostgreSQL Outbox 和 Redis Streams。
