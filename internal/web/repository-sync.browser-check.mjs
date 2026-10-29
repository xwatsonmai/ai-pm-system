// Temporary SQLite + real Git; hold clone at a gate to inspect background UI.
import assert from 'node:assert/strict';
import {spawn, execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp, mkdir, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import {once} from 'node:events';
import net from 'node:net';

const require = createRequire(process.env.AI_PM_QA_NODE_MODULES
  ? join(process.env.AI_PM_QA_NODE_MODULES, 'package.json') : import.meta.url);
const {chromium} = require('playwright');
const root = await mkdtemp(join(tmpdir(), 'ai-pm-auto-sync-'));
const source = join(root, 'source'), shim = join(root, 'shim'), gate = join(root, 'release');
const output = resolve('test-results/repository-sync-ui');
await Promise.all([mkdir(source), mkdir(shim), mkdir(output, {recursive: true})]);
const realGit = execFileSync('which', ['git'], {encoding: 'utf8'}).trim();
const git = (cwd, ...args) => execFileSync(realGit, args, {
  cwd, encoding: 'utf8', env: {...process.env, GIT_AUTHOR_NAME: 'QA', GIT_AUTHOR_EMAIL: 'qa@example.invalid', GIT_COMMITTER_NAME: 'QA', GIT_COMMITTER_EMAIL: 'qa@example.invalid'},
}).trim();
async function repository(dir) {
  git(dir, 'init', '-b', 'main');
  await writeFile(join(dir, 'README.md'), '# Automatically synced code\n');
  git(dir, 'add', '.');
  git(dir, 'commit', '-m', 'initial');
}
await repository(source);
await writeFile(join(shim, 'git'), `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = clone ]; then
    while [ ! -f "$AI_PM_QA_SYNC_GATE" ]; do sleep 0.05; done
  fi
done
exec '${realGit.replaceAll("'", "'\\''")}' "$@"
`, {mode: 0o700});
const reserve = net.createServer().listen(0, '127.0.0.1');
await once(reserve, 'listening');
const port = reserve.address().port;
await new Promise(done => reserve.close(done));
const base = `http://127.0.0.1:${port}`;
const server = spawn(resolve(process.env.AI_PM_QA_BIN || 'bin/ai-pm-auto-sync-check'), [
  '-addr', `127.0.0.1:${port}`, '-data', join(root, 'state'), '-sync-interval', '0',
], {env: {...process.env, PATH: `${shim}:${process.env.PATH}`, AI_PM_QA_SYNC_GATE: gate}, stdio: ['ignore', 'ignore', 'pipe']});
const exit = once(server, 'exit');
server.stderr.resume();
const browser = await chromium.launch({headless: true});
const page = await browser.newPage({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce'});
const errors = [];
page.on('pageerror', e => errors.push(e.message));
async function create(name, repository, first = false) {
  if (first) await page.getByRole('button', {name: '创建项目', exact: true}).first().click();
  else {
    await page.locator('#project-switch').click();
    await page.locator('.project-create').click();
  }
  await page.locator('#project-form [name=name]').fill(name);
  await page.locator('#project-form [name=repository]').fill(repository);
  const response = page.waitForResponse(r => r.url() === `${base}/api/projects` && r.request().method() === 'POST');
  await page.locator('#project-form button[type=submit]').click();
  const result = await response;
  assert.equal(result.status(), 200);
  const project = await result.json();
  await page.locator('#project-form').waitFor({state: 'detached'});
  return project;
}
const nav = pageName => page.locator(`.nav [data-page=${pageName}]`).click();
try {
  for (let n = 0; n < 100; n++) {
    try { if ((await fetch(base + '/api/health')).ok) break; } catch {}
    await new Promise(done => setTimeout(done, 50));
  }
  await page.goto(base);
  const project = await create('创建后自动同步', source, true);
  assert.equal(project.syncing, true);
  assert.equal(project.sha, '');
  await page.locator('#repository-status').getByText('正在同步代码仓库，完成后即可读取和分析代码。', {exact: true}).waitFor();
  // Reload while running: status comes from the server, not local assumptions.
  await page.reload();
  await page.locator('#repository-status .notice.info').waitFor();
  await nav('code');
  await page.getByRole('heading', {name: '正在同步代码仓库', exact: true}).waitFor();
  assert.equal(await page.locator('#page [data-action=sync]').isDisabled(), true);
  await page.screenshot({path: join(output, 'syncing.png')});
  await writeFile(gate, 'go');
  await page.getByRole('button', {name: 'README.md', exact: true}).waitFor({timeout: 15000});
  await page.getByRole('button', {name: 'README.md', exact: true}).click();
  await page.locator('#file-content').getByText('# Automatically synced code', {exact: true}).waitFor();
  assert.equal(await page.locator('#repository-status .notice').count(), 0);
  await page.screenshot({path: join(output, 'synced.png')});

  // Polling must preserve an in-progress question while the clone completes.
  await rm(gate);
  await create('同步时可继续编辑', source);
  await nav('chat');
  await page.locator('textarea[name=question]').fill('这条未发送的问题应当保留');
  await writeFile(gate, 'go');
  await page.waitForFunction(() => !document.querySelector('#repository-status .notice'), null, {timeout: 15000});
  assert.equal(await page.locator('textarea[name=question]').inputValue(), '这条未发送的问题应当保留');

  const missing = join(root, 'initially-missing');
  await create('失败后可重试', missing);
  await page.locator('#repository-status').getByText(/仓库同步失败/).waitFor({timeout: 15000});
  assert.equal(await page.locator('#repository-status .notice').count(), 1);
  assert.equal(await page.locator('#project-switch').innerText().then(t => t.includes('失败后可重试')), true);
  await page.setViewportSize({width: 390, height: 844});
  await page.screenshot({path: join(output, 'failed-mobile.png')});
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  await mkdir(missing);
  await repository(missing);
  await page.getByRole('button', {name: '重试同步', exact: true}).click();
  await page.waitForFunction(() => !document.querySelector('#repository-status .notice'), null, {timeout: 15000});
  const retried = await fetch(`${base}/api/projects`).then(r => r.json()).then(ps => ps.find(p => p.name === '失败后可重试'));
  assert(retried.sha && retried.synced_at && !retried.sync_error && !retried.syncing);
  const status = await fetch(`${base}/api/projects/${project.id}/repository-status`).then(r => r.json());
  assert.equal(status.sha, git(source, 'rev-parse', 'HEAD'));
  assert.equal(status.syncing, false);
  assert.deepEqual(errors, []);
  await writeFile(join(output, 'checks.json'), JSON.stringify({automaticSync: true, reloadWhileSyncing: true, codeAppearsAutomatically: true, draftPreserved: true, failureRetainsProject: true, retry: true, mobile: true, pageErrors: errors}, null, 2));
  console.log('Repository auto-sync browser checks passed');
} finally {
  await browser.close();
  server.kill('SIGTERM');
  await exit;
  await rm(root, {recursive: true, force: true});
}
