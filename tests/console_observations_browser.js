// Start console_observations_smoke.py --hold and authenticate first.
async (page) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  for (const agent of ['codex', 'claude', 'opencode']) {
    await page.getByRole('button', { name: new RegExp(`^${agent} · same-native-id `) }).click();
    await page.getByRole('heading', { name: `${agent} · same-native-id`, exact: true }).waitFor();
    await page.getByText('仅接收活动事件，不提供控制权、完整原生历史或可信路由信息。请在原终端继续操作。', { exact: true }).waitFor();
    if (await page.locator('.composer, .inspector-actions, .associate-form').count()) throw Error('observed session exposed control form');
    if (agent !== 'codex') {
      await page.getByText('此交互暂不支持网页回复，请返回原端处理。', { exact: true }).waitFor();
      if (await page.getByRole('region', { name: '待处理请求' }).locator('button, input, form').count()) throw Error('unsupported interaction exposed reply controls');
    }
    await page.locator('.event-timeline summary').click();
    await page.locator('.event-timeline').getByText('通知处理方：原通知工具', { exact: true }).first().waitFor();
    await page.locator('.event-timeline').getByText(new RegExp(`来源：.*${agent}`)).first().waitFor();
    await page.screenshot({ path: `.dev/logs/T06-observed-${agent}.png`, fullPage: true });
    await page.locator('.event-timeline summary').click();
  }
  await page.getByRole('button', { name: '归档会话', exact: true }).click();
  await page.getByRole('button', { name: '重新显示会话', exact: true }).waitFor();
  await page.getByRole('combobox', { name: '会话范围', exact: true }).selectOption('archived');
  await page.getByRole('button', { name: /^opencode · same-native-id / }).waitFor();
  await page.reload();
  await page.getByRole('button', { name: '重新显示会话', exact: true }).waitFor();
  await page.getByRole('button', { name: '重新显示会话', exact: true }).click();
  await page.getByRole('button', { name: '归档会话', exact: true }).waitFor();
  await page.getByRole('combobox', { name: '会话范围', exact: true }).selectOption('active');
  await page.setViewportSize({ width: 768, height: 1000 });
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw Error('observed page overflows');
  await page.screenshot({ path: '.dev/logs/T06-observed-768.png', fullPage: true });
  return { agents: 3, controls: 'hidden', pending: 'original terminal only', sources: 'visible', archive_reload: true, overflow: false };
}
