const escape = value => String(value ?? '').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export function pageQuery(page = 1, pageSize = 20) { return new URLSearchParams({page:String(page),page_size:String(pageSize)}); }
export function pagerHTML(result, key) {
 if (!result) return '';
 const {total=0,page=1,page_size=20}=result, pages=Math.max(1,Math.ceil(total/page_size));
 return `<nav class="list-pagination" aria-label="列表分页" data-pager="${escape(key)}"><span class="hint">共 ${total} 项 · 第 ${page} / ${pages} 页</span><div class="row"><label class="hint">每页 <select data-page-size="${escape(key)}" aria-label="每页条数">${[20,50,100].map(size=>`<option value="${size}" ${size===page_size?'selected':''}>${size}</option>`).join('')}</select></label><button type="button" data-action="list-page" data-list="${escape(key)}" data-page="${page-1}" ${page<=1?'disabled':''}>上一页</button><button type="button" data-action="list-page" data-list="${escape(key)}" data-page="${page+1}" ${page>=pages?'disabled':''}>下一页</button></div></nav>`;
}
const markup = new WeakMap();
const sameKind = (a,b) => a.nodeType===b.nodeType && a.nodeName===b.nodeName && a.namespaceURI===b.namespaceURI;
function keyOf(node,keyAttribute) {
 if(node.nodeType!==1)return null;
 for(const attr of [keyAttribute,'id','data-run-id'])if(node.hasAttribute(attr))return `${node.nodeName}:${attr}:${node.getAttribute(attr)}`;
 return null;
}
function patchNode(current,next,keyAttribute) {
 if(current.nodeType===3||current.nodeType===8){if(current.data===next.data)return false;current.data=next.data;return true;}
 let changed=false;
 // User-owned state survives a server refresh. Validation still reads current
 // versions from APIs before a mutation, rather than trusting selected rows.
 for(const attr of [...current.attributes]){
  if(!next.hasAttribute(attr.name)&&!(attr.name==='open'&&current.nodeName==='DETAILS')){current.removeAttribute(attr.name);changed=true;}
 }
 for(const attr of [...next.attributes]){
  if(attr.name==='open'&&current.nodeName==='DETAILS')continue;
  if(current.getAttribute(attr.name)!==attr.value){current.setAttribute(attr.name,attr.value);changed=true;}
 }
 if(['TEXTAREA','INPUT','SELECT'].includes(current.nodeName))return changed;
 return patchChildren(current,next,keyAttribute)||changed;
}
function patchChildren(current,next,keyAttribute){
 let changed=false,cursor=current.firstChild;
 const keyed=new Map([...current.childNodes].map(node=>[keyOf(node,keyAttribute),node]).filter(([key])=>key));
 const retained=new Set();
 for(const desired of [...next.childNodes]){
  const key=keyOf(desired,keyAttribute);
  let node=key?keyed.get(key):cursor;
  if(!key&&node&&keyOf(node,keyAttribute))node=null;
  if(!node||!sameKind(node,desired)){node=desired.cloneNode(true);current.insertBefore(node,cursor);changed=true;}
  else{changed=patchNode(node,desired,keyAttribute)||changed;if(node!==cursor){current.insertBefore(node,cursor);changed=true;}}
  retained.add(node);cursor=node.nextSibling;
 }
 for(const node of [...current.childNodes])if(!retained.has(node)){node.remove();changed=true;}
 return changed;
}
// Escaped application HTML only. Reconciliation follows business IDs, not row
// positions, so an insertion never transfers a user's selection to another row.
export function patchKeyedHTML(root,html,keyAttribute='data-key'){
 if(!root||markup.get(root)===html)return false;
 const template=root.ownerDocument.createElement('template');template.innerHTML=html;
 const changed=patchChildren(root,template.content,keyAttribute);markup.set(root,html);return changed;
}
