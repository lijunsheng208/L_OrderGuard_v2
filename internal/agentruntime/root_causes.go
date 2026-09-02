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
		Code:             "PAYMENT_EVENT_NOT_PUBLISHED",
		Meaning:          "支付事务已经成功，但 payment.succeeded 没有成功进入 Redis Stream，或 Outbox 长时间保持 PENDING，库存消费者因此没有收到事件。",
		SupportingSignal: "订单为 PAID、支付为 SUCCESS、库存为 NOT_DEDUCTED，并且事件未找到，或 Outbox 显示 PENDING/发布失败。",
		Exclusion:        "事件已发布且库存消费者已经成功处理时，不能选择此原因。",
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
		SupportingSignal: "必须说明缺少哪些证据或哪些证据互相冲突。",
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
