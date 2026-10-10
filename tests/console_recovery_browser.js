// Start console_browser_fixture.py --info-file .dev/logs/T06-recovery-info.json.
async (page) => {
  // The harness drives filesystem faults between phases, outside the browser.
  const { phase, native } = page.netcapRecovery;
  await page.setViewportSize({ width: 1440, height: 1000 });
  async function create(title) {
    await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
    await page.getByLabel('会话名称', { exact: true }).fill(title);
    await page.getByRole('button', { name: '创建会话', exact: true }).click();
    await page.getByRole('heading', { name: title, exact: true }).waitFor();
  }
  async function snapshot() { return page.evaluate(async () => (await fetch('/api/snapshot')).json()); }
  if (phase === 'create') {
    await create('Unknown 浏览器验收');
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    return { created: true };
  }
  if (phase === 'unknown') {
    await page.getByRole('textbox', { name: '消息', exact: true }).fill('NETCAP_A_TURN_1');
    await page.getByRole('button', { name: '发送消息' }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '待核对');
    await page.getByRole('button', { name: '转人工', exact: true }).click();
    await page.getByText('人工接管', { exact: true }).waitFor();
    if (!await page.getByRole('button', { name: '发送消息' }).isDisabled()) throw Error('unknown execution admitted send');
    await page.getByRole('button', { name: '恢复自动化', exact: true }).click();
    await page.getByRole('alert').filter({ hasText: '未决操作不会被 resume 清除' }).waitFor();
    const unknown = (await snapshot()).operations.find((op) => op.intent.kind === 'send');
    if (unknown.status !== 'uncertain') throw Error('unknown operation missing');
    return { unknown: 'retained', resume: 'rejected', takeover: 'preserved' };
  }
  if (phase === 'restart') {
    await page.waitForFunction(async () => {
      try { const s = await (await fetch('/api/snapshot')).json(); return s.operations.some((op) => op.status === 'uncertain' && op.evidence.some((e) => e.source === 'console.restart')); }
      catch { return false; }
    });
    await page.reload();
    await page.getByText('人工接管', { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '待核对');
    await page.locator('.operation').filter({ hasText: 'send · 待核对' }).locator('summary').click();
    await page.getByRole('button', { name: '验证并关联原执行', exact: true }).waitFor();
    await page.screenshot({ path: '.dev/logs/T06-unknown-restart.png', fullPage: true });
    return { restart: 'unknown and takeover preserved' };
  }
  if (phase === 'reconcile') {
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    await page.getByText('人工接管', { exact: true }).waitFor();
    return { full_evidence: 'original turn resolved' };
  }
  if (phase === 'lost-create') {
    await create('原执行关联验收');
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '待核对');
    await page.locator('.operation').filter({ hasText: 'create · 待核对' }).locator('summary').click();
    return { creation: 'uncertain' };
  }
  if (phase === 'associate') {
    await page.getByLabel('候选原生会话 ID', { exact: true }).fill(native);
    await page.getByRole('button', { name: '验证并关联原执行', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
    await page.getByText(native, { exact: true }).first().waitFor();
    await page.screenshot({ path: '.dev/logs/T06-associated.png', fullPage: true });
    return { association: 'original creation verified' };
  }
  throw Error('unknown recovery test phase');
}
