import { iterationContextHTML } from './requirement-iterations.mjs';
import { createID } from './id.mjs';
import { renderMarkdown, renderDocumentMarkdown } from './markdown.mjs';
import { BASE } from './base.mjs';
import { answerRows, askIsOpen, chatTimeline, currentTopic, hasAnswerDraft, hasReviewDraft, hasQuestionNotes, lineDiff, proposalStates, questionCardStatus, questionIsActive, questionReplies, sameAnswerSheet, sameQuestionNotes, taskActive, taskTerminated } from './requirement-review-state.mjs';

const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const STATUS = {not_started:'等待开始',queued:'排队中',running:'Agent 评审中',needs_input:'等待补充',rejection_pending:'驳回待确认',ready_to_confirm:'待确认需求稿',confirmed:'已确认',failed:'评审未完成',closed:'已结束',alignment_replied:'等待开发确认',alignment_completed:'对齐完成'};
const KINDS = {overlap:'范围重叠',dependency:'存在依赖',conflict:'业务规则冲突'};
const time = value => {
  const at = new Date(value), today = new Date().toDateString() === at.toDateString();
  return at.toLocaleString('zh-CN', today ? {hour:'2-digit',minute:'2-digit'} : {month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'});
};
const hue = text => [...String(text)].reduce((sum, c) => (sum * 31 + c.codePointAt(0)) >>> 0, 7) % 4 + 1;

export function relationHTML(relations = []) {
  return `<ul class="rc-relations">${relations.map(r=>`<li><button type="button" data-review-action="related" data-task="${esc(r.task_id)}">${esc(r.title || r.task_id)}</button> · ${esc(KINDS[r.kind] || r.kind)}<p>${esc(r.reason)}</p></li>`).join('')}</ul>`;
}

export function requirementReviewHTML(task) {
  return `<section class="requirement-review" aria-label="需求评审对话">
    <header class="rc-head">
      <div class="rc-title"><h1>${esc(task.title)}</h1><span class="rc-status" role="status"></span><span class="rc-rev">R${task.revision}</span></div>
      <div class="rc-head-actions">
        <div class="rc-members" aria-label="参与成员"></div>
        <button type="button" class="rc-source-toggle" data-review-action="source" aria-expanded="false">需求原文</button>
        <button type="button" data-review-action="relations" class="rc-relations-toggle" aria-expanded="false" hidden></button>
        <button type="button" data-review-action="details">查看需求</button>
        <div class="rc-menu-wrap"><button type="button" data-review-action="menu" aria-label="更多操作" aria-haspopup="menu" aria-expanded="false">···</button>
          <div class="rc-menu" role="menu" hidden><button type="button" role="menuitem" class="danger-outline" data-review-action="decline">拒绝需求</button><button type="button" role="menuitem" data-review-action="close-task">关闭需求</button></div></div>
      </div>
    </header>
    <nav class="rc-topics" aria-label="话题进度" hidden></nav>
    <div class="rc-body">
      <div class="rc-chat">
        <div class="review-feed" role="log" aria-label="评审消息" tabindex="0"></div>
        <form class="review-composer">
          <p class="review-composer-note" hidden></p>
          <p class="rc-composer-hint" hidden></p>
          <textarea id="review-input" rows="2" aria-label="发消息" placeholder="直接回答、提问或纠正理解。看不懂可以问：这是什么意思？"></textarea>
          <div class="review-pending-files"></div>
          <div class="review-send-error" role="status" aria-live="polite"></div>
          <div class="rc-composer-row"><button type="button" data-review-action="attach">添加截图 / 文档</button><input type="file" class="review-file-input" multiple hidden accept="image/png,image/jpeg,image/webp,image/gif,.txt,.md,.csv,.json,.log,.pdf,.docx"><span class="review-draft-status">草稿自动保存在此浏览器</span><button type="submit" class="primary review-send">发送</button></div>
        </form>
      </div>
      <aside class="review-source" aria-label="需求原文"></aside>
    </div>
    <div class="rc-pop" hidden></div>
  </section>`;
}

function attachments(files, project) {
  return files?.length ? `<div class="review-attachments">${files.map(file => {
    const url = `${BASE}/api/projects/${encodeURIComponent(project)}/attachments/${encodeURIComponent(file.id)}`;
    return `<a href="${url}" download="${esc(file.name)}">${file.kind==='image'?`<img src="${url}?preview=1" alt="${esc(file.name)}">`:''}<span>${esc(file.name)}</span></a>`;
  }).join('')}</div>` : '';
}

function questionHTML(question, index, message) {
  const name = 'question_'+index, id = `${message}_${index}`;
  let control;
  if (question.type === 'select') control = `<select id="${id}" aria-label="${esc(question.label)}" name="${name}"><option value="">请选择，也可以直接补充说明</option>${question.options.map(option=>`<option value="${esc(option)}">${esc(option)}</option>`).join('')}</select>`;
  else if (question.type === 'radio' || question.type === 'multiselect') control = `<div class="review-options">${question.options.map(option=>`<label><input name="${name}" type="${question.type==='radio'?'radio':'checkbox'}" value="${esc(option)}"><span>${esc(option)}</span>${question.recommended?.includes(option)?'<em class="rc-recommended">推荐</em>':''}</label>`).join('')}</div>`;
  else if (question.type === 'textarea') control = `<textarea id="${id}" aria-label="${esc(question.label)}" name="${name}" rows="3" placeholder="补充你希望明确的细节"></textarea>`;
  else control = `<input id="${id}" aria-label="${esc(question.label)}" name="${name}" type="text" autocomplete="off" placeholder="填写补充信息">`;
  const choice = ['radio','multiselect','select'].includes(question.type);
  const note = choice ? `<details class="review-question-note" data-question-id="${esc(question.id)}" data-key="${esc(message)}:note:${esc(question.id)}"><summary>补充说明 <span>选填 · 可粘贴截图</span></summary><textarea id="${id}_note" class="review-question-note-text" data-question-id="${esc(question.id)}" aria-label="${esc(question.label)}的补充说明" rows="2" placeholder="选项不合适？直接写，或粘贴截图。"></textarea><div class="review-note-files"></div><div class="review-note-actions"><button type="button" data-review-action="attach-note">添加图片</button><input type="file" class="review-note-file-input" accept="image/png,image/jpeg,image/webp,image/gif" multiple hidden><span class="review-note-status" role="status"></span></div></details>` : '';
  const recommendation = question.recommended?.length ? `<div class="review-recommendation"><p>${question.reason?`<strong>推荐理由</strong>${esc(question.reason)}`:`<strong>推荐</strong>${question.recommended.map(esc).join('、')}`}</p><button type="button" class="rc-link" data-review-action="recommend" data-question="${index}">采用推荐</button></div>` : '';
  return `<fieldset data-question-index="${index}"><legend><span class="rc-q-no">${index+1}</span><span class="rc-q-label">${esc(question.label)}${question.type==='multiselect'?' <small>可多选</small>':''}</span></legend>${control}${recommendation}${note}</fieldset>`;
}

function answerHTML(questions, values, notes = {}, project) {
  return `<dl class="review-saved-answers">${answerRows(questions,values,notes).map(row=>`<div class="review-answer-row"><dt>${esc(row.label)}</dt><dd class="${row.values.length?'':'review-answer-empty'}">${row.values.length?row.values.map(esc).join('、'):'未填写'}</dd>${row.note?`<dd class="review-answer-question-note">${row.note.body?`<p class="prewrap">补充：${esc(row.note.body)}</p>`:''}${attachments(row.note.attachments || row.note.files,project)}</dd>`:''}</div>`).join('')}</dl>`;
}

export function mountRequirementReview(root, {task,api,userID,onDetails,onConfirmed,onResolve}) {
  const $ = selector => root.querySelector(selector), feed = $('.review-feed'), input = $('#review-input');
  const key = `ai-pm:review-draft:${userID}:${task.project_id}:${task.id}`;
  let draft; try { draft = JSON.parse(localStorage.getItem(key)||'null'); } catch {}
  let v = null, disposed = false, saving = false, uploading = false, polling = false, lastVersion = -1, lastExtra = '';
  let files = draft?.files || [], answers = draft?.answers || {}, pending = draft?.pending || null;
  const notes = Object.assign(Object.create(null),draft?.question_notes || {});
  const operations = draft?.operations || {};
  const signatures = new WeakMap();
  let sourceView = null; // null: closed; {kind:'current'|'snapshot'|'diff', message}
  input.value = draft?.body || '';
  const endpoint = `/tasks/${encodeURIComponent(task.id)}/requirement-review`;
  const source = () => v?.source || task;
  const activeForm = () => v?.messages.findLast(message => message.result?.questions?.length && questionIsActive(v,message));
  const hasDraft = () => {
    const id = activeForm()?.id;
    return hasReviewDraft({body:input.value,files,answers:id?{[id]:answers[id]}:{},questionNotes:id?{[id]:notes[id]}:{},pending});
  };
  const bugReadOnly = () => source().parent_requirement_id && !['clarify','ready'].includes(source().status) && v?.mode !== 'development_alignment';
  const alignmentDone = () => v?.mode === 'development_alignment' && v.status === 'alignment_completed';
  const closed = () => v?.status==='closed' || taskTerminated(source());
  const readOnly = () => closed() || bugReadOnly() || alignmentDone();
  const topicOf = id => (v?.agenda || []).find(t => t.id === id);
  const node = id => [...feed.children].find(child => child.dataset.key === id);

  function saveDraft() {
    if (disposed) return;
    try {
      localStorage.setItem(key,JSON.stringify({body:input.value,files,answers,question_notes:notes,pending,operations}));
      $('.review-draft-status').textContent = '草稿已保存在此浏览器';
    } catch { $('.review-draft-status').textContent = '浏览器无法保存草稿，请及时发送'; }
  }
  function fileList() {
    $('.review-pending-files').innerHTML = files.map(file=>`<span>${esc(file.name)}<button type="button" data-review-action="remove-file" data-file="${esc(file.id)}" aria-label="移除 ${esc(file.name)}">×</button></span>`).join('');
  }
  function noteDraft(messageID,questionID) {
    if (!Object.hasOwn(notes,messageID)) notes[messageID]=Object.create(null);
    const sheet=notes[messageID];
    if (!Object.hasOwn(sheet,questionID)) sheet[questionID]={body:'',files:[]};
    return sheet[questionID];
  }
  function noteFileList(container,messageID) {
    const note=notes[messageID]?.[container.dataset.questionId];
    container.querySelector('.review-note-files').innerHTML=(note?.files || []).map(file=>`<div class="review-note-image"><a href="${BASE}/api/projects/${encodeURIComponent(task.project_id)}/attachments/${encodeURIComponent(file.id)}?preview=1" target="_blank" rel="noopener"><img src="${BASE}/api/projects/${encodeURIComponent(task.project_id)}/attachments/${encodeURIComponent(file.id)}?preview=1" alt="${esc(file.name)}"></a><button type="button" data-review-action="remove-note-file" data-file="${esc(file.id)}" aria-label="移除 ${esc(file.name)}">×</button></div>`).join('');
  }
  function restoreForm(form,message) {
    const saved = answers[message.id] || {};
    for (const [index,question] of message.result.questions.entries()) {
      const values = saved[question.id] || [];
      for (const element of form.querySelectorAll(`[name="question_${index}"]`)) {
        if (element.type === 'checkbox' || element.type === 'radio') element.checked = values.includes(element.value);
        else element.value = values[0] || '';
      }
    }
    for (const container of form.querySelectorAll('.review-question-note')) {
      const note = notes[message.id]?.[container.dataset.questionId];
      container.querySelector('textarea').value = note?.body || '';
      if (note?.body?.trim() || note?.files?.length) container.open = true;
      noteFileList(container,message.id);
    }
    progress(form,message);
  }
  function readAnswers(form) {
    const message = v?.messages.find(message=>message.id === form.dataset.message);
    const data = new FormData(form), out = Object.create(null);
    for (const [index,question] of (message?.result?.questions || []).entries()) {
      const values = data.getAll('question_'+index).map(String).filter(value=>value.trim());
      if (values.length) out[question.id] = values;
    }
    return out;
  }
  function filledCount(message) {
    return new Set([...Object.keys(answers[message.id] || {}),...Object.keys(notes[message.id] || {}).filter(id=>hasQuestionNotes({[id]:notes[message.id][id]}))]).size;
  }
  function progress(form,message) {
    form.querySelector('.review-question-count').textContent = `已填写 ${filledCount(message)} / ${message.result.questions.length}`;
  }

  /* ---------- message rendering ---------- */
  function avatar(sender) {
    if (sender.kind === 'platform') return '<span class="rc-avatar rc-av-agent" aria-hidden="true">AI</span>';
    return `<span class="rc-avatar rc-av-${sender.kind === 'dev_agent' ? 'dev' : hue(sender.id)}" aria-hidden="true">${esc([...sender.name][0] || '?')}</span>`;
  }
  function cardHead(message) {
    const ask = message.result.ask, topic = topicOf(ask?.topic_id), agenda = v?.agenda || [];
    if (topic) return `<span class="rc-topic-no">话题 ${agenda.indexOf(topic)+1}/${agenda.length}</span><strong>${esc(topic.title)}</strong>`;
    return `<strong>${message.action === 'request_alignment' ? '开发对齐问题' : '需要你补充的问题'}</strong><span class="rc-muted">${message.result.questions.length} 题</span>`;
  }
  function formHTML(message) {
    const questions = message.result.questions, state = questionCardStatus(v,message), head = cardHead(message);
    if (state === 'active') return `<form class="review-question-card review-question-form rc-card" data-status="active" data-message="${esc(message.id)}"><div class="rc-card-head">${head}<span class="rc-state review-question-count"></span></div>${questions.map((question,index)=>questionHTML(question,index,message.id)).join('')}<div class="rc-card-foot"><span class="rc-card-tip">可以只填一部分，也可以直接在下方输入框说明</span><button type="submit" class="primary rc-form-submit">提交回答</button></div></form>`;
    if (state === 'submitted') {
      const reply = questionReplies(v,message.id).at(-1);
      return `<div class="review-question-card rc-card rc-card-done" data-status="submitted" data-message="${esc(message.id)}"><div class="rc-card-head">${head}<span class="rc-state ok">${esc(reply.actor || '产品')} 已提交 · ${time(reply.at)}</span></div></div>`;
    }
    const reason = message.form_expired_reason || (closed() ? '需求已结束' : '本轮已结束');
    const local = answers[message.id] || {}, localNotes = notes[message.id];
    const unsent = (hasAnswerDraft({[message.id]:local}) || hasQuestionNotes(localNotes)) && !questionReplies(v,message.id).some(reply=>sameAnswerSheet(reply.answers || {},local) && sameQuestionNotes(reply.question_notes,localNotes));
    return `<details class="review-question-card rc-card rc-card-expired" data-status="expired" data-message="${esc(message.id)}" data-key="${esc(message.id)}:expired"><summary class="rc-card-head">${head}<span class="rc-state">已失效：${esc(reason)}</span></summary><ol class="rc-expired-list">${questions.map(q=>`<li>${esc(q.label)}</li>`).join('')}</ol>${unsent?`<section class="review-unsent-sheet"><h4>未发送的填写 · 仅供查看</h4>${answerHTML(questions,local,localNotes,task.project_id)}</section>`:''}</details>`;
  }
  function directionHTML(message) {
    const ask = message.result.ask, open = askIsOpen(v,message), topic = topicOf(ask.topic_id);
    const index = v.messages.indexOf(message), answered = v.messages.slice(index+1).some(m=>m.role==='user');
    const state = open ? '' : `<span>· ${answered ? '已回答' : '已失效'}</span>`;
    let html = `<div class="rc-bubble rc-ask${open?'':' rc-ask-done'}"><div class="rc-ask-tag">方向问题${topic?` · ${esc(topic.title)}`:''}${state}</div><div class="markdown-content">${renderMarkdown(ask.question)}</div></div>`;
    if (open && !readOnly() && ask.quick_replies?.length) html += `<div class="rc-quick">${ask.quick_replies.map(text=>`<button type="button" data-review-action="quick" data-text="${esc(text)}">${esc(text)}</button>`).join('')}</div><p class="rc-quick-hint">点选会填进输入框，可以改完再发送</p>`;
    return html;
  }
  function proposalHTML(message, states) {
    const proposal = message.result.proposal, {number, state, supersededBy} = states.get(message.id);
    const tag = {current:'<span class="rc-state warn">待确认</span>',confirmed:'<span class="rc-state ok">已确认</span>',superseded:`<span class="rc-state">已被 v${supersededBy} 替代</span>`,draft:'<span class="rc-state">尚未生效</span>'}[state];
    const head = `<strong>需求稿 v${number}</strong><span class="rc-muted">${proposal.acceptance.length} 条验收条件</span>${tag}`;
    const full = `<div class="markdown-content">${renderDocumentMarkdown(proposal.description)}</div>`;
    if (state === 'superseded' || state === 'draft') return `<details class="rc-card rc-proposal rc-card-muted" data-state="${state}" data-key="${esc(message.id)}:proposal"><summary class="rc-card-head">${head}</summary><div class="rc-proposal-body">${full}<h4>验收条件</h4><ol>${proposal.acceptance.map(value=>`<li>${esc(value)}</li>`).join('')}</ol></div></details>`;
    const foot = state === 'current' ? `<div class="rc-card-foot"><span class="rc-card-tip rc-confirm-tip">确认后需求进入待认领，等待开发领取</span><button type="button" class="primary review-confirm" data-review-action="confirm">确认需求，进入待认领</button></div>` : '';
    return `<div class="rc-card rc-proposal" data-state="${state}"><div class="rc-card-head">${head}</div><div class="rc-proposal-body"><h4>验收条件</h4><ol>${proposal.acceptance.map(value=>`<li>${esc(value)}</li>`).join('')}</ol><details class="rc-proposal-full" data-key="${esc(message.id)}:full"><summary>查看完整需求稿</summary>${full}</details></div>${foot}</div>`;
  }
  function rejectionHTML(message) {
    const pending = v.status === 'rejection_pending' && message.generation === v.generation && !closed();
    return `<div class="rc-card rc-reject"><div class="rc-card-head"><strong>Agent 建议驳回这个 Bug</strong><span class="rc-state ${pending?'warn':''}">${pending?'等待你决定':'已处理'}</span></div>${pending?`<div class="rc-card-foot"><span class="rc-card-tip rc-confirm-tip">同意后关闭 Bug；不同意可以在下方说明原因或补充截图。</span><button type="button" class="primary review-confirm-rejection" data-review-action="confirm_rejection">同意驳回，关闭 Bug</button></div>`:''}</div>`;
  }
  function agentBody(message) {
    const result = message.result || {};
    // Records written before the length limit keep long analysis out of the way.
    if (!result.analysis && message.role === 'assistant' && !message.action && message.body.length > 600) {
      const lead = [...message.body.replace(/\s+/g,' ')].slice(0,160).join('');
      return `<div class="rc-bubble"><p>${esc(lead)}…</p></div><details class="rc-analysis" data-key="${esc(message.id)}:body"><summary>展开全文</summary><div class="markdown-content">${renderMarkdown(message.body)}</div></details>`;
    }
    return `<div class="rc-bubble markdown-content">${renderMarkdown(message.body)}</div>`;
  }
  function itemHTML(item, states) {
    if (item.type === 'event') {
      const b = item.button;
      const button = b ? `<button type="button" class="rc-event-button" data-review-action="${b.action}"${b.message?` data-message="${esc(b.message)}"`:''}>${b.label}</button>` : '';
      return `<div class="rc-event${item.tone?' '+item.tone:''}"><span class="rc-event-text">${esc(item.text)}</span>${item.note?`<span class="rc-event-note">${esc(item.note)}</span>`:''}${item.at?`<time>${time(item.at)}</time>`:''}${button}</div>`;
    }
    if (item.type === 'typing') {
      const detail = item.activity ? `<div class="rc-activity"><span>${esc(item.activity)}</span>${item.done?.length?`<span class="rc-activity-done">已完成：${item.done.map(esc).join(' · ')}</span>`:''}</div>` : '';
      return `<div class="rc-row rc-agent">${avatar({kind:'platform'})}<div class="rc-col"><div class="rc-who"><strong>评审 Agent</strong></div><div class="rc-bubble rc-typing"><div class="rc-typing-title"><span class="rc-dots" aria-hidden="true"><i></i><i></i><i></i></span><span>${esc(item.text)}</span></div>${detail}</div></div></div>`;
    }
    const message = item.message, sender = item.sender;
    const who = `<div class="rc-who">${item.mine?'':`<strong>${esc(sender.name)}</strong>`}${sender.kind==='dev_agent'?'<span class="rc-tag">开发代理</span>':''}${item.delegated?'<span class="rc-tag">经 Claude 代办</span>':''}<time>${time(message.at)}</time></div>`;
    let body = '';
    if (item.type === 'member') {
      const sheet = Object.keys(message.answers || {}).length || hasQuestionNotes(message.question_notes);
      if (sheet) {
        const parent = v.messages.find(m=>m.id === message.reply_to), topic = topicOf(parent?.result?.ask?.topic_id);
        body = `<div class="rc-bubble rc-submission"><div class="rc-submission-head">${topic?`提交了「${esc(topic.title)}」`:'提交了表单回答'}</div>${answerHTML(parent?.result?.questions || [],message.answers || {},message.question_notes,task.project_id)}${message.body?`<p class="prewrap">${esc(message.body)}</p>`:''}</div>`;
      } else if (message.body.length > 600) body = `<div class="rc-bubble prewrap">${esc([...message.body].slice(0,240).join(''))}…</div><details class="rc-analysis" data-key="${esc(message.id)}:body"><summary>展开全文（${[...message.body].length} 字）</summary><div class="rc-long prewrap">${esc(message.body)}</div></details>`;
      else if (message.body) body = `<div class="rc-bubble prewrap">${esc(message.body)}</div>`;
      body += attachments(message.attachments,task.project_id);
    } else {
      const result = message.result || {};
      body = agentBody(message);
      if (result.analysis) body += `<details class="rc-analysis" data-key="${esc(message.id)}:analysis"><summary>展开分析依据</summary><div class="markdown-content">${renderMarkdown(result.analysis)}</div></details>`;
      if (result.relations?.length) body += `<div class="rc-rel-tags">关联${result.relations.map(r=>`<button type="button" data-review-action="related" data-task="${esc(r.task_id)}" title="${esc(r.reason)}">${esc(r.title || '相关需求')} · ${esc(KINDS[r.kind] || r.kind)}</button>`).join('')}</div>`;
      if (message.action === 'request_alignment' && message.source) body += `<button type="button" class="rc-link" data-review-action="snapshot" data-message="${esc(message.id)}">查看提问时的需求</button>`;
      if (result.ask?.kind === 'direction') body += directionHTML(message);
      if (result.questions?.length) body += formHTML(message);
      if (result.proposal) body += proposalHTML(message, states);
      if (result.decision === 'reject_bug') body += rejectionHTML(message);
      body += attachments(message.attachments,task.project_id);
    }
    return `<div class="rc-row rc-${item.type === 'agent' ? (sender.kind === 'dev_agent' ? 'dev' : 'agent') : 'member'}${item.mine?' rc-mine':''}${item.grouped?' rc-grouped':''}">${avatar(sender)}<div class="rc-col">${who}${body}</div></div>`;
  }
  // Keyed updates keep focus, typed form values and expanded sections across polling.
  function syncFeed(items) {
    const states = proposalStates(v), existing = new Map([...feed.children].map(child => [child.dataset.key, child]));
    let previous = null, appended = null;
    for (const item of items) {
      const html = itemHTML(item, states);
      let current = existing.get(item.key);
      if (!current || signatures.get(current) !== html) {
        const template = document.createElement('template');
        template.innerHTML = html.trim();
        const fresh = template.content.firstElementChild;
        fresh.dataset.key = item.key;
        signatures.set(fresh, html);
        if (current) {
          const open = new Set([...current.querySelectorAll('details[open][data-key]')].map(d => d.dataset.key));
          for (const details of fresh.querySelectorAll('details[data-key]')) if (open.has(details.dataset.key)) details.open = true;
          current.replaceWith(fresh);
        } else if (item.type !== 'typing') appended = fresh;
        current = fresh;
        const form = current.querySelector('form.review-question-form');
        if (form) restoreForm(form, v.messages.find(m => m.id === form.dataset.message));
      }
      if ((previous ? previous.nextElementSibling : feed.firstElementChild) !== current) previous ? previous.after(current) : feed.prepend(current);
      existing.delete(item.key);
      previous = current;
    }
    for (const stale of existing.values()) stale.remove();
    return appended;
  }

  /* ---------- header, topics, source drawer and relations ---------- */
  function renderHeader(items) {
    const status = $('.rc-status');
    status.textContent = v.needs_recheck ? '需要复核' : v.mode === 'development_alignment' && v.status === 'needs_input' ? '待对齐' : STATUS[v.status] || v.status;
    status.dataset.status = v.needs_recheck ? 'recheck' : v.status;
    $('.rc-rev').textContent = 'R' + source().revision;
    const people = new Map();
    if (v.creator?.name) people.set(v.creator.id || 'name:'+v.creator.name, {kind:'human', id:v.creator.id || 'name:'+v.creator.name, name:v.creator.name});
    for (const item of items) if (item.sender) people.set(item.sender.id, item.sender);
    people.set('platform', {kind:'platform', id:'platform', name:'评审 Agent'});
    const list = [...people.values()];
    $('.rc-members').innerHTML = list.slice(0,5).map(p=>avatar(p).replace('aria-hidden="true"',`title="${esc(p.name)}"`)).join('') + (list.length > 5 ? `<span class="rc-more">+${list.length-5}</span>` : '');
    const relations = relationSets();
    const toggle = $('.rc-relations-toggle');
    toggle.hidden = !relations.count;
    toggle.textContent = `关联 ${relations.count}`;
  }
  function relationSets() {
    const latest = v?.messages.findLast(m => m.result?.relations?.length && m.generation === v.generation) || v?.messages.findLast(m => m.result?.relations?.length);
    const outgoing = latest?.result.relations || [], incoming = v?.incoming_relations || [];
    return {outgoing, incoming, count: outgoing.length + incoming.length};
  }
  function renderTopics() {
    const nav = $('.rc-topics'), agenda = v.agenda || [];
    nav.hidden = !agenda.length;
    if (!agenda.length) { nav.innerHTML = ''; return; }
    const current = readOnly() ? '' : currentTopic(v);
    const icon = t => t.status === 'resolved' ? '✓' : t.status === 'dropped' ? '–' : t.id === current ? '●' : '○';
    const done = agenda.every(t => t.status !== 'open');
    nav.innerHTML = `<span class="rc-topics-label">话题</span>${agenda.map(t=>`<button type="button" class="rc-topic rc-topic-${t.status}${t.id===current&&t.status==='open'?' is-current':''}" data-review-action="topic" data-topic="${esc(t.id)}" title="${esc(t.conclusion || (t.kind==='direction'?'方向性话题':'细节话题'))}"><span aria-hidden="true">${icon(t)}</span>${esc(t.title)}${t.kind==='direction'?'<small>方向</small>':''}</button>`).join('<span class="rc-topic-sep" aria-hidden="true">›</span>')}${done?'<span class="rc-topics-done">全部确认</span>':''}`;
  }
  function documentHTML(doc) {
    return `${iterationContextHTML(doc.iteration)}<div class="markdown-content">${renderDocumentMarkdown(doc.description || '尚未补充描述，可在对话中继续说明。')}</div>${attachments(doc.attachments,task.project_id)}${doc.acceptance?.length?`<h3>验收条件</h3><ul>${doc.acceptance.map(x=>`<li>${esc(x)}</li>`).join('')}</ul>`:''}`;
  }
  function diffHTML(message) {
    const index = v.messages.indexOf(message), after = message.source;
    const before = v.messages.slice(0,index).findLast(m => m.source)?.source;
    if (!before) return documentHTML(after);
    const lines = lineDiff(before.description || '', after.description || '');
    let html = before.title !== after.title ? `<p class="rc-diff-line"><del>${esc(before.title)}</del> → <ins>${esc(after.title)}</ins></p>` : '';
    if (!lines) return html + `<p class="rc-muted">内容较长，显示新版本全文。</p>` + documentHTML(after);
    const changed = lines.some(l => l.type !== 'same');
    html += changed ? `<div class="rc-diff">${lines.map(l=>l.type==='same'?`<div class="rc-diff-same">${esc(l.text) || '&nbsp;'}</div>`:`<div class="rc-diff-${l.type}">${l.type==='add'?'+':'−'} ${esc(l.text) || '&nbsp;'}</div>`).join('')}</div>` : '<p class="rc-muted">正文没有变化。</p>';
    const acceptance = lineDiff((before.acceptance || []).join('\n'), (after.acceptance || []).join('\n'));
    if (acceptance?.some(l => l.type !== 'same')) html += `<h3>验收条件</h3><div class="rc-diff">${acceptance.filter(l=>l.text).map(l=>`<div class="rc-diff-${l.type}">${l.type==='add'?'+ ':l.type==='del'?'− ':''}${esc(l.text)}</div>`).join('')}</div>`;
    return html;
  }
  function renderSource() {
    const aside = $('.review-source');
    root.classList.toggle('source-open', Boolean(sourceView));
    $('.rc-source-toggle').setAttribute('aria-expanded', String(Boolean(sourceView)));
    if (!sourceView) { aside.innerHTML = ''; return; }
    const message = sourceView.message && v?.messages.find(m => m.id === sourceView.message);
    const doc = message?.source || source();
    let banner = '', content;
    if (message && sourceView.kind === 'diff') { banner = `${esc(message.actor || '')} 的修改 · R${message.event?.from_revision ?? doc.revision-1} → R${message.event?.to_revision ?? doc.revision}`; content = diffHTML(message); }
    else if (message) { banner = `R${doc.revision} · ${time(message.at)} 保存的版本`; content = documentHTML(doc); }
    else {
      const resolved = (v?.agenda || []).filter(t => t.status === 'resolved');
      content = documentHTML(doc) + (resolved.length ? `<h3>已确认的话题</h3><ul class="rc-conclusions">${resolved.map(t=>`<li><strong>${esc(t.title)}</strong>：${esc(t.conclusion)}</li>`).join('')}</ul>` : '');
    }
    aside.innerHTML = `<div class="rc-source-head"><strong>需求原文</strong><span class="rc-rev">R${doc.revision}</span><button type="button" data-review-action="source-close" aria-label="收起需求原文">×</button></div>${banner?`<div class="rc-source-banner"><span>${banner}</span><button type="button" class="rc-link" data-review-action="source-latest">回到最新版本</button></div>`:''}<div class="review-source-content">${content}</div>`;
  }
  function renderPop(kind) {
    const pop = $('.rc-pop');
    $('.rc-relations-toggle').setAttribute('aria-expanded', String(kind === 'relations'));
    if (kind !== 'relations') { pop.hidden = true; pop.innerHTML = ''; return; }
    const {outgoing, incoming} = relationSets();
    pop.innerHTML = `<div class="rc-pop-head"><strong>关联需求</strong><button type="button" data-review-action="pop-close" aria-label="关闭">×</button></div>${outgoing.length?`<p class="rc-pop-label">本需求关联的</p>${relationHTML(outgoing)}`:''}${incoming.length?`<p class="rc-pop-label">其他需求的关联判断</p>${relationHTML(incoming)}`:''}`;
    pop.hidden = false;
  }
  function closeMenu() {
    $('.rc-menu').hidden = true;
    $('[data-review-action=menu]').setAttribute('aria-expanded','false');
  }

  /* ---------- state ---------- */
  function updateActions() {
    if (disposed) return;
    const note = $('.review-composer-note'), hint = $('.rc-composer-hint');
    note.textContent = closed() ? '需求已结束，评审记录只读' : bugReadOnly() ? 'Bug 已进入修复或验证流程，评审历史保留；后续补充请使用工作记录。' : alignmentDone() ? '开发已确认对齐完成，新的问题会开启新一轮对齐。' : '';
    note.hidden = !note.textContent;
    const form = activeForm(), count = form ? filledCount(form) : 0;
    hint.textContent = pending ? '上次发送的结果还不确定，内容已保留；重试会核对保存结果，不会重复发送。' : count && !readOnly() ? `发送时会一并提交「${topicOf(form.result.ask?.topic_id)?.title || '当前问题'}」里已填的 ${count} 项` : '';
    hint.hidden = !hint.textContent;
    input.readOnly = saving || readOnly();
    const send = $('.review-send');
    send.textContent = saving ? '正在保存…' : pending ? '重试保存' : '发送';
    send.disabled = saving || uploading || (readOnly() && !pending);
    for (const button of root.querySelectorAll('[data-review-action=attach], [data-review-action=remove-file], [data-review-action=attach-note], [data-review-action=remove-note-file], [data-review-action=recommend], [data-review-action=quick]')) button.disabled = saving || uploading || readOnly();
    for (const element of root.querySelectorAll('.review-question-form :is(input,textarea,select)')) element.disabled = saving || readOnly();
    for (const button of root.querySelectorAll('.rc-form-submit')) button.disabled = saving || uploading || readOnly();
    for (const tip of root.querySelectorAll('.review-question-form .rc-card-tip')) tip.textContent = input.value.trim() ? '会一并发送输入框里的文字' : '可以只填一部分，也可以直接在下方输入框说明';
    const blocked = hasDraft() ? '输入框或表单里还有没发送的内容，先发送，让 Agent 按最新说明评审后再确认' : v?.needs_recheck ? '其他需求有变化，点下方「重新核对」后再确认' : '';
    for (const button of root.querySelectorAll('[data-review-action=confirm], [data-review-action=confirm_rejection]')) {
      button.disabled = saving || uploading || Boolean(blocked);
      const tip = button.parentElement.querySelector('.rc-confirm-tip');
      if (tip) tip.textContent = blocked || (button.dataset.reviewAction === 'confirm' ? '确认后需求进入待认领，等待开发领取' : '同意后关闭 Bug；不同意可以在下方说明原因或补充截图。');
    }
    for (const button of root.querySelectorAll('[data-review-action=retry]')) button.disabled = saving || uploading;
    for (const button of root.querySelectorAll('[data-review-action=decline], [data-review-action=close-task]')) button.disabled = saving || uploading;
    $('.rc-menu-wrap').hidden = closed() || !taskActive(source());
  }
  function render(next) {
    if (disposed || next.version < lastVersion) return;
    // Progress changes while the review version stays the same during a round.
    const extra = JSON.stringify([next.needs_recheck,next.recheck_reason,next.compaction,next.incoming_relations,next.creator,next.progress]);
    if (next.version === lastVersion && extra === lastExtra) return;
    v = next; lastVersion = next.version; lastExtra = extra;
    const nearEnd = feed.scrollHeight-feed.scrollTop-feed.clientHeight < 120;
    const items = chatTimeline(v,{viewerID:userID});
    const appended = syncFeed(items);
    renderHeader(items); renderTopics();
    if (sourceView) renderSource();
    updateActions();
    if (nearEnd && appended) {
      // A long new reply starts at its top; short updates keep the newest line in view.
      const top = appended.getBoundingClientRect().top - feed.getBoundingClientRect().top + feed.scrollTop - 12;
      feed.scrollTop = Math.min(top, feed.scrollHeight);
      if (appended.offsetHeight < feed.clientHeight) feed.scrollTop = feed.scrollHeight;
    } else if (nearEnd) feed.scrollTop = feed.scrollHeight;
  }
  function setSaving(value) { saving = value; updateActions(); }
  async function command(payload) {
    const next = await api(endpoint,payload);
    if (!disposed) render(next);
    return next;
  }
  function submissionTarget() {
    const message = activeForm();
    if (!message) return {reply:'',values:{},questionNotes:{}};
    const form = feed.querySelector(`.review-question-form[data-message="${CSS.escape(message.id)}"]`);
    const values = form ? readAnswers(form) : answers[message.id] || {};
    if (form) answers[message.id] = values;
    const questionNotes=Object.fromEntries(Object.entries(notes[message.id] || {}).filter(([id,note])=>hasQuestionNotes({[id]:note})).map(([id,note])=>[id,{body:note.body || '',attachment_ids:(note.files || []).map(file=>file.id)}]));
    return {reply:message.id,values,questionNotes};
  }
  async function submit() {
    if (saving || uploading || disposed || (readOnly() && !pending)) return;
    const {reply,values,questionNotes} = submissionTarget();
    if (!pending && !input.value.trim() && !files.length && !Object.keys(values).length && !hasQuestionNotes(questionNotes)) {
      $('.review-send-error').textContent = '请输入说明，或回答至少一个问题。'; input.focus(); return;
    }
    pending ||= {action:'message',generation:v?.generation,request_id:createID(),body:input.value,reply_to:reply,answers:values,question_notes:questionNotes,attachment_ids:files.map(file=>file.id)};
    saveDraft(); setSaving(true); $('.review-send-error').textContent = '正在保存消息…';
    const submitted = pending;
    try {
      await command(submitted);
      if (disposed) return;
      if (input.value === submitted.body) input.value = '';
      files = files.filter(file=>!submitted.attachment_ids.includes(file.id));
      if (submitted.reply_to && JSON.stringify(answers[submitted.reply_to] || {}) === JSON.stringify(submitted.answers)) delete answers[submitted.reply_to];
      if (submitted.reply_to && sameQuestionNotes(notes[submitted.reply_to],submitted.question_notes)) delete notes[submitted.reply_to];
      pending = null; saveDraft(); fileList();
      // Sending asks to continue the conversation: follow the newest messages.
      feed.scrollTop = feed.scrollHeight;
      $('.review-send-error').textContent = '消息已保存';
    } catch (error) {
      if (!disposed) {
        if (error.status >= 400 && error.status < 500) { pending = null; saveDraft(); await poll(); }
        $('.review-send-error').textContent = `${error.message}。草稿已保留${pending?'，点击「重试保存」会核对同一条消息':'，请核对最新评审后继续'}。`;
      }
    } finally { if (!disposed) setSaving(false); }
  }
  async function operate(action) {
    if (saving || uploading) return;
    if (['confirm','confirm_rejection'].includes(action) && hasDraft()) { $('.review-send-error').textContent = '请先发送当前补充，让 Agent 根据最新说明评审后再确认。'; saveDraft(); input.focus(); return; }
    const operationKey = `${action}:${v?.generation || 0}`;
    const payload = {action,request_id:operations[operationKey] ||= createID(),generation:v?.generation || 0};
    saveDraft(); setSaving(true);
    try {
      const next = await command(payload);
      if (disposed) return;
      saveDraft();
      $('.review-send-error').textContent = next.status === 'closed'?'已确认驳回并关闭 Bug':next.status === 'confirmed'?'评审已确认，进入待认领':'评审已重新开始，内容已保存';
      if (['confirmed','closed'].includes(next.status)) onConfirmed?.();
    } catch (error) {
      if (!disposed) {
        if (error.status >= 400 && error.status < 500) { delete operations[operationKey]; saveDraft(); void poll(); }
        $('.review-send-error').textContent = `${error.message}。输入已保留，请核对最新评审后继续。`;
      }
    } finally { if (!disposed) setSaving(false); }
  }
  async function upload(incoming, target = null) {
    if (uploading || saving || readOnly()) return;
    uploading = true; updateActions();
    const list = target ? noteDraft(target.messageID,target.questionID).files : files;
    const container = () => target && [...(feed.querySelector(`.review-question-form[data-message="${CSS.escape(target.messageID)}"]`)?.querySelectorAll('.review-question-note') || [])].find(node=>node.dataset.questionId===target.questionID);
    const notice = text => {
      $('.review-send-error').textContent=text;
      const box = container(); if (box) box.querySelector('.review-note-status').textContent=text;
    };
    try {
      for (const file of incoming) {
        const messageID=target?.messageID || activeForm()?.id;
        const allFiles=[...files,...Object.values(notes[messageID] || {}).flatMap(note=>note.files || [])];
        if (target && !file.type.startsWith('image/')) throw Error('这里支持图片，请在下方消息区添加文档');
        if (allFiles.length >= 6) throw Error('每条消息最多添加 6 个附件');
        if (!file.size || file.size > 10*1048576) throw Error(`${file.name}：文件为空或超过 10 MB`);
        if (allFiles.reduce((size,item)=>size+item.size,0)+file.size > 20*1048576) throw Error('附件总大小不能超过 20 MB');
        notice(`正在上传 ${file.name}…`);
        const body = new FormData(); body.append('file',file,file.name || '截图.png');
        const response = await fetch(`${BASE}/api/projects/${encodeURIComponent(task.project_id)}/attachments`,{method:'POST',body});
        const data = await response.json(); if (!response.ok) throw Error(data.error || '上传失败');
        if (target && data.kind!=='image') throw Error('逐题说明只支持图片');
        list.push(data); saveDraft();
        if (!disposed) { fileList(); const box = container(); if (box) noteFileList(box,target.messageID); const form = target && feed.querySelector(`.review-question-form[data-message="${CSS.escape(target.messageID)}"]`); if (form) progress(form,v.messages.find(m=>m.id===target.messageID)); }
      }
      if (!disposed) notice(target?'图片已保存在草稿中，随本轮回答发送':'附件已上传，发送后进入评审对话');
    } catch (error) { if (!disposed) notice(error.message); }
    finally { uploading = false; updateActions(); }
  }
  async function poll() {
    if (disposed || polling) return;
    polling = true;
    try { render(await api(endpoint)); }
    catch (error) { if (!disposed) $('.review-send-error').textContent = `无法刷新评审：${error.message}。输入保留在草稿中。`; }
    finally { polling = false; }
  }

  /* ---------- events ---------- */
  root.addEventListener('submit',event=>{ event.preventDefault(); event.stopPropagation(); void submit(); });
  function changed(event) {
    if (saving) return;
    const form = event.target.closest('.review-question-form');
    if (form) {
      answers[form.dataset.message] = readAnswers(form);
      if (event.target.matches('.review-question-note-text')) noteDraft(form.dataset.message,event.target.dataset.questionId).body=event.target.value;
      progress(form,v.messages.find(message=>message.id === form.dataset.message));
    }
    saveDraft(); updateActions();
  }
  root.addEventListener('input',changed);
  root.addEventListener('change',event=>{
    if (event.target.matches('.review-file-input')) { void upload([...event.target.files]); event.target.value = ''; }
    else if (event.target.matches('.review-note-file-input')) { const box=event.target.closest('.review-question-note'),messageID=event.target.closest('form').dataset.message; void upload([...event.target.files],{messageID,questionID:box.dataset.questionId});event.target.value=''; }
    else changed(event);
  });
  root.addEventListener('paste',event=>{
    if (!event.target.matches('.review-question-note-text')) return;
    const incoming=[...(event.clipboardData?.files || [])];
    if (!incoming.length) return;
    event.preventDefault();
    if (saving || uploading || event.target.disabled) return;
    const text=event.clipboardData.getData('text/plain');
    if (text) { event.target.setRangeText(text,event.target.selectionStart,event.target.selectionEnd,'end');changed(event); }
    void upload(incoming,{messageID:event.target.closest('form').dataset.message,questionID:event.target.dataset.questionId});
  });
  input.addEventListener('paste',event=>{const pasted = [...(event.clipboardData?.files || [])]; if (pasted.length) { event.preventDefault(); void upload(pasted); }});
  input.addEventListener('dragover',event=>{if (event.dataTransfer?.types.includes('Files')) event.preventDefault();});
  input.addEventListener('drop',event=>{if (event.dataTransfer?.files.length) { event.preventDefault(); void upload([...event.dataTransfer.files]); }});
  input.addEventListener('keydown',event=>{ if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void submit(); } });
  root.addEventListener('keydown',event=>{
    if (event.key !== 'Escape') return;
    const open = !$('.rc-menu').hidden || !$('.rc-pop').hidden || sourceView;
    if (!open) return;
    event.preventDefault(); event.stopPropagation();
    closeMenu(); renderPop(''); sourceView = null; renderSource();
  });
  root.addEventListener('click',event=>{
    if (!event.target.closest('.rc-menu-wrap')) closeMenu();
    if (!event.target.closest('.rc-pop, .rc-relations-toggle, [data-review-action=related]')) renderPop('');
    const button = event.target.closest('[data-review-action]'); if (!button || button.matches(':disabled')) return;
    event.preventDefault(); event.stopPropagation();
    const action = button.dataset.reviewAction;
    if (action === 'details' || action === 'related') { if (uploading || saving) return; saveDraft(); onDetails(action==='related'?button.dataset.task:undefined); return; }
    if (action === 'decline' || action === 'close-task' || action === 'reopen') { closeMenu(); if (uploading || saving) return; saveDraft(); onResolve?.(action==='decline'?'decline':action==='reopen'?'reopen':'close'); return; }
    if (action === 'menu') { const menu = $('.rc-menu'); menu.hidden = !menu.hidden; button.setAttribute('aria-expanded',String(!menu.hidden)); if (!menu.hidden) menu.querySelector('button')?.focus(); return; }
    if (action === 'relations') { renderPop($('.rc-pop').hidden ? 'relations' : ''); return; }
    if (action === 'pop-close') { renderPop(''); return; }
    if (action === 'source') { sourceView = sourceView && !button.closest('.rc-event') ? null : {kind:'current'}; renderSource(); return; }
    if (action === 'source-latest') { sourceView = {kind:'current'}; renderSource(); return; }
    if (action === 'source-close') { sourceView = null; renderSource(); return; }
    if (action === 'snapshot' || action === 'diff') { sourceView = {kind:action,message:button.dataset.message}; renderSource(); return; }
    if (action === 'topic') {
      const target = v?.messages.findLast(m => m.result?.ask?.topic_id === button.dataset.topic);
      const row = target && node(target.id);
      if (row) { const top = row.getBoundingClientRect().top - feed.getBoundingClientRect().top + feed.scrollTop - 12; feed.scrollTop = top; row.querySelector('.rc-card, .rc-ask')?.classList.add('rc-flash'); setTimeout(()=>row.querySelector('.rc-flash')?.classList.remove('rc-flash'),1200); }
      return;
    }
    if (action === 'quick') { input.value = input.value.trim() ? `${input.value.trimEnd()}\n${button.dataset.text}` : button.dataset.text; input.focus(); saveDraft(); updateActions(); return; }
    if (action === 'attach-note') { if (saving || uploading) return; button.closest('.review-question-note').querySelector('.review-note-file-input').click(); return; }
    if (action === 'remove-note-file') {
      if (saving || uploading) return;
      const box=button.closest('.review-question-note'),form=button.closest('form'),messageID=form.dataset.message,note=noteDraft(messageID,box.dataset.questionId);
      note.files=note.files.filter(file=>file.id!==button.dataset.file);noteFileList(box,messageID);progress(form,v.messages.find(message=>message.id===messageID));saveDraft();updateActions();return;
    }
    if (action === 'attach') { $('.review-file-input').click(); return; }
    if (action === 'remove-file') { if (saving || uploading) return; files = files.filter(file=>file.id !== button.dataset.file); fileList(); saveDraft(); updateActions(); return; }
    if (action === 'recommend') {
      if (saving || uploading) return;
      const form = button.closest('form'), message = v.messages.find(message=>message.id === form.dataset.message), question = message.result.questions[Number(button.dataset.question)];
      for (const element of form.querySelectorAll(`[name="question_${button.dataset.question}"]`)) {
        if (element.type === 'checkbox' || element.type === 'radio') element.checked = question.recommended.includes(element.value);
        else element.value = question.recommended[0] || '';
      }
      answers[message.id] = readAnswers(form); progress(form,message); saveDraft(); updateActions(); return;
    }
    if (['confirm','confirm_rejection','retry'].includes(action)) void operate(action);
  });
  fileList(); updateActions();
  void command({action:'start',request_id:createID()}).catch(error=>{
    if (!disposed) $('.review-send-error').textContent = `需求已保存，评审暂时无法打开：${error.message}。可稍后重新进入。`;
  });
  const timer = setInterval(poll,1500);
  return {get busy(){return uploading;},destroy(){saveDraft();disposed = true;clearInterval(timer);}};
}
