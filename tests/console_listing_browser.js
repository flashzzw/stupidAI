// Start console_listing_smoke.py --hold; authenticate before running.
async (page) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole('button', { name: /^分页验收 / }).click();
  await page.waitForFunction(() => document.querySelectorAll('.message').length === 50);
  await page.getByRole('region', { name: '本地验收项目', exact: true }).waitFor();
  await page.getByRole('region', { name: '第二项目', exact: true }).waitFor();
  await page.getByRole('button', { name: '加载更早的消息', exact: true }).click();
  await page.waitForFunction(() => document.querySelectorAll('.message').length === 54);
  if (await page.locator('.message').first().locator('.message-text').textContent() !== 'NETCAP_A_TURN_1') throw Error('oldest history missing');
  if (await page.getByRole('button', { name: '加载更早的消息', exact: true }).count()) throw Error('history end not detected');
  await page.getByRole('button', { name: '归档会话', exact: true }).click();
  await page.getByRole('button', { name: '重新显示会话', exact: true }).waitFor();
  await page.getByRole('button', { name: /^分页验收 / }).waitFor({ state: 'detached' });
  await page.getByRole('combobox', { name: '会话范围', exact: true }).selectOption('archived');
  await page.getByRole('button', { name: /^分页验收 / }).waitFor();
  await page.getByRole('combobox', { name: 'Agent 筛选', exact: true }).selectOption('codex');
  await page.getByText('没有符合筛选条件的会话。', { exact: true }).waitFor();
  await page.getByRole('combobox', { name: 'Agent 筛选', exact: true }).selectOption('opencode');
  await page.getByLabel('搜索会话', { exact: true }).fill('不存在的名称');
  await page.getByText('没有符合筛选条件的会话。', { exact: true }).waitFor();
  await page.getByLabel('搜索会话', { exact: true }).fill('本地验收项目');
  await page.getByRole('button', { name: /^分页验收 / }).waitFor();
  await page.screenshot({ path: '.dev/logs/T06-archive-filter.png', fullPage: true });
  await page.getByRole('button', { name: '重新显示会话', exact: true }).click();
  await page.getByRole('button', { name: '归档会话', exact: true }).waitFor();
  await page.getByLabel('搜索会话', { exact: true }).fill('');
  await page.getByRole('combobox', { name: '会话范围', exact: true }).selectOption('active');
  await page.getByRole('button', { name: /^分页验收 / }).waitFor();
  await page.reload();
  await page.waitForFunction(() => document.querySelectorAll('.message').length === 50);
  for (const width of [1440, 768]) {
    await page.setViewportSize({ width, height: 1024 });
    if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw Error('layout overflow');
    await page.screenshot({ path: `.dev/logs/T06-listing-${width}.png`, fullPage: true });
  }
  const data = await page.evaluate(async () => (await fetch('/api/workspace')).json());
  if (data.sessions.some((s) => s.view.messages?.length) || data.operations.length) throw Error('list response includes conversation bodies');
  return { projects: 2, initial_page: 50, older_messages_loaded: 54, filters: ['agent', 'search', 'archive'], archive: 'local visibility restored', reload: 'latest page', workspace: 'metadata only', overflow: false };
}
