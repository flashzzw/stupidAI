// Playwright browser_run_code callback. Start opencode_console_smoke.py --hold and log in
// with that isolated instance's token before running this file.
async (page) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
  await page.getByLabel('会话名称', { exact: true }).fill('浏览器两轮验收');
  await page.getByRole('button', { name: '创建会话', exact: true }).click();
  await page.getByRole('heading', { level: 1, name: '浏览器两轮验收', exact: true }).waitFor();
  await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
  for (const turn of [1, 2]) {
    await page.getByRole('textbox', { name: '消息', exact: true }).fill(`NETCAP_A_TURN_${turn}`);
    await page.getByRole('button', { name: '发送消息' }).click();
    await page.waitForFunction((turn) => document.querySelector('.badge')?.textContent === '执行中' &&
      [...document.querySelectorAll('.message.assistant')].some((el) => el.textContent.includes(`fixture A: NETCAP_A_TURN_${turn}`)), turn);
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
  }
  const nativeId = await page.locator('dd.mono').first().textContent();
  await page.reload();
  await page.waitForFunction(() => document.querySelectorAll('.message.assistant').length === 2);
  if (await page.locator('dd.mono').first().textContent() !== nativeId) throw Error('native identity changed after reload');
  const wideOverflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
  if (wideOverflow) throw Error('desktop overflow');
  await page.screenshot({ path: '.dev/logs/T05-desktop-final.png', fullPage: true });
  await page.setViewportSize({ width: 768, height: 1024 });
  await page.getByRole('button', { name: '详情', exact: true }).click();
  await page.getByRole('button', { name: '暂停自动化', exact: true }).click();
  await page.getByRole('button', { name: '恢复自动化', exact: true }).waitFor();
  await page.getByRole('button', { name: '关闭', exact: true }).click();
  const narrowOverflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
  if (narrowOverflow) throw Error('tablet overflow');
  await page.screenshot({ path: '.dev/logs/T05-tablet-final.png', fullPage: true });
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('offline draft stays unsent');
  if (!await page.getByRole('button', { name: '发送消息' }).isEnabled()) throw Error('online idle send unavailable');
  await page.context().setOffline(true);
  await page.getByText('连接断开 · 数据已过期', { exact: true }).waitFor();
  if (!await page.getByRole('button', { name: '发送消息' }).isDisabled()) throw Error('disconnected write still enabled');
  await page.context().setOffline(false);
  await page.getByText('控制台已连接', { exact: true }).waitFor();
  return { native_id: nativeId, streamed_turns: 2, messages_after_reload: await page.locator('.message').count(),
    pause: 'persisted', offline: 'writes disabled', reconnect: 'snapshot restored', desktop_overflow: wideOverflow, tablet_overflow: narrowOverflow };
}
