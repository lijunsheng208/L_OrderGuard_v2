package inventory

import (
	"net/http"

	"github.com/lijunsheng/orderguard/internal/httpx"
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
	mux.HandleFunc("GET /stocks/{id}", handler.getStock)
	return mux
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
