#!/usr/bin/env bash
set -euo pipefail

# 创建 OrderGuard 演示数据：正常、事件丢失、状态冲突三种场景。
COMPOSE_FILE="${COMPOSE_FILE:-deployments/docker-compose.yml}"
ORDER_URL="${ORDER_URL:-http://localhost:8081}"
PAYMENT_URL="${PAYMENT_URL:-http://localhost:8082}"

compose_psql() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U orderguard -d orderguard "$@"
}

create_and_pay() {
  local order_id="$1" payment_id="$2"
  curl --fail --silent --show-error -X POST "$ORDER_URL/demo/orders" \
    -H 'Content-Type: application/json' \
    -d "{\"id\":\"$order_id\",\"user_id\":\"U-DEMO\",\"items\":[{\"sku_id\":\"SKU1\",\"quantity\":1,\"unit_price\":19900}]}" >/dev/null
  curl --fail --silent --show-error -X POST "$PAYMENT_URL/demo/orders/$order_id/pay" \
    -H 'Content-Type: application/json' \
    -d "{\"payment_id\":\"$payment_id\",\"amount\":19900}" >/dev/null
}

echo "清理旧演示数据..."
compose_psql <<'SQL'
DELETE FROM agent.investigation_runs WHERE order_id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
DELETE FROM inventory.deductions WHERE order_id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
DELETE FROM inventory.consumed_events WHERE event_id IN ('evt_P-DEMO-HEALTHY', 'evt_P-DEMO-EVENT-LOST', 'evt_P-DEMO-STATE-CONFLICT');
DELETE FROM observability.signals WHERE order_id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
DELETE FROM payments.outbox_events WHERE aggregate_id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
DELETE FROM payments.payments WHERE order_id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
DELETE FROM orders.order_items WHERE order_id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
DELETE FROM orders.state_history WHERE order_id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
DELETE FROM orders.orders WHERE id IN ('O-DEMO-HEALTHY', 'O-DEMO-EVENT-LOST', 'O-DEMO-STATE-CONFLICT');
SQL

echo "创建正常订单 O-DEMO-HEALTHY..."
create_and_pay O-DEMO-HEALTHY P-DEMO-HEALTHY

echo "创建事件丢失订单 O-DEMO-EVENT-LOST..."
create_and_pay O-DEMO-EVENT-LOST P-DEMO-EVENT-LOST
compose_psql <<'SQL'
-- 保留 PAID/SUCCESS，但移除库存成功扣减和消费记录。
DELETE FROM inventory.deductions WHERE order_id = 'O-DEMO-EVENT-LOST';
DELETE FROM inventory.consumed_events WHERE event_id = 'evt_P-DEMO-EVENT-LOST';
UPDATE payments.outbox_events
SET publish_status = 'PUBLISHED', attempts = 1, published_at = now()
WHERE event_id = 'evt_P-DEMO-EVENT-LOST';
SQL

# 清除事件索引，确保 get_event_record 返回 found=false。
docker compose -f "$COMPOSE_FILE" exec -T redis \
  redis-cli HDEL "orderguard:event-index:O-DEMO-EVENT-LOST" "type:payment.succeeded" >/dev/null

echo "创建状态冲突订单 O-DEMO-STATE-CONFLICT..."
create_and_pay O-DEMO-STATE-CONFLICT P-DEMO-STATE-CONFLICT
compose_psql <<'SQL'
-- 保留库存消费者成功日志，但删除最终扣减记录，模拟事务持久化异常。
DELETE FROM inventory.deductions WHERE order_id = 'O-DEMO-STATE-CONFLICT';
DELETE FROM inventory.consumed_events WHERE event_id = 'evt_P-DEMO-STATE-CONFLICT';
INSERT INTO observability.signals (
  trace_id, order_id, service_name, signal_type, operation, status, message,
  attributes, started_at, finished_at
)
SELECT COALESCE(payload->>'trace_id', 'trace-demo-state-conflict'), 'O-DEMO-STATE-CONFLICT', 'inventory-service', 'LOG',
  'deduct_inventory', 'OK', 'payment event consumed and inventory deducted',
  '{"event_id":"evt_P-DEMO-STATE-CONFLICT","deduction_count":1}'::jsonb,
  now(), now()
FROM payments.outbox_events
WHERE event_id = 'evt_P-DEMO-STATE-CONFLICT';
SQL

cat <<'INFO'

演示事故已创建：

1. O-DEMO-HEALTHY
   PAID / SUCCESS / PUBLISHED / DEDUCTED
   预期：Diagnosis = NO_ISSUE，审批确认后进入 NO_ANOMALY，不调用 Verify Agent。

2. O-DEMO-EVENT-LOST
   PAID / SUCCESS / PUBLISHED / NOT_DEDUCTED
   预期：Diagnosis = EVENT_NOT_AVAILABLE_AFTER_PUBLISH；需要补充投递证据后再决定是否修复。

3. O-DEMO-STATE-CONFLICT
   Trace/Logs 显示库存扣减成功，但快照为 NOT_DEDUCTED
   预期：Diagnosis = NO_CONFIRMED_ROOT_CAUSE，进入 INCONCLUSIVE，不审批、不修复、不调用 Verify。

4. 其他故障可通过前端“演示故障注入”创建：Outbox 未发布、库存消费者失败、库存扣减未持久化。

在前端分别使用对应订单号发起调查即可。
INFO
