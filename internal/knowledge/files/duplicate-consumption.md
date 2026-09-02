# 重复消费支付事件

## 症状

同一个支付事件可能被 Redis 重投，但库存扣减记录只能有一份。

## 判断

检查 `event_delivery_count` 和 `successful_deduction_count`。投递次数大于一不等于重复扣库存。

## 保护

库存服务以事件 ID 和订单商品幂等键约束重复消费。
