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

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { serve, open } = require('../../browser.js');

const PHOTOS = 300;
let srv, b;

test.before(async () => {
  srv = await serve({
    photos: PHOTOS,
    files: { 'notes.txt': 'hello\n', 'docs/readme.md': '# readme\n', 'zz-broken.jpg': 'this is not a picture' },
  });
  b = await open();
  // Notes each frame in which tiles were shown, and how many.
  await b.page.addInitScript(() => {
    window.shownPerFrame = [];
    let pending = 0;
    new MutationObserver((records) => {
      for (const r of records) {
        if (r.target.classList && r.target.classList.contains('tile') && r.target.classList.contains('ready') && !/\bready\b/.test(r.oldValue || '')) {
          if (!pending) requestAnimationFrame(() => { window.shownPerFrame.push(pending); pending = 0; });
          pending++;
        }
      }
    }).observe(document, { subtree: true, attributes: true, attributeFilter: ['class'], attributeOldValue: true });
  });
  await b.page.goto(srv.url);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const count = (sel) => b.page.locator(sel).count();
// Every tile on screen has its picture.
const settled = () => b.page.waitForFunction(() => {
  const tiles = Array.from(document.querySelectorAll('.tile')).filter((t) => {
    const r = t.getBoundingClientRect();
    return r.bottom > 0 && r.top < innerHeight;
  });
  return tiles.length > 0 && tiles.every((t) => t.classList.contains('ready') || t.classList.contains('err'));
});

test('the folder is listed and counted', async () => {
  assert.equal(await b.page.locator('#summary').textContent(), '1 folder, 1 file, ' + (PHOTOS + 1) + ' photos');
  assert.equal(await count('.row'), 2);
});

test('the tiles on screen get their thumbnails, a batch at a time', async () => {
  await settled();
  const frames = await b.page.evaluate(() => window.shownPerFrame);
  const shown = frames.reduce((a, n) => a + n, 0);
  assert.ok(frames.length > 1, 'more than one batch: ' + frames);
  assert.ok(Math.max(...frames) > 1, 'tiles are shown together: ' + frames);
  // Batches of four over HTTP/1.1; two can land in one frame.
  assert.ok(frames.length <= Math.ceil(shown / 2), 'and not one by one: ' + frames);
});

test('thumbnails are the size of the tiles, not the files', async () => {
  const src = await b.page.locator('.tile.ready img').first().getAttribute('src');
  assert.match(src, /^photo-00000\.png\?width=\d+&height=\d+&fit=outside$/);
  const w = await b.page.locator('.tile.ready img').first().evaluate((img) => img.naturalWidth);
  assert.ok(w < 640, 'the server resized it: ' + w);
});

test('only the rows near the window are in the page', async () => {
  const mounted = await count('.tile');
  assert.ok(mounted > 0 && mounted < PHOTOS, mounted + ' of ' + PHOTOS + ' tiles mounted');
  await b.page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
  await b.page.waitForSelector('.tile[aria-label="photo-00299.png"]');
  assert.equal(await count('.tile[aria-label="photo-00000.png"]'), 0, 'the first rows are gone');
  await settled();
});

test('a picture that can\'t be shown gets the placeholder', async () => {
  const broken = b.page.locator('.tile[aria-label="zz-broken.jpg"]');
  await broken.waitFor();
  await b.page.waitForFunction(() => document.querySelector('.tile[aria-label="zz-broken.jpg"]').classList.contains('err'));
  assert.notEqual(await broken.evaluate((t) => getComputedStyle(t, '::before').content), 'none');
});

test('the filter narrows the list and the grid', async () => {
  await b.page.evaluate(() => scrollTo(0, 0));
  await b.page.fill('#filter', 'photo-0001');
  await b.page.waitForFunction(() => document.querySelectorAll('.tile').length === 10);
  assert.equal(await b.page.locator('#summary').textContent(), '10 photos');
  await b.page.fill('#filter', '');
  await b.page.waitForFunction((n) => document.getElementById('summary').textContent.indexOf(n + ' photos') >= 0, PHOTOS + 1);
});

test('the slideshow opens on a tile and holds three slides however far it goes', async () => {
  await settled();
  await b.page.locator('.tile[aria-label="photo-00002.png"]').click();
  await b.page.waitForSelector('dialog#ss[open]');
  assert.equal(await b.page.locator('#ss-name').textContent(), 'photo-00002.png');
  for (let i = 0; i < 12; i++) await b.page.keyboard.press('ArrowRight');
  assert.equal(await b.page.locator('#ss-name').textContent(), 'photo-00014.png');
  assert.equal(await count('#ss-stage > *'), 3);
  assert.ok(await count('#ss-strip > button') <= 25);
  await b.page.keyboard.press('Escape');
  await b.page.waitForSelector('dialog#ss[open]', { state: 'detached' }).catch(() => b.page.waitForFunction(() => !document.getElementById('ss').open));
  assert.equal(await count('#ss-stage > *'), 0);
});

test('the filmstrip stays when the other controls fade, except in full screen', async () => {
  await settled();
  await b.page.locator('.tile[aria-label="photo-00003.png"]').click();
  await b.page.waitForSelector('dialog#ss[open]');
  await b.page.waitForFunction(() => document.querySelectorAll('#ss-strip > button').length > 1);
  const opacity = (sel) => b.page.evaluate((s) => getComputedStyle(document.querySelector(s)).opacity, sel);
  const settle = () => b.page.waitForTimeout(600); // past the fade
  // As after a few seconds without a mouse moving, or a swipe on a
  // touch screen.
  await b.page.evaluate(() => document.getElementById('ss').classList.add('chrome-off'));
  await settle();
  assert.equal(await opacity('.ss-bar'), '0', 'the other controls fade');
  assert.equal(await opacity('#ss-strip'), '1', 'the filmstrip stays');
  // In full screen it keeps out of the way until asked for.
  await b.page.evaluate(() => document.getElementById('ss').classList.add('is-fs'));
  await settle();
  assert.equal(await opacity('#ss-strip'), '0');
  await b.page.evaluate(() => document.getElementById('ss').classList.add('strip-on'));
  await settle();
  assert.equal(await opacity('#ss-strip'), '1', 'near the top edge it shows');
  await b.page.evaluate(() => document.getElementById('ss').classList.remove('is-fs', 'strip-on', 'chrome-off'));
  await b.page.keyboard.press('Escape');
  await b.page.waitForFunction(() => !document.getElementById('ss').open);
});

test('the filmstrip hangs at the top of the frame, clear of a video\'s controls', async () => {
  await settled();
  await b.page.locator('.tile[aria-label="photo-00006.png"]').click();
  await b.page.waitForSelector('dialog#ss[open]');
  await b.page.waitForFunction(() => document.querySelectorAll('#ss-strip > button').length > 1);
  // The browser paints its own control panel, scrubber included, along
  // the bottom of a video, so the strip belongs at the top: under the
  // bar, and above the picture the slide keeps room for it.
  const geo = () => b.page.evaluate(() => {
    const f = document.getElementById('ss-frame').getBoundingClientRect();
    const s = document.querySelector('#ss-strip').getBoundingClientRect();
    const slide = document.querySelector('#ss-stage > * > *').getBoundingClientRect();
    return {
      belowBar: Math.round(s.top - f.top),
      height: Math.round(s.height),
      clearOfBottom: Math.round(f.bottom - s.bottom),
      abovePic: Math.round(slide.top - s.bottom),
    };
  });
  const g = await geo();
  assert.ok(g.belowBar > 0 && g.belowBar < 120, `the strip hangs under the bar, ${g.belowBar}px below the top edge`);
  assert.equal(g.height, 63, 'one row of thumbnails');
  assert.ok(g.clearOfBottom > 400, `the bottom edge is left to a video's panel, ${g.clearOfBottom}px clear`);
  assert.ok(g.abovePic >= 0, `the picture starts below the strip, ${g.abovePic}px clear`);
  await b.page.keyboard.press('Escape');
  await b.page.waitForFunction(() => !document.getElementById('ss').open);
});

test('the theme button switches the page and remembers', async () => {
  await b.page.click('#theme');
  assert.equal(await b.page.evaluate(() => document.documentElement.dataset.theme), 'light');
  await b.page.click('#theme');
  assert.equal(await b.page.evaluate(() => document.documentElement.dataset.theme), 'dark');
  assert.equal(await b.page.evaluate(() => localStorage.getItem('gows-theme')), 'dark');
  await b.page.reload();
  assert.equal(await b.page.evaluate(() => document.documentElement.dataset.theme), 'dark', 'before the page draws');
});

test('nothing went wrong along the way', () => {
  // The broken picture is the one thing meant to fail.
  assert.deepEqual(b.problems.filter((p) => p.indexOf('zz-broken.jpg') < 0), []);
});
