// Transport-only fixture. The native Windows probe verifies the real SDK/tool.
import { mock } from 'node:test';
import { randomUUID } from 'node:crypto';
import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const root = process.argv[2];
appendFileSync(join(root, 'bridge-starts.jsonl'), JSON.stringify({pid: process.pid}) + '\n');

class Session {
  constructor(root, config, fresh = true) {
    this.root = root;
    this.config = config;
    this.connection = randomUUID();
    this.live = false;
    this.active = '';
    this.inputs = [];
    this.pending = [];
    this.results = [];
    this.executions = 0;
    this.interrupts = 0;
    if (fresh) writeFileSync(join(root, config.id + '.json'), JSON.stringify(config));
  }
  static restore(root, id) {
    return new Session(root, JSON.parse(readFileSync(join(root, id + '.json'), 'utf8')), false);
  }
  async open() { this.live = true; }
  async send(params) {
    this.inputs.push(params.operation);
    this.active = params.operation;
    this.pending.push({id: randomUUID(), connection: this.connection, operation: params.operation, status: 'pending'});
    return {native_id: this.config.id, native_turn: params.operation};
  }
  async respond() {
    this.pending[0].status = 'submitted';
    this.executions++;
    this.timer = setInterval(() => {
      if (!existsSync(join(this.root, 'release'))) return;
      this.results.push({operation: this.active, output: '中文完整结果'});
      this.active = '';
      this.pending[0].status = 'expired';
      clearInterval(this.timer);
    }, 10);
    return {native_id: this.config.id, native_turn: this.active};
  }
  async snapshot() {
    return {native_id: this.config.id, bridge_pid: process.pid, connection: this.connection, live: this.live,
      active: this.active, inputs: this.inputs, executions: this.executions, interrupts: this.interrupts, pending: this.pending, results: this.results};
  }
  async interrupt() {
    this.interrupts++;
    writeFileSync(join(this.root, 'interrupt-started'), 'accepted once');
    while (!existsSync(join(this.root, 'interrupt-release'))) await new Promise(resolve => setTimeout(resolve, 10));
    writeFileSync(join(this.root, 'interrupt-finished'), 'original command settled');
    return {native_id: this.config.id};
  }
  async close() {
    clearInterval(this.timer);
    this.live = false;
    appendFileSync(join(this.root, 'closed.jsonl'), JSON.stringify({native_id: this.config.id, pid: process.pid}) + '\n');
  }
}

mock.module(new URL('../bridges/claude/dist/session.js', import.meta.url), {namedExports: {Session}});
