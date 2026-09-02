# inventory-service

## 职责

消费 `payment.succeeded`，锁定库存并记录一次幂等扣减。

## 关键状态

库存汇总为 `NOT_DEDUCTED` 或 `DEDUCTED`，并同时记录事件投递次数。

## 依赖

Redis Streams Consumer Group 和 PostgreSQL 库存表、消费记录表。
