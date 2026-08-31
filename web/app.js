(() => {
  const $ = (selector) => document.querySelector(selector);
  const elements = {
    targetForm: $('#target-form'),
    baseURL: $('#base-url'),
    profile: $('#profile'),
    apiKey: $('#api-key'),
    preflightButton: $('#preflight-button'),
    preflightMessage: $('#preflight-message'),
    targetState: $('#target-state'),
    planStage: $('#plan-stage'),
    planState: $('#plan-state'),
    model: $('#model'),
    modelOptions: $('#model-options'),
    modelMessage: $('#model-message'),
    planButton: $('#plan-button'),
    planPreview: $('#plan-preview'),
    planMetrics: $('#plan-metrics'),
    planGroups: $('#plan-groups'),
    confirmPlan: $('#confirm-plan'),
    runButton: $('#run-button'),
    runStage: $('#run-stage'),
    runState: $('#run-state'),
    runCopy: $('#run-copy'),
    progressScenarios: $('#progress-scenarios'),
    progressAssertions: $('#progress-assertions'),
    progressTrack: $('#progress-track'),
    progressBar: $('#progress-bar'),
    resultsStage: $('#results-stage'),
    verdict: $('#verdict'),
    verdictTitle: $('#verdict-title'),
    verdictCopy: $('#verdict-copy'),
    runMeta: $('#run-meta'),
    summaryChips: $('#summary-chips'),
    download: $('#download-report'),
    resultTree: $('#result-tree'),
    evidenceEmpty: $('#evidence-empty'),
    evidenceContent: $('#evidence-content'),
    detailStatus: $('#detail-status'),
    detailHeading: $('#detail-heading'),
    detailSummary: $('#detail-summary'),
    assertionList: $('#assertion-list'),
    exchangeList: $('#exchange-list'),
    globalError: $('#global-error')
  };

  const profileRoutes = {
    openai: ['chat', 'responses'],
    claude: ['messages'],
    gateway: ['chat', 'responses', 'messages'],
    custom: ['chat', 'responses', 'messages']
  };
  const capabilityNames = {
    route: '路由发现', basic: '基础响应', context: '上下文兼容', stream: '流式协议', tools: '工具调用', errors: '错误契约'
  };
  const statusNames = {
    PASS: '通过', WARN: '建议项警告', FAIL: '失败', BLOCKED: '已阻断', ERROR: '执行错误',
    RUNNING: '执行中', PENDING: '等待中', SKIP: '已跳过'
  };
  const state = {preflight: false, plan: null, report: null, selectedCase: '', source: null};

  elements.profile.addEventListener('change', () => {
    applyProfileRoutes();
    invalidateTarget();
  });
  elements.baseURL.addEventListener('input', invalidateTarget);
  elements.apiKey.addEventListener('input', invalidateTarget);
  elements.model.addEventListener('input', invalidatePlan);
  document.querySelectorAll('input[name="level"], input[name="route"]').forEach((input) => input.addEventListener('change', invalidatePlan));
  elements.confirmPlan.addEventListener('change', () => {
    elements.runButton.disabled = !elements.confirmPlan.checked || !state.plan;
  });
  elements.targetForm.addEventListener('submit', preflight);
  elements.planButton.addEventListener('click', generatePlan);
  elements.runButton.addEventListener('click', startRun);

  applyProfileRoutes();

  async function preflight(event) {
    event.preventDefault();
    clearError();
    setButtonBusy(elements.preflightButton, true, '验证中…');
    elements.preflightMessage.textContent = '正在检查 /v1/models 与鉴权…';
    try {
      const result = await postJSON('/api/preflight', {
        base_url: elements.baseURL.value.trim(),
        api_key: elements.apiKey.value,
        profile: elements.profile.value
      });
      state.preflight = true;
      elements.baseURL.value = result.base_url;
      elements.targetState.textContent = '验证通过';
      elements.targetState.className = 'stage-state state-pass';
      renderModels(result.models || [], result.warning || '');
      elements.preflightMessage.textContent = result.warning || `连接成功，发现 ${(result.models || []).length} 个模型`;
      elements.planStage.hidden = false;
      setStep('plan');
      elements.planStage.scrollIntoView({behavior: 'smooth', block: 'start'});
    } catch (error) {
      state.preflight = false;
      elements.targetState.textContent = '验证失败';
      elements.targetState.className = 'stage-state state-fail';
      elements.preflightMessage.textContent = '未能验证目标服务';
      showError(error.message);
    } finally {
      setButtonBusy(elements.preflightButton, false, '验证并继续');
    }
  }

  function renderModels(models, warning) {
    elements.modelOptions.replaceChildren();
    for (const model of models) {
      const option = document.createElement('option');
      option.value = model.id;
      option.label = model.owned_by ? `${model.id} · ${model.owned_by}` : model.id;
      elements.modelOptions.append(option);
    }
    if (!elements.model.value && models.length) elements.model.value = models[0].id;
    elements.modelMessage.textContent = warning || (models.length ? `发现 ${models.length} 个候选模型，也可以手动填写` : '未发现候选模型，请手动填写 model id');
  }

  async function generatePlan() {
    clearError();
    if (!state.preflight) {
      showError('目标配置已经变化，请重新验证。');
      return;
    }
    if (!elements.model.value.trim()) {
      showError('请填写要测试的 model id。');
      elements.model.focus();
      return;
    }
    if (!selectedRoutes().length) {
      showError('至少选择一个测试路由。');
      return;
    }
    setButtonBusy(elements.planButton, true, '生成中…');
    try {
      state.plan = await postJSON('/api/run-plans', runPayload());
      renderPlan(state.plan);
      elements.planState.textContent = '已生成';
      elements.planState.className = 'stage-state state-pass';
      elements.planPreview.hidden = false;
      elements.confirmPlan.checked = false;
      elements.runButton.disabled = true;
      elements.planPreview.scrollIntoView({behavior: 'smooth', block: 'nearest'});
    } catch (error) {
      showError(error.message);
    } finally {
      setButtonBusy(elements.planButton, false, '重新生成计划');
    }
  }

  function renderPlan(plan) {
    const metrics = [
      ['测试场景', plan.scenario_count],
      ['原子断言', plan.assertion_count],
      ['模型调用', plan.model_calls],
      ['最大输出预算', `${plan.max_output_tokens} tokens`]
    ];
    elements.planMetrics.replaceChildren(...metrics.map(([name, value]) => metric(name, value)));
    elements.planGroups.replaceChildren();
    for (const routeID of plan.routes) {
      const cases = plan.cases.filter((item) => item.route_id === routeID);
      if (!cases.length) continue;
      const group = document.createElement('details');
      group.className = 'plan-group';
      group.open = elements.planGroups.childElementCount === 0;
      const summary = document.createElement('summary');
      const routeName = cases[0].route_name;
      summary.append(textElement('strong', routeName), textElement('span', `${cases.length} 场景 · ${sum(cases, 'assertions')} 断言`));
      const list = document.createElement('div');
      list.className = 'plan-case-list';
      for (const item of cases) {
        const row = document.createElement('div');
        row.className = 'plan-case';
        const copy = document.createElement('div');
        copy.append(textElement('strong', item.name), textElement('p', item.description));
        const tags = document.createElement('div');
        tags.className = 'case-tags';
        tags.append(tag(capabilityNames[item.capability] || item.capability), tag(`${item.assertions.length} 断言`));
        if (item.model_calls) tags.append(tag('模型调用'));
        if (item.depends_on?.length) tags.append(tag('有前置依赖'));
        row.append(copy, tags);
        list.append(row);
      }
      group.append(summary, list);
      elements.planGroups.append(group);
    }
  }

  async function startRun() {
    if (!state.plan || !elements.confirmPlan.checked) return;
    clearError();
    elements.runButton.disabled = true;
    elements.runButton.textContent = '启动中…';
    elements.runStage.hidden = false;
    elements.resultsStage.hidden = false;
    setStep('run');
    try {
      const report = await postJSON('/api/runs', runPayload());
      elements.apiKey.value = '';
      state.selectedCase = '';
      renderReport(report);
      watchRun(report.id);
      elements.runStage.scrollIntoView({behavior: 'smooth', block: 'start'});
    } catch (error) {
      elements.runButton.disabled = false;
      elements.runButton.textContent = '开始执行';
      showError(error.message);
    }
  }

  function watchRun(id) {
    state.source?.close();
    const source = new EventSource(`/api/runs/${encodeURIComponent(id)}/events`);
    state.source = source;
    source.addEventListener('report', (event) => {
      const report = JSON.parse(event.data);
      renderReport(report);
      if (report.state === 'COMPLETE') finishWatching();
    });
    source.onerror = async () => {
      source.close();
      if (state.source === source) state.source = null;
      try {
        const response = await fetch(`/api/runs/${encodeURIComponent(id)}`);
        if (!response.ok) throw new Error();
        const report = await response.json();
        renderReport(report);
        if (report.state !== 'COMPLETE') throw new Error();
      } catch (_) {
        showError('实时进度连接已断开，请重新执行测试。');
      }
    };
  }

  function finishWatching() {
    state.source?.close();
    state.source = null;
    elements.runButton.disabled = false;
    elements.runButton.textContent = '重新执行';
    elements.runState.textContent = 'COMPLETE';
    elements.runState.className = 'stage-state state-pass';
    setStep('result');
  }

  function renderReport(report) {
    state.report = report;
    renderProgress(report);
    renderVerdict(report);
    renderResultTree(report);
    if (state.selectedCase) restoreSelectedCase();
    if (report.state === 'COMPLETE' && !state.selectedCase) selectSuggestedCase(report);
  }

  function renderProgress(report) {
    const progress = report.progress || {};
    const total = progress.total || 0;
    const current = progress.current || 0;
    const percent = total ? Math.round(current / total * 100) : 0;
    elements.progressScenarios.textContent = `${current} / ${total} 场景`;
    elements.progressAssertions.textContent = `${progress.assertions_current || 0} / ${progress.assertions_total || 0} 断言`;
    elements.runCopy.textContent = progress.label || '准备执行测试计划';
    elements.progressBar.style.width = `${percent}%`;
    elements.progressTrack.setAttribute('aria-valuenow', String(percent));
  }

  function renderVerdict(report) {
    const counts = report.summary || {};
    const complete = report.state === 'COMPLETE';
    let title = '检查中';
    let tone = 'running';
    let copy = '测试结果会随着场景完成实时更新。';
    if (complete && (counts.error || counts.fail)) {
      title = counts.error ? '检查异常' : '部分兼容';
      tone = 'fail';
      copy = '至少一个必要场景未通过。选择失败项查看精确断言与原始证据。';
    } else if (complete && counts.warn) {
      title = '基本兼容';
      tone = 'warn';
      copy = '必要能力已通过，但存在建议项差异，可能影响可观测性或严格客户端。';
    } else if (complete) {
      title = '兼容';
      tone = 'pass';
      copy = '本次计划内的必要断言均已通过。此结论只覆盖已选择的路由、模型和测试深度。';
    }
    elements.verdict.dataset.tone = tone;
    elements.verdictTitle.textContent = title;
    elements.verdictCopy.textContent = copy;
    elements.runMeta.textContent = `${report.model || ''} · ${elapsedText(report.started_at, report.finished_at)} · 目录 ${report.catalog_version || '-'}`;
    renderSummaryChips(counts);
    elements.download.hidden = !complete;
    if (complete) elements.download.href = `/api/runs/${encodeURIComponent(report.id)}/report`;
  }

  function renderSummaryChips(counts) {
    elements.summaryChips.replaceChildren();
    for (const status of ['pass', 'warn', 'fail', 'blocked', 'error']) {
      const count = counts[status] || 0;
      if (!count && status !== 'pass') continue;
      const chip = textElement('span', `${status.toUpperCase()} ${count}`);
      chip.className = `summary-chip status-${status}`;
      elements.summaryChips.append(chip);
    }
  }

  function renderResultTree(report) {
    elements.resultTree.replaceChildren();
    for (const route of report.routes || []) {
      const card = document.createElement('section');
      card.className = 'route-result';
      const heading = document.createElement('div');
      heading.className = 'route-result-heading';
      const title = document.createElement('div');
      title.append(textElement('strong', route.name), textElement('code', route.path));
      heading.append(title, statusPill(route.status));
      card.append(heading);

      const capabilities = [...new Set((route.cases || []).map((item) => item.capability))];
      for (const capability of capabilities) {
        const group = document.createElement('div');
        group.className = 'capability-group';
        group.append(textElement('p', capabilityNames[capability] || capability));
        for (const item of route.cases.filter((candidate) => candidate.capability === capability)) {
          const button = document.createElement('button');
          button.type = 'button';
          button.className = 'case-result';
          button.dataset.caseId = item.id;
          button.classList.toggle('is-selected', item.id === state.selectedCase);
          const label = document.createElement('span');
          label.append(textElement('strong', item.name), textElement('small', assertionCountText(item.assertions || [])));
          button.append(statusDot(item.status), label, textElement('span', '›'));
          button.addEventListener('click', () => selectCase(route, item));
          group.append(button);
        }
        card.append(group);
      }
      elements.resultTree.append(card);
    }
  }

  function selectCase(route, item) {
    state.selectedCase = item.id;
    elements.resultTree.querySelectorAll('.case-result').forEach((button) => button.classList.toggle('is-selected', button.dataset.caseId === item.id));
    elements.evidenceEmpty.hidden = true;
    elements.evidenceContent.hidden = false;
    elements.detailStatus.replaceWith(statusPill(item.status, 'detail-status'));
    elements.detailStatus = $('#detail-status');
    elements.detailHeading.textContent = `${route.name} · ${item.name}`;
    elements.detailSummary.textContent = item.summary || (item.reason_code ? `原因：${item.reason_code}` : '逐项断言结果如下。');
    renderAssertions(item.assertions || []);
    renderExchanges(item.evidence || []);
  }

  function renderAssertions(assertions) {
    elements.assertionList.replaceChildren();
    for (const assertion of assertions) {
      const row = document.createElement('details');
      row.className = 'assertion';
      if (['FAIL', 'WARN', 'ERROR'].includes(assertion.status)) row.open = true;
      const summary = document.createElement('summary');
      summary.append(statusDot(assertion.status), textElement('strong', assertion.name), textElement('small', assertion.severity === 'advisory' ? '建议项' : '必要项'));
      const body = document.createElement('div');
      body.className = 'assertion-body';
      if (assertion.reason_code) body.append(evidenceRow('原因代码', assertion.reason_code));
      if (assertion.expected) body.append(evidenceRow('预期', assertion.expected));
      if (assertion.observed) body.append(evidenceRow('实际', assertion.observed));
      if (!body.childNodes.length) body.append(textElement('p', '该断言尚未产生证据。'));
      row.append(summary, body);
      elements.assertionList.append(row);
    }
  }

  function renderExchanges(exchanges) {
    elements.exchangeList.replaceChildren();
    for (const exchange of exchanges) {
      const group = document.createElement('section');
      group.className = 'exchange';
      const title = document.createElement('div');
      title.className = 'exchange-heading';
      title.append(textElement('strong', exchange.label || 'HTTP 证据'), textElement('span', exchange.http_status ? `HTTP ${exchange.http_status} · ${exchange.duration_ms} ms` : `${exchange.duration_ms || 0} ms`));
      group.append(title);
      if (exchange.request) group.append(codeDetails('请求', exchange.request));
      if (exchange.response) group.append(codeDetails('响应', exchange.response));
      elements.exchangeList.append(group);
    }
  }

  function restoreSelectedCase() {
    for (const route of state.report.routes || []) {
      const item = route.cases?.find((candidate) => candidate.id === state.selectedCase);
      if (item) return selectCase(route, item);
    }
    state.selectedCase = '';
  }

  function selectSuggestedCase(report) {
    for (const wanted of ['FAIL', 'ERROR', 'WARN', 'PASS', 'BLOCKED']) {
      for (const route of report.routes || []) {
        const item = route.cases?.find((candidate) => candidate.status === wanted);
        if (item) return selectCase(route, item);
      }
    }
  }

  function invalidateTarget() {
    state.preflight = false;
    elements.targetState.textContent = '需重新验证';
    elements.targetState.className = 'stage-state';
    elements.preflightMessage.textContent = '目标配置已变化';
    elements.planStage.hidden = true;
    invalidatePlan();
    setStep('target');
  }

  function invalidatePlan() {
    state.plan = null;
    elements.planPreview.hidden = true;
    elements.confirmPlan.checked = false;
    elements.runButton.disabled = true;
    elements.planState.textContent = '待生成';
    elements.planState.className = 'stage-state';
  }

  function applyProfileRoutes() {
    const selected = new Set(profileRoutes[elements.profile.value] || []);
    document.querySelectorAll('input[name="route"]').forEach((input) => { input.checked = selected.has(input.value); });
  }

  function selectedRoutes() {
    return [...document.querySelectorAll('input[name="route"]:checked')].map((input) => input.value);
  }

  function runPayload() {
    return {
      base_url: elements.baseURL.value.trim(),
      api_key: elements.apiKey.value,
      model: elements.model.value.trim(),
      profile: elements.profile.value,
      level: document.querySelector('input[name="level"]:checked')?.value || 'standard',
      routes: selectedRoutes()
    };
  }

  async function postJSON(url, body) {
    const response = await fetch(url, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
    let payload;
    try { payload = await response.json(); } catch (_) { throw new Error(`服务返回了无法解析的响应（HTTP ${response.status}）。`); }
    if (!response.ok) throw new Error(payload?.error?.message || `请求失败（HTTP ${response.status}）。`);
    return payload;
  }

  function metric(name, value) {
    const item = document.createElement('div');
    item.append(textElement('strong', String(value)), textElement('span', name));
    return item;
  }

  function tag(value) {
    const item = textElement('span', value);
    item.className = 'tag';
    return item;
  }

  function statusPill(status, id) {
    const item = textElement('span', statusNames[status] || status);
    item.className = `status-pill status-${String(status || 'PENDING').toLowerCase()}`;
    if (id) item.id = id;
    return item;
  }

  function statusDot(status) {
    const item = document.createElement('i');
    item.className = `status-dot status-${String(status || 'PENDING').toLowerCase()}`;
    item.setAttribute('aria-label', statusNames[status] || status);
    return item;
  }

  function assertionCountText(assertions) {
    const done = assertions.filter((item) => !['PENDING', 'RUNNING'].includes(item.status)).length;
    const failed = assertions.filter((item) => ['FAIL', 'ERROR'].includes(item.status)).length;
    if (failed) return `${failed} 项失败 · ${assertions.length} 项断言`;
    return `${done} / ${assertions.length} 项断言`;
  }

  function evidenceRow(label, value) {
    const row = document.createElement('div');
    row.append(textElement('span', label), textElement('code', value));
    return row;
  }

  function codeDetails(label, value) {
    const details = document.createElement('details');
    const summary = textElement('summary', `查看${label}`);
    const pre = document.createElement('pre');
    pre.append(textElement('code', value));
    details.append(summary, pre);
    return details;
  }

  function sum(cases, field) {
    if (field === 'assertions') return cases.reduce((total, item) => total + item.assertions.length, 0);
    return cases.reduce((total, item) => total + Number(item[field] || 0), 0);
  }

  function textElement(tagName, value) {
    const item = document.createElement(tagName);
    item.textContent = value ?? '';
    return item;
  }

  function setButtonBusy(button, busy, label) {
    button.disabled = busy;
    button.textContent = label;
  }

  function setStep(step) {
    const order = ['target', 'plan', 'run', 'result'];
    const current = order.indexOf(step);
    document.querySelectorAll('.steps li').forEach((item, index) => {
      item.classList.toggle('is-current', index === current);
      item.classList.toggle('is-done', index < current);
    });
  }

  function showError(message) {
    elements.globalError.textContent = message;
    elements.globalError.hidden = false;
    elements.globalError.scrollIntoView({behavior: 'smooth', block: 'center'});
  }

  function clearError() { elements.globalError.hidden = true; }

  function elapsedText(start, end) {
    if (!start) return '0 ms';
    const ms = Math.max(0, new Date(end || Date.now()).getTime() - new Date(start).getTime());
    return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
  }
})();
