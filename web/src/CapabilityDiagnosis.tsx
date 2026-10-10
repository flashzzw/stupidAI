export type Diagnosis = {
  format: string; status: string; native_status: string; portable: boolean; boundary: string;
  components: { kind: string; name: string; path: string; backend?: string; independent_switch: boolean }[];
  issues: { severity: string; code: string; path: string; detail: string }[];
  required_environment: string[];
  plugin_sources?: { backend: string; layout: string; identity: string; settings: string }[];
  resources?: { path: string; kind: string; media_type: string; inspection: string }[];
};

export function CapabilityDiagnosis({ value }: { value?: Diagnosis }) {
  if (!value) return null;
  return <div className="capability-diagnosis">
    <p>{value.status === 'invalid' ? '静态检查发现问题' : '已检查声明结构'} · {value.portable ? '可打包' : '暂不可打包'} · 原生加载尚未验证</p>
    {value.required_environment.length ? <p>运行时需要环境变量：<span className="mono">{value.required_environment.join('、')}</span></p> : null}
    {value.plugin_sources?.map(source => <p key={source.backend}>
      {source.backend} · {source.layout === 'portable' ? '通用插件结构' : 'CLI 插件结构'}
      <small className="mono">身份入口：{source.identity}</small><small className="mono">配置来源：{source.settings}</small>
    </p>)}
    {value.issues.filter(issue => issue.code !== 'binary_resource').map((issue, index) => <p className={issue.severity === 'error' ? 'error' : 'warning'} key={index}>
      <span className="mono">{issue.path}</span>：{issue.detail}
    </p>)}
    {value.components.length ? <details><summary>入口与组件 · {value.components.length}</summary>
      {value.components.map((component, index) => <p key={index}>
        {component.backend} · {component.kind} · {component.name}<small className="mono">{component.path}</small>
        {value.format === 'cli_plugin' ? <small>随插件整体引用；此处不提供组件独立开关。</small> : null}
      </p>)}
    </details> : null}
    {value.resources?.length ? <details><summary>非文本资源 · {value.resources.length}</summary>
      <p className="hint">按文件内容标识类型，保持字节和权限。类型识别不代表内容或运行效果已验证。</p>
      {value.resources.map(resource => <p key={resource.path}><span className="mono">{resource.path}</span>
        <small>{resource.media_type} · {resource.inspection === 'members_scanned' ? '已检查压缩成员' : resource.inspection === 'incomplete' ? '压缩检查未完成' : '已知内容模式检查'}</small>
      </p>)}
    </details> : null}
  </div>;
}
