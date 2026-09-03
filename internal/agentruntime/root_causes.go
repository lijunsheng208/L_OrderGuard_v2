package agentruntime

import "strings"

// RootCauseDefinition 定义一个允许 Diagnosis Agent 使用的根因。
type RootCauseDefinition struct {
	Code             string
	Meaning          string
	SupportingSignal string
	Exclusion        string
}

// RootCauseCatalog 是系统允许输出的根因目录，枚举和描述只维护这一份。
var RootCauseCatalog = []RootCauseDefinition{
	{
		Code:             "NO_ISSUE",
		Meaning:          "当前证据显示订单链路正常，没有发现需要修复的异常。",
		SupportingSignal: "支付为 SUCCESS、Outbox 为 PUBLISHED、库存为 DEDUCTED，且没有失败或未消费证据。",
		Exclusion:        "存在未扣减、发布失败、消费失败或状态冲突证据时，不能选择此结果。",
	},
	{
		Code:             "PAYMENT_EVENT_NOT_PUBLISHED",
		Meaning:          "支付事务已经成功，但 payment.succeeded 没有成功进入 Redis Stream，或 Outbox 长时间保持 PENDING，库存消费者因此没有收到事件。",
		SupportingSignal: "订单为 PAID、支付为 SUCCESS、库存为 NOT_DEDUCTED，并且事件未找到，或 Outbox 显示 PENDING/发布失败。",
		Exclusion:        "事件已发布且库存消费者已经成功处理时，不能选择此原因。",
	},
	{
		Code:             "PAYMENT_EVENT_NOT_CREATED",
		Meaning:          "支付已经成功，但没有创建对应的支付成功 Outbox 事件。",
		SupportingSignal: "支付为 SUCCESS，Outbox found=false。",
		Exclusion:        "存在 Outbox 记录时不能选择此原因。",
	},
	{
		Code:             "EVENT_NOT_AVAILABLE_AFTER_PUBLISH",
		Meaning:          "Outbox 标记已发布，但消息系统中无法读取对应事件。",
		SupportingSignal: "Outbox 为 PUBLISHED 且事件查询 found=false。",
		Exclusion:        "事件明确存在，或 Outbox 尚未发布时不能选择此原因。",
	},
	{
		Code:             "INVENTORY_EVENT_NOT_CONSUMED",
		Meaning:          "支付成功事件存在，但库存消费者没有处理该事件。",
		SupportingSignal: "事件 found=true、库存 NOT_DEDUCTED，且没有消费者接收或处理记录。",
		Exclusion:        "存在消费者失败记录或消费者成功记录时不能选择此原因。",
	},
	{
		Code:             "INVENTORY_DEDUCTION_FAILED",
		Meaning:          "库存消费者收到了事件，但扣减操作失败。",
		SupportingSignal: "消费者收到事件且存在明确的错误、失败或异常状态，库存为 NOT_DEDUCTED。",
		Exclusion:        "消费者成功且库存未持久化时应选择 INVENTORY_DEDUCTION_NOT_PERSISTED。",
	},
	{
		Code:             "INVENTORY_DEDUCTION_NOT_PERSISTED",
		Meaning:          "消费者日志或 Trace 显示扣减成功，但最终库存状态没有持久化。",
		SupportingSignal: "消费者成功/扣减 OK，同时库存为 NOT_DEDUCTED。",
		Exclusion:        "没有消费者成功证据时不能选择此原因。",
	},
	{
		Code:             "INVENTORY_CONSUMER_UNAVAILABLE",
		Meaning:          "支付成功事件已经发布，但库存消费者没有成功处理该事件，例如消费者不可用、消费失败或消息持续处于待处理状态。",
		SupportingSignal: "事件存在且 Outbox 为 PUBLISHED，但库存仍为 NOT_DEDUCTED，同时存在库存服务错误日志、Trace 或未确认消息证据。",
		Exclusion:        "只有库存未扣减而没有消费者故障证据时，不能单独选择此原因。",
	},
	{
		Code:             "NO_CONFIRMED_ROOT_CAUSE",
		Meaning:          "现有证据不足以确认任何根因，或不同证据之间存在冲突。",
		SupportingSignal: "必须说明缺少哪些证据或哪些证据互相冲突，例如同一库存操作同时出现成功与回滚，或工具返回无法解析的业务状态。",
		Exclusion:        "不得推荐执行修复动作，confidence 不得超过 0.5。",
	},
}

// RootCauseCodes 返回允许的根因代码。
func RootCauseCodes() map[string]bool {
	result := make(map[string]bool, len(RootCauseCatalog))
	for _, item := range RootCauseCatalog {
		result[item.Code] = true
	}
	return result
}

// RootCauseContext 将完整根因目录格式化为 Diagnosis Prompt 上下文。
func RootCauseContext() string {
	var builder strings.Builder
	builder.WriteString("以下是本系统允许使用的完整根因目录。只能从这些 Code 中选择，不得创建新值：\n\n")
	for index, item := range RootCauseCatalog {
		builder.WriteString(item.Code)
		builder.WriteString("\n含义：")
		builder.WriteString(item.Meaning)
		builder.WriteString("\n支持证据：")
		builder.WriteString(item.SupportingSignal)
		builder.WriteString("\n排除条件：")
		builder.WriteString(item.Exclusion)
		if index < len(RootCauseCatalog)-1 {
			builder.WriteString("\n\n")
		}
	}
	return builder.String()
}
