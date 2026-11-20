// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// gowebserver embedded in another program (gowebserver.NewHandler), at
// /media/ beside the host's own API and single-page app: everything the
// file browser, the gallery and the text viewer link to or load stays
// under /media/, and the host's app still works at /.
//
// With CRAWL_LOG set, the requests are written there as JSON; with
// SCREENSHOTS set, screenshots are saved in that folder.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { serveEmbedded, open, png } = require('../../browser.js');

let srv, b;
// Every request the browser made, and the page it was on.
const requests = [];

test.before(async () => {
  const files = {
    'notes.txt': 'notes\n'.repeat(40),
    'trips/2024/readme.md': '# Rome\n\nTwo weeks.\n',
    'trips/2025/readme.md': '# Paris\n',
    'old archive name #1/note.txt': 'odd names stay under the mount\n',
  };
  for (let i = 0; i < 6; i++) files['trips/2024/rome-' + i + '.png'] = png(800, 600, i * 40, 90, 30);
  for (let i = 0; i < 3; i++) files['trips/2025/paris-' + i + '.png'] = png(600, 800, 200, i * 50, 120);
  srv = await serveEmbedded({ photos: 24, files, args: ['-title', 'Media', '-diagnostics', 'diag.log'] });
  b = await open();
  // The context's, so beacons sent as a page is left are seen too.
  b.page.context().on('request', (r) => requests.push({ url: r.url(), method: r.method(), type: r.resourceType(), page: b.page.url() }));
});

test.after(async () => {
  if (process.env.CRAWL_LOG) fs.writeFileSync(process.env.CRAWL_LOG, JSON.stringify({ base: srv && srv.url, requests }, null, 1));
  if (b) await b.close();
  if (srv) srv.close();
});

async function shot(name) {
  if (process.env.SCREENSHOTS) await b.page.screenshot({ path: path.join(process.env.SCREENSHOTS, name + '.png') });
}

// Every tile on screen has its picture.
const settled = () => b.page.waitForFunction(() => {
  const tiles = Array.from(document.querySelectorAll('.tile')).filter((t) => {
    const r = t.getBoundingClientRect();
    return r.bottom > 0 && r.top < innerHeight;
  });
  return tiles.length > 0 && tiles.every((t) => t.classList.contains('ready') || t.classList.contains('err'));
});

const under = (u) => u.startsWith(srv.media);
const outside = (list) => list.filter((r) => !under(r.url)).map((r) => r.method + ' ' + r.url + ' (from ' + r.page + ')');

test('the host\'s app loads at /, routes on the client and reaches its API', async () => {
  await b.page.goto(srv.url);
  await b.page.waitForSelector('body[data-ready]');
  assert.equal(await b.page.locator('#api').textContent(), 'hello from the host\'s API');
  await shot('spa');
  await b.page.click('a[href="/settings"]');
  assert.equal(await b.page.locator('#route').textContent(), '/settings');
  // A route loaded straight away, with the app's own query, is the app.
  await b.page.goto(srv.url + 'settings?view=rich&width=64&sort=size');
  await b.page.waitForSelector('body[data-ready]');
  assert.equal(await b.page.locator('#query').textContent(), '?view=rich&width=64&sort=size');
});

test('the file browser at /media/ starts its breadcrumb there, named by its title', async () => {
  const start = requests.length;
  await b.page.goto(srv.media);
  await settled();
  const back = b.page.locator('#crumbs a.host');
  assert.equal(await back.textContent(), 'Host app');
  assert.equal(await back.evaluate((a) => a.href), srv.url);
  const home = b.page.locator('#crumbs a.home');
  assert.equal(await home.textContent(), 'Media');
  assert.equal(await home.evaluate((a) => a.href), srv.media);
  assert.equal(await b.page.locator('#title').textContent(), 'Media');
  await b.page.waitForFunction(() => document.querySelectorAll('.row-strip img').length > 0);
  await shot('file-browser');

  await b.page.goto(srv.media + 'trips/2024/');
  await settled();
  const crumbs = await b.page.$$eval('#crumbs a', (as) => as.map((a) => [a.textContent, a.href]));
  assert.deepEqual(crumbs, [['Host app', srv.url], ['Media', srv.media], ['trips', srv.media + 'trips/']]);
  assert.deepEqual(outside(requests.slice(start)), []);
});

test('the crumbs\' panel lists the mount\'s own folders', async () => {
  await b.page.goto(srv.media + 'trips/2024/');
  await b.page.hover('#crumbs a.home');
  await b.page.waitForSelector('#crumb-panel.hp-open');
  const links = await b.page.$$eval('#crumb-panel a', (as) => as.map((a) => a.href));
  assert.ok(links.includes(srv.media + 'trips/'), links.join(' '));
  assert.deepEqual(links.filter((l) => !under(l)), []);
});

test('the gallery opens and moves along, under /media/', async () => {
  const start = requests.length;
  await b.page.goto(srv.media);
  await settled();
  await b.page.locator('.tile[aria-label="photo-00002.png"]').click();
  await b.page.waitForSelector('dialog#ss[open]');
  await b.page.waitForFunction(() => document.querySelectorAll('#ss-strip > button').length > 1);
  await b.page.waitForTimeout(400);
  await shot('gallery');
  for (let i = 0; i < 5; i++) await b.page.keyboard.press('ArrowRight');
  assert.equal(await b.page.locator('#ss-name').textContent(), 'photo-00007.png');
  await b.page.keyboard.press('Escape');
  assert.deepEqual(outside(requests.slice(start)), []);
});

test('the text viewer\'s links stay under /media/', async () => {
  await b.page.goto(srv.media + 'trips/2024/readme.md?view=rich');
  const crumbs = await b.page.$$eval('#crumbs a', (as) => as.map((a) => [a.textContent, a.href]));
  assert.deepEqual(crumbs, [['Host app', srv.url], ['Media', srv.media], ['trips', srv.media + 'trips/'], ['2024', srv.media + 'trips/2024/']]);
});

// Every page reachable from /media/, through every link on it: each link
// and everything each page loads (thumbnails, previews, the diagnostics
// it sends) is under /media/.
test('a crawl of every link from /media/ never leaves it', async () => {
  const start = requests.length;
  const seen = new Set([srv.media]), queue = [srv.media];
  let pages = 0;
  while (queue.length && pages < 80) {
    const url = queue.shift();
    pages++;
    const res = await b.page.goto(url);
    assert.ok(res && res.status() < 400, url + ': ' + (res && res.status()));
    await b.page.waitForLoadState('load');
    if (await b.page.locator('.tile').count()) await settled();
    // Every link but the one back to the host's app, which it asked for.
    const links = await b.page.$$eval('a[href]:not(.host)', (as) => as.map((a) => a.href.split('#')[0]));
    for (const l of links) {
      assert.ok(under(l), 'a link on ' + url + ' goes to ' + l);
      // Pages, not the files themselves (a download is no page).
      const page = l.endsWith('/') || /[?&]view=rich/.test(l) || /\/\?/.test(l);
      if (page && !seen.has(l)) {
        seen.add(l);
        queue.push(l);
      }
    }
  }
  assert.ok(pages > 8, 'only ' + pages + ' pages crawled');
  const made = requests.slice(start);
  assert.ok(made.some((r) => /[?&]width=/.test(r.url)), 'no thumbnails were loaded');
  assert.ok(made.some((r) => /\?preview/.test(r.url)), 'no folder previews were loaded');
  assert.deepEqual(outside(made), []);
  // The page sends its diagnostics as it's left; the host serves nothing
  // that takes them but the mount's __diag.
  assert.equal(await b.page.evaluate(() => new URL(window.GOWS.diag, location.href).href), srv.media + '__diag');
  await b.page.goto('about:blank');
  await b.page.waitForTimeout(300);
  const log = fs.readFileSync(path.join(srv.tmp, 'diag.log'), 'utf8');
  assert.match(log, / client page /, 'no events from the page reached the mount\'s __diag');
  await shot('crawled');
});

test('the host\'s app still loads after browsing the mount', async () => {
  await b.page.goto(srv.url + 'settings');
  await b.page.waitForSelector('body[data-ready]');
  assert.equal(await b.page.locator('#route').textContent(), '/settings');
});

test('the bare mount redirects to it, relative to where it is', async () => {
  const res = await fetch(srv.url + 'media?x=1', { redirect: 'manual' });
  assert.equal(res.status, 307);
  assert.equal(res.headers.get('location'), './media/?x=1');
  await b.page.goto(srv.url + 'media');
  assert.equal(b.page.url(), srv.media);
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
