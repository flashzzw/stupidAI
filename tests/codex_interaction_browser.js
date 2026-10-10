// Run with console_browser_fixture.py and an authenticated browser page.
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
  for (const [kind, label] of [['APPROVE', '允许本次'], ['DECLINE', '拒绝并中止']]) {
    await create(`浏览器 ${kind}`);
    await send(`NETCAP_${kind}`);
    await page.getByRole('button', { name: label, exact: true }).waitFor();
    await page.screenshot({ path: `.dev/logs/T03-${kind.toLowerCase()}-pending.png`, fullPage: true });
    await page.getByRole('button', { name: label, exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    if (await page.locator('.pending-panel').count()) throw Error('resolved approval remains replyable');
  }
  await create('浏览器异步回答');
  await send('NETCAP_QUESTION');
  await page.getByRole('radio', { name: 'green', exact: true }).check();
  await page.screenshot({ path: '.dev/logs/T03-question-pending.png', fullPage: true });
  await page.getByRole('button', { name: '提交回答', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
  await page.waitForFunction(() => [...document.querySelectorAll('.message.user')].some((el) => el.textContent.includes('green')));
  await page.reload();
  await page.waitForFunction(() => [...document.querySelectorAll('.message.user')].some((el) => el.textContent.includes('green')));
  await page.getByText('返回原终端', { exact: true }).click();
  const terminal = await page.locator('.native-location pre').textContent();
  if (!terminal.includes('--remote') || !terminal.includes('resume')) throw Error('missing original terminal reference');
  for (const width of [1440, 768]) {
    await page.setViewportSize({ width, height: 1024 });
    if (width === 768) await page.getByRole('button', { name: '详情', exact: true }).click();
    if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw Error(`overflow at ${width}`);
    await page.screenshot({ path: `.dev/logs/T03-interactions-${width}.png`, fullPage: true });
  }
  await page.getByRole('button', { name: '关闭', exact: true }).click();
  await page.setViewportSize({ width: 1440, height: 900 });
  await create('浏览器停止');
  await send('NETCAP_A_TURN_1');
  await page.getByRole('button', { name: '停止当前轮次', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
  await page.getByRole('button', { name: '恢复自动化', exact: true }).waitFor();
  return { approvals: ['accept', 'cancel'], async_question: 'answered and retained after reload', terminal_command: true, interrupt: 'paused and ended', overflow: false };
}
