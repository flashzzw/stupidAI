// Run against the authenticated console_observations_smoke.py fixture.
async (page) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByText('claude-local · 配置不可用', { exact: true }).click();
  await dialog.locator('.instance-details[open]').getByText(/^CLI 无法执行或版本检查超时/).waitFor();
  await dialog.evaluate((element) => { element.scrollTop = 0; });
  await page.screenshot({ path: '.dev/logs/T06-capability-unavailable.png', fullPage: true });
  await dialog.getByText('claude-local · 配置不可用', { exact: true }).click();
  await dialog.getByText('codex-future · 未验证', { exact: true }).click();
  await dialog.getByText('codex-cli 99.0.0', { exact: true }).waitFor();
  const option = dialog.getByRole('option', { name: /route-future/ });
  if (!await option.evaluate((element) => element.disabled)) throw Error('unverified CLI offered for creation');
  await dialog.evaluate((element) => { element.scrollTop = 0; });
  await page.screenshot({ path: '.dev/logs/T06-capability-unverified.png', fullPage: true });
  await page.getByRole('button', { name: '关闭新建会话', exact: true }).click();
  return { unavailable_cli: 'visible', declared_version: 'does not grant capabilities', unverified_route: 'disabled' };
}
