import test from 'node:test';
import assert from 'node:assert/strict';
import {routeFromURL, routeURL, allowedPage, firstAccessiblePage, navigationHTML, expandedForPage} from './static/navigation.mjs';

test('routes preserve deployment mount and unrelated parameters, and reject unknown pages', () => {
  assert.deepEqual(routeFromURL('https://example.invalid/work/?page=git&project=p1'), {page:'git',project:'p1'});
  assert.equal(routeFromURL('/?page=constructor').page, 'overview');
  assert.equal(routeURL('https://example.invalid/work/?task=t&review_message=r&keep=yes', 'access', 'p2'), '/work/?keep=yes&page=access&project=p2');
});

test('permission filtering removes empty groups and never grants access through the parent', () => {
  const access = {pages:['versions','git']};
  const html = navigationHTML({access,page:'git',collapsed:['requirements'],icon:() => '',escape:String});
  assert.match(html, /项目需求/); assert.match(html, /项目管理/);
  assert.doesNotMatch(html, /data-page="tasks"/); assert.doesNotMatch(html, /data-page="access"/);
  assert.match(html, /id="nav-group-requirements" hidden/);
  assert.match(html, /aria-current="page"/);
  assert.equal(allowedPage('access',access),false);
  assert.equal(allowedPage('platform',access),false);
  assert.equal(firstAccessiblePage(access),'versions');
  assert.equal(firstAccessiblePage({pages:[]}),'mcp');
  assert.doesNotMatch(navigationHTML({access:{pages:['overview']},page:'overview',icon:()=>'',escape:String}), /nav-group/);
});

test('navigation only expands the active group', () => {
  assert.deepEqual(expandedForPage(['requirements','management'],'git'), ['requirements']);
  assert.deepEqual(expandedForPage(['requirements','management'],'chat'), ['requirements','management']);
});
