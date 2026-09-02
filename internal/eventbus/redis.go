package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStream 封装 Redis Streams 发布和消费组操作。
type RedisStream struct {
	client   *redis.Client
	stream   string
	group    string
	consumer string
}

// Message 表示从消费组读到的一条消息。
type Message struct {
	ID      string
	EventID string
	Payload []byte
}

// NewRedisStream 创建 Redis Streams 适配器。
func NewRedisStream(client *redis.Client, stream, group, consumer string) *RedisStream {
	return &RedisStream{
		client: client, stream: stream, group: group, consumer: consumer,
	}
}

// EnsureGroup 创建消费组，已存在时视为成功。
func (s *RedisStream) EnsureGroup(ctx context.Context) error {
	err := s.client.XGroupCreateMkStream(ctx, s.stream, s.group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create redis consumer group: %w", err)
	}
	return nil
}

// Publish 把 JSON 事件写入 Stream。
func (s *RedisStream) Publish(ctx context.Context, eventID string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	if err := s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: s.stream,
		Values: map[string]any{"event_id": eventID, "payload": encoded},
	}).Err(); err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	return nil
}

// Consume 读取当前消费组中的新消息。
func (s *RedisStream) Consume(
	ctx context.Context,
	count int64,
	block time.Duration,
) ([]Message, error) {
	streams, err := s.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: s.group, Consumer: s.consumer,
		Streams: []string{s.stream, ">"}, Count: count, Block: block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("consume events: %w", err)
	}
	return convertMessages(streams), nil
}

// ClaimPending 接管空闲超时的未确认消息。
func (s *RedisStream) ClaimPending(
	ctx context.Context,
	minIdle time.Duration,
	count int64,
) ([]Message, error) {
	messages, _, err := s.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: s.stream, Group: s.group, Consumer: s.consumer,
		MinIdle: minIdle, Start: "0-0", Count: count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim pending events: %w", err)
	}
	return convertEntries(messages), nil
}

// Ack 确认消息已经成功持久化处理结果。
func (s *RedisStream) Ack(ctx context.Context, messageID string) error {
	if err := s.client.XAck(ctx, s.stream, s.group, messageID).Err(); err != nil {
		return fmt.Errorf("ack event: %w", err)
	}
	return nil
}

// convertMessages 展开 Redis 按 Stream 分组的消息。
func convertMessages(streams []redis.XStream) []Message {
	result := make([]Message, 0)
	for _, stream := range streams {
		result = append(result, convertEntries(stream.Messages)...)
	}
	return result
}

// convertEntries 转换 Redis 消息字段。
func convertEntries(entries []redis.XMessage) []Message {
	result := make([]Message, 0, len(entries))
	for _, entry := range entries {
		result = append(result, Message{
			ID:      entry.ID,
			EventID: fmt.Sprint(entry.Values["event_id"]),
			Payload: []byte(fmt.Sprint(entry.Values["payload"])),
		})
	}
	return result
}
