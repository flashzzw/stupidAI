export type DomainEvent = {
  event_id: string; kind: string; source: string; sources: string[]; occurred_at: string;
  summary: string; operation_id?: string; managed: boolean; active: boolean; notification_sender: string;
};

const names: Record<string, string> = {
  permission_required: '等待审批', input_required: '等待回答', run_failed: '运行失败',
  'session.started': '会话开始', 'session.stopped': '轮次中止', 'session.activity': '新活动',
  'session.turn_completed': '轮次结束', 'interaction.resolved': '交互已处理',
  'task.succeeded': '任务验收通过', 'goal.completed': 'Goal 完成', 'route.unavailable': '路由不可用', 'route.recovered': '路由恢复',
};

export function EventTimeline({ events }: { events: DomainEvent[] }) {
  return <details className="event-timeline"><summary>事件时间线 · {events.length}</summary>
    <ol>{[...events].reverse().map((event) => <li key={event.event_id}>
      <strong>{names[event.kind] ?? event.kind}</strong><time dateTime={event.occurred_at}>{new Date(event.occurred_at).toLocaleString()}</time>
      <p>{event.summary}</p><small>{event.managed ? '托管范围' : '仅观察'} · {event.active ? '当前事件' : '已被后续活动替代'}</small>
      <small>来源：{event.sources.join('、')}</small>
      <small>通知处理方：{event.notification_sender === 'console' ? '控制台' : '原通知工具'}</small>
      {event.operation_id ? <code>Operation {event.operation_id}</code> : <small>尚无已验证的 Operation 关联</small>}
    </li>)}</ol>{events.length === 0 ? <p className="hint">还没有领域事件。</p> : null}
  </details>;
}
