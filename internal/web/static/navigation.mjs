export const projectPages = [
  {id: 'overview', title: '项目概览', icon: 'grid'},
  {id: 'tasks', title: '需求池', icon: 'list'},
  {id: 'reviews', title: '验收与交付', icon: 'check'},
  {id: 'versions', title: '项目版本', icon: 'grid'},
  {id: 'timeline', title: '项目时间线', icon: 'code'},
  {id: 'chat', title: '项目问答', icon: 'spark'},
  {id: 'code', title: '代码与变更', icon: 'code'},
  {id: 'settings', title: '项目设置', icon: 'settings'},
  {id: 'git', title: 'Git 设置', icon: 'code'},
  {id: 'access', title: '角色与权限', icon: 'settings'},
];
export const pageTitles = Object.fromEntries([...projectPages, {id:'mcp',title:'我的 MCP'}, {id:'platform',title:'平台设置'}].map(p => [p.id, p.title]));
export const navigationGroups = [
  {id:'requirements', title:'项目需求', icon:'list', pages:['tasks','reviews','versions','timeline']},
  {id:'management', title:'项目管理', icon:'settings', pages:['settings','git','access']},
];

export function routeFromURL(href) {
  const params = new URL(href, 'http://localhost').searchParams;
  const page = params.get('page');
  return {page: Object.hasOwn(pageTitles, page) ? page : 'overview', project: params.get('project') || ''};
}

export function routeURL(href, page, project) {
  const url = new URL(href, 'http://localhost');
  url.searchParams.delete('task');
  url.searchParams.delete('review_message');
  if (Object.hasOwn(pageTitles, page)) url.searchParams.set('page', page);
  else url.searchParams.delete('page');
  if (project) url.searchParams.set('project', project);
  else url.searchParams.delete('project');
  return url.pathname + url.search + url.hash;
}

export function allowedPage(page, access, platformAdmin = false) {
  if (page === 'mcp') return true;
  if (page === 'platform') return platformAdmin;
  return access?.pages?.includes(page) === true;
}

export function firstAccessiblePage(access) {
  return projectPages.find(p => access?.pages?.includes(p.id))?.id || 'mcp';
}

export function navigationHTML({access, page, collapsed = [], icon, escape, badge = () => ''}) {
  const allowed = new Set(access?.pages || []);
  const button = id => {
    const p = projectPages.find(p => p.id === id);
    return allowed.has(id) ? `<button type="button" data-action="navigate" data-page="${id}" aria-label="${p.title}" ${page===id?'aria-current="page"':''} class="${page===id?'active':''}">${icon(p.icon)}${p.title}${badge(id)}</button>` : '';
  };
  const group = id => {
    const g = navigationGroups.find(g => g.id === id);
    const children = g.pages.filter(id => allowed.has(id));
    if (!children.length) return '';
    const open = !collapsed.includes(g.id);
    return `<section class="nav-group"><button type="button" class="nav-group-toggle ${children.includes(page)?'is-current':''}" data-action="toggle-nav-group" data-group="${g.id}" aria-expanded="${open}" aria-controls="nav-group-${g.id}">${icon(g.icon)}<span>${escape(g.title)}</span><span class="nav-group-chevron">${icon('chevron')}</span></button><div class="nav-children" id="nav-group-${g.id}" ${open?'':'hidden'}>${children.map(button).join('')}</div></section>`;
  };
  return button('overview') + group('requirements') + button('chat') + button('code') + group('management');
}

export function expandedForPage(collapsed, page) {
  return collapsed.filter(id => !navigationGroups.find(g => g.id === id)?.pages.includes(page));
}
