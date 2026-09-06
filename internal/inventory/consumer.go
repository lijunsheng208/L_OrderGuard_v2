package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lijunsheng/orderguard/internal/eventbus"
	"github.com/lijunsheng/orderguard/internal/payment"
)

// Consumer 消费支付成功事件并在事务提交后 ACK。
type Consumer struct {
	stream     *eventbus.RedisStream
	repository *Repository
}

// NewConsumer 创建库存事件消费者。
func NewConsumer(stream *eventbus.RedisStream, repository *Repository) *Consumer {
	return &Consumer{stream: stream, repository: repository}
}

// Run 持续处理新消息和服务重启前遗留的 Pending 消息。
func (c *Consumer) Run(ctx context.Context) error {
	if err := c.stream.EnsureGroup(ctx); err != nil {
		return err
	}
	for {
		// Read new deliveries first. A permanently failing pending message must
		// not starve later events and prevent their diagnostic signals from being
		// recorded. Pending work is retried whenever there are no new messages.
		messages, err := c.stream.Consume(ctx, 20, time.Second)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			messages, err = c.stream.ClaimPending(ctx, 30*time.Second, 20)
			if err != nil {
				return err
			}
		}
		for _, message := range messages {
			if err := c.process(ctx, message); err != nil {
				continue
			}
			if err := c.stream.Ack(ctx, message.ID); err != nil {
				return err
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// process 解析并持久化一条支付成功事件。
func (c *Consumer) process(ctx context.Context, message eventbus.Message) error {
	var event payment.Event
	if err := json.Unmarshal(message.Payload, &event); err != nil {
		return fmt.Errorf("decode payment event: %w", err)
	}
	if event.EventType != "payment.succeeded" || event.EventID != message.EventID {
		return errors.New("unexpected payment event")
	}
	_, err := c.repository.Consume(ctx, event)
	return err
}
