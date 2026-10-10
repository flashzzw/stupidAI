// Run with claude_console_smoke.py --hold --interactions and an authenticated browser page.
async (page) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  async function create(name) {
    await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
    await page.getByLabel('会话名称', { exact: true }).fill(name);
    await page.getByRole('button', { name: '创建会话', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
  }
  async function send(text) {
    await page.getByRole('textbox', { name: '消息', exact: true }).fill(text);
    await page.getByRole('button', { name: '发送消息' }).click();
  }
  for (const [kind, label] of [['APPROVE', '允许本次'], ['DECLINE', '拒绝本次']]) {
    await create(`浏览器 ${kind}`);
    await send(`NETCAP_CLAUDE_${kind}`);
    await page.getByRole('button', { name: label, exact: true }).waitFor();
    if (!(await page.locator('.pending-title').textContent()).includes('printf')) throw Error('approval hides the actual command');
    await page.getByText('工具参数', { exact: true }).click();
    await page.screenshot({ path: `.dev/logs/T04-${kind.toLowerCase()}-pending.png`, fullPage: true });
    await page.getByRole('button', { name: label, exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    if (await page.locator('.pending-panel').count()) throw Error('resolved approval remains replyable');
  }
  await create('浏览器 Claude 问题');
  await send('NETCAP_CLAUDE_QUESTION');
  await page.getByRole('radio', { name: /^green/ }).check();
  await page.screenshot({ path: '.dev/logs/T04-question-pending.png', fullPage: true });
  await page.getByRole('button', { name: '提交回答', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
  await page.waitForFunction(() => [...document.querySelectorAll('.message.tool')].some((el) => el.textContent.includes('green')));
  await page.reload();
  await page.waitForFunction(() => [...document.querySelectorAll('.message.tool')].some((el) => el.textContent.includes('green')));
  await page.getByText('返回原终端', { exact: true }).click();
  const terminal = await page.locator('.native-location pre').textContent();
  if (!terminal.includes('CLAUDE_CONFIG_DIR') || !terminal.includes('--resume')) throw Error('missing original terminal reference');
  for (const width of [1440, 768]) {
    await page.setViewportSize({ width, height: 1024 });
    if (width === 768) await page.getByRole('button', { name: '详情', exact: true }).click();
    if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw Error(`overflow at ${width}`);
    await page.screenshot({ path: `.dev/logs/T04-interactions-${width}.png`, fullPage: true });
  }
  await page.getByRole('button', { name: '关闭', exact: true }).click();
  await page.setViewportSize({ width: 1440, height: 900 });
  await create('浏览器停止');
  await send('NETCAP_A_TURN_1');
  await page.getByRole('button', { name: '停止当前轮次', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
  await page.getByRole('button', { name: '恢复自动化', exact: true }).waitFor();
  return { approvals: ['allow', 'deny'], native_question: 'answered and retained after reload', terminal_command: true, interrupt: 'paused and ended', overflow: false };
}
