package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/lijunsheng/orderguard/internal/mcp"
)

var ErrToolDenied = errors.New("tool call denied by runtime guard")

// ToolGuard 为单次调查限制工具权限和调用预算。
type ToolGuard struct {
	mu            sync.Mutex
	orderID       string
	maxCalls      int
	maxDuplicates int
	total         int
	seen          map[string]int
}

// NewToolGuard 创建绑定订单的只读工具守卫。
func NewToolGuard(orderID string, maxCalls, maxDuplicates int) *ToolGuard {
	return &ToolGuard{
		orderID: orderID, maxCalls: maxCalls, maxDuplicates: maxDuplicates,
		seen: make(map[string]int),
	}
}

// Authorize 校验工具只读属性、订单边界和调用预算。
func (g *ToolGuard) Authorize(definition mcp.Tool, arguments map[string]any) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !definition.Annotations.ReadOnlyHint || definition.Annotations.DestructiveHint {
		return fmt.Errorf("%w: tool %s is not read-only", ErrToolDenied, definition.Name)
	}
	if orderID, ok := arguments["order_id"].(string); ok && orderID != g.orderID {
		return fmt.Errorf("%w: order_id is outside this investigation", ErrToolDenied)
	}
	if g.total >= g.maxCalls {
		return fmt.Errorf("%w: maximum tool calls reached", ErrToolDenied)
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("encode guarded arguments: %w", err)
	}
	key := definition.Name + ":" + string(encoded)
	if g.seen[key] >= g.maxDuplicates {
		return fmt.Errorf("%w: duplicate tool call limit reached", ErrToolDenied)
	}
	g.total++
	g.seen[key]++
	return nil
}

// Total 返回当前已授权工具调用次数。
func (g *ToolGuard) Total() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.total
}
