package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lijunsheng/orderguard/internal/httpx"
	"github.com/lijunsheng/orderguard/internal/tracing"
)

// Handler 提供阶段 3 调查任务 REST 和 SSE 接口。
type Handler struct {
	repository   *Repository
	orchestrator *Orchestrator
}

// NewHandler 创建 Agent Runtime HTTP Handler。
func NewHandler(repository *Repository, orchestrator *Orchestrator) http.Handler {
	handler := &Handler{repository: repository, orchestrator: orchestrator}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("POST /api/v1/investigations", handler.createRun)
	mux.HandleFunc("GET /api/v1/investigations/{id}", handler.getRun)
	mux.HandleFunc("GET /api/v1/investigations/{id}/steps", handler.getSteps)
	mux.HandleFunc("GET /api/v1/investigations/{id}/evidence", handler.getEvidence)
	mux.HandleFunc("GET /api/v1/investigations/{id}/events", handler.streamEvents)
	return tracing.Middleware(mux)
}

// health 返回 Runtime 存活状态和模型配置状态。
func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "service": "agent-runtime",
		"model_configured": h.orchestrator.Ready(),
	})
}

// createRun 创建异步调查任务。
func (h *Handler) createRun(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Message string `json:"message"`
		OrderID string `json:"order_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid investigation request")
		return
	}
	run, err := h.orchestrator.CreateRun(
		request.Context(), strings.TrimSpace(input.Message),
		strings.TrimSpace(input.OrderID), tracing.ID(request.Context()),
	)
	if err != nil {
		status := http.StatusBadRequest
		code := "INVALID_INVESTIGATION"
		if !h.orchestrator.Ready() {
			status = http.StatusServiceUnavailable
			code = "MODEL_NOT_CONFIGURED"
		}
		httpx.WriteError(w, status, code, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, run)
}

// getRun 返回调查任务当前状态和结果。
func (h *Handler) getRun(w http.ResponseWriter, request *http.Request) {
	run, err := h.repository.GetRun(request.Context(), request.PathValue("id"))
	if errors.Is(err, ErrRunNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "RUN_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_RUN_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

// getSteps 返回 Planner 和 Investigator 执行轨迹。
func (h *Handler) getSteps(w http.ResponseWriter, request *http.Request) {
	if _, err := h.repository.GetRun(request.Context(), request.PathValue("id")); errors.Is(err, ErrRunNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "RUN_NOT_FOUND", ErrRunNotFound.Error())
		return
	} else if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_RUN_FAILED", err.Error())
		return
	}
	steps, err := h.repository.ListSteps(request.Context(), request.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_STEPS_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"steps": steps})
}

// getEvidence 返回任务的不可变证据集合。
func (h *Handler) getEvidence(w http.ResponseWriter, request *http.Request) {
	if _, err := h.repository.GetRun(request.Context(), request.PathValue("id")); errors.Is(err, ErrRunNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "RUN_NOT_FOUND", ErrRunNotFound.Error())
		return
	} else if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_RUN_FAILED", err.Error())
		return
	}
	evidence, err := h.repository.ListEvidence(request.Context(), request.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_EVIDENCE_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"evidence": evidence})
}

// streamEvents 按事件 ID 持续输出可重放的 SSE 进度。
func (h *Handler) streamEvents(w http.ResponseWriter, request *http.Request) {
	if _, err := h.repository.GetRun(request.Context(), request.PathValue("id")); errors.Is(err, ErrRunNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "RUN_NOT_FOUND", ErrRunNotFound.Error())
		return
	} else if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_RUN_FAILED", err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "SSE_UNSUPPORTED", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	afterID, _ := strconv.ParseInt(request.Header.Get("Last-Event-ID"), 10, 64)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, err := h.repository.ListEvents(
			request.Context(), request.PathValue("id"), afterID, 100,
		)
		if err != nil {
			return
		}
		for _, event := range events {
			_, _ = fmt.Fprintf(
				w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Payload,
			)
			afterID = event.ID
		}
		if len(events) > 0 {
			flusher.Flush()
			lastType := events[len(events)-1].Type
			if lastType == "run.completed" || lastType == "run.failed" {
				return
			}
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
