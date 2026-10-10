// Substitute fixture paths from the owned capability_formats_smoke process.
async page => {
  const fixture = /*FIXTURE*/{};
  const id = 'formats-browser-' + Date.now();
  const checks = {}, errors = [];
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw Error(name); };
  const onError = error => errors.push(String(error)); page.on('pageerror', onError);
  const card = asset => page.locator('.capability-card').filter({ has: page.locator('.muted', { hasText: `· ${asset} ·` }) });
  const dialog = page.getByRole('dialog');
  try {
    await page.reload();
    await page.getByRole('button', { name: '能力库', exact: true }).click();
    await card('portable-dual').waitFor();
    const original = card('portable-dual');
    check('identity_and_selected_settings_visible', (await original.textContent()).includes('配置来源：plugin.json#extensions.com.openai') && (await original.textContent()).includes('身份入口：.claude-plugin/plugin.json'));
    check('replaced_overlay_explanation_visible', (await original.textContent()).includes('整体替代此 overlay'));
    await original.getByText(/^非文本资源 ·/).click();
    check('binary_types_and_archive_inspection_visible', (await original.textContent()).includes('image/png') && (await original.textContent()).includes('application/wasm') && (await original.textContent()).includes('已检查压缩成员'));
    await original.getByRole('button', { name: '导出资源包', exact: true }).click();
    await dialog.getByRole('button', { name: '预览资源包', exact: true }).click();
    await dialog.getByRole('region', { name: '资源包预览' }).waitFor();
    await dialog.getByText(/^非文本资源 ·/).click();
    check('binary_export_is_available_after_preview', await dialog.getByRole('button', { name: '确认下载资源包', exact: true }).isEnabled());
    for (const width of [1440, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      check(`format_preview_layout_${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth && document.querySelector('dialog').scrollWidth <= document.querySelector('dialog').clientWidth));
      await dialog.evaluate(element => { element.scrollTop = 0; });
      await page.screenshot({ path: `/home/zzw/codex-netcap/.dev/w0205-formats/export-${width}.png` });
      await dialog.evaluate(element => { element.scrollTop = element.scrollHeight; });
      await page.screenshot({ path: `/home/zzw/codex-netcap/.dev/w0205-formats/export-resources-${width}.png` });
    }
    const pending = page.waitForEvent('download');
    await dialog.getByRole('button', { name: '确认下载资源包', exact: true }).click();
    const download = await pending; await download.saveAs('/home/zzw/codex-netcap/.dev/w0205-formats/formats-browser-download.zip');
    check('large_binary_zip_download', await download.failure() === null);
    await dialog.getByRole('button', { name: '关闭资源包', exact: true }).click();
    await page.getByRole('button', { name: '导入资源包', exact: true }).click();
    await dialog.locator('input[name="archive"]').setInputFiles(fixture.packages['portable-dual']);
    await dialog.locator('input[name="asset_id"]').fill(id);
    await dialog.getByRole('button', { name: '预览资源包', exact: true }).click();
    await dialog.getByRole('region', { name: '资源包预览' }).waitFor();
    check('large_binary_import_preview_retains_source_explanation', (await dialog.textContent()).includes('通用插件结构') && (await dialog.textContent()).includes('原生加载尚未验证'));
    await dialog.getByRole('button', { name: '确认导入固定版本', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' }); await card(id).waitFor();
    const item = await page.evaluate(async id => (await (await fetch('/api/capabilities')).json()).find(item => item.asset.id === id), id);
    check('binary_import_has_fixed_provenance', item.asset.management === 'managed' && item.versions[0].imported_from.startsWith('package:') && item.diagnosis.resources.length >= 4);
    check('binary_bytes_not_saved_in_storage', await page.evaluate(() => !Object.entries(sessionStorage).some(([key,value]) => key.includes('package') && value.length > 2048)));
    await page.reload(); await page.getByRole('button', { name: '能力库', exact: true }).click(); await card(id).waitFor();
    check('reload_preserves_binary_and_manifest_diagnosis', (await card(id).textContent()).includes('配置来源：plugin.json#extensions.com.openai'));
    check('no_runtime_errors', errors.length === 0);
    return { checks, errors, asset_id: id, expectations_met: Object.values(checks).every(Boolean) };
  } finally { page.off('pageerror', onError); }
}
