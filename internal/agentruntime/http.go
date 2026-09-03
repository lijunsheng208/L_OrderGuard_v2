package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/httpx"
	"github.com/lijunsheng/orderguard/internal/mcp"
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
	mux.HandleFunc("GET /api/v1/investigations/{id}/approvals", handler.getApprovals)
	mux.HandleFunc("GET /api/v1/investigations/{id}/events", handler.streamEvents)
	mux.HandleFunc("POST /api/v1/investigations/{id}/approve", handler.approveRun)
	mux.HandleFunc("POST /api/v1/investigations/{id}/execute", handler.executeRun)
	mux.HandleFunc("POST /api/v1/demo/faults", handler.injectDemoFault)
	return tracing.Middleware(cors(mux))
}

func (h *Handler) injectDemoFault(w http.ResponseWriter, request *http.Request) {
	var input struct {
		OrderID   string `json:"order_id"`
		FaultType string `json:"fault_type"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, request.Body, 1<<16)).Decode(&input); err != nil {
		httpx.WriteError(w, 400, "INVALID_JSON", "invalid fault request")
		return
	}
	orderID := strings.TrimSpace(input.OrderID)
	if orderID == "" {
		var err error
		orderID, err = h.createDemoOrder(request.Context(), input.FaultType)
		if err != nil {
			httpx.WriteError(w, 502, "DEMO_ORDER_CREATION_FAILED", err.Error())
			return
		}
	} else if err := h.repository.InjectDemoFault(request.Context(), orderID, input.FaultType); err != nil {
		if errors.Is(err, ErrRunNotFound) {
			httpx.WriteError(w, 404, "ORDER_NOT_FOUND", "order was not found")
			return
		}
		httpx.WriteError(w, 400, "FAULT_INJECTION_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"order_id": orderID, "fault_type": input.FaultType, "status": "injected"})
}

// createDemoOrder creates a disposable order, enables its fault switch, then pays it.
func (h *Handler) createDemoOrder(ctx context.Context, faultType string) (string, error) {
	orderID := "O-DEMO-INJECT-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	paymentID := "P-DEMO-INJECT-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	client := &http.Client{Timeout: 5 * time.Second}
	orderBody := map[string]any{"id": orderID, "user_id": "U-DEMO", "items": []map[string]any{{"sku_id": "SKU1", "quantity": 1, "unit_price": 19900}}}
	if err := postJSON(ctx, client, config.OrderServiceURL()+"/demo/orders", orderBody); err != nil {
		return "", err
	}
	// Enable the switch before payment creates and publishes the event.
	if err := h.repository.InjectDemoFault(ctx, orderID, faultType); err != nil {
		return "", err
	}
	if err := postJSON(ctx, client, config.PaymentServiceURL()+"/demo/orders/"+url.PathEscape(orderID)+"/pay", map[string]any{"payment_id": paymentID, "amount": 19900}); err != nil {
		return "", err
	}
	return orderID, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// cors 处理本地 React 控制台的跨域预检请求。
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID, X-Trace-ID")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// executeRun 在无需人工审批的模式下执行幂等修复并验证结果。
func (h *Handler) executeRun(w http.ResponseWriter, request *http.Request) {
	run, err := h.repository.GetRun(request.Context(), request.PathValue("id"))
	if errors.Is(err, ErrRunNotFound) {
		httpx.WriteError(w, 404, "RUN_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "QUERY_RUN_FAILED", err.Error())
		return
	}
	if run.Status != StatusAwaitingApproval {
		httpx.WriteError(w, 409, "EXECUTION_REJECTED", "investigation is not awaiting approval")
		return
	}
	var input struct {
		Items          []map[string]any `json:"items"`
		IdempotencyKey string           `json:"idempotency_key"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		httpx.WriteError(w, 400, "INVALID_EXECUTION", "items and idempotency_key are required")
		return
	}
	var investigation InvestigationResult
	if err := json.Unmarshal(run.FinalSummary, &investigation); err != nil || investigation.Diagnosis == nil {
		httpx.WriteError(w, http.StatusConflict, "DIAGNOSIS_REQUIRED", "investigation diagnosis is unavailable")
		return
	}
	action, ok := RepairActionForRootCause(investigation.Diagnosis.RootCause)
	if !ok {
		httpx.WriteError(w, http.StatusConflict, "REPAIR_ACTION_UNAVAILABLE", "root cause has no executable repair action")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = "repair-" + run.ID + "-" + strings.ToLower(action)
	}
	if action == "RETRY_INVENTORY_DEDUCTION" && (len(input.Items) == 0 || input.IdempotencyKey == "") {
		httpx.WriteError(w, 400, "INVALID_EXECUTION", "items and idempotency_key are required for inventory repair")
		return
	}
	eventID := ""
	if action == "RETRY_OUTBOX_PUBLISH" || action == "RETRY_INVENTORY_CONSUMER" {
		evidence, _ := h.repository.ListEvidence(request.Context(), run.ID)
		eventID = eventIDFromEvidence(evidence)
		if eventID == "" {
			httpx.WriteError(w, http.StatusConflict, "EVENT_ID_REQUIRED", "repair requires the original event_id")
			return
		}
	}
	if err := h.repository.Transition(request.Context(), &run, StatusExecuting, "repair.started", nil); err != nil {
		httpx.WriteError(w, 409, "EXECUTION_REJECTED", err.Error())
		return
	}
	planID, err := h.repository.CreateRepairPlan(request.Context(), run.ID, action, run.Version, map[string]any{"items": input.Items, "idempotency_key": input.IdempotencyKey, "event_id": eventID})
	if err != nil {
		httpx.WriteError(w, 500, "REPAIR_PLAN_FAILED", err.Error())
		return
	}
	executionID, err := h.repository.StartRepairExecution(request.Context(), planID, run.ID, input.IdempotencyKey, input)
	if err != nil {
		httpx.WriteError(w, 500, "REPAIR_EXECUTION_RECORD_FAILED", err.Error())
		return
	}
	client := mcp.NewClient(config.RemediationMCPURL(), &http.Client{Timeout: 5 * time.Second})
	callErr := client.Initialize(request.Context())
	var repairResult mcp.ToolCallResult
	if callErr == nil {
		var args map[string]any
		args, callErr = remediationArguments(action, run.OrderID, eventID, input.IdempotencyKey, input.Items)
		if callErr == nil {
			repairResult, callErr = client.CallToolWithMetadata(request.Context(), remediationToolForAction(action), args, mcp.RequestMetadata{RunID: run.ID, TraceID: run.TraceID, Caller: "runtime-policy"})
		}
	}
	if callErr != nil || !repairResult.StructuredContent.Success {
		detail := fmt.Sprint(callErr)
		if callErr == nil && repairResult.StructuredContent.Error != nil {
			detail = repairResult.StructuredContent.Error.Code + ": " + repairResult.StructuredContent.Error.Message
		}
		if detail == "<nil>" || detail == "" {
			detail = "remediation tool returned an unsuccessful result"
		}
		_ = h.repository.FinishRepairExecution(request.Context(), executionID, "FAILED", "EXECUTION_FAILED", map[string]any{"action": action, "tool": remediationToolForAction(action), "error": detail})
		_, _ = h.repository.AppendEvent(request.Context(), run.ID, "repair.failed", map[string]any{"action": action, "tool": remediationToolForAction(action), "error": detail})
		_ = h.repository.Fail(request.Context(), &run, StatusExecutionFailed, "EXECUTION_FAILED", detail)
		httpx.WriteError(w, 502, "EXECUTION_FAILED", detail)
		return
	}
	_ = h.repository.FinishRepairExecution(request.Context(), executionID, "SUCCEEDED", "", repairResult.StructuredContent)
	_ = h.repository.Transition(request.Context(), &run, StatusVerifying, "verification.started", nil)
	status, verifyErr := h.waitForInventoryRepair(request.Context(), run.OrderID)
	if verifyErr != nil {
		_, _ = h.repository.AppendEvent(request.Context(), run.ID, "verification.failed", map[string]any{"error": verifyErr.Error()})
		_ = h.repository.Fail(request.Context(), &run, StatusVerificationFailed, "VERIFICATION_FAILED", verifyErr.Error())
		httpx.WriteError(w, 502, "VERIFICATION_FAILED", "inventory verification failed")
		return
	}
	facts := map[string]any{"inventory_status": status.Status, "successful_deduction_count": status.Successful}
	_, _ = h.repository.AppendEvent(request.Context(), run.ID, "verify.started", map[string]any{"action": action})
	var assertionErr error
	if status.Status != "DEDUCTED" || status.Successful != 1 {
		assertionErr = fmt.Errorf("inventory status=%s successful_deduction_count=%d", status.Status, status.Successful)
	} else if h.orchestrator == nil {
		assertionErr = errors.New("verify agent is not configured")
	} else {
		assertionErr = h.orchestrator.RunVerifyAgent(request.Context(), run, facts)
	}
	if assertionErr != nil {
		_, _ = h.repository.AppendEvent(request.Context(), run.ID, "verify.completed", map[string]any{"approved": false, "error": assertionErr.Error()})
		_, _ = h.repository.AppendEvent(request.Context(), run.ID, "verification.failed", map[string]any{"error": assertionErr.Error(), "facts": facts})
		_ = h.repository.SaveVerificationResult(request.Context(), run.ID, executionID, "FAILED", facts, nil, "verification assertions failed")
		_ = h.repository.Fail(request.Context(), &run, StatusVerificationFailed, "VERIFICATION_FAILED", assertionErr.Error())
		httpx.WriteError(w, 409, "VERIFICATION_FAILED", "verification assertions failed")
		return
	}
	_, _ = h.repository.AppendEvent(request.Context(), run.ID, "verify.completed", map[string]any{"approved": true, "facts": facts})
	_ = h.repository.SaveVerificationResult(request.Context(), run.ID, executionID, "PASSED", facts, nil, "repair verified")
	_ = h.repository.Transition(request.Context(), &run, StatusRepaired, "run.repaired", map[string]any{"successful_deduction_count": status.Successful})
	httpx.WriteJSON(w, http.StatusOK, run)
}

// waitForInventoryRepair allows the asynchronous consumer to process a retried event.
type inventoryRepairStatus struct {
	Status     string `json:"status"`
	Successful int    `json:"successful_deduction_count"`
}

func (h *Handler) waitForInventoryRepair(ctx context.Context, orderID string) (inventoryRepairStatus, error) {
	var status inventoryRepairStatus
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(8 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.InventoryServiceURL(), "/")+"/inventory/"+url.PathEscape(orderID)+"/status", nil)
		if err != nil {
			return status, err
		}
		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode < 300 {
				_ = json.NewDecoder(resp.Body).Decode(&status)
			}
			resp.Body.Close()
			if status.Status == "DEDUCTED" && status.Successful >= 1 {
				return status, nil
			}
		}
		if time.Now().After(deadline) {
			return status, fmt.Errorf("inventory status did not reach DEDUCTED")
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// approveRun 审批诊断结论；健康订单直接结束，异常订单进入修复执行准备。
func (h *Handler) approveRun(w http.ResponseWriter, request *http.Request) {
	run, err := h.repository.GetRun(request.Context(), request.PathValue("id"))
	if errors.Is(err, ErrRunNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "RUN_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_RUN_FAILED", err.Error())
		return
	}
	if run.Status != StatusEvidenceCollected {
		httpx.WriteError(w, http.StatusConflict, "POLICY_REJECTED", "investigation is not ready for approval")
		return
	}
	var result InvestigationResult
	if err := json.Unmarshal(run.FinalSummary, &result); err != nil || result.Diagnosis == nil {
		httpx.WriteError(w, http.StatusConflict, "DIAGNOSIS_REQUIRED", "investigation diagnosis is unavailable")
		return
	}
	if result.Diagnosis.RootCause == "NO_ISSUE" {
		record, err := h.repository.SaveApprovalRecord(request.Context(), run.ID, "NO_ISSUE_CONFIRMATION", "APPROVED", result.Diagnosis.RootCause, StatusNoAnomaly)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "APPROVAL_RECORD_FAILED", err.Error())
			return
		}
		if err := h.repository.Transition(request.Context(), &run, StatusNoAnomaly, "approval.completed", map[string]any{
			"approval_id": record.ID, "type": record.Type, "decision": record.Decision,
			"conclusion": record.Conclusion, "next_status": record.NextStatus,
		}); err != nil {
			httpx.WriteError(w, http.StatusConflict, "APPROVAL_FAILED", err.Error())
			return
		}
		httpx.WriteJSON(w, http.StatusOK, run)
		return
	}
	if _, ok := RepairActionForRootCause(result.Diagnosis.RootCause); !ok {
		httpx.WriteError(w, http.StatusConflict, "REPAIR_ACTION_UNAVAILABLE", "diagnosis has no approved repair action")
		return
	}
	if err := h.repository.Transition(request.Context(), &run, StatusPolicyCheck, "policy.checked", map[string]any{"approved": true}); err != nil {
		httpx.WriteError(w, http.StatusConflict, "POLICY_REJECTED", err.Error())
		return
	}
	if err := h.repository.Transition(request.Context(), &run, StatusAwaitingApproval, "approval.requested", nil); err != nil {
		httpx.WriteError(w, http.StatusConflict, "APPROVAL_FAILED", err.Error())
		return
	}
	if _, err := h.repository.SaveApprovalRecord(request.Context(), run.ID, "REPAIR_APPROVAL", "APPROVED", result.Diagnosis.RootCause, StatusAwaitingApproval); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "APPROVAL_RECORD_FAILED", err.Error())
		return
	}
	_, _ = h.repository.AppendEvent(request.Context(), run.ID, "approval.completed", map[string]any{
		"type": "REPAIR_APPROVAL", "decision": "APPROVED", "next_status": StatusAwaitingApproval,
	})
	httpx.WriteJSON(w, http.StatusOK, run)
}

// getApprovals 返回指定调查的审批历史。
func (h *Handler) getApprovals(w http.ResponseWriter, request *http.Request) {
	if _, err := h.repository.GetRun(request.Context(), request.PathValue("id")); errors.Is(err, ErrRunNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "RUN_NOT_FOUND", ErrRunNotFound.Error())
		return
	} else if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_RUN_FAILED", err.Error())
		return
	}
	records, err := h.repository.ListApprovalRecords(request.Context(), request.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "QUERY_APPROVALS_FAILED", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"approvals": records})
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
