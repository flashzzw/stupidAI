// Substitute only paths and URL from an owned capability_package_smoke fixture.
async page => {
  const fixture = /*FIXTURE*/{};
  const browserID = 'browser-copy-' + Date.now();
  const checks = {}, errors = [];
  const onError = error => errors.push(String(error));
  page.on('pageerror', onError);
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw Error(name); };
  const library = () => page.evaluate(async () => (await fetch('/api/capabilities')).json());
  const card = id => page.locator('.capability-card').filter({ has: page.locator('.muted', { hasText: `· ${id} ·` }) });
  const dialog = page.getByRole('dialog');
  try {
    await page.reload();
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('button', { name: '能力库', exact: true }).click();
    await page.getByRole('heading', { name: '能力库', exact: true }).waitFor();
    await card('shared-mcp').waitFor();
    check('mcp_environment_and_native_unknown_visible', (await card('shared-mcp').textContent()).includes('CAPABILITY_TEST_KEY') && (await card('shared-mcp').textContent()).includes('原生加载尚未验证'));
    await card('claude-plugin').locator('.capability-diagnosis summary').click();
    check('plugin_components_and_duplicate_warning_visible', (await card('claude-plugin').textContent()).includes('独立开关') && (await card('claude-plugin').textContent()).includes('也有定义'));
    await card('shared-method').locator('.library-history > summary').click();
    await card('shared-method').getByRole('button', { name: '导出 v1 资源包', exact: true }).click();
    await dialog.getByRole('button', { name: '预览资源包', exact: true }).click();
    await dialog.getByRole('region', { name: '资源包预览' }).waitFor();
    check('historical_export_preview_uses_v1', (await dialog.textContent()).includes('shared-method · v1'));
    await dialog.locator('.package-files > summary').click();
    check('export_lists_script_and_executable_permission', (await dialog.textContent()).includes('helper.py') && (await dialog.textContent()).includes('700'));
    const downloaded = page.waitForEvent('download');
    await dialog.getByRole('button', { name: '确认下载资源包', exact: true }).click();
    const download = await downloaded;
    await download.saveAs('/home/zzw/codex-netcap/.dev/w0205-package/browser-download.zip');
    check('zip_download_completes', (await download.failure()) === null && download.suggestedFilename() === 'shared-method.zip');
    await dialog.getByRole('button', { name: '关闭资源包', exact: true }).click();
    await page.getByRole('button', { name: '导入资源包', exact: true }).click();
    await dialog.locator('input[name="archive"]').setInputFiles(fixture.packages['shared-method']);
    await dialog.locator('input[name="asset_id"]').fill(browserID);
    await dialog.getByRole('button', { name: '预览资源包', exact: true }).click();
    await dialog.getByRole('region', { name: '资源包预览' }).waitFor();
    check('import_preview_has_no_catalog_write', !(await library()).some(item => item.asset.id === browserID));
    await dialog.locator('input[name="asset_id"]').fill(browserID + '-adjusted');
    check('edited_import_target_invalidates_preview', await dialog.getByRole('region', { name: '资源包预览' }).count() === 0);
    await dialog.locator('input[name="asset_id"]').fill(browserID);
    await dialog.getByRole('button', { name: '预览资源包', exact: true }).click();
    await dialog.getByRole('region', { name: '资源包预览' }).waitFor();
    for (const width of [1440, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      check(`preview_layout_${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/home/zzw/codex-netcap/.dev/w0205-package/package-preview-${width}.png` });
    }
    await dialog.getByRole('button', { name: '确认导入固定版本', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    await card(browserID).waitFor();
    let imported = (await library()).find(item => item.asset.id === browserID);
    check('import_creates_managed_version_and_provenance', imported.management === 'managed' && imported.versions[0].imported_from.startsWith('package:'));
    const original = imported.asset.source;
    await page.getByRole('button', { name: '导入资源包', exact: true }).click();
    await dialog.locator('input[name="archive"]').setInputFiles(fixture.source_root + '/shared-method-v2.zip');
    await dialog.getByRole('combobox', { name: '导入方式', exact: true }).selectOption('update');
    await dialog.getByRole('combobox', { name: '更新目标', exact: true }).selectOption(browserID);
    await dialog.getByRole('button', { name: '预览资源包', exact: true }).click();
    await dialog.getByRole('region', { name: '资源包预览' }).waitFor();
    await dialog.getByRole('button', { name: '确认导入固定版本', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    await card(browserID).locator('.muted').filter({ hasText: '· v2 ·' }).waitFor();
    imported = (await library()).find(item => item.asset.id === browserID);
    check('import_update_retains_historical_source', imported.versions.length === 2 && imported.versions[0].asset.source === original && imported.asset.source !== original);
    check('archive_bytes_not_persisted_in_browser_storage', await page.evaluate(() => !Object.entries(sessionStorage).some(([key, value]) => key.includes('package') && (value.includes('UEsDB') || value.length > 2048))));
    await page.getByRole('button', { name: '导入资源包', exact: true }).click();
    await dialog.locator('input[name="archive"]').setInputFiles('/home/zzw/codex-netcap/.dev/w0205-package/invalid.zip');
    await dialog.getByRole('button', { name: '预览资源包', exact: true }).click();
    await dialog.getByRole('alert').waitFor();
    check('invalid_archive_has_actionable_error_and_no_confirmation', (await dialog.getByRole('alert').textContent()).includes('ZIP') && await dialog.getByRole('button', { name: '确认导入固定版本', exact: true }).count() === 0);
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'hidden' });
    await page.reload();
    await page.getByRole('button', { name: '能力库', exact: true }).click();
    await card(browserID).waitFor();
    check('reload_retains_imported_version', (await card(browserID).textContent()).includes('· v2 ·'));
    for (const width of [1440, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      check(`library_layout_${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/home/zzw/codex-netcap/.dev/w0205-package/package-library-${width}.png` });
    }
    check('no_browser_runtime_errors', errors.length === 0);
    return { checks, errors, expectations_met: Object.values(checks).every(Boolean) };
  } finally { page.off('pageerror', onError); }
}
