// Inject only owned fixture definitions at PROFILES; never embed login tokens.
async (page) => {
  const profiles = /*PROFILES*/[];
  const checks = {}, errors = [];
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw Error(name); };
  const onError = error => errors.push(String(error));
  const onConsole = message => { if (message.type() === 'error') errors.push(message.text()); };
  page.on('pageerror', onError); page.on('console', onConsole);
  const snapshot = () => page.evaluate(async () => (await fetch('/api/snapshot')).json());
  try {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('button', { name: '执行配置', exact: true }).click();
    await page.getByRole('heading', { name: '执行配置', exact: true }).waitFor();
    check('empty_registration_state', await page.getByText(/尚未登记执行配置。/).count() === 1);
    for (const profile of profiles) {
      const i = profile.instance, r = profile.route;
      await page.getByRole('button', { name: '登记执行配置', exact: true }).click();
      const dialog = page.getByRole('dialog', { name: '登记执行配置', exact: true });
      await dialog.getByRole('combobox', { name: '执行后端', exact: true }).selectOption(i.kind);
      const values = { name: i.name, instance_id: i.id, executable: i.executable, profile: i.profile, policy_revision: i.policy_revision,
        env_allow: i.env_allow.join(', '), route_id: r.id, provider: r.provider, endpoint: r.endpoint, model: r.model,
        credential_ref: r.credential_ref, credential_revision: r.credential_revision, config_revision: r.config_revision };
      if (i.kind === 'codex') values.socket = i.socket;
      else Object.assign(values, { settings_file: i.settings_file, bridge: i.bridge, node_executable: i.node_executable });
      for (const [name, value] of Object.entries(values)) await dialog.locator(`[name="${name}"]`).fill(value);
      if (i.id === 'codex-a') {
        await dialog.locator('[name=instance_id]').fill('invalid/id');
        check('id_pattern_rejects_invalid_characters', await dialog.locator('[name=instance_id]').evaluate(input => !input.checkValidity()));
        await dialog.locator('[name=instance_id]').fill(i.id);
      }
      const count = (await snapshot()).instances.length;
      await dialog.getByRole('button', { name: '预览并诊断', exact: true }).click();
      await dialog.getByRole('region', { name: '登记预览' }).waitFor();
      check(`${i.id}_preview_not_persisted`, (await snapshot()).instances.length === count);
      if (i.kind === 'claude') {
        await dialog.locator('.instance-details > summary').click();
        check(`${i.id}_settings_and_state_separate`, await dialog.getByText(i.settings_file, { exact: true }).count() === 1 && await dialog.getByText(i.profile, { exact: true }).count() >= 1);
      }
      if (i.id === 'codex-a') {
        await page.setViewportSize({ width: 768, height: 1000 });
        check('dialog_768_no_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
        await dialog.getByRole('region', { name: '登记预览' }).scrollIntoViewIfNeeded();
        await page.screenshot({ path: '/home/zzw/codex-netcap/.dev/w0201/registration-768.png' });
        await page.setViewportSize({ width: 1440, height: 1000 });
      }
      await dialog.getByRole('button', { name: '确认登记', exact: true }).click();
      await dialog.waitFor({ state: 'hidden' });
      await page.getByText(`已登记 ${i.id}；可在下方或新建会话中选用。`, { exact: true }).waitFor();
    }
    await page.waitForFunction(() => document.querySelectorAll('.execution-profile').length === 4);
    check('four_same_named_profiles_visible', await page.getByRole('heading', { name: '同名开发配置', exact: true }).count() === 4);
    check('registration_created_no_sessions', (await snapshot()).sessions.length === 0);
    await page.getByRole('button', { name: '重新诊断配置', exact: true }).click();
    await page.getByText('诊断已刷新，未发送模型请求。', { exact: true }).waitFor();
    const card = page.locator('.execution-profile').filter({ has: page.getByText('claude · claude-b', { exact: true }) });
    await card.getByRole('button', { name: '使用此配置', exact: true }).click();
    const create = page.getByRole('dialog', { name: '新建会话', exact: true });
    check('use_profile_selects_exact_route', await create.locator('select[name="route"]').inputValue() === 'claude-b-route');
    await create.getByLabel('会话名称', { exact: true }).fill('W02.01 浏览器原生选用');
    const created = page.waitForResponse(r => r.url().endsWith('/api/commands') && r.request().postDataJSON()?.kind === 'create');
    await create.getByRole('button', { name: '创建会话', exact: true }).click();
    const sid = (await (await created).json()).session_id;
    await page.getByRole('heading', { name: 'W02.01 浏览器原生选用', exact: true }).waitFor();
    await page.locator('.execution-snapshot > summary').click();
    await page.getByText('Claude 自定义 settings 来源', { exact: true }).waitFor();
    await page.waitForFunction(async sid => (await (await fetch(`/api/sessions/${sid}`)).json()).session.status === 'idle', sid);
    await page.getByLabel('消息', { exact: true }).fill('NETCAP_B_TURN_1 BROWSER_claude_b');
    await page.getByRole('button', { name: '发送消息', exact: false }).click();
    await page.getByText('fixture B: NETCAP_B_TURN_1', { exact: true }).waitFor();
    check('browser_creation_uses_registered_native_route', (await snapshot()).sessions.find(s => s.id === sid).scope.instance.id === 'claude-b');
    await page.reload();
    await page.getByRole('heading', { name: 'W02.01 浏览器原生选用', exact: true }).waitFor();
    check('reload_keeps_selected_session', await page.getByText('fixture B: NETCAP_B_TURN_1', { exact: true }).count() === 1);
    await page.getByRole('button', { name: '执行配置', exact: true }).click();
    await page.getByRole('heading', { name: '执行配置', exact: true }).waitFor();
    for (const width of [1440, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      check(`profiles_layout_${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/home/zzw/codex-netcap/.dev/w0201/profiles-${width}.png` });
    }
    await page.getByRole('button', { name: '登记执行配置', exact: true }).click();
    await page.keyboard.press('Escape');
    check('escape_closes_dialog', await page.getByRole('dialog').count() === 0);
    check('no_javascript_errors', errors.length === 0);
    return { checks, errors, session_id: sid, expectations_met: Object.values(checks).every(Boolean) };
  } finally { page.off('pageerror', onError); page.off('console', onConsole); }
}
