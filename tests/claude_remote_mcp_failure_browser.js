// Authenticated claude_remote_mcp_smoke.py --hold fixture, after the normal browser suite.
async (page) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.reload();
  await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
  await page.getByLabel('会话名称', { exact: true }).fill('远程 MCP 认证失败验收');
  await page.locator('select[name="capability_selection:wrongauth"]').selectOption('enabled');
  const created = page.waitForResponse(r => r.url().endsWith('/api/commands') && r.request().postDataJSON()?.kind === 'create');
  await page.getByRole('button', { name: '创建会话', exact: true }).click();
  const sid = (await (await created).json()).session_id;
  await page.waitForFunction(async id => {
    const response = await fetch('/api/snapshot');
    const state = await response.json();
    const session = state.sessions.find(s => s.id === id);
    return session?.status === 'closed' && !session.archived && state.operations.some(o => o.session_id === id && ['failed', 'not_sent'].includes(o.status));
  }, sid);
  await page.locator('.execution-snapshot summary').click();
  const unverified = page.getByText('等待原生加载核对；尚不能确认本次选择已生效。', { exact: true });
  await unverified.waitFor();
  await page.getByText('create · 确认未发送', { exact: true }).click();
  await page.locator('.operation .error').filter({ hasText: 'MCP selection could not be verified; check failed connections, authentication or a same-named inherited server' }).waitFor();
  const message = page.getByRole('textbox', { name: '消息', exact: true });
  if (await message.isEnabled()) await message.fill('这条输入必须被阻止');
  const checks = {
    closed_is_not_labeled_archived: await page.locator('.badge').textContent() === '已关闭',
    failure_shown_as_unverified: await unverified.isVisible() && await page.getByText('尚未取得', { exact: true }).isVisible(),
    nonempty_input_stays_blocked: await page.getByRole('button', { name: '发送消息', exact: true }).isDisabled(),
    credentials_absent_from_page: !/fixture-mcp-|invalid-fixture-key/.test(await page.locator('body').innerText()),
    layout: await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
  };
  await unverified.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0206-remote/auth-failure.png' });
  if (!Object.values(checks).every(Boolean)) throw Error(JSON.stringify(checks));
  return { checks, session: sid, expectations_met: true };
}
