import {renderMarkdown} from './markdown.mjs';
import {BASE} from './base.mjs';
import {reviewAttention} from './requirement-review-state.mjs';
const esc = value => String(value ?? '').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export function mergeIntakes(incoming,current) {
 const latest=new Map(current.map(v=>[v.id,v]));
 return incoming.map(v=>latest.get(v.id)?.version>v.version?latest.get(v.id):v);
}
export function requirementIntakeCards(items, runID, busy=new Set(), context={}) {
 return items.filter(v=>v.origin_run_id===runID).map(v=>{
  const bug=v.kind==='bug', kind=bug?'Bug 修复':'迭代需求', created=v.status==='created', pending=v.status==='suggested';
  const disabled=busy.has(v.id)?'disabled':'';
  const task=context.tasks?.find(t=>t.id===v.task_id);
  const review=task && reviewAttention(task,context.requirement_reviews?.find(r=>r.task_id===task.id));
  const reviewBadge=review ? `<span class="badge ${review.attention?'review-attention '+(review.label==='待确认'?'pending':'failed'):''}">${review.label}</span>` : '';
  const button=(action,text,cls='')=>`<button type="button" class="${cls}" data-action="${action}" data-id="${esc(v.id)}" ${disabled}>${text}</button>`;
  const files=v.attachments?.length?`<div class="intake-files">${v.attachments.map(f=>`<a href="${BASE}/api/projects/${encodeURIComponent(v.project_id)}/attachments/${encodeURIComponent(f.id)}" download="${esc(f.name)}">${esc(f.name)}</a>`).join('')}</div>`:'';
  const createdActions=bug
   ? `<p class="hint">Bug 与附件已保存，可在详情中查看处理进度。</p><div class="intake-actions"><button type="button" class="primary" data-action="open-task" data-id="${esc(v.task_id)}">查看 Bug</button></div>`
   : `${reviewBadge}<p class="hint">需求与附件已保存，评审在后台进行。有结论后在需求池标记待确认，也可随时进入评审补充说明。</p><div class="intake-actions"><button type="button" data-action="requirement-review" data-id="${esc(v.task_id)}">进入需求评审</button><button type="button" data-action="open-task" data-id="${esc(v.task_id)}">查看需求</button></div>`;
  return `<section class="requirement-intake-card" data-intake-id="${esc(v.id)}" aria-label="${created?'已创建需求':'需求创建建议'}"><div class="intake-heading"><span class="badge">${kind}</span><span class="small muted">${created?'✓ 已保存到需求池':pending?'Agent 建议创建':'已暂不创建'}</span></div><h3>${esc(v.title)}</h3><p class="intake-reason">${esc(v.reason)}</p><details class="intake-content"><summary>查看整理内容${v.attachments?.length?' · '+v.attachments.length+' 个附件':''}</summary><div class="markdown-content">${renderMarkdown(v.description)}</div>${v.acceptance?.length?`<h4>${bug?'验收条件':'验收建议'}</h4><ul>${v.acceptance.map(x=>`<li>${esc(x)}</li>`).join('')}</ul>`:''}${files}</details>${created?`<p class="small mono intake-task-id">${esc(v.task_id)}</p>${createdActions}`:pending?`<p class="hint">确认后，以上内容与附件将保存到项目需求池${bug?'，Bug 直接进入待认领':''}。也可以直接回复修改意见或说“创建吧”。</p><div class="intake-actions">${button('create-intake',bug?'创建并交给开发':'创建并评审','primary')}${button('dismiss-intake','暂不创建','ghost')}</div>`:'<p class="hint">建议保留在对话中。需要重新整理时，可以直接告诉 Agent。</p>'}<p class="intake-error form-error" role="alert"></p></section>`;
 }).join('');
}
