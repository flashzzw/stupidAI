// Run with playwright-cli run-code --filename after login to claude_mcp_selection_smoke.py --hold.
async (page) => {
  const errors = [];
  const onError = error => errors.push(String(error));
  page.on('pageerror', onError);
  const checks = {};
  const sessions = {};
  function check(name, value) { checks[name] = Boolean(value); if (!value) throw Error(name); }
  async function execution(id) {
    return page.evaluate(async id => {
      const response = await fetch(`/api/sessions/${id}/execution`);
      if (!response.ok) throw Error(`execution read: ${response.status}`);
      return response.json();
    }, id);
  }
  try {
    await page.setViewportSize({ width: 1440, height: 1000 });
    for (const mode of ['enabled', 'disabled']) {
      await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
      const choice = page.locator('select[name="capability_selection:shared"]');
      check(`${mode}_defaults_to_inherit`, await choice.inputValue() === 'inherit');
      check(`${mode}_reference_unchecked`, !await page.locator('input[name=capability_ref][value=shared]').isChecked());
      await page.getByLabel('会话名称', { exact: true }).fill(`Claude MCP ${mode} 浏览器验收`);
      await choice.selectOption(mode);
      const created = page.waitForResponse(r => r.url().endsWith('/api/commands') && r.request().postDataJSON()?.kind === 'create');
      await page.getByRole('button', { name: '创建会话', exact: true }).click();
      const response = await created;
      const body = response.request().postDataJSON();
      const id = (await response.json()).session_id;
      sessions[mode] = id;
      check(`${mode}_explicit_selection_only`, body.capability_refs.length === 0 && body.capability_selections.length === 1 && body.capability_selections[0].mode === mode);
      await page.locator('.execution-snapshot summary').click();
      await page.getByText(`identity · ${mode === 'enabled' ? '已连接' : '已禁用'} · 与选择一致`, { exact: true }).waitFor();
      const before = await execution(id);
      check(`${mode}_automatic_reference`, before.snapshot.capability_refs[0].asset.id === 'shared');
      check(`${mode}_other_servers_inherit`, before.capability_evidence.some(e => e.server === 'inherited' && e.mode === 'inherit' && !e.verified));
      await page.getByRole('textbox', { name: '消息', exact: true }).fill('NETCAP_A_TURN_1');
      await page.getByRole('button', { name: '发送消息', exact: true }).click();
      if (mode === 'enabled') {
        await page.getByRole('button', { name: '允许本次', exact: true }).waitFor();
        check('single_call_options_only', await page.getByRole('button', { name: '允许此会话', exact: true }).count() === 0);
        await page.screenshot({ path: '.dev/w0206/browser-approval.png' });
        await page.getByRole('button', { name: '允许本次', exact: true }).click();
        await page.getByText('历史成功调用：whoami', { exact: true }).waitFor();
      }
      // A streamed draft can briefly coexist with the final SDK message until
      // the result arrives. Check the unique completed message after idle.
      await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
      await page.getByText('fixture A: NETCAP_A_TURN_1', { exact: true }).waitFor();
      check(`${mode}_turn_completed`, await page.getByRole('button', { name: '允许本次', exact: true }).count() === 0);
      if (mode === 'enabled') {
        await page.getByRole('textbox', { name: '消息', exact: true }).fill('NETCAP_A_TURN_2');
        await page.getByRole('button', { name: '发送消息', exact: true }).click();
        await page.getByRole('button', { name: '拒绝本次', exact: true }).waitFor();
        check('repeat_call_needs_fresh_approval', await page.getByRole('button', { name: '允许本次', exact: true }).isVisible());
        await page.getByRole('button', { name: '拒绝本次', exact: true }).click();
        await page.waitForFunction(() => document.querySelector('.badge')?.textContent === '空闲');
        await page.getByText('fixture A: NETCAP_A_TURN_2', { exact: true }).waitFor();
      }
      check(`${mode}_scope_limit_visible`, await page.getByText('Claude 支持 stdio 与 HTTP/SSE MCP 的启动选择、原 ID 恢复及新增、显式资源版本替换、启停与恢复继承。OAuth 登录命令可在下方准备，原生 CLI 负责授权回调、令牌保管与刷新。', { exact: true }).isVisible());
      await page.getByRole('button', { name: '核对当前来源', exact: true }).click();
      await page.getByText('已记录的来源与绑定未发现变化。', { exact: true }).waitFor();
      await page.reload();
      await page.locator('.execution-snapshot summary').click();
      const after = await execution(id);
      check(`${mode}_snapshot_survives_reload`, Boolean(before.snapshot.id) && JSON.stringify(before.snapshot) === JSON.stringify(after.snapshot));
    }
    for (const width of [1440, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      if (width === 768) await page.getByRole('button', { name: '详情', exact: true }).click();
      await page.getByText('identity · 已禁用 · 与选择一致', { exact: true }).scrollIntoViewIfNeeded();
      check(`layout_${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `.dev/w0206/browser-${width}.png` });
    }
    check('no_javascript_errors', errors.length === 0);
    return { checks, sessions, errors, expectations_met: Object.values(checks).every(Boolean) };
  } finally {
    page.off('pageerror', onError);
  }
}
