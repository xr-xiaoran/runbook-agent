'use strict';
// Persist IDs only; credentials remain in the current page.
const $ = id => document.getElementById(id);
const historyKey = 'runbook.workbench.ids.v1';
let current = null, busy = false, pendingAction = null, history = [];
try {
  const saved = JSON.parse(localStorage.getItem(historyKey) || '[]');
  if (Array.isArray(saved)) history = saved.filter(id => /^[a-f0-9]{32}$/.test(id)).slice(0, 8);
} catch { history = []; }
function message(text, error = false) {
  $('status').textContent = text;
  $('status').classList.toggle('error', error);
}
function syncButtons() {
  for (const id of ['run', 'load', 'clear']) $(id).disabled = busy;
  $('approve').disabled = busy || current?.status !== 'completed' || !current?.answer?.draft;
  $('replay').disabled = busy || current?.status !== 'completed' || !current?.replay_proposal;
  for (const b of $('history').querySelectorAll('button')) b.disabled = busy;
}
function setBusy(value) {
  busy = value;
  $('run').textContent = value ? '处理中，请稍候…' : '开始排查';
  syncButtons();
}
// Treat all model and server content as text, never HTML.
function node(tag, text, cls = '') {
  const el = document.createElement(tag);
  el.textContent = text;
  el.className = cls;
  return el;
}
function showHistory() {
  $('history').replaceChildren();
  if (!history.length) $('history').append(node('p', '暂无本地记录'));
  for (const id of history) {
    const b = node('button', id.slice(0, 8) + '…');
    b.title = id;
    b.addEventListener('click', () => loadRun(id));
    $('history').append(b);
  }
  syncButtons();
}
function remember(id) {
  history = [id, ...history.filter(v => v !== id)].slice(0, 8);
  try { localStorage.setItem(historyKey, JSON.stringify(history)); } catch { /* Storage may be disabled. */ }
  showHistory();
}
async function api(path, key, body) {
  if (!key.trim()) throw Error('请先填写对应的访问或审批密钥。');
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), 75000);
  try {
    const res = await fetch(path, {
      method: body === undefined ? 'GET' : 'POST',
      headers: {'Content-Type': 'application/json', Authorization: 'Bearer ' + key.trim()},
      body: body === undefined ? undefined : JSON.stringify(body), signal: ctl.signal
    });
    const value = await res.json();
    if (!res.ok) throw Error(`HTTP ${res.status}：${value.error || '请求失败'}`);
    return value;
  } catch (e) {
    if (e.name === 'AbortError') throw Error('等待超时，结果可能已提交。审批请对同一运行重试，不要盲目新建运行。');
    throw e;
  } finally { clearTimeout(timer); }
}
function selectTab(name) {
  for (const tab of document.querySelectorAll('[data-tab]')) {
    const selected = tab.dataset.tab === name;
    tab.setAttribute('aria-selected', String(selected));
    $('panel-' + tab.dataset.tab).hidden = !selected;
  }
}
function render(v) {
  current = v;
  $('empty').hidden = true;
  $('result').hidden = false;
  $('result-status').textContent = v.status === 'completed' ? '已完成' : '运行失败';
  $('run-meta').textContent = `ID ${v.id} · ${v.mode} · 上报 token ${v.reported_tokens ?? 0}`;
  $('lookup').value = v.id;
  $('operation').textContent = '';
  $('answer').textContent = v.answer?.summary || v.error || '没有答案';
  $('steps').replaceChildren(...(v.answer?.steps || []).map(s => node('li', s)));
  $('citations').replaceChildren(...(v.answer?.citations || []).map(id => node('span', id, 'cite')));
  $('draft').textContent = v.answer?.draft ? v.answer.draft.title + '\n' + v.answer.draft.body : '没有可审批的工单草稿。';
  const p = v.replay_proposal;
  $('proposal').textContent = p ? `抽奖 ${p.draw_id} · 预期版本 ${p.expected_version}\n有效期至 ${new Date(p.expires_at).toLocaleString()}\n${p.warning}\n过期后仍可查询原回执；未执行的新操作会被拒绝。` : '无重放提议。需要完成的运行且快照为 dead。';
  $('facts').replaceChildren();
  if (v.live) {
    for (const [label, data] of [['抽奖编号', v.live.draw_id], ['发奖状态', v.live.status], ['已尝试次数', v.live.attempt], ['任务版本', v.live.version], ['抽奖结果', v.live.outcome], ['故障注入', v.live.fail_first]]) {
      const fact = node('div', label, 'fact');
      fact.append(node('strong', String(data)));
      $('facts').append(fact);
    }
  }
  $('live-note').textContent = v.live ? `观察时间：${new Date(v.live.observed_at).toLocaleString()}。这是本次快照，不是持续监控。` : '本次仅查文档，没有实时快照。';
  $('trace').replaceChildren();
  for (const [i, t] of (v.trace || []).entries()) {
    const details = document.createElement('details');
    details.append(node('summary', `${i + 1}. ${t.kind} / ${t.name} · ${t.ms} ms`));
    details.append(node('pre', t.data || '模型调用元信息；不保存内部思维链。'));
    $('trace').append(details);
  }
  $('raw').textContent = JSON.stringify(v, null, 2);
  selectTab('answer');
  remember(v.id);
}
async function loadRun(id) {
  if (busy) return;
  if (!/^[a-f0-9]{32}$/.test(id)) return message('请输入完整的 32 位运行编号。', true);
  setBusy(true);
  message('正在读取记录…');
  try { render(await api('/api/runs/' + id, $('key').value)); message('已恢复记录，没有重新调用模型。'); }
  catch (e) { message(e.message, true); }
  finally { setBusy(false); }
}
$('run-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (busy) return;
  const question = $('question').value.trim();
  const bytes = new TextEncoder().encode(question).length;
  if (bytes < 2 || bytes > 4000) return message('问题长度必须为 2–4000 字节。', true);
  const input = {question};
  if ($('drawid').value) {
    const id = Number($('drawid').value);
    if (!Number.isSafeInteger(id) || id <= 0) return message('抽奖 ID 必须为有效正整数。', true);
    input.draw_id = id;
  }
  current = null;
  $('result').hidden = true;
  $('empty').hidden = false;
  $('result-status').textContent = '运行中';
  $('operation').textContent = '';
  setBusy(true);
  message('正在取证与检索，最长可能需要一分钟…');
  try {
    const v = await api('/api/runs', $('key').value, input);
    render(v);
    message(v.status === 'completed' ? '排查完成。请审阅依据，再决定是否操作。' : '排查失败：' + v.error, v.status !== 'completed');
  } catch (e) { $('result-status').textContent = '请求失败'; message(e.message, true); }
  finally { setBusy(false); }
});
$('load').addEventListener('click', () => loadRun($('lookup').value.trim()));
for (const b of document.querySelectorAll('[data-question]')) b.addEventListener('click', () => { $('question').value = b.dataset.question; });
for (const b of document.querySelectorAll('[data-tab]')) b.addEventListener('click', () => selectTab(b.dataset.tab));
function requestApproval(kind) {
  if (busy || current?.status !== 'completed') return;
  pendingAction = {kind, id: current.id};
  $('confirm-title').textContent = kind === 'ticket' ? '确认保存本地工单' : '确认重放失败任务';
  $('confirm-text').textContent = kind === 'ticket' ? `运行 ${current.id}：将已展示草稿保存为本地工单，不发送到外部平台。` : `运行 ${current.id}：抽奖 ${current.replay_proposal.draw_id} 将重新排队，不保证发奖成功；故障未排除会再次失败。`;
  $('confirm-check').checked = false;
  $('confirm-action').disabled = true;
  $('confirm-dialog').showModal();
}
$('approve').addEventListener('click', () => requestApproval('ticket'));
$('replay').addEventListener('click', () => requestApproval('replay'));
$('confirm-check').addEventListener('change', () => { $('confirm-action').disabled = !$('confirm-check').checked; });
$('cancel').addEventListener('click', () => { pendingAction = null; $('confirm-dialog').close(); });
$('confirm-dialog').addEventListener('cancel', () => { pendingAction = null; });
$('confirm-action').addEventListener('click', async () => {
  if (!$('confirm-check').checked || !pendingAction || busy) return;
  const action = pendingAction;
  pendingAction = null;
  $('confirm-dialog').close();
  setBusy(true);
  $('operation').textContent = '正在提交批准…';
  try {
    const suffix = action.kind === 'ticket' ? '/approve' : '/replay/approve';
    const v = await api('/api/runs/' + action.id + suffix, $('approval').value, {confirm: true});
    $('operation').textContent = (action.kind === 'ticket' ? '本地工单已保存。' : '重放已接受或原回执已取回；这不代表发奖成功。') + '\n' + JSON.stringify(v, null, 2);
  } catch (e) { $('operation').textContent = e.message + '\n如结果未知，请保留当前运行并重试相同操作。'; }
  finally { setBusy(false); }
});
async function health() {
  $('refresh').disabled = true;
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), 5000);
  try {
    const res = await fetch('/health', {signal: ctl.signal});
    if (!res.ok) throw Error('unavailable');
    const v = await res.json();
    $('health').textContent = '本地服务已连接';
    $('mode').textContent = v.mode === 'demo-scripted-no-llm' ? 'DEMO / 确定性演示，不调用真实模型、不产生模型费用。建议来自检索摘录，不代表已确认故障根因。' : `REMOTE / ${v.mode}：调用配置的模型服务，资料可能外发并计费。`;
  } catch { $('health').textContent = '连接失败'; $('mode').textContent = '未连接服务，请运行启动脚本，再检查连接。'; }
  finally { clearTimeout(timer); $('refresh').disabled = false; }
}
$('refresh').addEventListener('click', health);
$('clear').addEventListener('click', () => {
  $('key').value = ''; $('approval').value = ''; history = []; current = null;
  try { localStorage.removeItem(historyKey); } catch { /* Storage may be disabled. */ }
  $('result').hidden = true; $('empty').hidden = false;
  $('result-status').textContent = '等待运行'; $('operation').textContent = ''; $('lookup').value = '';
  showHistory(); message('页面凭证和本地 ID 已清除，服务端记录未删除。');
});
showHistory();
health();
