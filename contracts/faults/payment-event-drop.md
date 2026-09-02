# Payment event drop

阶段 0 冻结的唯一演示故障为：支付事务提交成功，但 `payment.succeeded` 事件没有发布，导致库存未扣减。

固定观测结果：

- 订单状态为 `PAID`。
- 支付状态为 `SUCCESS`。
- 支付金额与订单金额一致。
- `payment.succeeded` 事件不存在。
- 支付 Outbox 记录存在但未成功发布。
- 库存状态为 `NOT_DEDUCTED`，成功扣减次数为 `0`。

固定根因枚举：`PAYMENT_EVENT_NOT_PUBLISHED`。

阶段 0 只定义该场景及工具契约，不实现故障注入、业务状态持久化、事件发布或修复。

