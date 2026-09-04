export type RunStatus = string
export type Run = { run_id: string; message: string; order_id: string; status: RunStatus; final_summary?: { diagnosis?: Diagnosis } }
export type Diagnosis = { root_cause: string; confidence: number; evidence_ids: string[]; recommended_action?: string }
export type Step = { step_id: string; agent_type: string; status: string; error_code?: string; started_at: string }
export type Evidence = { evidence_id: string; tool_name: string; source: string; data: unknown; collected_at: string }
export type TimelineEvent = { id?: number; type: string; payload: unknown; created_at?: string }
export type Approval = { approval_id: string; run_id: string; type: 'NO_ISSUE_CONFIRMATION' | 'REPAIR_APPROVAL'; decision: string; conclusion: string; next_status: string; created_at: string }

const API = import.meta.env.VITE_API_URL || 'http://localhost:8090'
async function request<T>(path: string, init?: RequestInit): Promise<T> { const response = await fetch(`${API}${path}`, init); if (!response.ok) throw new Error(await response.text() || `HTTP ${response.status}`); return response.json() as Promise<T> }
export const createInvestigation = (message: string, orderId: string) => request<Run>('/api/v1/investigations', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({message, order_id: orderId}) })
export const getRun = (id: string) => request<Run>(`/api/v1/investigations/${id}`)
export const getSteps = (id: string) => request<{steps: Step[]}>(`/api/v1/investigations/${id}/steps`)
export const getEvidence = (id: string) => request<{evidence: Evidence[]}>(`/api/v1/investigations/${id}/evidence`)
export const getApprovals = (id: string) => request<{approvals: Approval[]}>(`/api/v1/investigations/${id}/approvals`)
export const approveRun = (id: string) => request<Run>(`/api/v1/investigations/${id}/approve`, {method:'POST'})
export const executeRun = (id: string, items: {sku_id:string; quantity:number}[], key: string) => request<Run>(`/api/v1/investigations/${id}/execute`, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({items,idempotency_key:key})})
export const injectDemoFault = (orderId: string, faultType: string) => request<{order_id:string; status:string}>('/api/v1/demo/faults', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({order_id: orderId || undefined, fault_type: faultType})})
export function subscribe(id: string, onEvent: (event: TimelineEvent) => void) { const source = new EventSource(`${API}/api/v1/investigations/${id}/events`); const names = ['run.created','run.status_changed','planner.started','planner.completed','investigator.started','investigator.completed','knowledge.started','knowledge.completed','diagnosis.started','diagnosis.completed','diagnosis.validated','diagnosis.validation_failed','investigation.supplement_requested','verify.started','verify.completed','tool.call_started','tool.call_completed','evidence.created','policy.checked','approval.requested','approval.completed','repair.started','repair.failed','verification.started','run.repaired','run.completed','run.failed']; names.forEach(type => source.addEventListener(type, event => { const message = event as MessageEvent; let payload: unknown = message.data; try { payload = JSON.parse(message.data) } catch {} onEvent({id: Number(message.lastEventId) || undefined, type, payload, created_at: new Date().toISOString()}) })); return () => source.close() }
