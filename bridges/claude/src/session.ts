import { sanitizeHookEvent, unfinishedHooks } from './hook_execution.js';
import { localSettingsHooks, settingsGuardEvidence, type SettingsChange } from './settings_guard.js';
import { requiredToolApprovalHooks } from './tool_approvals.js';
import { spawnSettingsConsumer } from './settings_file_consumer.js';
import { installedSettings, installedSettingsRecovery, verifyInstalledSettings, verifyInstalledSettingsAdoption } from './installed_settings.js';
import { settingsSources, verifySettingsSources, adoptedSettingsHookSources } from './settings_sources.js';
import { selectedPlugins, executionPlugins, pluginInventory, pluginSelections, pluginChangeMatches, pluginRecoveryMatches, inheritedPluginComponents, verifyPlugins, verifyPluginSources, type PluginPackage } from './plugins.js';
import { mcpDefinitions, mcpInventory, mcpMatches, mcpSelections, mcpToolPrefix, verifyMCP } from './mcp.js';
import { prepareSkillResources } from './skill_resources.js';
import { NativeHookChannel } from './hook_catalog.js';
import { applySkillSelections, inheritedSkills, skillChangeMatches, skillInventory, skillSelections, verifySkillSources, verifySkills } from './skills.js';
import { fingerprint, readMetadata, writeMetadata, configurationFailure, originalQueryRecovery, originalQueryRecoveryMatches, type ConfigurationChange, type ConfigurationFailure, type ConfigurationRecovery } from './configuration.js';
import { randomUUID } from 'node:crypto';
import { appendFileSync, closeSync, existsSync, fsyncSync, openSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { ChildProcess } from 'node:child_process';
import { query, getSessionMessages, type SDKMessage, type SDKUserMessage, type Query, type PermissionResult, type Settings } from '@anthropic-ai/claude-agent-sdk';
import { MCPAuthenticationRequired, NotSent, type SessionConfig, record, text, uuid } from './protocol.js';

type Entry = ({ kind: 'input'; operation: string; text: string } |
  { kind: 'configuration_failure'; failure: ConfigurationFailure } |
  { kind: 'settings_change'; change: SettingsChange } |
  { kind: 'event'; operation: string; event: SDKMessage } |
  { kind: 'initialized' | 'closed'; connection: string; pid?: number }) & { at?: string };
type Request = { id: string; connection: string; operation: string; tool: string; input: Record<string, unknown>;
  title: string; status: 'pending' | 'submitted' | 'expired'; resolve: (value: PermissionResult) => void };

// Only SDK transport data is journaled here. Admission, scheduling, routing and
// operation outcomes remain in Go. Fsync precedes every input handoff/receipt.
export class Session {
  connection = randomUUID();
  readonly entries: Entry[] = [];
  readonly requests = new Map<string, Request>();
  config: SessionConfig;
  private readonly metadata: string;
  private configuration?: ConfigurationChange;
  private changing = false;
  private readonly journal: string;
  private run?: Query;
  private child?: ChildProcess;
  private hookChannel?: NativeHookChannel;
  private pluginPackages?: PluginPackage[];
  private queue: SDKUserMessage[] = [];
  private wake?: () => void;
  private stopping = false;
  private active = '';
  private reading?: Promise<void>;
  private stream = '';
  private fault = '';
  private settingsRecordFailed = false;
  private tasks = new Map<string, string>();

  constructor(private readonly root: string, config: SessionConfig, creating: boolean) {
    this.config = config;
    const metadata = join(root, `${config.id}.json`);
    this.metadata = metadata;
    this.journal = join(root, `${config.id}.jsonl`);
    if (existsSync(metadata)) {
      const stored = readMetadata(metadata);
      const original = stored.config;
      this.configuration = stored.change;
      if (JSON.stringify(original) !== JSON.stringify(config)) throw new NotSent('session identity/configuration mismatch');
    } else {
      if (!creating) throw new NotSent('original session does not exist');
      const fd = openSync(metadata, 'wx', 0o600);
      try { writeFileSync(fd, JSON.stringify(config)); fsyncSync(fd); } finally { closeSync(fd); }
      const dir = openSync(root, 'r');
      try { fsyncSync(dir); } finally { closeSync(dir); }
    }
    if (existsSync(this.journal)) {
      const lines = readFileSync(this.journal, 'utf8');
      if (lines && !lines.endsWith('\n')) this.fault = 'incomplete SDK journal';
      else for (const line of lines.trim().split('\n').filter(Boolean)) { const entry = JSON.parse(line) as Entry; this.entries.push(entry.kind === 'event' ? { ...entry, event: sanitizeHookEvent(entry.event) } : entry); }
    }
  }

  private append(entry: Entry) {
    entry = { ...entry, at: new Date().toISOString() };
    const fd = openSync(this.journal, 'a', 0o600);
    try { appendFileSync(fd, JSON.stringify(entry) + '\n'); fsyncSync(fd); } finally { closeSync(fd); }
    this.entries.push(entry);
  }

  static restore(root: string, id: string) {
    return new Session(root, readMetadata(join(root, `${id}.json`)).config, false);
  }

  async open(apiKey: string, applying = false) {
    verifySettingsSources(this.config.settings_sources);
    verifyInstalledSettings(this.config);
    if (!applying && (this.changing || this.configuration?.status === 'applying')) throw new NotSent('original configuration change remains unverified');
    if (this.fault) throw new NotSent(this.fault);
    if (this.run) return;
    const history = await getSessionMessages(this.config.id, { dir: this.config.cwd, includeSystemMessages: true });
    const unresolved = this.entries.filter(e => e.kind === 'input').some(input =>
      !this.entries.some(e => e.kind === 'event' && e.operation === input.operation && e.event.type === 'result'));
    if (unfinishedHooks(this.entries).length) throw new NotSent('原生 hook 生命周期尚未核对完整，不能重开查询；请先核对原执行');
    if (unresolved) throw new NotSent('original SDK input has no verified terminal result; manual reconciliation required');
    const owner = this;
    async function* input(): AsyncGenerator<SDKUserMessage> {
      while (!owner.stopping) {
        if (!owner.queue.length) await new Promise<void>(resolve => { owner.wake = resolve; });
        while (owner.queue.length && !owner.stopping) yield owner.queue.shift()!;
      }
    }
    const config = this.config;
    const plugins = [...prepareSkillResources(this.root, config), ...selectedPlugins(config)];
    this.pluginPackages = undefined;
    verifySkillSources(config);
    const selections = config.mcp_selections ?? [];
    const mcpServers = mcpDefinitions(selections);
    const settingsFile = config.settings_artifact?.file.path ?? config.settings_file;
    const overlay = settingsFile ? record(JSON.parse(readFileSync(settingsFile, 'utf8'))) : {};
    if ('apiKeyHelper' in overlay) throw new NotSent('settings apiKeyHelper conflicts with the registered credential');
    const settingsEnv = overlay.env === undefined ? {} : record(overlay.env);
    if (Object.entries(settingsEnv).some(([key, value]) => typeof value !== 'string' || /^(ANTHROPIC_|CLAUDE_CODE_USE_|XDG_)/.test(key) || ['HOME', 'CLAUDE_CONFIG_DIR', 'CLAUDE_CODE_PROJECT_DIR_NAME'].includes(key))) {
      throw new NotSent('settings env conflicts with the registered route, credential or state directory');
    }
    const permissions = overlay.permissions === undefined ? {} : record(overlay.permissions);
    if (permissions.ask !== undefined && (!Array.isArray(permissions.ask) || permissions.ask.some(x => typeof x !== 'string'))) throw new NotSent('invalid settings permissions.ask');
    const readOnlySettings: Settings = { permissions: { ...permissions, ask: [...new Set([...(Array.isArray(permissions.ask) ? permissions.ask : []), 'Bash', 'Write', 'Edit', 'NotebookEdit'])] } };
    const approvalHooks = requiredToolApprovalHooks(config);
    this.run = query({ prompt: input(), options: {
      cwd: config.cwd, pathToClaudeCodeExecutable: config.executable,
      systemPrompt: { type: 'preset', preset: 'claude_code' },
      settingSources: config.probe ? [] : ['user', 'project', 'local'],
      ...(plugins.length ? { plugins, pluginDelivery: 'initialize' } : {}),
      ...(selections.length ? {mcpServers, strictMcpConfig: false} : {}),
      env: { ...process.env, ANTHROPIC_BASE_URL: config.endpoint, ANTHROPIC_API_KEY: apiKey,
        CLAUDE_CODE_RESUME_INTERRUPTED_TURN: '0' },
      model: config.model, permissionMode: 'default', persistSession: true,
      includePartialMessages: true, includeHookEvents: true, extraArgs: { 'replay-user-messages': null },
      ...(history.length ? { resume: config.id } : { sessionId: config.id }),
      ...(config.probe ? { tools: ['Bash'], settings: { disableAllHooks: true } } : {}),
      ...(config.permission === 'read_only' && !settingsFile ? { settings: { ...(config.probe ? { disableAllHooks: true } : {}),
        permissions: { ask: ['Bash', 'Write', 'Edit', 'NotebookEdit'] } } } : {}),
      ...(settingsFile ? { settings: settingsFile } : {}),
      ...(config.settings_guard_version || approvalHooks.PreToolUse ? { hooks: { ...(config.settings_guard_version ? localSettingsHooks(config.id,
        change => this.append({kind:'settings_change',change}),
        () => { this.settingsRecordFailed = true; this.fault = '设置变化记录写入失败，不能接受输入'; }) : {}), ...approvalHooks } } : {}),
      canUseTool: async (tool, args, options) => {
        const id = options.requestId;
        if (options.signal.aborted) return { behavior: 'deny', message: 'Request expired' };
        if (config.probe) {
          const command = `netcap_probe_utc=$(/usr/bin/date -u +%FT%TZ); netcap_probe_exit=$?; printf '__NETCAP_UTC__%s %s\\n' "$netcap_probe_exit" "$netcap_probe_utc"; exit "$netcap_probe_exit"`;
          return tool === 'Bash' && args.command === command && !args.run_in_background
            ? { behavior: 'allow', updatedInput: args }
            : { behavior: 'deny', message: 'This isolated probe permits only the fixed UTC date command.' };
        }
        return new Promise<PermissionResult>(resolve => {
          const request: Request = { id, connection: this.connection, operation: this.active, tool, input: args,
            title: options.title ?? options.decisionReason ?? tool, status: 'pending', resolve };
          this.requests.set(id, request);
          options.signal.addEventListener('abort', () => { request.status = 'expired'; resolve({ behavior: 'deny', message: 'Request expired' }); }, { once: true });
        });
      },
      spawnClaudeCodeProcess: options => {
        const child = spawnSettingsConsumer(options, config);
        // Never forward raw CLI stderr: settings and providers may include secrets.
        child.stderr.resume();
        this.child = child;
        this.hookChannel = new NativeHookChannel(child);
        return this.hookChannel.process;
      },
    } });
    this.reading = this.readEvents(this.run);
    try {
      const initialized = await this.run.initializationResult();
      if (approvalHooks.PreToolUse && initialized?.hooks_applied !== true) throw new NotSent('原生 CLI 未确认逐次工具审批约束');
      if (plugins.length && initialized.plugins_applied !== true) throw new NotSent('原生 CLI 未确认全部 Skill 加载包');
      // Load the exact native file (including its path semantics), then apply
      // the existing read-only ask rules before allowing any user input.
      if (settingsFile && config.permission === 'read_only') await this.run.applyFlagSettings(readOnlySettings);
      if (this.requiresPluginInventory()) {
        const refreshed = await this.run.reloadPlugins();
        if (refreshed.error_count || !Array.isArray(refreshed.plugins)) throw new NotSent('原生插件加载报告错误，不能核对本次选择');
        this.pluginPackages = refreshed.plugins;

      }
      await applySkillSelections(this.run, config, overlay);
      await this.verifyPluginBinding();
      if (!applying && this.configuration?.status === 'verified') await verifySkills(this.run, config, this.configuration.skill_previous ?? []);
      const pluginAsk = executionPlugins(config).filter(p => p.mode === 'enabled').flatMap(p => p.components.filter(c => c.kind === 'mcp').map(c => `${mcpToolPrefix(c.native_name)}*`));
      if (selections.length || pluginAsk.length) {
        await this.run.applyFlagSettings({ permissions: { ...permissions, ask: [...new Set([...(config.permission === 'read_only' ? readOnlySettings.permissions?.ask ?? [] : Array.isArray(permissions.ask) ? permissions.ask as string[] : []), ...selections.filter(s => s.mode === 'enabled').map(s => `mcp__${s.server}__*`), ...pluginAsk])] } });
        await verifyMCP(this.run, selections);
      }
      verifySettingsSources(this.config.settings_sources);
      verifyInstalledSettings(this.config);
      this.append({ kind: 'initialized', connection: this.connection, pid: this.child?.pid });
    } catch (error) {
      const retryableAuthentication = error instanceof MCPAuthenticationRequired && !applying && history.length > 0 &&
        this.entries.some(entry => entry.kind === 'initialized') && !this.fault && !this.hasUnfinishedWork();
      this.fault = 'SDK initialization failed';
      if (retryableAuthentication) {
        // A rejected startup has not handed off the new input. Retire only its
        // owned query, and keep the fault until both reader and child are gone.
        // Background/permission events arriving during close must still block.
        await this.close();
        if (!this.hasUnfinishedWork()) {
          this.stopping = false;
          this.fault = '';
          this.connection = randomUUID();
          this.requests.clear();
        }
      } else this.run?.close();
      throw error;
    }
  }

  private async readEvents(run: Query) {
    try {
      for await (const event of run) {
        if (event.session_id && event.session_id !== this.config.id) throw new Error('SDK session identity changed');
        if (event.type === 'stream_event') {
          if (event.event.type === 'content_block_delta' && event.event.delta.type === 'text_delta') this.stream += event.event.delta.text;
          continue;
        }
        const hookOwner = event.type === 'system' && 'hook_id' in event
          ? this.entries.findLast(entry => entry.kind === 'event' && 'hook_id' in entry.event && entry.event.hook_id === event.hook_id)
          : undefined;
        const operation = hookOwner && 'operation' in hookOwner ? hookOwner.operation
          : 'user_message_uuid' in event && typeof event.user_message_uuid === 'string' ? event.user_message_uuid : this.active;
        this.append({ kind: 'event', operation, event: sanitizeHookEvent(event) });
        if (event.type === 'system' && event.subtype === 'init' && this.pluginPackages) {
          // init reports the current package paths/versions but no source keys.
          // Retain the startup source proof only while the full list agrees.
          if (event.plugins.length !== this.pluginPackages.length || event.plugins.some(p =>
            this.pluginPackages!.filter(old => old.name === p.name && old.path === p.path && (old.version ?? '') === (p.version ?? '')).length !== 1)) {
            this.pluginPackages = undefined;
          }
        }
        if (event.type === 'system' && event.subtype === 'background_tasks_changed') {
          this.tasks = new Map(event.tasks.map(task => [task.task_id, this.tasks.get(task.task_id) ?? operation]));
        }
        if (event.type === 'result') {
          this.active = '';
          this.stream = '';
          for (const request of this.requests.values()) request.status = 'expired';
        }
      }
    } catch {
      if (!this.stopping) this.fault = 'SDK event stream closed before a verified terminal state';
    } finally {
      this.run = undefined;
    }
  }

  async send(params: Record<string, unknown>) {
    const operation = uuid(params.operation);
    const body = text(params.text, 'message');
    const previous = this.entries.find(e => e.kind === 'input' && e.operation === operation);
    if (previous) {
      if (previous.kind !== 'input' || previous.text !== body) throw new NotSent('operation reused with different input');
      return { native_id: this.config.id, native_turn: operation };
    }
    if (this.active) throw new NotSent('SDK query already has an input in flight');
    await this.open(text(params.api_key, 'credential'));
    if (this.run) await verifyMCP(this.run, this.config.mcp_selections ?? []);
    if (this.run) await verifySkills(this.run, this.config, (this.configuration?.status==='recovered'?this.configuration.skill_target:this.configuration?.skill_previous) ?? []);
    await this.verifyPluginBinding();
    if (unfinishedHooks(this.entries).length) throw new NotSent('原生 hook 尚未报告终态，不能接受新输入');
    this.append({ kind: 'input', operation, text: body });
    this.active = operation;
    this.queue.push({ type: 'user', uuid: operation as `${string}-${string}-${string}-${string}-${string}`, session_id: this.config.id,
      message: { role: 'user', content: body }, parent_tool_use_id: null });
    this.wake?.();
    return { native_id: this.config.id, native_turn: operation };
  }

  async respond(params: Record<string, unknown>) {
    verifySettingsSources(this.config.settings_sources);
    verifyInstalledSettings(this.config);
    if (this.run) await verifySkills(this.run, this.config, (this.configuration?.status==='recovered'?this.configuration.skill_target:this.configuration?.skill_previous) ?? []);
    await this.verifyPluginBinding();
    const request = this.requests.get(text(params.request_id, 'request ID'));
    if (!request || request.connection !== params.connection || request.status !== 'pending') throw new NotSent('request expired or belongs to a previous connection');
    let result: PermissionResult;
    if (request.tool === 'AskUserQuestion') {
      const answers = record(params.answers);
      result = { behavior: 'allow', updatedInput: { ...request.input, answers } };
    } else if (params.decision === 'allow') {
      result = { behavior: 'allow', updatedInput: request.input, decisionClassification: 'user_temporary' };
    } else if (params.decision === 'deny') {
      result = { behavior: 'deny', message: '用户拒绝了此次工具操作', decisionClassification: 'user_reject' };
    } else throw new NotSent('invalid permission decision');
    request.status = 'submitted';
    request.resolve(result);
    return { native_id: this.config.id, native_turn: request.operation };
  }

  async interrupt(params: Record<string, unknown>) {
    const operation = uuid(params.native_turn);
    const tasks = [...this.tasks].filter(([, owner]) => owner === operation).map(([id]) => id);
    if (!this.run || (this.active !== operation && !tasks.length)) throw new NotSent('no matching owned SDK turn or task');
    if (this.active && this.active !== operation) throw new NotSent('a different SDK turn is active');
    if (this.active === operation) await this.run.interrupt();
    for (const id of tasks) await this.run.stopTask(id);
    return { native_id: this.config.id, native_turn: operation };
  }

  async snapshot() {
    const history = await getSessionMessages(this.config.id, { dir: this.config.cwd, includeSystemMessages: true });
    let settingsPolicy;
    if (this.config.settings_guard_version && this.run && this.hookChannel) {
      try { settingsPolicy = (await this.hookChannel.read()).policy; } catch { /* Unavailable evidence cannot claim an active guard. */ }
    }
    const settingsGuard = settingsGuardEvidence(this.config.settings_guard_version, !!this.run, settingsPolicy,
      this.entries.flatMap(e => e.kind === 'settings_change' ? [e.change] : []), this.settingsRecordFailed);
    const failure=this.entries.flatMap(e=>e.kind==='configuration_failure'?[configurationFailure(e.failure)]:[]).filter(f=>f&&f.operation===this.configuration?.operation&&f.baseline===this.configuration?.baseline).at(-1);
    const recoveryFailure=this.entries.flatMap(e=>e.kind==='configuration_failure'?[configurationFailure(e.failure)]:[]).filter(f=>f&&f.operation===this.configuration?.recovery?.operation&&f.baseline===this.configuration?.baseline).at(-1);
    return { config: this.config, connection: this.connection, live: !!this.run, active: this.active, fault: this.fault,
      entries: this.entries, stream: this.stream,
      ...(settingsGuard ? {settings_guard:settingsGuard} : {}),
      ...(recoveryFailure && this.configuration?.status!=='recovered' ? {configuration_recovery_failure:recoveryFailure} : {}),
      ...(failure && this.configuration?.status!=='verified' && this.configuration?.status!=='recovered' ? {configuration_failure:failure} : {}),
      ...(this.config.mcp_selections?.length || this.configuration ? {mcp: await mcpInventory(this.run)} : {}),
      ...(this.config.skill_selections?.length || (this.configuration?.status==='recovered'?this.configuration.skill_target:this.configuration?.skill_previous)?.length ? {skills: await skillInventory(this.run, this.config)} : {}),
      ...(this.requiresPluginInventory() ? {plugins: await pluginInventory(this.run,this.hookChannel,this.pluginPackages,this.config)} : {}),
      history: history.map(sanitizeHookEvent), history_digest: fingerprint(history), configuration_baseline: fingerprint({ config: this.config, history }), configuration: this.configuration,
      pending: [...this.requests.values()].map(({ resolve: _, ...request }) => request) };
  }

  async configurationRecovery(params: Record<string,unknown>) {
    const operation=text(params.operation,'operation'),baseline=text(params.baseline,'baseline');
    const before=readMetadata(this.metadata),connection=this.connection;
    const checks: {kind:string;status:'passed'|'blocked'|'unavailable'}[]=[];
    const add=(kind:string,ok:boolean)=>checks.push({kind,status:ok?'passed':'blocked'});
    const history=await getSessionMessages(this.config.id,{dir:this.config.cwd,includeSystemMessages:true});
    const snapshot=await this.snapshot(),change=before.change,failure=snapshot.configuration_failure;
    add('transaction',!!change&&change.status==='applying'&&change.operation===operation&&change.baseline===baseline&&fingerprint(change)===fingerprint(snapshot.configuration??null)&&failure?.operation===operation&&failure.baseline===baseline);
    add('owned_query_stopped',!this.run&&(!this.child||this.child.exitCode!==null||this.child.signalCode!==null)&&failure?.cleanup==='confirmed');
    add('work_clear',!this.hasUnfinishedWork()&&(!this.fault||this.fault==='SDK initialization failed'&&['open_candidate','open_original'].includes(failure?.stage ?? '')));
    add('original_history',fingerprint({config:before.config,history})===baseline);
    let installedRecovery;
    const originalRetry = originalQueryRecovery(change, failure);
    if(!before.config.settings_sources?.length)checks.push({kind:'original_settings',status:'unavailable'});
    else {
      try {
        verifySettingsSources(before.config.settings_sources);
        if (params.installed_recovery !== undefined) {
          installedRecovery = installedSettingsRecovery(before.config, params.installed_recovery, change?.settings_target).installed_settings;
        } else verifyInstalledSettings(before.config);
        verifyPluginSources(before.config);
        if (change?.installed_target || change?.installed_previous) {
          if (!change.original_plugins?.available && !originalRetry) throw new Error('original plugin inventory unavailable');
          // The first installed override still has a complete startup/source
          // binding in its failed candidate, although the original inherited it.
          if (!before.config.installed_settings && change.installed_target) {
            const candidate={...before.config,installed_settings:change.installed_target,settings_artifact:change.installed_target.artifact};
            verifyInstalledSettings(candidate);
            verifyPluginSources(candidate);
          }
        }
        add('original_settings',true);
      }
      catch {add('original_settings',false);}
    }
    add('read_stable',!this.changing&&connection===this.connection&&fingerprint(before)===fingerprint(readMetadata(this.metadata))&&fingerprint(history)===snapshot.history_digest);
    return {snapshot,version:1,native_id:this.config.id,operation,baseline,at:new Date().toISOString(),checks,source:'claude.configuration_recovery',read_only:true,...(installedRecovery ? {installed_recovery:installedRecovery} : {}),...(originalRetry ? {original_query_retry:originalRetry} : {})};
  }

  async recoverExecution(params: Record<string,unknown>) {
    const operation=uuid(params.operation),originalOperation=uuid(params.original_operation),baseline=text(params.baseline,'baseline');
    if(params.original_query_retry!==undefined&&typeof params.original_query_retry!=='boolean')throw new NotSent('恢复方式无效');
    const originalRetry=params.original_query_retry===true;
    if(operation===originalOperation)throw new NotSent('恢复需要独立于原失败应用的操作 ID');
    const prior=readMetadata(this.metadata),change=prior.change;
    if(!change||change.operation!==originalOperation||change.baseline!==baseline||(!change.settings_target||!change.settings_previous)&&!change.installed_target)throw new NotSent('原配置应用事务不匹配');
    if(change.recovery){
      if(change.status==='recovered'&&change.recovery.status==='verified'&&change.recovery.operation===operation&&Boolean(change.recovery.original_query_retry)===originalRetry&&(!originalRetry||originalQueryRecoveryMatches(change,change.recovery.original_query_retry))&&fingerprint(change.recovery.installed_settings??null)===fingerprint(params.installed_recovery??null))return {native_id:this.config.id,...(change.recovery.installed_settings?{installed_settings:change.recovery.installed_settings}:{}),...(change.recovery.original_query_retry?{original_query_retry:change.recovery.original_query_retry}:{})};
      throw new Error('original recovery outcome must be reconciled without replay');
    }
    const report=await this.configurationRecovery({operation:originalOperation,baseline,...(params.installed_recovery===undefined?{}:{installed_recovery:params.installed_recovery})});
    if(report.checks.some(c=>c.status!=='passed'))throw new NotSent('原配置恢复条件未满足');
    if(Boolean(report.original_query_retry)!==originalRetry)throw new NotSent('请先核对并确认当前原查询恢复方式');
    const original=prior.config;
    if(fingerprint(original.settings_sources)!==fingerprint(change.settings_previous??original.settings_sources)||fingerprint(original.mcp_selections??[])!==fingerprint(change.previous)||fingerprint(original.skill_selections??[])!==fingerprint(change.skill_previous??[])||fingerprint(original.plugin_selections??[])!==fingerprint(change.plugin_previous??[])||fingerprint(original.installed_settings??null)!==fingerprint(change.installed_previous??null))throw new NotSent('持久原配置与失败事务不一致');
    if(this.changing||fingerprint(prior)!==fingerprint(readMetadata(this.metadata)))throw new NotSent('恢复前事务已变化');
    const restored=params.installed_recovery===undefined?original:installedSettingsRecovery(original,params.installed_recovery,change.settings_target,true);
    const recovery:ConfigurationRecovery={operation,status:'recovering',at:new Date().toISOString(),...(params.installed_recovery===undefined?{}:{installed_settings:restored.installed_settings}),...(report.original_query_retry?{original_query_retry:report.original_query_retry}:{})};
    const recovering:ConfigurationChange={...change,recovery};
    writeMetadata(this.metadata,original,recovering);this.configuration=recovering;this.changing=true;
    let stage:ConfigurationFailure['stage']='open_candidate';
    const failure=(cleanup:ConfigurationFailure['cleanup'])=>{try{this.append({kind:'configuration_failure',failure:{version:1,operation,baseline,stage,reason:'stage_failed',cleanup,at:new Date().toISOString()}});}catch{/* Keep the uncertain native outcome even if diagnostics cannot be stored. */}};
    try{
      this.config=restored;this.stopping=false;this.fault='';this.connection=randomUUID();this.requests.clear();
      await this.open(text(params.api_key,'credential'),true);
      stage='check_settings';
      const policy=(await this.hookChannel!.read()).policy;
      if(settingsGuardEvidence(1,true,policy,[],this.settingsRecordFailed)?.state!=='registered')throw new Error('original guard unavailable after reopen');
      stage='check_candidate_history';
      const history=await getSessionMessages(original.id,{dir:original.cwd,includeSystemMessages:true});
      if(fingerprint({config:original,history})!==baseline)throw new Error('original history changed during recovery');
      stage='check_components';
      const inventory=await mcpInventory(this.run),skills=await skillInventory(this.run,this.config);
      const tracksPlugins=!!(executionPlugins(restored).length||change.plugin_target?.length||change.installed_target);
      if(tracksPlugins&&!this.pluginPackages){
        const refreshed=await this.run!.reloadPlugins();
        if(refreshed.error_count||!Array.isArray(refreshed.plugins))throw new Error('original plugin package report unavailable');
        this.pluginPackages=refreshed.plugins;
      }
      const plugins=tracksPlugins?await pluginInventory(this.run,this.hookChannel,this.pluginPackages,restored):undefined;
      if(!mcpMatches(restored.mcp_selections??[],inventory,change.target)||!skillChangeMatches(restored.skill_selections??[],change.skill_target??[],skills)||tracksPlugins&&!pluginRecoveryMatches(restored,recovering,plugins!))throw new Error('original resources or inherited components unverified');
      stage='check_idle';this.assertConfigurationIdle();
      stage='check_sources';verifySettingsSources(restored.settings_sources);verifyInstalledSettings(restored);
      stage='commit';
      const recovered:ConfigurationChange={...recovering,status:'recovered',recovery:{...recovery,status:'verified',at:new Date().toISOString(),history_digest:fingerprint(history),inventory,skills,...(plugins?{plugins}:{})}};
      writeMetadata(this.metadata,restored,recovered);this.configuration=recovered;
      return {native_id:original.id,...(recovery.installed_settings?{installed_settings:recovery.installed_settings}:{}),...(recovery.original_query_retry?{original_query_retry:recovery.original_query_retry}:{})};
    }catch{
      failure('unconfirmed');await this.close();failure('confirmed');
      throw new Error('original configuration recovery requires reconciliation; no replay');
    }finally{this.changing=false;}
  }

  private hasUnfinishedWork() {
    const unfinished = this.entries.some(input => input.kind === 'input' && !this.entries.some(entry => entry.kind === 'event' && entry.operation === input.operation && entry.event.type === 'result'));
    return unfinishedHooks(this.entries).length > 0 || !!this.active || this.queue.length > 0 || this.tasks.size > 0 || unfinished || [...this.requests.values()].some(r => r.status !== 'expired');
  }

  private assertConfigurationIdle() {
    if (this.fault || this.hasUnfinishedWork()) throw new NotSent('configuration change requires a verified idle query');
  }

  async skillCatalog() {
    if (!this.run || this.stopping) throw new NotSent('原生查询未开启；读取清单不会启动或恢复查询');
    if (this.changing || this.configuration?.status === 'applying') throw new NotSent('配置正在变化，暂不能读取 Skill 来源清单');
    const run = this.run, connection = this.connection, binding = fingerprint(this.config);
    // Observe what this query reports, even when a saved source drifted. This
    // does not reload files, apply flags, or grant input authority.
    const inventory = await skillInventory(run, undefined, true);
    if (!inventory.available) throw new NotSent('当前原生 Skill 清单不可用');
    if (run !== this.run || connection !== this.connection || binding !== fingerprint(this.config) || this.changing || this.stopping) throw new NotSent('读取期间原生查询或配置已变化，请重新读取');
    return { config: this.config, connection, inventory };
  }

  async hookCatalog() {
    if (!this.run || !this.hookChannel || this.stopping) throw new NotSent('原生查询未开启；读取清单不会启动或恢复查询');
    if (this.changing || this.configuration?.status === 'applying') throw new NotSent('配置正在变化，暂不能读取 hook 来源清单');
    const run = this.run, channel = this.hookChannel, connection = this.connection, binding = fingerprint(this.config);
    const inventory = await channel.read();
    if (run !== this.run || channel !== this.hookChannel || connection !== this.connection || binding !== fingerprint(this.config) || this.changing || this.stopping) throw new NotSent('读取期间原生查询或配置已变化，请重新读取');
    return { config: this.config, connection, inventory };
  }

  private async verifyPluginBinding(adoptionTarget?: SessionConfig) {
    if (adoptionTarget && this.config.installed_settings) verifyInstalledSettingsAdoption(this.config, adoptionTarget);
    else verifyInstalledSettings(this.config);
    const run = this.run, channel = this.hookChannel, packages = this.pluginPackages;
    await verifyPlugins(run, channel, packages, this.config, this.previousPlugins());
    if (this.configuration?.status === 'recovered' && (this.configuration.original_plugins || this.configuration.recovery?.original_query_retry)) {
      const inventory = await pluginInventory(run, channel, packages, this.config);
      if (!pluginRecoveryMatches(this.config, this.configuration, inventory)) throw new NotSent('恢复后的原继承组件尚未核对一致');
    }
    if (this.requiresPluginInventory() && (run !== this.run || channel !== this.hookChannel || packages !== this.pluginPackages)) {
      throw new NotSent('核对期间原生插件清单发生变化，不能接受输入');
    }
  }

  private previousPlugins() {
    const change = this.configuration;
    return change?.status === 'recovered'
      ? change.plugin_target ?? []
      : [...(change?.plugin_previous ?? []), ...(change?.installed_previous?.selections ?? [])];
  }

  private requiresPluginInventory() {
    return executionPlugins(this.config).length > 0 || this.previousPlugins().length > 0 ||
      this.configuration?.status === 'recovered' && !!(this.configuration.original_plugins || this.configuration.recovery?.original_query_retry);
  }

  async prepareExecution(settingsTarget?: unknown, installedTarget?: unknown) {
    if (this.changing || this.configuration?.status === 'applying') throw new NotSent('configuration change remains unverified');
    this.assertConfigurationIdle();
    const targetSources = settingsTarget === undefined ? undefined : settingsSources(settingsTarget);
    const targetInstalled = installedTarget === undefined ? this.config.installed_settings : installedSettings(installedTarget);
    const candidate = {...this.config, ...(targetSources ? {settings_sources:targetSources} : {}), ...(targetInstalled ? {installed_settings:targetInstalled,settings_artifact:targetInstalled.artifact} : {})};
    if (targetSources && this.config.installed_settings) verifyInstalledSettingsAdoption(this.config, candidate);
    else verifyInstalledSettings(this.config);
    if (installedTarget !== undefined) {
      verifyInstalledSettings(candidate, false);
      verifyPluginSources(candidate);
    }
    if (targetSources !== undefined) {
      const target=targetSources, previous=this.config.settings_sources;
      if (!previous || this.config.settings_guard_version!==1 || !this.run || !this.hookChannel || fingerprint(previous.map(s=>s.path))!==fingerprint(target.map(s=>s.path))) throw new NotSent('本地设置采纳需要原连接、原文件版本与相同来源路径集合');
      const guard=settingsGuardEvidence(1,true,(await this.hookChannel.read()).policy,[],this.settingsRecordFailed);
      if(guard?.state!=='registered')throw new NotSent('当前本地设置阻止机制不可用，不能确认原版本的安全改配边界');
      verifySettingsSources(target);
    } else verifySettingsSources(this.config.settings_sources);
    if (this.run) await verifyMCP(this.run, this.config.mcp_selections ?? []);
    if (this.run) await verifySkills(this.run, this.config, (this.configuration?.status==='recovered'?this.configuration.skill_target:this.configuration?.skill_previous) ?? []);
    verifyPluginSources(this.config);
    if (this.run) await this.verifyPluginBinding(targetSources ? candidate : undefined);
    const history = await getSessionMessages(this.config.id, { dir: this.config.cwd, includeSystemMessages: true });
    this.assertConfigurationIdle();
    return { native_id: this.config.id, configuration_baseline: fingerprint({ config: this.config, history }) };
  }

  async applyExecution(params: Record<string, unknown>) {
    const operation = uuid(params.operation), baseline = text(params.baseline, 'configuration baseline');
    const selections = mcpSelections(params.mcp_selections), original = this.config;
    const previous = original.mcp_selections ?? [];
    const skillPrevious = original.skill_selections ?? [];
    const skillTarget = params.skill_selections === undefined ? skillPrevious : skillSelections(params.skill_selections);
    const tracksSkills = skillPrevious.length > 0 || skillTarget.length > 0;
    const pluginPrevious = original.plugin_selections ?? [];
    const pluginTarget = params.plugin_selections === undefined ? pluginPrevious : pluginSelections(params.plugin_selections);
    const installedPrevious = original.installed_settings;
    const installedTarget = params.installed_settings === undefined ? installedPrevious : installedSettings(params.installed_settings);
    const tracksInstalled = !!(installedPrevious || installedTarget);
    const allPluginPrevious = [...pluginPrevious, ...(installedPrevious?.selections ?? [])];
    const allPluginTarget = [...pluginTarget, ...(installedTarget?.selections ?? [])];
    const tracksPlugins = allPluginPrevious.length > 0 || allPluginTarget.length > 0;
    const settingsTarget=params.settings_sources===undefined?undefined:settingsSources(params.settings_sources);
    const settingsPrevious=original.settings_sources;
    if (this.configuration?.operation === operation) {
      if (this.configuration.status === 'verified' && this.configuration.baseline === baseline && fingerprint(this.configuration.target) === fingerprint(selections) && fingerprint(this.configuration.skill_target ?? skillPrevious) === fingerprint(skillTarget) && fingerprint(this.configuration.plugin_target ?? pluginPrevious) === fingerprint(pluginTarget) && fingerprint(this.configuration.settings_target??null)===fingerprint(settingsTarget??null) && fingerprint(this.configuration.installed_target??null)===fingerprint(installedTarget??null)) return { native_id: original.id, ...(installedTarget ? {installed_settings:installedTarget} : {}) };
      throw new Error('original configuration outcome must be reconciled without replay');
    }
    const prepared = await this.prepareExecution(settingsTarget, installedTarget);
    const adoptedHookSources = adoptedSettingsHookSources(settingsPrevious,settingsTarget);
    if (prepared.configuration_baseline !== baseline) throw new NotSent('native history or configuration changed since preparation');
    mcpDefinitions(selections); // Validate sources before closing the original query.
    const candidate = {...original, ...(settingsTarget ? {settings_sources:settingsTarget} : {}), ...(params.plugin_selections === undefined ? {} : {plugin_selections:pluginTarget}), ...(installedTarget ? {installed_settings:installedTarget,settings_artifact:installedTarget.artifact} : {})};
    verifyPluginSources(candidate);
    verifyInstalledSettings(candidate);
    this.assertConfigurationIdle();
    const change: ConfigurationChange = { operation, baseline, target: selections, previous, skill_target: skillTarget, skill_previous: skillPrevious, ...(tracksPlugins ? {plugin_target:pluginTarget,plugin_previous:pluginPrevious} : {}), ...(tracksInstalled ? {installed_target:installedTarget,installed_previous:installedPrevious,progress:{version:1,phase:'original'}} : {}), ...(settingsTarget ? {settings_target:settingsTarget,settings_previous:settingsPrevious} : {}), status: 'applying', at: new Date().toISOString() };
    writeMetadata(this.metadata, original, change);
    this.configuration = change;
    this.changing = true;
    let stage: ConfigurationFailure['stage']='open_original';
    let reason: ConfigurationFailure['reason']='stage_failed';
    const recordFailure=(cleanup:ConfigurationFailure['cleanup'])=>{
      try { this.append({kind:'configuration_failure',failure:{version:1,operation,baseline,stage,reason,cleanup,at:new Date().toISOString()}}); }
      catch { /* Diagnostic failure must not skip owned-process cleanup or alter the outcome. */ }
    };
    try {
      // Reopening the original query can itself run startup hooks. Journal the
      // new operation first, including cold sessions with no current query.
      if ((tracksSkills || tracksPlugins) && !this.run) {
        await this.open(text(params.api_key, 'credential'), true);
        const history = await getSessionMessages(original.id, {dir:original.cwd,includeSystemMessages:true});
        if (fingerprint({config:original,history}) !== baseline) throw new Error('原查询重开后历史发生变化');
      }
      stage='check_original_components';
      const skillsBefore = tracksSkills ? await skillInventory(this.run, original) : undefined;
      if (tracksSkills && !skillsBefore?.available) throw new Error('Skill 原生清单不可用');
      if (tracksPlugins && !this.pluginPackages) {
        const refreshed = await this.run!.reloadPlugins();
        if (refreshed.error_count || !Array.isArray(refreshed.plugins)) throw new Error('原生插件来源清单不可用');
        this.pluginPackages = refreshed.plugins;
      }
      const pluginsBefore = tracksPlugins ? await pluginInventory(this.run,this.hookChannel,this.pluginPackages,original) : undefined;
      if (tracksPlugins && !pluginsBefore?.available) throw new Error('原生插件组件清单不可用');
      if (tracksInstalled) {
        change.original_plugins = pluginsBefore;
        writeMetadata(this.metadata, original, change);
      }
      this.assertConfigurationIdle();
      stage='close_original';
      await this.close();
      stage='check_original_history';
      const history = await getSessionMessages(original.id, { dir: original.cwd, includeSystemMessages: true });
      if (fingerprint({ config: original, history }) !== baseline) throw new Error('history changed while closing query');
      this.stopping = false;
      this.connection = randomUUID();
      this.requests.clear();
      if (tracksInstalled) {
        // Persist before the first candidate action. A later failure cannot
        // claim the candidate was never attempted, even if its receipt is lost.
        change.progress = {version:1,phase:'candidate'};
        writeMetadata(this.metadata, original, change);
      }
      this.config = { ...candidate, mcp_selections: selections, ...(params.skill_selections === undefined ? {} : { skill_selections: skillTarget }), ...(params.plugin_selections === undefined ? {} : {plugin_selections:pluginTarget}), ...(settingsTarget ? {settings_sources:settingsTarget} : {}) };
      stage='open_candidate';
      await this.open(text(params.api_key, 'credential'), true);
      if(settingsTarget || tracksInstalled){
        stage='check_settings';
        const policy=(await this.hookChannel!.read()).policy;
        if(policy.settings_error_count>0){reason='settings_parse_errors';throw new Error('native settings source was skipped');}
        if(policy.policy_unreadable){reason='policy_unreadable';throw new Error('native policy source was unreadable');}
      }
      stage='check_candidate_history';
      const verifiedHistory = await getSessionMessages(original.id, { dir: original.cwd, includeSystemMessages: true });
      if (fingerprint(history) !== fingerprint(verifiedHistory)) throw new Error('history changed while loading configuration');
      stage='check_components';
      const verified: ConfigurationChange = { ...change, status: 'verified', at: new Date().toISOString(),
        inventory: await mcpInventory(this.run), ...(tracksSkills ? { skills: await skillInventory(this.run, this.config) } : {}), ...(tracksPlugins ? {plugins:await pluginInventory(this.run,this.hookChannel,this.pluginPackages,this.config)} : {}), history_digest: fingerprint(verifiedHistory) };
      if (tracksPlugins && (!pluginChangeMatches(allPluginTarget,allPluginPrevious,verified.plugins!) ||
        inheritedPluginComponents(pluginsBefore!,[...allPluginPrevious,...allPluginTarget],[...skillPrevious,...skillTarget],[...previous,...selections].map(s=>s.server),adoptedHookSources) !==
        inheritedPluginComponents(verified.plugins!,[...allPluginPrevious,...allPluginTarget],[...skillPrevious,...skillTarget],[...previous,...selections].map(s=>s.server),adoptedHookSources))) throw new Error('Plugin selection, retired namespace or inherited components changed before commit');
      if (!mcpMatches(selections, verified.inventory!, previous)) throw new Error('MCP inventory changed before commit');
      if (tracksSkills && (!skillChangeMatches(skillTarget, skillPrevious, verified.skills!) ||
        !tracksPlugins && inheritedSkills(skillsBefore!, [...skillPrevious, ...skillTarget]) !== inheritedSkills(verified.skills!, [...skillPrevious, ...skillTarget]))) throw new Error('Skill selection, released namespace or inherited inventory changed before commit');
      stage='check_idle';this.assertConfigurationIdle();
      stage='check_sources';verifySettingsSources(this.config.settings_sources);
      verifyInstalledSettings(this.config);
      stage='commit';
      writeMetadata(this.metadata, this.config, verified);
      this.configuration = verified;
      return { native_id: original.id, ...(installedTarget ? {installed_settings:installedTarget} : {}) };
    } catch {
      // A failed candidate can be released only after its owned child exited.
      // A crash/failed close leaves the applying record for read-only recovery.
      recordFailure('unconfirmed');
      await this.close();
      recordFailure('confirmed');
      // Local file adoption can execute candidate startup hooks. Failure after
      // closing the old query remains uncertain; never label it safely unsent
      // or reopen the old binding against the changed files.
      if (settingsTarget || tracksInstalled) throw new Error('candidate settings application requires reconciliation; no replay');
      this.config = original;
      this.stopping = false;
      this.fault = '';
      const failed: ConfigurationChange = { ...change, status: 'not_sent', at: new Date().toISOString() };
      writeMetadata(this.metadata, original, failed);
      this.configuration = failed;
      throw new NotSent('candidate configuration could not be verified; original binding retained with automation paused');
    } finally { this.changing = false; }
  }

  async close() {
    this.stopping = true;
    this.wake?.();
    this.run?.close();
    await this.reading;
    if (this.child && this.child.exitCode === null && this.child.signalCode === null) {
      const child = this.child;
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => { child.off('close', closed); reject(new Error('owned Claude process has not exited')); }, 8000);
        const closed = () => { clearTimeout(timer); resolve(); };
        child.once('close', closed);
      });
    }
    this.append({ kind: 'closed', connection: this.connection, pid: this.child?.pid });
  }
}
