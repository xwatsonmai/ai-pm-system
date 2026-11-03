// Selected mentions are metadata over ordinary textarea text. Typing a name
// (including an exact match) never creates this metadata.
export function rebaseMentions(before, after, mentions = []) {
  if (before === after) return mentions;
  let start = 0, suffix = 0;
  while (start < Math.min(before.length, after.length) && before[start] === after[start]) start++;
  while (suffix < Math.min(before.length, after.length) - start && before[before.length-1-suffix] === after[after.length-1-suffix]) suffix++;
  const oldEnd = before.length - suffix, delta = after.length - before.length;
  return mentions.flatMap(m => {
    if (m.end <= start) return [m];
    if (m.start >= oldEnd) return [{...m,start:m.start+delta,end:m.end+delta}];
    return [];
  }).filter(m => after.slice(m.start,m.end) === '@'+m.name);
}

export function mentionQuery(body, start, end = start, mentions = []) {
  if (start !== end) return null;
  const match = /@([^\s@，。！？、：；（）【】<>"']{0,64})$/u.exec(body.slice(0,start));
  if (!match) return null;
  const at = start - match[0].length;
  // Leave email addresses and selected mentions alone, while allowing 请@麦.
  if (at && /[\w.+-]/.test(body[at-1])) return null;
  if (mentions.some(m => at >= m.start && at < m.end)) return null;
  return {start:at,end:start,query:match[1]};
}

const esc = value => String(value ?? '').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

export function mountReviewMentions(root, {input,api,projectID,initial = [],changed}) {
  const popup = root.querySelector('.rc-mention-popup'), selected = root.querySelector('.rc-mention-selected');
  let body = input.value, mentions = initial.filter(m=>Number.isInteger(m.start)&&Number.isInteger(m.end)&&m.start>=0&&m.end>m.start&&body.slice(m.start,m.end)==='@'+m.name);
  let members = null, loading = false, failed = false, disposed = false, composing = false, dismissed = null, active = -1, query = null, matches = [];
  function sync() { mentions = rebaseMentions(body,input.value,mentions); body = input.value; }
  function chips() {
    const unique = [...new Map(mentions.map(m=>[m.user_id,m])).values()];
    selected.hidden = !unique.length;
    selected.innerHTML = unique.length ? '<span>已选成员：</span>'+unique.map(m=>`<span class="rc-mention-chip">${esc('@'+m.name)}<button type="button" data-remove-mention="${esc(m.user_id)}" aria-label="取消提及 ${esc(m.name)}">×</button></span>`).join('') : '';
  }
  function close() {
    popup.hidden = true; input.setAttribute('aria-expanded','false'); input.removeAttribute('aria-activedescendant');
  }
  function draw() {
    if (disposed) return;
    sync(); chips();
    const next = !composing && !input.readOnly && document.activeElement === input ? mentionQuery(body,input.selectionStart,input.selectionEnd,mentions) : null;
    if (!next) { query = null; dismissed = null; failed = false; close(); return; }
    if (dismissed === next.start) { close(); return; }
    if (!query || next.query !== query.query || next.start !== query.start) active = 0;
    query = next;
    if (!members && !loading && !failed) void load();
    const needle = next.query.toLocaleLowerCase();
    matches = (members || []).filter(m => m.user.name.toLocaleLowerCase().includes(needle) || m.user.login_id?.toLocaleLowerCase().includes(needle));
    active = Math.min(Math.max(active,0),matches.length-1);
    popup.innerHTML = `<div id="review-mention-options" role="listbox" aria-label="项目成员">${matches.map((m,i)=>`<button type="button" role="option" id="review-mention-option-${i}" aria-selected="${i===active}" class="rc-mention-option" data-mention-index="${i}">${esc(m.user.name)}</button>`).join('') || `<p class="rc-mention-empty" role="status">${loading?'加载中…':failed?'暂时无法加载成员':'无匹配成员'}</p>`}</div>`;
    popup.hidden = false; input.setAttribute('aria-expanded','true');
    if (active >= 0) input.setAttribute('aria-activedescendant','review-mention-option-'+active);
    else input.removeAttribute('aria-activedescendant');
  }
  async function load() {
    loading = true;
    try { const result = await api(`/projects/${encodeURIComponent(projectID)}/members`); if (!disposed) members = result.items; }
    catch { failed = true; }
    finally { loading = false; if (!disposed) draw(); }
  }
  function choose(index) {
    if (!query || !matches[index] || input.readOnly) return;
    const {user} = matches[index], start = query.start, name = '@'+user.name;
    input.setRangeText(name+' ',start,query.end,'end');
    sync(); mentions.push({user_id:user.id,name:user.name,start,end:start+name.length}); mentions.sort((a,b)=>a.start-b.start);
    query = null; dismissed = null; close(); chips(); input.focus(); changed();
  }
  input.addEventListener('input',()=>{sync(); draw();});
  input.addEventListener('click',draw);
  input.addEventListener('keyup',e=>{if (['ArrowLeft','ArrowRight','Home','End'].includes(e.key)) draw();});
  input.addEventListener('focus',draw);
  input.addEventListener('blur',close);
  input.addEventListener('compositionstart',()=>{composing=true;close();});
  input.addEventListener('compositionend',()=>{composing=false;draw();});
  input.addEventListener('keydown',e=>{
    if (composing || e.isComposing || e.keyCode===229 || popup.hidden) return;
    if (e.key==='Escape') {e.preventDefault();e.stopPropagation();dismissed=query?.start;close();return;}
    if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
    if (['ArrowDown','ArrowUp'].includes(e.key) && matches.length) {
      e.preventDefault();e.stopPropagation();active=(active+(e.key==='ArrowDown'?1:-1)+matches.length)%matches.length;draw();popup.querySelector('[aria-selected=true]')?.scrollIntoView({block:'nearest'});
    } else if (e.key==='Enter' && active>=0) {e.preventDefault();e.stopImmediatePropagation();choose(active);}
  });
  popup.addEventListener('pointerdown',e=>e.preventDefault());
  popup.addEventListener('click',e=>{const option=e.target.closest('[data-mention-index]');if(option)choose(Number(option.dataset.mentionIndex));});
  selected.addEventListener('click',e=>{const button=e.target.closest('[data-remove-mention]');if(!button || input.readOnly)return;mentions=mentions.filter(m=>m.user_id!==button.dataset.removeMention);chips();changed();});
  chips();
  return {
    get mentions(){sync();return mentions.map(m=>({...m}));},
    clear(){mentions=[];body=input.value;close();chips();},
    refresh(){if(input.readOnly)close();sync();chips();},
    destroy(){disposed=true;close();}
  };
}
