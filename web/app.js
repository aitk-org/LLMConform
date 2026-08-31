(() => {
  const form = document.querySelector('#run-form');
  const runButton = document.querySelector('#run-button');
  const loadModelsButton = document.querySelector('#load-models');
  const modelInput = document.querySelector('#model');
  const modelToggle = document.querySelector('#model-toggle');
  const modelMenu = document.querySelector('#model-menu');
  const modelControl = document.querySelector('.model-control');
  const modelStatus = document.querySelector('#model-status');
  const apiKey = document.querySelector('#api-key');
  const errorBox = document.querySelector('#form-error');
  const emptyState = document.querySelector('#empty-state');
  const results = document.querySelector('#results');
  const rows = document.querySelector('#result-rows');
  const runMeta = document.querySelector('#run-meta');
  const download = document.querySelector('#download-report');
  const progressWrap = document.querySelector('#progress-wrap');
  const progressCopy = document.querySelector('#progress-copy');
  const progressValue = document.querySelector('#progress-value');
  const progressTrack = document.querySelector('#progress-track');
  const progressBar = document.querySelector('#progress-bar');
  const detail = document.querySelector('#detail');
  const detailStatus = document.querySelector('#detail-status');
  const detailHeading = document.querySelector('#detail-heading');
  const detailCopy = document.querySelector('#detail-copy');
  const detailExpected = document.querySelector('#detail-expected');
  const detailRequest = document.querySelector('#detail-request');
  const detailResponse = document.querySelector('#detail-response');

  let source = null;
  let selectedCheck = null;
  let latestReport = null;
  let loadedModels = [];
  let highlightedModel = -1;

  const routeOrder = ['chat', 'responses', 'messages'];
  const checkOrder = ['basic', 'stream', 'tools', 'usage', 'errors'];

  modelToggle.addEventListener('click', () => {
    if (modelMenu.hidden) {
      renderModelMenu('');
      setModelMenuOpen(true);
      modelInput.focus();
    } else {
      setModelMenuOpen(false);
    }
  });

  modelInput.addEventListener('focus', () => {
    if (loadedModels.length && modelMenu.hidden) {
      renderModelMenu('');
      setModelMenuOpen(true);
    }
  });

  modelInput.addEventListener('input', () => {
    if (!loadedModels.length) return;
    renderModelMenu(modelInput.value);
    setModelMenuOpen(true);
  });

  modelInput.addEventListener('keydown', (event) => {
    if (!loadedModels.length) return;
    const options = [...modelMenu.querySelectorAll('[role="option"]')];
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      if (modelMenu.hidden) {
        renderModelMenu('');
        setModelMenuOpen(true);
      }
      highlightedModel = Math.min(highlightedModel + 1, options.length - 1);
      updateHighlightedModel(options);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      highlightedModel = Math.max(highlightedModel - 1, 0);
      updateHighlightedModel(options);
    } else if (event.key === 'Enter' && highlightedModel >= 0 && options[highlightedModel]) {
      event.preventDefault();
      selectModel(options[highlightedModel].dataset.modelId);
    } else if (event.key === 'Escape') {
      setModelMenuOpen(false);
    }
  });

  document.addEventListener('click', (event) => {
    if (!modelControl.contains(event.target)) setModelMenuOpen(false);
  });

  loadModelsButton.addEventListener('click', async () => {
    errorBox.hidden = true;
    const baseURL = document.querySelector('#base-url').value.trim();
    if (!baseURL) {
      showError('请先填写服务地址。');
      document.querySelector('#base-url').focus();
      return;
    }
    loadModelsButton.disabled = true;
    loadModelsButton.textContent = '获取中…';
    modelStatus.textContent = '正在请求 /v1/models';
    modelStatus.classList.remove('is-error');
    try {
      const response = await fetch('/api/models', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({base_url: baseURL, api_key: apiKey.value})
      });
      const payload = await response.json();
      if (!response.ok) {
        throw new Error(payload?.error?.message || '无法获取模型列表。');
      }
      loadedModels = payload.models || [];
      renderModelMenu('');
      if (!modelInput.value && payload.models?.length) {
        modelInput.value = payload.models[0].id;
      }
      modelStatus.textContent = `已获取 ${payload.models?.length || 0} 个模型，可从候选中选择或继续手填`;
    } catch (error) {
      modelStatus.textContent = error instanceof Error ? error.message : '无法获取模型列表。';
      modelStatus.classList.add('is-error');
    } finally {
      loadModelsButton.disabled = false;
      loadModelsButton.textContent = '获取模型';
    }
  });

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    errorBox.hidden = true;
    const routes = [...form.querySelectorAll('input[name="route"]:checked')].map((input) => input.value);
    if (routes.length === 0) {
      showError('至少选择一个路由。');
      return;
    }
    setRunning(true);
    selectedCheck = null;
    detail.hidden = true;

    try {
      const response = await fetch('/api/runs', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
          base_url: document.querySelector('#base-url').value,
          api_key: apiKey.value,
          model: modelInput.value,
          routes
        })
      });
      const payload = await response.json();
      if (!response.ok) {
        throw new Error(payload?.error?.message || '无法开始检查。');
      }
      apiKey.value = '';
      emptyState.hidden = true;
      results.hidden = false;
      progressWrap.hidden = false;
      download.href = `/api/runs/${encodeURIComponent(payload.id)}/report`;
      render(payload);
      watch(payload.id);
    } catch (error) {
      setRunning(false);
      showError(error instanceof Error ? error.message : '无法开始检查。');
    }
  });

  function watch(id) {
    if (source) source.close();
    source = new EventSource(`/api/runs/${encodeURIComponent(id)}/events`);
    source.addEventListener('report', (event) => {
      const report = JSON.parse(event.data);
      render(report);
      if (report.state === 'COMPLETE') {
        source.close();
        source = null;
        setRunning(false);
      }
    });
    source.onerror = async () => {
      if (source) {
        source.close();
        source = null;
      }
      try {
        const response = await fetch(`/api/runs/${encodeURIComponent(id)}`);
        if (response.ok) {
          const report = await response.json();
          render(report);
          if (report.state === 'COMPLETE') setRunning(false);
          return;
        }
      } catch (_) {
        // The message below is enough for the local UI.
      }
      setRunning(false);
      showError('实时连接已断开，请重新运行检查。');
    };
  }

  function render(report) {
    latestReport = report;
    const total = report.progress?.total || 0;
    const current = report.progress?.current || 0;
    const percent = total === 0 ? 0 : Math.round((current / total) * 100);
    progressCopy.textContent = report.progress?.label || '准备检查';
    progressValue.textContent = `${current} / ${total}`;
    progressBar.style.width = `${percent}%`;
    progressTrack.setAttribute('aria-valuenow', String(percent));

    const summary = report.summary || {pass: 0, warn: 0, fail: 0, skip: 0};
    const elapsed = elapsedText(report.started_at, report.finished_at);
    runMeta.textContent = `${summary.pass} 通过 · ${summary.warn} 警告 · ${summary.fail} 失败 · ${elapsed}`;

    const routeMap = new Map((report.routes || []).map((route) => [route.id, route]));
    rows.replaceChildren();
    for (const routeID of routeOrder) {
      const route = routeMap.get(routeID);
      if (!route) continue;
      const row = document.createElement('tr');
      const routeCell = document.createElement('td');
      const routeName = document.createElement('span');
      routeName.className = 'route-name';
      const dot = document.createElement('span');
      dot.className = 'protocol-dot';
      const label = document.createElement('span');
      label.textContent = route.name;
      const path = document.createElement('span');
      path.className = 'route-path';
      path.textContent = route.path;
      label.append(path);
      routeName.append(dot, label);
      routeCell.append(routeName);
      row.append(routeCell);

      const checkMap = new Map(route.checks.map((check) => [check.id, check]));
      for (const checkID of checkOrder) {
        const check = checkMap.get(checkID);
        const cell = document.createElement('td');
        const button = document.createElement('button');
        button.type = 'button';
        button.className = `status status-${(check?.status || 'PENDING').toLowerCase()}`;
        button.textContent = check?.status || 'PENDING';
        button.disabled = !check || ['PENDING', 'RUNNING'].includes(check.status);
        button.setAttribute('aria-label', `${route.name} ${check?.name || checkID}: ${button.textContent}`);
        const key = `${route.id}:${checkID}`;
        if (selectedCheck === key) button.classList.add('is-selected');
        button.addEventListener('click', () => selectResult(route, check));
        cell.append(button);
        row.append(cell);
      }
      rows.append(row);
    }

    if (selectedCheck) {
      restoreSelection();
    } else if (report.state === 'COMPLETE') {
      const firstFailure = findFirstResult(['FAIL', 'WARN']);
      if (firstFailure) selectResult(firstFailure.route, firstFailure.check);
    }
  }

  function selectResult(route, check) {
    if (!check || ['PENDING', 'RUNNING'].includes(check.status)) return;
    selectedCheck = `${route.id}:${check.id}`;
    detail.hidden = false;
    detailStatus.className = `status status-${check.status.toLowerCase()}`;
    detailStatus.textContent = check.status;
    detailHeading.textContent = `${route.name} · ${check.name}`;
    detailCopy.textContent = check.summary || '没有附加说明。';
    detailExpected.textContent = check.expected || '';
    detailRequest.textContent = check.request || '未记录请求内容。';
    const statusLine = check.http_status ? `HTTP ${check.http_status} · ${check.duration_ms} ms\n\n` : '';
    detailResponse.textContent = statusLine + (check.response || '未收到响应内容。');
    document.querySelectorAll('.status.is-selected').forEach((item) => item.classList.remove('is-selected'));
    const buttons = [...rows.querySelectorAll('button.status')];
    const match = buttons.find((button) => button.getAttribute('aria-label') === `${route.name} ${check.name}: ${check.status}`);
    if (match) match.classList.add('is-selected');
  }

  function restoreSelection() {
    const [routeID, checkID] = selectedCheck.split(':');
    const route = latestReport?.routes?.find((item) => item.id === routeID);
    const check = route?.checks?.find((item) => item.id === checkID);
    if (route && check) selectResult(route, check);
  }

  function findFirstResult(states) {
    for (const route of latestReport?.routes || []) {
      for (const check of route.checks || []) {
        if (states.includes(check.status)) return {route, check};
      }
    }
    return null;
  }

  function setRunning(running) {
    runButton.disabled = running;
    loadModelsButton.disabled = running;
    runButton.textContent = running ? '检查中…' : (latestReport ? '重新检查' : '开始检查');
  }

  function showError(message) {
    errorBox.textContent = message;
    errorBox.hidden = false;
  }

  function elapsedText(start, end) {
    if (!start) return '0s';
    const milliseconds = Math.max(0, new Date(end || Date.now()).getTime() - new Date(start).getTime());
    if (milliseconds < 1000) return `${milliseconds}ms`;
    return `${(milliseconds / 1000).toFixed(1)}s`;
  }
})();
