package order

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lijunsheng/orderguard/internal/httpx"
	"github.com/lijunsheng/orderguard/internal/tracing"
)

// Handler 提供订单创建和快照查询接口。
type Handler struct {
	repository *Repository
}

// NewHandler 创建订单 HTTP Handler。
func NewHandler(repository *Repository) http.Handler {
	handler := &Handler{repository: repository}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /demo/orders", handler.create)
	mux.HandleFunc("GET /orders/{id}/snapshot", handler.get)
	mux.HandleFunc("GET /orders/{id}/history", handler.getHistory)
	return tracing.Middleware(mux)
}

// getHistory 返回订单状态机的不可变变更历史。
func (h *Handler) getHistory(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.GetHistory(request.Context(), request.PathValue("id"))
	if errors.Is(err, ErrOrderNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "ORDER_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_HISTORY_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order_id": request.PathValue("id"), "history": result,
	})
}

// health 返回订单服务健康状态。
func health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "service": "order-service",
	})
}

// create 创建订单。
func (h *Handler) create(w http.ResponseWriter, request *http.Request) {
	var input struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
		Items  []Item `json:"items"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid order request")
		return
	}
	created, err := h.repository.Create(
		request.Context(), input.ID, input.UserID, input.Items,
	)
	if errors.Is(err, ErrInvalidOrder) {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_ORDER", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusConflict, "CREATE_ORDER_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

// get 返回订单快照。
func (h *Handler) get(w http.ResponseWriter, request *http.Request) {
	result, err := h.repository.Get(request.Context(), request.PathValue("id"))
	if errors.Is(err, ErrOrderNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "ORDER_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_ORDER_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}
