package inventory

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lijunsheng/orderguard/internal/httpx"
	"github.com/lijunsheng/orderguard/internal/tracing"
)

// Handler 提供库存扣减和库存快照查询。
type Handler struct {
	repository *Repository
}

// NewHandler 创建库存 HTTP Handler。
func NewHandler(repository *Repository) http.Handler {
	handler := &Handler{repository: repository}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", inventoryHealth)
	mux.HandleFunc("GET /inventory/{id}/deductions", handler.getDeductions)
	mux.HandleFunc("GET /inventory/{id}/status", handler.getStatus)
	mux.HandleFunc("GET /stocks/{id}", handler.getStock)
	mux.HandleFunc("POST /inventory/{id}/deduct-once", handler.deductOnce)
	mux.HandleFunc("POST /inventory/events/{id}/redeliver", handler.redeliver)
	mux.HandleFunc("POST /inventory/{id}/deduct-retry", handler.deductRetry)
	mux.HandleFunc("GET /inventory/{id}/reconcile", handler.reconcile)
	return tracing.Middleware(mux)
}

func (h *Handler) redeliver(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.RetryEvent(request.Context(), request.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, 502, "EVENT_REDELIVERY_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "processed", "deductions": result})
}
func (h *Handler) deductRetry(w http.ResponseWriter, request *http.Request) { h.deductOnce(w, request) }
func (h *Handler) reconcile(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.ReconcileOrder(request.Context(), request.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, 502, "RECONCILE_FAILED", err.Error())
		return
	}
	status, err := h.repository.GetOrderStatus(request.Context(), request.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, 502, "RECONCILE_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order_id": request.PathValue("id"), "deductions": result, "inventory": status, "reconciled": status.Status == Deducted && status.SuccessfulDeductionCount >= 1})
}

// deductOnce 在二次读取业务状态后执行幂等库存扣减。
func (h *Handler) deductOnce(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Items          []Item `json:"items"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid deduction request")
		return
	}
	result, err := h.repository.DeductOnce(request.Context(), request.PathValue("id"), input.Items, input.IdempotencyKey)
	if errors.Is(err, ErrOrderNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "ORDER_NOT_FOUND", err.Error())
		return
	}
	if errors.Is(err, ErrInsufficientStock) {
		httpx.WriteError(w, http.StatusConflict, "INSUFFICIENT_STOCK", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusConflict, "DEDUCTION_REJECTED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order_id": request.PathValue("id"), "deductions": result})
}

// getStatus 返回订单维度的库存处理状态。
func (h *Handler) getStatus(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.GetOrderStatus(request.Context(), request.PathValue("id"))
	if errors.Is(err, ErrOrderNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "ORDER_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_INVENTORY_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

// inventoryHealth 返回库存服务健康状态。
func inventoryHealth(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "service": "inventory-service",
	})
}

// getDeductions 返回订单库存扣减记录。
func (h *Handler) getDeductions(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.GetDeductions(
		request.Context(), request.PathValue("id"),
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_DEDUCTIONS_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

// getStock 返回指定 SKU 库存快照。
func (h *Handler) getStock(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.GetStock(request.Context(), request.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "STOCK_NOT_FOUND", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}
