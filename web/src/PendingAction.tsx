import { useState, type FormEvent } from 'react';

export type PendingAction = {
  id: string; kind: string; status: string; title: string; details?: string; response_kind: string;
  options: { id: string; label: string; description?: string; value: unknown }[];
  questions: { id: string; header: string; question: string; is_secret: boolean; multiple?: boolean; allow_custom?: boolean;
    options: { id: string; label: string; description?: string }[] }[];
};

export type CommandFields = { kind: string; session_id?: string; [key: string]: unknown };

export function PendingPanel({ action, sessionId, disabled, send }: {
  action: PendingAction; sessionId: string; disabled: boolean;
  send: (fields: CommandFields) => Promise<void>;
}) {
  const [answers, setAnswers] = useState<Record<string, string[]>>({});
  const [custom, setCustom] = useState<Record<string, string>>({});
  const unavailable = disabled || action.status !== 'pending';
  const canAnswer = ['user_input', 'async_input', 'message_input'].includes(action.response_kind);
  const question = canAnswer || action.kind === 'input_required';
  function submit(event: FormEvent) {
    event.preventDefault();
    void send({ kind: 'respond', session_id: sessionId, pending_id: action.id,
      answers: Object.fromEntries(action.questions.map((q) => [q.id, [...(answers[q.id] ?? []), ...(custom[q.id]?.trim() ? [custom[q.id]] : [])]])) });
  }
  return <section className="pending-panel" aria-label="待处理请求">
    <div className="eyebrow">{question ? '需要你的回答' : '需要你的审批'}</div>
    <p className="pending-title">{action.title}</p>
    {action.details ? <details><summary>工具参数</summary><pre>{action.details}</pre></details> : null}
    {action.status === 'unverified' ? <p>原连接已变化，此请求尚未确认有效。请重新核对或返回原端处理。</p> : null}
    {action.status === 'submitted' ? <p role="status">回复已发送，等待原生确认。请勿重复回答。</p> : null}
    {action.response_kind === 'approval' ? <div className="pending-options">{action.options.map((option) => <button key={option.id} disabled={unavailable}
      onClick={() => void send({ kind: 'respond', session_id: sessionId, pending_id: action.id, option_id: option.id })}>
      {option.label}{option.description ? <small>{option.description}</small> : null}</button>)}</div> : null}
    {action.response_kind === 'approval' && action.kind !== 'mcpServer/elicitation/request' && action.options.some((option) => typeof option.value === 'object') ? <details><summary>原生规则详情</summary><pre>{JSON.stringify(action.options.filter((option) => typeof option.value === 'object').map((option) => option.value), null, 2)}</pre></details> : null}
    {canAnswer ? <form onSubmit={submit}>{action.questions.map((q) => <fieldset key={q.id} disabled={unavailable}>
      <legend>{q.header ? `${q.header} · ` : ''}{q.question}</legend>
      {q.options.map((option) => <label className="question-option" key={option.id}><input type={q.multiple ? 'checkbox' : 'radio'} name={q.id}
        checked={answers[q.id]?.includes(option.label) ?? false} onChange={(event) => {
          const checked = event.target.checked;
          setAnswers((old) => ({ ...old, [q.id]: q.multiple ? checked ? [...(old[q.id] ?? []), option.label] : (old[q.id] ?? []).filter((value) => value !== option.label) : [option.label] }));
          if (!q.multiple) setCustom((old) => ({ ...old, [q.id]: '' }));
        }} />
        <span>{option.label}<small>{option.description}</small></span></label>)}
      {q.allow_custom !== false ? <label>你的回答<input type={q.is_secret ? 'password' : 'text'} autoComplete="off" value={custom[q.id] ?? ''}
        onChange={(e) => {
          setCustom((old) => ({ ...old, [q.id]: e.target.value }));
          if (!q.multiple) setAnswers((old) => ({ ...old, [q.id]: [] }));
        }} /></label> : null}
    </fieldset>)}<button disabled={unavailable || action.questions.some((q) => !(answers[q.id]?.length || custom[q.id]?.trim()))}>提交回答</button></form> : null}
    {action.response_kind === 'unsupported' ? <p>此交互暂不支持网页回复，请返回原端处理。</p> : null}
  </section>;
}
