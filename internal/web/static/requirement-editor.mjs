import { Editor, StarterKit, Image, Placeholder, TableKit, Markdown } from './vendor/requirement-editor.mjs';
import { renderMarkdown } from './markdown.mjs';
import { BASE, savedLinks } from './base.mjs';
import DOMPurify from './vendor/dompurify.mjs';

const esc = (value) => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[c]);
const size = (n) => n < 1048576 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / 1048576).toFixed(1)} MB`;
const templates = {
  requirement: '## 背景与目标\n\n描述当前问题、目标用户和希望达成的结果。\n\n## 需求范围\n\n- 本次需要实现的功能\n- 不在本次范围内的内容\n\n## 交互与规则\n\n描述操作流程、业务规则及异常情况，可在对应位置插入截图。',
  bug: '## 问题现象\n\n描述实际发生的问题及影响范围。\n\n## 复现步骤\n\n1. 前置条件与操作入口\n2. 具体操作\n3. 实际结果\n\n## 预期结果\n\n描述正确的行为。\n\n## 环境与证据\n\n记录环境、版本和发生时间，在此插入截图或日志。',
};
const tools = [['heading','H₂','二级标题'],['bold','B','加粗'],['italic','I','斜体'],['strike','S','删除线'],['bullet','☷','无序列表'],['ordered','1.','有序列表'],['quote','❞','引用'],['code','〈/〉','代码块'],['table','▦','插入表格'],['link','↗','插入链接'],['undo','↶','撤销'],['redo','↷','重做']];
const accept = 'image/png,image/jpeg,image/gif,image/webp,.pdf,.docx,text/*,.md,.log,.json,.csv,.yaml,.yml,.xml,.html,.css,.js,.ts,.tsx,.jsx,.vue,.go,.py,.sql,.sh,.java,.c,.cpp,.h,.toml,.ini,.conf';
export function requirementFormHTML(task = {}) {
  return `<form id="task-form" data-id="${esc(task.id || '')}" data-version="${task.version || 0}" novalidate>
    <div class="requirement-layout"><main class="requirement-document">
      <label class="sr-only" for="requirement-title">需求标题</label><input id="requirement-title" class="requirement-title" name="title" placeholder="为这个需求起一个清晰的标题" maxlength="200" required autocomplete="off">
      <div class="document-heading"><label id="description-label">需求描述</label><div class="row"><button type="button" class="ghost" data-editor-action="template">插入填写模板</button><button type="button" class="ghost" data-editor-action="preview" aria-pressed="false">预览</button></div></div>
      <div class="document-editor" id="requirement-dropzone"><div class="document-toolbar" role="toolbar" aria-label="文档格式">${tools.map(([cmd, label, title]) => `<button type="button" data-editor-action="${cmd}" aria-label="${title}" title="${title}">${label}</button>`).join('')}<span class="toolbar-divider"></span><button type="button" data-editor-action="image" title="插入图片" aria-label="插入图片">图片</button><button type="button" data-editor-action="files" title="添加附件" aria-label="添加附件">附件</button></div>
      <div class="document-link" hidden><input type="url" aria-label="链接地址" placeholder="https://"><button type="button" data-editor-action="apply-link">插入</button><button type="button" data-editor-action="cancel-link">取消</button></div>
      <div class="document-table-tools" hidden><span>表格</span><button type="button" data-editor-action="add-row">添加行</button><button type="button" data-editor-action="add-column">添加列</button><button type="button" data-editor-action="delete-row">删除行</button><button type="button" data-editor-action="delete-column">删除列</button><button type="button" data-editor-action="delete-table">删除表格</button></div>
      <div id="requirement-editor"></div><div class="document-preview markdown-content" hidden></div>
      <div class="document-hint">可直接粘贴截图，或拖入图片和文档<span class="document-count"></span></div></div>
      <section class="requirement-attachments"><div class="document-heading"><h3>附件 <span class="attachment-count">0</span></h3><button type="button" class="ghost" data-editor-action="files">＋ 添加文件</button></div><p class="hint">图片、PDF、Word（DOCX）、文本 · 单个 10 MB，最多 6 个，合计 20 MB</p><div class="requirement-file-list"></div><p class="upload-status" role="status" aria-live="polite"></p></section>
      <input type="file" id="requirement-files" hidden multiple accept="${accept}"><input type="file" id="requirement-images" hidden multiple accept="image/png,image/jpeg,image/gif,image/webp">
    </main><aside class="requirement-properties"><h3>需求属性</h3><fieldset class="requirement-kind"><legend>类型</legend><label><input type="radio" name="kind" value="requirement"><span>迭代需求</span></label><label><input type="radio" name="kind" value="bug"><span>Bug 修复</span></label></fieldset>
      <label class="field"><span>优先级</span><select name="priority"><option value="now">现在做</option><option value="next">接下来做</option><option value="later">暂存</option></select></label>
      <label class="field"><span>验收负责人</span><input name="reviewer" placeholder="可选"></label>
      <label class="field acceptance-field"><span>验收条件</span><textarea name="acceptance" rows="6" placeholder="每行一条可验证的条件"></textarea><small class="hint">明确什么结果代表需求完成。</small></label>
      <p class="requirement-state-note">${task.id ? '保存内容变更会更新需求修订。' : '需求先保存成功，再进入独立评审对话；可以随时补充或纠正 Agent。'}</p>
    </aside></div><footer class="requirement-footer"><div class="requirement-feedback"><p class="form-error" role="alert"></p><span class="draft-status" role="status"></span><button type="button" class="ghost discard-draft" data-editor-action="discard" hidden>放弃草稿</button></div><div class="row"><button type="button" data-action="close">暂存并关闭</button><button type="submit" class="primary">${task.id ? '保存修订' : '创建需求'}</button></div></footer>
  </form>`;
}

export function mountRequirementEditor(form, { task = {}, projectID, userID, onDiscard, onUnauthorized, draftScope, sourceVersion, parentRequirement }) {
  const $ = (s) => form.querySelector(s);
  const key = `pm-requirement-draft:${userID}:${projectID}:${draftScope || task.id || 'new'}`;
  let draft;
  try { draft = JSON.parse(localStorage.getItem(key)); } catch { /* unavailable storage */ }
  let files = [...(draft?.attachments || task.attachments || [])];
  let disposed = false, completed = false, submitting = false, pending = 0, timer, request = draft?.request, descriptionChanged = false;
  const initial = draft || task;
  form.elements.title.value = initial.title || '';
  form.elements.kind.value = initial.kind || 'requirement';
  form.elements.priority.value = initial.priority || 'next';
  form.elements.reviewer.value = initial.reviewer || '';
  form.elements.acceptance.value = (initial.acceptance || []).join('\n');
  if (draft) form.dataset.version = draft.version || 0;
  const stale = draft && ((task.id && draft.version !== task.version) || (sourceVersion != null && draft.source_version !== sourceVersion) || (parentRequirement && draft.parent_version !== parentRequirement.version));
  let positions = [];
  const editor = new Editor({
    element: $('#requirement-editor'),
    extensions: [StarterKit.configure({heading:{levels:[1,2,3]}, link:{openOnClick:false}, underline:false}), Image, TableKit, Markdown, Placeholder.configure({placeholder:'写下背景、目标、交互规则和边界情况…\n可以从填写模板开始，也可以直接粘贴截图。'})],
    // Sanitize legacy Markdown and pasted HTML before it enters the editor.
    content: renderMarkdown(initial.description || ''),
    editorProps: {
      transformPastedHTML: (html) => DOMPurify.sanitize(html, {USE_PROFILES:{html:true}, FORBID_TAGS:['form','input','button','select','textarea','style'], FORBID_ATTR:['style']}),
      attributes: { role:'textbox', 'aria-multiline':'true', 'aria-labelledby':'description-label', class:'requirement-prose markdown-content' },
      handlePaste(view, event) {
        const incoming = [...(event.clipboardData?.files || [])];
        if (!incoming.length) return false;
        event.preventDefault(); void upload(incoming); return true;
      },
      handleDrop(view, event) {
        const incoming = [...(event.dataTransfer?.files || [])];
        if (!incoming.length) return false;
        event.preventDefault();
        void upload(incoming, view.posAtCoords({left:event.clientX, top:event.clientY})?.pos);
        return true;
      },
    },
    onUpdate: () => { descriptionChanged = true; changed(); },
    onSelectionUpdate: () => toolbarState(),
    onTransaction: ({transaction}) => { for (const position of positions) position.pos = transaction.mapping.map(position.pos); },
  });
  function snapshot() {
    return {title:form.elements.title.value, kind:form.elements.kind.value, priority:form.elements.priority.value, reviewer:form.elements.reviewer.value, acceptance:form.elements.acceptance.value.split('\n'), description:descriptionChanged ? savedLinks(editor.getMarkdown()) : (initial.description || ''), attachments:files, version:Number(form.dataset.version), ...(sourceVersion != null ? {source_version:draft?.source_version ?? sourceVersion} : {}), ...(parentRequirement ? {parent_requirement_id:parentRequirement.id,parent_version:draft?.parent_version ?? parentRequirement.version} : {})};
  }
  function saveDraft() {
    if (disposed || completed) return;
    try {
      localStorage.setItem(key, JSON.stringify({...snapshot(), request}));
      $('.draft-status').textContent = stale ? '草稿基于旧修订，请先复制内容，再放弃草稿载入最新版本' : '草稿已保存在此浏览器';
      $('.discard-draft').hidden = false;
    } catch { $('.draft-status').textContent = '浏览器无法保存草稿，请及时保存需求'; }
  }
  function changed() {
    if (disposed || completed) return;
    $('.document-count').textContent = `${editor.getText().length} 字`;
    $('.draft-status').textContent = '正在保存草稿…';
    clearTimeout(timer); timer = setTimeout(saveDraft, 250);
    toolbarState();
  }
  function toolbarState() {
    if (disposed) return;
    for (const [cmd, type] of Object.entries({heading:'heading',bold:'bold',italic:'italic',strike:'strike',bullet:'bulletList',ordered:'orderedList',quote:'blockquote',code:'codeBlock',link:'link'})) {
      $(`[data-editor-action="${cmd}"]`).setAttribute('aria-pressed', String(editor.isActive(type)));
    }
    $('.document-table-tools').hidden = !editor.isActive('table') || !$('.document-preview').hidden;
  }
  function url(file) { return `${BASE}/api/projects/${encodeURIComponent(projectID)}/attachments/${encodeURIComponent(file.id)}`; }
  function showFiles() {
    $('.attachment-count').textContent = files.length;
    $('.requirement-file-list').innerHTML = files.length ? files.map(file => `<div class="requirement-file"><a href="${url(file)}" download="${esc(file.name)}" class="requirement-file-info">${file.kind === 'image' ? `<img src="${url(file)}?preview=1" alt="">` : '<span class="file-glyph">DOC</span>'}<span><strong>${esc(file.name)}</strong><small>${size(file.size)}${file.truncated ? ' · 分析时仅提取部分正文' : ''}</small></span></a><button type="button" class="ghost" data-editor-action="insert-file" data-file="${esc(file.id)}" aria-label="插入正文：${esc(file.name)}">插入正文</button><button type="button" class="ghost" data-editor-action="remove-file" data-file="${esc(file.id)}" aria-label="移除 ${esc(file.name)}">×</button></div>`).join('') : '<p class="attachment-empty">还没有附件。截图可插入正文，与对应说明放在一起。</p>';
  }
  function insertFile(file, pos = editor.state.selection.to) {
    if (!$('.document-preview').hidden) togglePreview();
    const content = file.kind === 'image' ? {type:'image',attrs:{src:url(file)+'?preview=1',alt:file.name}} : {type:'paragraph',content:[{type:'text',text:file.name,marks:[{type:'link',attrs:{href:url(file)}}]}]};
    editor.chain().focus().insertContentAt(Math.min(pos, editor.state.doc.content.size), [content,{type:'paragraph'}]).run();
  }
  async function upload(incoming, pos = editor.state.selection.to) {
    if (submitting || disposed) return;
    if (pending) { $('.upload-status').textContent = '附件正在上传，请完成后继续添加'; return; }
    pending++;
    const marker = {pos}; positions.push(marker);
    updateBusy();
    const errors = [];
    for (const file of incoming) {
      if (files.length >= 6) { errors.push('每个需求最多添加 6 个附件'); break; }
      if (!file.size || file.size > 10 * 1048576) { errors.push(`${file.name}：文件为空或超过 10 MB`); continue; }
      if (files.reduce((n, a) => n + a.size, 0) + file.size > 20 * 1048576) { errors.push('附件总大小不能超过 20 MB'); continue; }
      $('.upload-status').textContent = `正在上传 ${file.name}…`;
      try {
        const data = new FormData(); data.append('file',file,file.name || '截图.png');
        const response = await fetch(`${BASE}/api/projects/${encodeURIComponent(projectID)}/attachments`, {method:'POST', body:data});
        if (response.status === 401) { onUnauthorized?.(); throw new Error('登录已过期，请重新登录'); }
        const result = await response.json();
        if (!response.ok) throw new Error(result.error || '上传失败，请重试');
        if (disposed) break;
        files.push(result); insertFile(result, marker.pos); marker.pos = editor.state.selection.to;
        showFiles(); saveDraft();
      } catch (error) { errors.push(`${file.name}：${error.message}`); }
    }
    positions = positions.filter(p => p !== marker); pending--;
    if (disposed) return;
    $('.upload-status').textContent = errors.join('；') || '附件已上传并插入正文';
    $('.upload-status').classList.toggle('upload-error', errors.length > 0);
    updateBusy(); saveDraft();
  }
  function updateBusy() {
    const busy = pending > 0 || submitting;
    form.querySelector('button[type=submit]').disabled = busy;
    for (const button of form.querySelectorAll('[data-editor-action=files], [data-editor-action=image], [data-editor-action=remove-file]')) button.disabled = busy;
  }
  function togglePreview() {
    const preview = $('.document-preview');
    const show = preview.hidden;
    if (show) preview.innerHTML = renderMarkdown(editor.getMarkdown()) || '<p class="muted">尚未填写描述</p>';
    preview.hidden = !show;
    $('#requirement-editor').hidden = show;
    $('.document-toolbar').hidden = show;
    $('.document-link').hidden = true;
    $('[data-editor-action=preview]').textContent = show ? '继续编辑' : '预览';
    $('[data-editor-action=preview]').setAttribute('aria-pressed', String(show));
    toolbarState();
  }
  $('.document-link input').addEventListener('keydown', event => { if (event.key === 'Enter') { event.preventDefault(); $('[data-editor-action=apply-link]').click(); } });
  form.addEventListener('input', event => { if (event.target.matches('input[name], textarea[name], select[name]')) changed(); });
  form.addEventListener('change', event => {
    if (event.target.type === 'file') { void upload([...event.target.files]); event.target.value = ''; }
    else if (event.target.name) changed();
  });
  form.addEventListener('click', event => {
    const button = event.target.closest('[data-editor-action]');
    if (!button || submitting) return;
    const cmd = button.dataset.editorAction;
    if (cmd === 'files' || cmd === 'image') { $(cmd === 'files' ? '#requirement-files' : '#requirement-images').click(); return; }
    if (cmd === 'preview') { togglePreview(); return; }
    if (cmd === 'discard') {
      if (pending || !confirm('放弃此浏览器中的草稿？已保存的需求不受影响。')) return;
      completed = true; localStorage.removeItem(key); onDiscard(); return;
    }
    if (cmd === 'template') {
      if (!$('.document-preview').hidden) togglePreview();
      editor.chain().focus().insertContent(renderMarkdown(templates[form.elements.kind.value])).run(); return;
    }
    if (cmd === 'insert-file') { insertFile(files.find(f => f.id === button.dataset.file)); return; }
    if (cmd === 'remove-file') {
      const file = files.find(f => f.id === button.dataset.file);
      const tr = editor.state.tr, remove = [];
      editor.state.doc.descendants((node, pos) => {
        if (node.type.name === 'image' && node.attrs.src === url(file)+'?preview=1') remove.push([pos, pos+node.nodeSize]);
        if (node.isText && node.marks.some(m => m.type.name === 'link' && m.attrs.href === url(file))) tr.removeMark(pos, pos+node.nodeSize, editor.schema.marks.link);
      });
      for (const [from,to] of remove.reverse()) tr.delete(from,to);
      editor.view.dispatch(tr); files = files.filter(f => f.id !== file.id); showFiles(); changed(); return;
    }
    if (cmd === 'link') { $('.document-link').hidden = false; $('.document-link input').value = editor.getAttributes('link').href || ''; $('.document-link input').focus(); return; }
    if (cmd === 'cancel-link') { $('.document-link').hidden = true; editor.commands.focus(); return; }
    if (cmd === 'apply-link') {
      const href = $('.document-link input').value.trim();
      if (!/^(https?:\/\/|mailto:|\/(?!\/))/i.test(href)) { $('.form-error').textContent = '请填写有效的 http、https 或邮件链接'; return; }
      if (editor.state.selection.empty) editor.chain().focus().insertContent({type:'text',text:href,marks:[{type:'link',attrs:{href}}]}).run();
      else editor.chain().focus().extendMarkRange('link').setLink({href}).run();
      $('.document-link').hidden = true; $('.form-error').textContent = ''; return;
    }
    const chain = editor.chain().focus();
    const commands = {heading:()=>chain.toggleHeading({level:2}),bold:()=>chain.toggleBold(),italic:()=>chain.toggleItalic(),strike:()=>chain.toggleStrike(),bullet:()=>chain.toggleBulletList(),ordered:()=>chain.toggleOrderedList(),quote:()=>chain.toggleBlockquote(),code:()=>chain.toggleCodeBlock(),table:()=>chain.insertTable({rows:3,cols:3,withHeaderRow:true}),undo:()=>chain.undo(),redo:()=>chain.redo(),'add-row':()=>chain.addRowAfter(),'add-column':()=>chain.addColumnAfter(),'delete-row':()=>chain.deleteRow(),'delete-column':()=>chain.deleteColumn(),'delete-table':()=>chain.deleteTable()};
    commands[cmd]?.().run();
  });
  // Keep the editor selection when clicking a toolbar command.
  $('.document-toolbar').addEventListener('mousedown', e => { if (e.target.closest('button')) e.preventDefault(); });
  const dropzone = $('#requirement-dropzone');
  dropzone.addEventListener('dragover', e => { if ([...(e.dataTransfer?.types || [])].includes('Files')) { e.preventDefault(); dropzone.classList.add('drag-over'); } });
  dropzone.addEventListener('dragleave', e => { if (!dropzone.contains(e.relatedTarget)) dropzone.classList.remove('drag-over'); });
  dropzone.addEventListener('drop', e => {
    dropzone.classList.remove('drag-over');
    if (e.defaultPrevented || !e.dataTransfer?.files.length) return;
    e.preventDefault(); void upload([...e.dataTransfer.files]);
  });
  function beforeUnload(e) { if (pending || submitting) { e.preventDefault(); e.returnValue = ''; } else saveDraft(); }
  window.addEventListener('beforeunload', beforeUnload);
  showFiles(); toolbarState();
  $('.document-count').textContent = `${editor.getText().length} 字`;
  $('.draft-status').textContent = draft ? (stale ? '草稿基于旧修订，请先复制内容，再放弃草稿载入最新版本' : '已恢复此浏览器中的草稿') : '填写内容自动暂存至此浏览器';
  $('.discard-draft').hidden = !draft;
  return {
    get busy() { return pending > 0 || submitting; },
    payload(createID) {
      if (pending) throw new Error('附件正在上传，请稍候');
      const value = snapshot();
      if (!value.title.trim()) { form.elements.title.focus(); throw new Error('请填写需求标题'); }
      if (new TextEncoder().encode(value.description).length > 60000) throw new Error('需求描述过长，请将补充内容整理为文档附件');
      const payload = {...value, attachment_ids:files.map(f=>f.id)}; delete payload.attachments;
      // An uncertain response can be retried with the same request ID and content.
      const fingerprint = JSON.stringify(payload);
      if (!request || request.fingerprint !== fingerprint) request = {fingerprint,id:createID()};
      saveDraft();
      return {...payload,request_id:request.id};
    },
    setSubmitting(value) { submitting = value; editor.setEditable(!value, false); for (const field of form.querySelectorAll('input[name],textarea[name],select[name]')) field.disabled = value; updateBusy(); },
    complete() { completed = true; submitting = false; clearTimeout(timer); try { localStorage.removeItem(key); } catch {} },
    destroy() { clearTimeout(timer); saveDraft(); disposed = true; window.removeEventListener('beforeunload',beforeUnload); editor.destroy(); },
  };
}
