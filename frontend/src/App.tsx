import { FormEvent, useEffect, useState } from 'react'
import {
  BookOpen, Check, CircleAlert, ClipboardList, RefreshCw, Search,
  SearchCheck, ShieldCheck, Stethoscope, type LucideIcon,
} from 'lucide-react'
import * as api from './api'

const statusText: Record<string, string> = {
  CREATED: '已创建', PLANNING: '正在规划', INVESTIGATING: '正在调查', KNOWLEDGE_LOOKUP: '正在检索知识',
  DIAGNOSING: '正在诊断', CRITIC_REVIEW: '正在审查', EVIDENCE_COLLECTED: '等待处理', NO_ANOMALY: '链路正常',
  POLICY_CHECK: '正在检查策略', AWAITING_APPROVAL: '等待执行', EXECUTING: '正在执行修复', VERIFYING: '正在验证',
  REPAIRED: '修复完成', REJECTED: '已拒绝', EXECUTION_FAILED: '执行失败', VERIFICATION_FAILED: '验证失败',
  INVESTIGATION_FAILED: '调查失败', INCONCLUSIVE: '结论不确定',
}

const agentPath: { key: string; label: string; icon: LucideIcon }[] = [
  { key: 'PLANNER', label: 'Planner', icon: ClipboardList },
  { key: 'INVESTIGATOR', label: 'Investigator', icon: SearchCheck },
  { key: 'KNOWLEDGE', label: 'Knowledge', icon: BookOpen },
  { key: 'DIAGNOSIS', label: 'Diagnosis', icon: Stethoscope },
  { key: 'CRITIC', label: 'Critic', icon: ShieldCheck },
  { key: 'VERIFY', label: 'Verify', icon: ShieldCheck },
]
const statusAgent: Record<string, string> = { PLANNING: 'PLANNER', INVESTIGATING: 'INVESTIGATOR', KNOWLEDGE_LOOKUP: 'KNOWLEDGE', DIAGNOSING: 'DIAGNOSIS', CRITIC_REVIEW: 'CRITIC', VERIFYING: 'VERIFY' }
const repairAction: Record<string, string> = { PAYMENT_EVENT_NOT_CREATED: 'REBUILD_OUTBOX_EVENT', PAYMENT_EVENT_NOT_PUBLISHED: 'RETRY_OUTBOX_PUBLISH', INVENTORY_EVENT_NOT_CONSUMED: 'RETRY_INVENTORY_CONSUMER', INVENTORY_DEDUCTION_FAILED: 'RETRY_INVENTORY_CONSUMER', INVENTORY_DEDUCTION_NOT_PERSISTED: 'RECONCILE_INVENTORY_STATE' }
const faultScenarios = [
  { value: 'OUTBOX_NOT_CREATED', label: 'Outbox 未创建', tool: 'rebuild_outbox_event' },
  { value: 'OUTBOX_PUBLISH_FAILED', label: 'Outbox 发布失败', tool: 'retry_outbox_publish' },
  { value: 'INVENTORY_EVENT_NOT_CONSUMED', label: '事件已发布但库存未消费', tool: 'retry_inventory_consumer' },
  { value: 'INVENTORY_CONSUMER_FAILED', label: '库存消费失败', tool: 'retry_inventory_consumer' },
  { value: 'INVENTORY_DEDUCTION_NOT_PERSISTED', label: '库存扣减成功但状态未持久化', tool: 'reconcile_inventory_state' },
  { value: 'NO_ISSUE', label: '正常订单', tool: 'none' },
  { value: 'EVIDENCE_CONFLICT', label: '多种证据冲突', tool: 'none (manual review)' },
  { value: 'TOOL_TIMEOUT_OR_DIRTY_DATA', label: '工具超时或返回脏数据', tool: 'none (recollect evidence)' },
]

function AgentProgress({ steps, status, events }: { steps: api.Step[]; status?: string; events: api.TimelineEvent[] }) {
  const completed = new Set(steps.filter(step => step.status === 'SUCCEEDED').map(step => step.agent_type))
  const failed = new Set(steps.filter(step => step.status === 'FAILED').map(step => step.agent_type))
  if (events.some(event => event.type === 'verify.completed' && (event.payload as { approved?: boolean })?.approved)) completed.add('VERIFY')
  if (events.some(event => event.type === 'verify.completed' && !(event.payload as { approved?: boolean })?.approved)) failed.add('VERIFY')
  const current = status ? statusAgent[status] : undefined
  return <section className="section-block"><div className="section-head"><div><h2>Agent Path</h2><p>Investigation workflow</p></div><span className="status-badge">{status ? statusText[status] || status : '未开始'}</span></div><div className="agent-path">{agentPath.map(({ key, label, icon: Icon }, index) => { const done = completed.has(key); const isFailed = failed.has(key); const active = current === key; return <div className={`agent-node ${done ? 'done' : ''} ${active ? 'active' : ''} ${isFailed ? 'failed' : ''}`} key={key}><div className="agent-icon">{done ? <Check size={18} /> : isFailed ? <CircleAlert size={18} /> : <Icon size={18} />}</div><div><strong>{label}</strong><small>{done ? 'Completed' : isFailed ? 'Failed' : active ? 'Running' : 'Pending'}</small></div>{index < agentPath.length - 1 && <span className="agent-line" />}</div> })}</div></section>
}

export function App() {
  const [message, setMessage] = useState('调查订单 O1001 的支付和库存链路')
  const [orderId, setOrderId] = useState('O1001')
  const [run, setRun] = useState<api.Run>()
  const [events, setEvents] = useState<api.TimelineEvent[]>([])
  const [steps, setSteps] = useState<api.Step[]>([])
  const [evidence, setEvidence] = useState<api.Evidence[]>([])
  const [approvals, setApprovals] = useState<api.Approval[]>([])
  const [error, setError] = useState('')
  const [sku, setSku] = useState('SKU1')
  const [qty, setQty] = useState(1)
  const [key, setKey] = useState('repair-' + Date.now())
  const [faultType, setFaultType] = useState('OUTBOX_NOT_CREATED')
  const [faultMessage, setFaultMessage] = useState('')

  const refresh = async () => { if (!run) return; try { const [next, stepResult, evidenceResult, approvalResult] = await Promise.all([api.getRun(run.run_id), api.getSteps(run.run_id), api.getEvidence(run.run_id), api.getApprovals(run.run_id)]); setRun(next); setSteps(stepResult.steps); setEvidence(evidenceResult.evidence); setApprovals(approvalResult.approvals) } catch (e) { setError((e as Error).message) } }
  useEffect(() => { if (!run) return; const stop = api.subscribe(run.run_id, event => { setEvents(current => [...current, event]); refresh() }); return stop }, [run?.run_id])
  const start = async (event: FormEvent) => { event.preventDefault(); setError(''); try { const created = await api.createInvestigation(message, orderId); setRun(created); setEvents([]); setSteps([]); setEvidence([]); setApprovals([]) } catch (e) { setError((e as Error).message) } }
  const approve = async () => { if (!run) return; try { setRun(await api.approveRun(run.run_id)); await refresh() } catch (e) { setError((e as Error).message) } }
  const execute = async () => { if (!run) return; try { const action = diagnosis ? repairAction[diagnosis.root_cause] : ''; const snapshot = evidence.find(item => item.tool_name === 'get_order_snapshot')?.data as { items?: { sku_id: string; quantity: number }[] } | undefined; const items = snapshot?.items?.map(item => ({ sku_id: item.sku_id, quantity: item.quantity })) || [{ sku_id: sku, quantity: qty }]; const idempotencyKey = key || `repair-${run.run_id}`; setRun(await api.executeRun(run.run_id, action === 'RETRY_INVENTORY_DEDUCTION' ? items : [], action === 'RETRY_INVENTORY_DEDUCTION' ? idempotencyKey : '')); await refresh() } catch (e) { setError((e as Error).message) } }
  const injectFault = async () => { setError(''); setFaultMessage(''); try { const result = await api.injectDemoFault('', faultType); setOrderId(result.order_id); setMessage(`调查订单 ${result.order_id} 的支付和库存链路`); setFaultMessage(`已创建场景：${result.order_id}；处置工具：${selectedFault.tool}`) } catch (e) { setError((e as Error).message) } }
  const diagnosis = run?.final_summary?.diagnosis
  const selectedFault = faultScenarios.find(item => item.value === faultType) || faultScenarios[0]
  const healthy = diagnosis?.root_cause === 'NO_ISSUE' || run?.status === 'NO_ANOMALY'
  const finishedWithError = ['REJECTED', 'EXECUTION_FAILED', 'VERIFICATION_FAILED', 'INVESTIGATION_FAILED', 'INCONCLUSIVE'].includes(run?.status || '')

  return <><header><div className="brand"><SearchCheck size={22} /><strong>OrderGuard</strong></div><span className="connection"><i />Runtime connected</span></header><main>
    <h1>订单调查</h1>
    <section className="section-block query-block"><div className="query-layout"><form onSubmit={start}><div className="section-head"><div><h2>发起调查</h2><p>输入订单和需要排查的问题</p></div></div><div className="query-fields"><label><span>调查问题</span><input value={message} onChange={event => setMessage(event.target.value)} /></label><label className="order-field"><span>订单号</span><input value={orderId} onChange={event => setOrderId(event.target.value)} /></label><button type="submit"><Search size={17} />开始调查</button></div></form><aside className="approval-panel"><div className="section-head"><div><h2>审批记录</h2><p>诊断确认与修复授权</p></div><span>{approvals.length} 条</span></div>{run?.status === 'EVIDENCE_COLLECTED' && diagnosis && (diagnosis.root_cause === 'NO_ISSUE' || repairAction[diagnosis.root_cause]) && <button className="approval-action" onClick={approve}>{diagnosis.root_cause === 'NO_ISSUE' ? '确认链路正常' : '批准修复方案'}</button>}{run?.status === 'EVIDENCE_COLLECTED' && diagnosis && diagnosis.root_cause !== 'NO_ISSUE' && !repairAction[diagnosis.root_cause] && <p className="empty">证据不足或互相冲突，不允许自动修复。</p>}<div className="approval-list">{approvals.length ? approvals.map(item => <div className="approval-row" key={item.approval_id}><Check size={16} /><div><strong>{item.type === 'NO_ISSUE_CONFIRMATION' ? '正常结论确认' : '修复方案批准'}</strong><span>{item.conclusion}</span><time>{new Date(item.created_at).toLocaleString()}</time></div></div>) : <p className="empty">当前没有审批记录。</p>}</div></aside></div></section>
    <section className="section-block demo-fault-block"><div className="section-head"><div><h2>演示事故集</h2><p>每次创建一个新订单，并在业务处理前启用选定场景</p></div></div><div className="fault-tools"><label><span>事故场景</span><select value={faultType} onChange={event => setFaultType(event.target.value)}>{faultScenarios.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><div className="fault-policy"><span>处置工具</span><code>{selectedFault.tool}</code></div><button type="button" className="secondary-button" onClick={injectFault}>{faultType === 'NO_ISSUE' ? '创建订单' : '注入事故'}</button>{faultMessage && <span className="success-note">{faultMessage}</span>}</div></section>
    {error && <div className="error"><CircleAlert size={18} />{error}</div>}
    <div className="run-content"><div className="run-meta"><div><span>Run ID</span><code>{run?.run_id || '尚未创建'}</code></div><div className="run-meta-actions"><span className="status-badge">{run ? statusText[run.status] || run.status : '未开始'}</span>{run && <button className="icon-button" onClick={refresh} title="刷新调查数据" aria-label="刷新调查数据"><RefreshCw size={17} /></button>}</div></div>
      <AgentProgress steps={steps} status={run?.status} events={events} />
      <section className={`section-block result-block ${healthy ? 'result-ok' : diagnosis || finishedWithError ? 'result-alert' : ''}`}><div className="result-icon">{healthy ? <Check size={22} /> : diagnosis || finishedWithError ? <CircleAlert size={22} /> : <SearchCheck size={22} />}</div><div><span className="section-label">调查结论</span><h2>{healthy ? '链路没有问题' : diagnosis ? '发现需要关注的异常' : finishedWithError ? (statusText[run?.status || ''] || '调查未完成') : run ? '正在检查订单链路' : '等待发起调查'}</h2><p>{healthy ? '支付、事件发布和库存处理均符合预期，无需执行修复。' : diagnosis ? `候选根因：${diagnosis.root_cause}` : run ? '系统正在收集证据并生成诊断。' : '提交订单后，调查结果将在这里显示。'}</p></div>{diagnosis && <span className="confidence">{Math.round(diagnosis.confidence * 100)}% confidence</span>}</section>
      <section className="section-block"><div className="section-head"><div><h2>诊断详情</h2><p>根因判断与处置建议</p></div></div>{diagnosis ? <div className="diagnosis"><h3 className={healthy ? 'healthy' : ''}>{diagnosis.root_cause}</h3>{diagnosis.evidence_ids?.length > 0 && <p>关联证据：{diagnosis.evidence_ids.join(', ')}</p>}{diagnosis.recommended_action && <span className="action-tag">{diagnosis.recommended_action}</span>}{run?.status === 'AWAITING_APPROVAL' && <div className="repair-form"><h3>{repairAction[diagnosis.root_cause] || '执行修复'}</h3>{repairAction[diagnosis.root_cause] === 'RETRY_INVENTORY_DEDUCTION' && <div><input value={sku} onChange={event => setSku(event.target.value)} placeholder="SKU ID（自动读取订单商品）" /><input type="number" min="1" value={qty} onChange={event => setQty(Number(event.target.value))} /><input value={key} onChange={event => setKey(event.target.value)} placeholder="幂等键（系统生成）" /></div>}<button onClick={execute}>执行并验证</button></div>}</div> : <p className="empty">调查完成后，根因判断将在这里显示。</p>}</section>
      <section className="section-block"><div className="section-head"><div><h2>证据链</h2><p>调查期间保存的不可变证据快照</p></div><span>{evidence.length} 条</span></div><div className="evidence-list">{evidence.length ? evidence.map(item => <div className="evidence-row" key={item.evidence_id}><div><strong>{item.tool_name}</strong><span>{item.source}</span></div><code>{item.evidence_id}</code></div>) : <p className="empty">等待实时证据。</p>}</div></section>
      <section className="section-block"><div className="section-head"><div><h2>事件记录</h2><p>SSE 实时运行日志</p></div><span>{events.length} 条</span></div><div className="event-window">{events.length ? events.slice().reverse().map((item, index) => <div className="event-row" key={`${item.id || ''}-${index}`}><time>{new Date(item.created_at || '').toLocaleTimeString()}</time><div><strong>{item.type}</strong><code>{JSON.stringify(item.payload)}</code></div></div>) : <p className="empty">等待运行事件。</p>}</div></section>
    </div>
  </main></>
}
