import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { query, getSessionMessages } from "@anthropic-ai/claude-agent-sdk";

const config = JSON.parse(await readFile(process.argv[2], "utf8"));
const mode = process.argv[3];
const stateFile = `${config.root}/session.json`;
const prior = mode === "resume" ? JSON.parse(await readFile(stateFile, "utf8")) : null;
const history = prior ? await getSessionMessages(prior.native_id, { dir: config.cwd }) : [];
if (prior) {
  const serialized = JSON.stringify(history);
  assert(serialized.includes(`NETCAP_${config.route}_TURN_1`));
  assert(serialized.includes(`NETCAP_${config.route}_TURN_2`));
  await writeFile(`${config.root}/history-before-resume.json`, JSON.stringify(history, null, 2));
}

// Each next input is gated by the prior native result, never by receipt alone.
let release;
let finished = false;
let messages = [];
const push = (number) => {
  messages.push({ type: "user", session_id: prior?.native_id ?? "",
    message: { role: "user", content: `NETCAP_${config.route}_TURN_${number}` }, parent_tool_use_id: null });
  release?.();
};
async function* input() {
  while (!finished) {
    if (!messages.length) await new Promise((resolve) => { release = resolve; });
    while (messages.length) yield messages.shift();
  }
}
push(prior ? 3 : 1);
const run = query({
  prompt: input(),
  options: {
    cwd: config.cwd,
    pathToClaudeCodeExecutable: config.executable,
    systemPrompt: { type: "preset", preset: "claude_code" },
    settingSources: ["user", "project", "local"],
    env: { ...process.env },
    model: "claude-sonnet-4-6",
    tools: [],
    permissionMode: "default",
    persistSession: true,
    includePartialMessages: true,
    ...(prior ? { resume: prior.native_id } : {}),
  },
});
const events = [];
const results = [];
let nativeId = prior?.native_id;
try {
  for await (const event of run) {
    events.push(event);
    if (event.session_id) {
      nativeId ??= event.session_id;
      assert.equal(event.session_id, nativeId);
    }
    if (event.type !== "result") continue;
    assert.equal(event.subtype, "success", JSON.stringify(event));
    const turn = prior ? 3 : results.length + 1;
    assert.equal(event.result, `fixture ${config.route}: NETCAP_${config.route}_TURN_${turn}`);
    results.push({ uuid: event.uuid, session_id: event.session_id, subtype: event.subtype });
    if (!prior && results.length === 1) push(2);
    else break;
  }
} finally {
  finished = true;
  release?.();
  run.close();
  await writeFile(`${config.root}/${mode}-events.json`, JSON.stringify(events, null, 2));
}
assert.equal(results.length, prior ? 1 : 2);
assert(events.some((e) => e.type === "stream_event"), "no native stream events");
const result = { native_id: nativeId, results: [...(prior?.results ?? []), ...results],
  history_messages: history.length, streaming_verified: true };
await writeFile(stateFile, JSON.stringify(result, null, 2));
console.log(JSON.stringify(result));
