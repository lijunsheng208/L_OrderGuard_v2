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

// value 读取环境变量，未设置时返回默认值。
func value(key, fallback string) string {
	if result := os.Getenv(key); result != "" {
		return result
	}
	return fallback
}
