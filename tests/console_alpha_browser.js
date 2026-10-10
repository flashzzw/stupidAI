// Run against an authenticated console_alpha_smoke.py --hold fixture.
async (page) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  async function create(agent, title) {
    await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('会话名称', { exact: true }).fill(title);
    await dialog.getByRole('combobox', { name: '项目', exact: true }).selectOption(agent === 'codex' ? 'fixture' : agent);
    await dialog.getByRole('combobox', { name: '路由', exact: true }).selectOption({ codex: 'route-a', claude: 'route-b', opencode: 'route-c' }[agent]);
    await dialog.getByRole('button', { name: '创建会话', exact: true }).click();
    await page.getByRole('heading', { name: title, exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲', undefined, { timeout: 125000 });
  }
  async function send(text) {
    await page.getByRole('textbox', { name: '消息', exact: true }).fill(text);
    await page.getByRole('button', { name: '发送消息' }).click();
  }
  const results = {};
  for (const agent of ['codex', 'claude', 'opencode']) {
    const prefix = { codex: 'NETCAP_', claude: 'NETCAP_CLAUDE_', opencode: 'NETCAP_OPENCODE_' }[agent];
    await create(agent, `共用界面 ${agent} 审批`);
    await send(prefix + 'APPROVE');
    await page.getByRole('button', { name: '允许本次', exact: true }).waitFor();
    await page.screenshot({ path: `.dev/logs/T06-alpha-${agent}-approval.png`, fullPage: true });
    await page.getByRole('button', { name: '允许本次', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    if (await page.locator('.pending-panel').count()) throw Error(agent + ' resolved approval still visible');
    await create(agent, `共用界面 ${agent} 问题`);
    await send(prefix + (agent === 'opencode' ? 'MULTI' : 'QUESTION'));
    await page.getByRole(agent === 'opencode' ? 'checkbox' : 'radio', { name: /^green/ }).check();
    if (agent === 'opencode') await page.getByRole('checkbox', { name: /^blue/ }).check();
    await page.getByRole('button', { name: '提交回答', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    await page.waitForFunction(() => [...document.querySelectorAll('.message')].some((el) => el.textContent.includes('green')));
    await page.reload();
    await page.waitForFunction(() => [...document.querySelectorAll('.message')].some((el) => el.textContent.includes('green')));
    await page.locator('.native-location summary').click();
    if (!await page.locator('.native-location pre').textContent()) throw Error(agent + ' original terminal reference missing');
    await page.locator('.event-timeline summary').click();
    await page.locator('.event-timeline').getByText('通知处理方：控制台', { exact: true }).first().waitFor();
    await page.locator('.inspector .instance-details summary').click();
    await page.locator('.inspector .instance-details').getByText('实际 CLI 版本', { exact: true }).waitFor();
    await page.screenshot({ path: `.dev/logs/T06-alpha-${agent}-history.png`, fullPage: true });
    await create(agent, `共用界面 ${agent} 停止`);
    await send('NETCAP_A_TURN_1');
    await page.getByRole('button', { name: '停止当前轮次', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    await page.getByRole('button', { name: '恢复自动化', exact: true }).waitFor();
    results[agent] = { approval: 'accepted', question: agent === 'opencode' ? 'multiple' : 'single', history_after_reload: true, interrupt: 'paused', capability: 'visible', source: 'runtime' };
  }
  await page.context().setOffline(true);
  await page.getByText('连接断开 · 数据已过期', { exact: true }).waitFor();
  if (!await page.getByRole('button', { name: '发送消息' }).isDisabled()) throw Error('offline controls remained enabled');
  await page.context().setOffline(false);
  await page.getByText('控制台已连接', { exact: true }).waitFor();
  await page.setViewportSize({ width: 768, height: 1000 });
  await page.getByRole('button', { name: '详情', exact: true }).click();
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw Error('narrow inspector overflow');
  await page.screenshot({ path: '.dev/logs/T06-alpha-768.png', fullPage: true });
  if (errors.length) throw Error(errors.join('\n'));
  return { agents: results, offline: 'disabled and recovered', overflow: false, page_errors: [] };
}
