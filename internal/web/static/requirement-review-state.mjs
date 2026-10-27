// Only the current revision in the review stage can require product attention.
export function reviewAttention(task, summary) {
  if (task?.status === 'aligning') return {label:'待对齐',detail:'回复开发问题',attention:true};
  if (task?.status !== 'clarify') return null;
  if (!summary || summary.task_revision !== task.revision) return {label:'后台评审中', attention:false};
  if (summary.status === 'rejection_pending') return {label:'驳回待确认',detail:'查看依据并确认关闭或补充',attention:true};
  if (summary.status === 'ready_to_confirm') return {label:'待确认', detail:'确认需求稿', attention:true};
  if (summary.status === 'needs_input') return {label:'待确认', detail:'查看问题并补充', attention:true};
  if (summary.status === 'failed') return {label:'评审失败', detail:'内容已保存，可重试', attention:true};
  return {label:'后台评审中', attention:false};
}

// A question ID can recur in later rounds. Only reply_to identifies its answer sheet.
export function questionReplies(review, messageID) {
  return (review?.messages || []).filter(message => message.role === 'user' && message.reply_to === messageID && message.action !== 'confirm');
}

export function questionIsActive(review, message) {
  return !review?.needs_recheck && !message.form_expired_at && !message.form_expired_reason && message.generation === review?.generation &&
    ['needs_input', 'ready_to_confirm'].includes(review?.status) &&
    (review?.source?.status === 'clarify' || (review?.mode === 'development_alignment' && review?.source?.status === 'aligning')) && !questionReplies(review, message.id).length;
}

export function questionCardStatus(review, message) {
  if (questionIsActive(review,message)) return 'active';
  return questionReplies(review,message.id).some(reply => hasAnswerDraft({sheet:reply.answers}) || hasQuestionNotes(reply.question_notes)) ? 'submitted' : 'expired';
}

export function answerRows(questions, answers = {}, notes = {}) {
  const rows = (questions || []).map(question => ({
    id: question.id, label: question.label,
    values: Array.isArray(answers[question.id]) ? answers[question.id] : [],
  }));
  // Preserve legacy answer keys even if the original question definition is missing.
  for (const [id, values] of Object.entries(answers || {})) {
    if (!rows.some(row => row.id === id)) rows.push({id, label: id, values: Array.isArray(values) ? values : []});
  }
  for (const [id,note] of Object.entries(notes || {})) {
    let row = rows.find(row=>row.id===id);
    if (!row) { row={id,label:id,values:[]}; rows.push(row); }
    row.note=note;
  }
  return rows;
}

export function hasAnswerDraft(answers = {}) {
  return Object.values(answers).some(sheet => Object.values(sheet || {}).some(values =>
    Array.isArray(values) && values.some(value => String(value).trim())));
}

export function hasQuestionNotes(notes = {}) {
  return Object.values(notes || {}).some(note=>note?.body?.trim() || (note?.files || note?.attachments || note?.attachment_ids || []).length);
}

export function sameQuestionNotes(left = {}, right = {}) {
  const stable = notes => JSON.stringify(Object.keys(notes || {}).sort().filter(key=>hasQuestionNotes({[key]:notes[key]})).map(key=>[key,notes[key].body || '',(notes[key].files || notes[key].attachments || notes[key].attachment_ids || []).map(file=>typeof file==='string'?file:file.id)]));
  return stable(left)===stable(right);
}

export function hasReviewDraft({body = '', files = [], answers = {}, questionNotes = {}, pending = null}) {
  return Boolean(body.trim() || files.length || pending || hasAnswerDraft(answers) || Object.values(questionNotes).some(hasQuestionNotes));
}

export function sameAnswerSheet(left = {}, right = {}) {
  const stable = sheet => JSON.stringify(Object.keys(sheet).sort().map(key => [key, sheet[key]]));
  return stable(left) === stable(right);
}
// A direction question is answered by any later user message; a form by its own reply.
export function askIsOpen(review, message) {
  const result = message?.result;
  if (message?.role !== 'assistant' || !result) return false;
  if (result.questions?.length) return questionIsActive(review, message);
  if (result.ask?.kind !== 'direction') return false;
  const messages = review?.messages || [], index = messages.findIndex(m => m.id === message.id);
  return !review?.needs_recheck && message.generation === review?.generation && review?.status === 'needs_input' &&
    review?.source?.status === 'clarify' && index >= 0 && !messages.slice(index + 1).some(m => m.role === 'user');
}

export function currentTopic(review) {
  const open = (review?.messages || []).findLast(m => m.result?.ask?.topic_id && askIsOpen(review, m));
  return open?.result.ask.topic_id || (review?.agenda || []).find(t => t.status === 'open')?.id || '';
}

// Proposals are numbered in order. A later proposal supersedes an earlier one;
// confirmation (explicit, or automatic for confirmed Bugs) is never lost.
export function proposalStates(review) {
  const messages = review?.messages || [], out = new Map(), confirmed = new Set();
  let latest = null;
  for (const m of messages) {
    if (m.role === 'assistant' && m.result?.proposal) latest = m;
    if (m.role === 'user' && m.action === 'confirm' && latest) confirmed.add(latest.id);
  }
  const list = messages.filter(m => m.role === 'assistant' && m.result?.proposal);
  // An edit recorded after the review's revision makes the latest proposal stale until it is reviewed again.
  const edited = messages.some(m => m.source?.revision > review.task_revision);
  list.forEach((m, i) => {
    const last = i === list.length - 1, current = last && m.generation === review.generation && !edited;
    let state = last ? 'draft' : 'superseded';
    if (confirmed.has(m.id) || (current && review.status === 'confirmed')) state = 'confirmed';
    else if (current && review.status === 'ready_to_confirm' && review.source?.status === 'clarify') state = 'current';
    out.set(m.id, {number: i + 1, state, supersededBy: last ? 0 : i + 2});
  });
  return out;
}

const endActions = ['task_closed', 'task_declined', 'task_canceled'];

// The conversation as a group chat: people and agents speak, everything else
// (creation, edits, decisions, failures) is a one-line event with its author.
export function chatTimeline(review, {viewerID = ''} = {}) {
  const items = [], messages = review?.messages || [], source = review?.source || {};
  // The first saved snapshot is the original document; it belongs to the creation line.
  const original = messages[0]?.role === 'system' && !messages[0].action && messages[0].source ? messages[0] : null;
  if (source.created_at) items.push({key: 'created', type: 'event', action: 'task_created', at: source.created_at,
    text: `${review.creator?.name ? review.creator.name + ' ' : ''}创建了需求`,
    button: original ? {action: 'snapshot', label: '查看原文', message: original.id} : {action: 'source', label: '查看原文'}});
  const lastFailure = messages.findLastIndex(m => m.action === 'review_failed');
  const lastUser = messages.findLastIndex(m => m.role === 'user');
  const lastEnd = messages.findLastIndex(m => endActions.includes(m.action));
  messages.forEach((m, index) => {
    if (m === original && source.created_at) return;
    if (m.role === 'system') {
      const item = {key: m.id, type: 'event', action: m.action || 'source_saved', message: m, at: m.at, text: m.body};
      if (m.action === 'task_edited') item.button = {action: 'diff', label: '查看变更', message: m.id};
      else if (m.action === 'review_failed') {
        Object.assign(item, {tone: 'warn', note: '需求和已发送消息均已保存'});
        if (index === lastFailure && review.status === 'failed') item.button = {action: 'retry', label: '重试评审'};
      } else if (endActions.includes(m.action)) {
        if (index === lastEnd && taskTerminated(source)) item.button = {action: 'reopen', label: '重新打开'};
      } else if (m.source) {
        if (!m.action) item.text = `需求内容已保存（R${m.source.revision}）`;
        item.button = {action: 'snapshot', label: '查看这一版', message: m.id};
      }
      items.push(item);
      return;
    }
    if (m.role === 'user' && (m.action === 'confirm' || m.action === 'confirm_rejection')) {
      items.push({key: m.id, type: 'event', action: m.action, message: m, at: m.at,
        text: m.action === 'confirm' ? `${m.actor || '产品'} 确认了需求稿与验收条件，需求进入待认领` : `${m.actor || '产品'} 同意驳回，Bug 已关闭`});
      return;
    }
    const agent = m.role === 'assistant';
    const sender = !agent ? {kind: m.actor_kind || 'human', id: m.actor_id || 'name:' + (m.actor || '产品'), name: m.actor || '产品'}
      : m.action === 'request_alignment' ? {kind: 'dev_agent', id: m.actor_id || 'dev:' + (m.actor || ''), name: m.actor || '开发代理'}
      : {kind: 'platform', id: 'platform', name: '评审 Agent'};
    items.push({key: m.id, type: agent ? 'agent' : 'member', message: m, at: m.at, sender,
      mine: !agent && Boolean(m.actor_id) && m.actor_id === viewerID, delegated: Boolean(m.authorization)});
  });
  // Older records did not store failures or closures as messages.
  if (review?.status === 'failed' && (lastFailure < 0 || lastFailure < lastUser)) items.push({key: `failure:${review.version}`, type: 'event', tone: 'warn',
    text: `评审没有完成：${review.error || '请重试'}`, note: '需求和已发送消息均已保存', button: {action: 'retry', label: '重试评审'}});
  if (review?.status === 'closed' && lastEnd < 0) items.push({key: 'closed', type: 'event', text: '需求已结束', button: {action: 'details', label: '查看需求'}});
  if (review?.needs_recheck) items.push({key: 'recheck', type: 'event', tone: 'warn', text: review.recheck_reason || '相关需求有变化，确认前需要重新核对',
    button: {action: 'retry', label: '重新核对'}});
  if (review?.mode === 'development_alignment' && review.status === 'alignment_replied') items.push({key: 'alignment-replied', type: 'event',
    text: '回复已提交，等待开发确认', note: '原开发客户端确认后继续开发；仍可继续补充文字。'});
  if (['queued', 'running'].includes(review?.status)) items.push({key: 'typing', type: 'typing',
    text: review.status === 'queued' ? '已保存，正在排队等待评审' : review.compaction?.status === 'running' ? 'Agent 正在整理较早的上下文' : 'Agent 正在评审',
    ...(review.status === 'running' ? reviewActivity(review.progress) : {})});
  let previous = null;
  for (const item of items) {
    item.grouped = Boolean(previous && item.sender && previous.type === item.type && previous.sender?.id === item.sender.id);
    previous = item;
  }
  return items;
}

// What the Agent is doing right now, and the last few things it finished.
export function reviewActivity(progress = []) {
  const last = progress.at(-1);
  let activity = '正在阅读需求和讨论记录';
  if (last?.status === 'running') activity = '正在' + last.label;
  else if (last?.label === '整理回复') activity = last.status === 'failed' ? '回复不符合评审规则，正在修改' : '正在保存评审结果';
  else if (last) activity = '正在梳理查到的信息';
  const done = [...new Set(progress.filter(s => s.status === 'done' && s.label !== '整理回复').map(s => s.label))].slice(-3);
  return {activity, done};
}

// Line diff for "查看变更"; very long documents fall back to showing the new version.
export function lineDiff(before = '', after = '') {
  const a = String(before).split('\n'), b = String(after).split('\n');
  if (a.length * b.length > 250000) return null;
  const lcs = Array.from({length: a.length + 1}, () => new Uint32Array(b.length + 1));
  for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--)
    lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
  const out = [];
  let i = 0, j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) { out.push({type: 'same', text: a[i]}); i++; j++; }
    else if (lcs[i + 1][j] >= lcs[i][j + 1]) out.push({type: 'del', text: a[i++]});
    else out.push({type: 'add', text: b[j++]});
  }
  while (i < a.length) out.push({type: 'del', text: a[i++]});
  while (j < b.length) out.push({type: 'add', text: b[j++]});
  return out;
}

export const taskActive = task => ['clarify','ready','doing','aligning','testing','fixing','retest','verifying','pending_release','review'].includes(task?.status);
export const taskTerminated = task => ['closed','rejected','canceled'].includes(task?.status);
export const taskFinished = task => ['done','resolved'].includes(task?.status) || taskTerminated(task);

export function alignmentActionState(review, hasDraft) {
  if (review?.mode !== 'development_alignment') return null;
  if (review.status === 'alignment_completed') return {action:'details',title:'开发已确认对齐完成',description:'开发代理已恢复开发。新的问题会开启新一轮对齐。',label:'查看需求'};
  if (review.status === 'alignment_replied') return hasDraft ? {action:'send',title:'继续补充你的看法',description:'补充将交给原开发客户端判断，是否完成对齐由开发代理决定。',label:'提交补充回复'} : {action:'details',title:'回复已提交，等待开发确认',description:'原开发客户端认为可以继续后，将需求更新为开发中；仍可在输入框补充。',label:'查看需求'};
  return {action:'send',title:'开发需要与你对齐',description:'表单可以部分填写或完全跳过，直接说明你的看法或纠正理解。',label:'提交回复，交给开发确认'};
}
