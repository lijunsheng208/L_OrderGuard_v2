package payment

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lijunsheng/orderguard/internal/httpx"
	"github.com/lijunsheng/orderguard/internal/tracing"
)

// Handler 提供支付和 Outbox 状态查询接口。
type Handler struct {
	repository *Repository
}

// NewHandler 创建支付 HTTP Handler。
func NewHandler(repository *Repository) http.Handler {
	handler := &Handler{repository: repository}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", paymentHealth)
	mux.HandleFunc("POST /demo/orders/{id}/pay", handler.pay)
	mux.HandleFunc("GET /payments/{id}", handler.get)
	mux.HandleFunc("GET /events/{id}/payment-succeeded", handler.getEvent)
	return tracing.Middleware(mux)
}

// paymentHealth 返回支付服务健康状态。
func paymentHealth(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "service": "payment-service",
	})
}

// pay 完成模拟支付，库存项从订单表读取而不是信任请求。
func (h *Handler) pay(w http.ResponseWriter, request *http.Request) {
	var input struct {
		PaymentID string `json:"payment_id"`
		Amount    int64  `json:"amount"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid payment request")
		return
	}
	if input.PaymentID == "" {
		input.PaymentID = "P_" + request.PathValue("id")
	}
	result, err := h.repository.Pay(
		request.Context(), input.PaymentID, request.PathValue("id"), input.Amount,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "PAYMENT_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

// get 返回订单对应的支付记录。
func (h *Handler) get(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.Get(request.Context(), request.PathValue("id"))
	if errors.Is(err, ErrOrderNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "PAYMENT_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_PAYMENT_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

// getEvent 返回订单支付成功事件的 Outbox 状态。
func (h *Handler) getEvent(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.GetOutboxStatus(
		request.Context(), request.PathValue("id"),
	)
	if errors.Is(err, ErrOrderNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "EVENT_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_EVENT_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order_id": request.PathValue("id"), "event_id": result.EventID,
		"event_type": result.EventType, "publish_status": result.PublishStatus,
		"published": result.PublishStatus == "PUBLISHED", "attempts": result.Attempts,
		"created_at": result.CreatedAt, "published_at": result.PublishedAt,
	})
}
