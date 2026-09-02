package config

import "os"

// DatabaseURL 返回 PostgreSQL 连接地址。
func DatabaseURL() string {
	return value("DATABASE_URL", "postgres://orderguard:orderguard@localhost:55432/orderguard?sslmode=disable")
}

// RedisAddress 返回 Redis 主机和端口。
func RedisAddress() string {
	return value("REDIS_ADDR", "localhost:56379")
}

// EventStream 返回支付事件使用的 Redis Stream 名称。
func EventStream() string {
	return value("EVENT_STREAM", "orderguard.events")
}

// OrderServiceURL 返回订单服务基础地址。
func OrderServiceURL() string {
	return value("ORDER_SERVICE_URL", "http://localhost:8081")
}

// PaymentServiceURL 返回支付服务基础地址。
func PaymentServiceURL() string {
	return value("PAYMENT_SERVICE_URL", "http://localhost:8082")
}

// InventoryServiceURL 返回库存服务基础地址。
func InventoryServiceURL() string {
	return value("INVENTORY_SERVICE_URL", "http://localhost:8083")
}

// BusinessMCPURL 返回 business-mcp 的调用端点。
func BusinessMCPURL() string {
	return value("BUSINESS_MCP_URL", "http://localhost:8091/mcp")
}

// ObservabilityMCPURL 返回 observability-mcp 的调用端点。
func ObservabilityMCPURL() string {
	return value("OBSERVABILITY_MCP_URL", "http://localhost:8092/mcp")
}

// KnowledgeMCPURL 返回 knowledge-mcp 的调用端点。
func KnowledgeMCPURL() string { return value("KNOWLEDGE_MCP_URL", "http://localhost:8093/mcp") }

// AgentAPIKey 返回 Eino ChatModel 使用的 API Key。
func AgentAPIKey() string {
	return os.Getenv("AGENT_API_KEY")
}

// AgentBaseURL 返回 OpenAI-compatible 模型端点。
func AgentBaseURL() string {
	return os.Getenv("AGENT_BASE_URL")
}

// AgentModel 返回 Eino ChatModel 使用的模型名。
func AgentModel() string {
	return os.Getenv("AGENT_MODEL")
}

// value 读取环境变量，未设置时返回默认值。
func value(key, fallback string) string {
	if result := os.Getenv(key); result != "" {
		return result
	}
	return fallback
}
