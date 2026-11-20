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
const { page, load } = require('../../harness.js');

// An entry of the list as the page is sent it: [kind, url, name, icon,
// size, date], where kind 2 is a folder and 3 an archive.
const KINDS = { file: 0, folder: 2, archive: 3 };
function entry(name, kind) {
  return [KINDS[kind], encodeURIComponent(name), name, kind === 'file' ? 'text' : 'folder', 10, '2024-01-01'];
}

// The query hover-panel.js asks for a mouse or trackpad.
const FINE = '(hover: hover) and (pointer: fine), (any-hover: hover) and (any-pointer: fine)';

// A page listing rows, whose server answers ?preview with previews[folder].
// seen(i) brings the i-th observed row near the screen.
function setup(rows, covers, opts) {
  opts = opts || {};
  const asked = [];
  let observer = null;
  const pg = page('<p id="summary"></p><p id="no-match" hidden></p><div id="files-body"></div>', {
    media: opts.media || { [FINE]: true },
    globals: {
      grid: null,
      GOWS: { sort: 'name', thumbs: opts.thumbs !== false, videoThumbs: opts.videoThumbs !== false, files: rows.map((r) => entry(r[0], r[1])), media: [], diag: '' },
      IntersectionObserver: class {
        constructor(fn) { this.fn = fn; this.rows = []; observer = this; }
        observe(el) { this.rows.push(el); }
        unobserve(el) { this.rows = this.rows.filter((r) => r !== el); }
      },
      fetch(url) {
        asked.push(url);
        const name = decodeURIComponent(url.replace(/\/\?preview$/, ''));
        const c = covers[name];
        return Promise.resolve({ ok: !!c, json: () => Promise.resolve(c) });
      },
    },
  });
  // The stand-in browser has no layout: a strip is as wide as the test
  // says (opts.room, 400 px by default).
  // A canvas gives back, in every pixel, the color opts.colors has for
  // the thumbnail drawn into it last (gray when it has none).
  const create = pg.document.createElement.bind(pg.document);
  pg.document.createElement = (tag) => {
    if (tag === 'canvas') {
      let last = null;
      const node = { width: 0, height: 0 };
      node.getContext = () => ({
        clearRect() { last = null; },
        drawImage(pic) { last = pic; },
        getImageData() {
          const rgb = (last && (opts.colors || {})[last.src]) || [128, 128, 128];
          const data = new Uint8ClampedArray(node.width * node.height * 4);
          for (let i = 0; i < data.length; i += 4) data.set(rgb.concat(255), i);
          return { data };
        },
      });
      return node;
    }
    const el = create(tag);
    // Only in the page: a strip out of it has no room, as in a browser.
    if (tag === 'span') {
      Object.defineProperty(el, 'clientWidth', { get: () => (el.isConnected ? (opts.room === undefined ? 400 : opts.room) : 0) });
    }
    return el;
  };
  const d = load(pg, ['page-theme/hover-panel.js', 'custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js',
    'custom-index/folders.js', 'custom-index/accent.js', 'custom-index/list.js'], ['applyFilter', 'fileList']);
  d.applyFilter('');
  const rowsEl = pg.document.querySelectorAll('.row');
  return {
    pg, asked, rows: rowsEl, fileList: d.fileList, applyFilter: d.applyFilter,
    // The rows as they are now, once the list has built them again.
    current: () => pg.document.querySelectorAll('.row'),
    observed: () => observer ? observer.rows.slice() : [],
    seen: (els) => observer.fn(els.map((target) => ({ target, isIntersecting: true }))),
    settle: async () => { for (let i = 0; i < 5; i++) await pg.settle(); },
  };
}

const it = (file, video) => (video ? { file, video: true } : { file });
const many = (n) => Array.from({ length: n }, (_, i) => it('p' + (i + 1) + '.jpg'));
const COVERS = {
  'Trip': { items: [it('a b.jpg'), it('c.jpg'), it('d.jpg')] },
  'Clips': { items: [it('one.mp4', true), it('two.mp4', true)] },
  'Years': { items: [it('2024/x.jpg')] },
  'Mixed': { items: [it('p.jpg'), it('v.mp4', true)] },
  'Empty': { items: [] },
  'Big': { items: many(20) },
};

const strip = (row) => row.querySelector('.row-strip');
const stripImgs = (row) => (strip(row) ? strip(row).querySelectorAll('img') : []);

test('only folders are watched: not files, not archives', () => {
  const t = setup([['Trip', 'folder'], ['notes.txt', 'file'], ['pics.zip', 'archive'], ['Clips', 'folder']], COVERS);
  assert.deepEqual(t.observed().map((r) => r.querySelector('.name').textContent), ['Trip', 'Clips']);
});

test('a folder is asked about once it comes near the screen, not before', async () => {
  const t = setup([['Trip', 'folder'], ['Clips', 'folder']], COVERS);
  await t.settle();
  assert.deepEqual(t.asked, []);
  t.seen([t.rows[0]]);
  await t.settle();
  assert.deepEqual(t.asked, ['Trip/?preview']);
  assert.equal(t.observed().length, 1, 'and is no longer watched');
});

test('a strip of the folder\'s photos goes between its name and its size, through the thumbnail queue', async () => {
  const t = setup([['Trip', 'folder']], COVERS);
  t.seen([t.rows[0]]);
  await t.settle();
  const row = t.rows[0];
  const s = strip(row);
  assert.ok(s, 'a strip');
  assert.equal(s.getAttribute('aria-hidden'), 'true');
  assert.equal(s.nextSibling, row.querySelector('.meta'), 'between the name and the size');
  assert.ok(row.classList.contains('has-strip'));
  // Heavy thumbnails: two of HTTP/1.1's four slots at a time.
  assert.deepEqual(t.pg.requests.map((r) => r.url), ['Trip/a%20b.jpg', 'Trip/c.jpg'].map((u) => u + '?width=128&height=128&fit=outside'));
  assert.equal(stripImgs(row).length, 3, 'all three in the strip, the third waiting its turn');
  assert.ok(row.querySelector('.chip svg'), 'the folder icon stays');
  assert.equal(row.querySelector('.size').textContent, 'Folder', 'and so does "Folder"');
});

test('only as many as fit are loaded, and one more for the edge it fades out at', async () => {
  const t = setup([['Big', 'folder']], COVERS, { room: 300 }); // 6 of 44 px, and a bit
  t.seen([t.rows[0]]);
  await t.settle();
  assert.equal(stripImgs(t.rows[0]).length, 7);
  assert.ok(strip(t.rows[0]).classList.contains('fade'));
});

test('a name too long to leave room gets no strip', async () => {
  const t = setup([['Big', 'folder']], COVERS, { room: 0 });
  t.seen([t.rows[0]]);
  await t.settle();
  assert.equal(strip(t.rows[0]), null);
  assert.ok(!t.rows[0].classList.contains('has-strip'));
  assert.deepEqual(t.pg.requests, []);
});

test('each thumbnail fades in once it has arrived; one that fails goes', async () => {
  const t = setup([['Trip', 'folder']], COVERS);
  t.seen([t.rows[0]]);
  await t.settle();
  const imgs = stripImgs(t.rows[0]);
  assert.ok(imgs.every((img) => !img.classList.contains('ready')), 'hidden until loaded');
  imgs[0].fire('load');
  imgs[1].fire('error');
  t.pg.frame();
  await t.settle();
  imgs[2].fire('load'); // the next batch
  t.pg.frame();
  assert.deepEqual(stripImgs(t.rows[0]).map((img) => img.classList.contains('ready')), [true, true]);
});

test('videos need video thumbnails', async () => {
  const t = setup([['Clips', 'folder'], ['Mixed', 'folder']], COVERS, { videoThumbs: false });
  t.seen([...t.rows]);
  await t.settle();
  assert.equal(strip(t.rows[0]), null, 'no strip of videos that can\'t be shown');
  assert.equal(stripImgs(t.rows[1]).length, 1, 'only the photo');
});

test('a strip from a folder below; none for an empty folder', async () => {
  const t = setup([['Years', 'folder'], ['Empty', 'folder']], COVERS);
  t.seen([...t.rows]);
  await t.settle();
  assert.equal(t.pg.requests[0].url, 'Years/2024/x.jpg?width=128&height=128&fit=outside');
  assert.equal(strip(t.rows[1]), null);
});

test('a couple of folders are asked about at a time', async () => {
  const t = setup([['Trip', 'folder'], ['Clips', 'folder'], ['Years', 'folder'], ['Mixed', 'folder']], COVERS);
  t.seen([...t.rows]);
  assert.equal(t.asked.length, 2);
  await t.settle();
  assert.equal(t.asked.length, 4, 'the rest once those are answered');
});

test('without thumbnails nothing is asked', async () => {
  const t = setup([['Trip', 'folder']], COVERS, { thumbs: false });
  assert.deepEqual(t.observed(), []);
});

// Every thumbnail request so far arrives, a batch at a time.
async function arriveAll(t) {
  for (let i = 0; i < 4; i++) {
    t.pg.requests.forEach((r) => { if (r.el._thumb === 'loading') r.el.fire('load'); });
    t.pg.frame();
    await t.settle();
  }
}

const thumb = (folder, file) => folder + '/' + file + '?width=128&height=128&fit=outside';

test('a folder of vivid photos tints its row with their color', async () => {
  const t = setup([['Trip', 'folder']], COVERS, {
    colors: { [thumb('Trip', 'a%20b.jpg')]: [255, 120, 0], [thumb('Trip', 'c.jpg')]: [255, 120, 0], [thumb('Trip', 'd.jpg')]: [128, 128, 128] },
  });
  t.seen([t.rows[0]]);
  await t.settle();
  assert.ok(!t.rows[0].classList.contains('row-accent'), 'not before the strip is in');
  await arriveAll(t);
  const row = t.rows[0];
  assert.ok(row.classList.contains('row-accent'));
  const h = parseFloat(row.style.getPropertyValue('--row-h'));
  assert.ok(h > 30 && h < 80, 'an orange hue, got ' + h);
  assert.ok(parseFloat(row.style.getPropertyValue('--row-c')) >= 0.14, 'never a muted one');
});

test('a folder of gray photos keeps the usual look', async () => {
  const t = setup([['Trip', 'folder']], COVERS);
  t.seen([t.rows[0]]);
  await t.settle();
  await arriveAll(t);
  assert.ok(!t.rows[0].classList.contains('row-accent'));
  assert.equal(t.rows[0].style.getPropertyValue('--row-h'), '');
});

// ===== A strip's slideshow =====

const DWELL = 600, STEP = 700;
const show = (row) => row.querySelector('.strip-show');
const frames = (row) => show(row).querySelector('.strip-track').querySelectorAll('img');
const shift = (row) => show(row).querySelector('.strip-track').style.transform || '';
const hover = (el, pointerType) => el.fire('pointerover', { pointerType: pointerType || 'mouse' });

// A page whose rows' strips are in, the pointer elsewhere.
async function strips(rows, opts) {
  const t = setup(rows, COVERS, opts);
  t.seen([...t.rows]);
  await t.settle();
  return t;
}

test('resting on a folder with a strip plays its photos over the strip, after a dwell', async () => {
  const t = await strips([['Big', 'folder']]); // 400 px: 9 fit
  const row = t.rows[0];
  hover(row.querySelector('.name'));
  t.pg.tick(DWELL - 1);
  assert.equal(show(row), null, 'not as the pointer crosses it');
  t.pg.tick(1);
  const panel = show(row);
  assert.ok(panel, 'a panel');
  assert.equal(panel.parentNode, strip(row), 'inside the strip, so it takes the strip\'s box');
  assert.equal(panel.getAttribute('aria-hidden'), 'true');
  t.pg.frame();
  assert.ok(panel.classList.contains('hp-open'), 'revealed a frame later, so it transitions');
  // Every one of the folder's twenty, then the first ones again for the loop.
  assert.equal(frames(row).length, 20 + 10);
  assert.equal(panel.querySelectorAll('button').length + panel.querySelectorAll('a').length, 0, 'no controls');
  assert.equal(strip(row).querySelectorAll('img').length, 10 + 30, 'the strip itself is untouched underneath');
});

test('the slideshow steps along on its own, loads only what is about to show, and loops', async () => {
  const t = await strips([['Big', 'folder']]);
  const row = t.rows[0];
  hover(row);
  t.pg.tick(DWELL);
  const loading = () => frames(row).filter((img) => img._wanted).length;
  assert.equal(loading(), 11, 'what fits, and the next');
  assert.equal(frames(row)[0]._thumbURL, thumb('Big', 'p1.jpg'), 'at the strip\'s own size, from the browser\'s cache');
  t.pg.tick(STEP);
  assert.equal(shift(row), 'translateX(44px)');
  assert.equal(loading(), 12);
  t.pg.tick(STEP * 19);
  assert.equal(shift(row), 'translateX(880px)', 'on the copy of the first');
  t.pg.tick(STEP / 2);
  assert.equal(shift(row), '', 'and back to the start, unseen');
  assert.ok(show(row).querySelector('.strip-track').classList.contains('jump'));
  t.pg.tick(STEP / 2);
  assert.equal(shift(row), 'translateX(44px)', 'round again');
});

test('leaving the row closes the slideshow at once, leaving the strip', async () => {
  const t = await strips([['Big', 'folder'], ['Trip', 'folder']]);
  const row = t.rows[0];
  hover(row);
  t.pg.tick(DWELL);
  t.pg.frame();
  hover(t.pg.document.body);
  t.pg.tick(0);
  assert.ok(!show(row).classList.contains('hp-open'), 'fading out');
  t.pg.tick(STEP * 3);
  assert.equal(show(row), null, 'and gone');
  assert.ok(strip(row));
});

test('the next row waits out its own dwell: a sweep down the list plays nothing', async () => {
  const t = await strips([['Big', 'folder'], ['Trip', 'folder'], ['Mixed', 'folder']]);
  hover(t.rows[0]);
  t.pg.tick(DWELL);
  const asked = t.pg.requests.length;
  // Rows touch: the pointer goes straight from one to the next.
  hover(t.rows[1]);
  t.pg.tick(0);
  assert.equal(show(t.rows[1]), null, 'not at once');
  t.pg.tick(DWELL / 3);
  hover(t.rows[2]);
  t.pg.tick(DWELL - 1);
  assert.equal(show(t.rows[1]), null, 'passed over');
  assert.equal(show(t.rows[2]), null);
  assert.equal(t.pg.requests.length, asked, 'and nothing was fetched for them');
  t.pg.tick(1);
  assert.ok(show(t.rows[2]), 'the one it rests on plays');
  t.pg.tick(STEP);
  assert.equal(show(t.rows[0]), null, 'the first is long gone');
});

test('a folder of fewer photos than fit fills every slot, all the way round the loop', async () => {
  const t = await strips([['Trip', 'folder']]); // 3 photos, 9 slots
  const row = t.rows[0];
  hover(row);
  t.pg.tick(DWELL);
  const fr = frames(row);
  assert.ok(fr.length >= 3 + 9 + 1, 'the slots, and one more, wherever the loop is: ' + fr.length);
  fr.forEach((img, i) => assert.equal(img._item.url, ['Trip/a%20b.jpg', 'Trip/c.jpg', 'Trip/d.jpg'][i % 3], 'frame ' + i));
  t.pg.tick(STEP * 3);
  assert.equal(shift(row), 'translateX(132px)', 'on the copy of the start');
  t.pg.tick(STEP / 2);
  assert.equal(shift(row), '', 'and back, seen in the same place');
});

test('closing fades the slideshow out with its pictures in place', async () => {
  const t = await strips([['Trip', 'folder']]);
  const row = t.rows[0];
  hover(row);
  t.pg.tick(DWELL);
  const fr = frames(row);
  await arriveAll(t);
  const loaded = fr.filter((img) => img.classList.contains('ready'));
  assert.ok(loaded.length > 0);
  hover(t.pg.document.body);
  t.pg.tick(0);
  assert.ok(loaded.every((img) => img.src), 'still showing as it fades');
  t.pg.tick(300);
  assert.equal(show(row), null, 'then gone');
  assert.ok(fr.every((img) => !img._thumb || img._thumb === ''), 'and nothing left loading');
});

test('no slideshow for a finger, without a mouse, with reduced motion, or with one photo', async () => {
  const reduced = '(prefers-reduced-motion: reduce)';
  for (const [why, opts, name, pointer] of [
    ['touch', {}, 'Big', 'touch'],
    ['no fine pointer', { media: {} }, 'Big'],
    ['reduced motion', { media: { [FINE]: true, [reduced]: true } }, 'Big'],
    ['one photo', {}, 'Years'],
  ]) {
    const t = await strips([[name, 'folder']], opts);
    hover(t.rows[0], pointer);
    t.pg.tick(DWELL * 2);
    assert.equal(show(t.rows[0]), null, why);
  }
});

test('no slideshow for a row with no strip, or before its strip is in', async () => {
  const t = setup([['Big', 'folder']], COVERS);
  hover(t.rows[0]);
  t.pg.tick(DWELL);
  assert.equal(show(t.rows[0]), null, 'not before');
  const u = await strips([['Big', 'folder']], { room: 0 });
  hover(u.rows[0]);
  u.pg.tick(DWELL);
  assert.equal(strip(u.rows[0]), null);
});

// The list drops every row and builds them again: a filter that shows
// none of them, then none at all.
function rebuild(t) {
  t.applyFilter('zzz-nothing');
  t.applyFilter('');
}

test('a row built again shows its strip and color once near the screen, without asking again', async () => {
  const t = setup([['Trip', 'folder']], COVERS, {
    colors: { [thumb('Trip', 'a%20b.jpg')]: [255, 120, 0], [thumb('Trip', 'c.jpg')]: [255, 120, 0], [thumb('Trip', 'd.jpg')]: [255, 120, 0] },
  });
  t.seen([t.rows[0]]);
  await t.settle();
  await arriveAll(t);
  assert.ok(t.rows[0].classList.contains('row-accent'));
  rebuild(t);
  const row = t.current()[0];
  assert.notEqual(row, t.rows[0], 'a new row');
  assert.equal(strip(row), null, 'not before it is near the screen');
  assert.deepEqual(t.observed(), [row], 'but watched for it');
  t.seen([row]);
  assert.ok(strip(row), 'then with its strip');
  assert.ok(row.classList.contains('row-accent'), 'and its color');
  assert.deepEqual(t.asked, ['Trip/?preview'], 'asked once');
});

test('a short list built whole lays out no strip until its row is near the screen', async () => {
  const names = Array.from({ length: 30 }, (_, i) => ['Trip ' + i, 'folder']);
  const covers = {};
  names.forEach((n) => { covers[n[0]] = COVERS.Trip; });
  const t = setup(names, covers);
  t.seen([...t.rows]);
  await t.settle();
  rebuild(t);
  const rows = t.current();
  assert.equal(rows.length, 30);
  assert.equal(rows.filter((r) => strip(r)).length, 0, 'answers are in, but no strip is shown yet');
  t.seen([rows[0], rows[1]]);
  assert.equal(rows.filter((r) => strip(r)).length, 2, 'only the rows that came near');
});

test('a color worked out while the row was built again goes to the new row', async () => {
  const t = setup([['Trip', 'folder']], COVERS, {
    colors: { [thumb('Trip', 'a%20b.jpg')]: [255, 120, 0], [thumb('Trip', 'c.jpg')]: [255, 120, 0], [thumb('Trip', 'd.jpg')]: [255, 120, 0] },
  });
  t.seen([t.rows[0]]);
  await t.settle();
  // Every thumbnail arrives; the last starts the color, and before it's
  // worked out the row is dropped and built again.
  // (Two at a time: two batches.)
  for (let i = 0; i < 2; i++) {
    t.pg.requests.forEach((r) => { if (r.el._thumb === 'loading') r.el.fire('load'); });
    t.pg.frame();
    if (i < 1) await t.settle();
  }
  rebuild(t);
  const row = t.current()[0];
  t.seen([row]);
  assert.ok(strip(row));
  assert.ok(!row.classList.contains('row-accent'), 'not yet');
  // The color is worked out before the new row's thumbnails are in.
  for (let i = 0; i < 4; i++) { t.pg.frame(); await t.settle(); }
  await arriveAll(t);
  assert.ok(row.classList.contains('row-accent'), 'the new row has the color');
  assert.ok(parseFloat(row.style.getPropertyValue('--row-c')) > 0);
});

test('a row dropped before it was asked about is asked about when built again', async () => {
  const t = setup([['Trip', 'folder'], ['Clips', 'folder'], ['Years', 'folder']], COVERS);
  t.seen([...t.rows]); // two asked, one waiting
  rebuild(t);
  await t.settle();
  assert.deepEqual(t.asked, ['Trip/?preview', 'Clips/?preview'], 'the waiting one was dropped with its row');
  assert.deepEqual(t.observed().map((r) => r.querySelector('.name').textContent), ['Years'],
    'the two being asked about aren\'t watched: their answers go to their new rows');
  assert.ok(strip(t.current()[0]), 'an answer for a dropped row shows on the new one');
});

test('a filter keeps the rows that still match, strip and all', async () => {
  const t = await strips([['Big', 'folder'], ['Trip', 'folder'], ['Clips', 'folder']]);
  const big = t.rows[0], s = strip(big);
  const loading = t.pg.requests.filter((r) => r.el._thumb === 'loading').length;
  t.applyFilter('i'); // Big, Trip, Clips all have an i
  t.applyFilter('bi');
  assert.deepEqual(t.current().map((r) => r.querySelector('.name').textContent), ['Big']);
  assert.equal(t.current()[0], big, 'the same row');
  assert.equal(strip(big), s, 'the same strip');
  assert.ok(t.pg.requests.filter((r) => r.el._thumb === 'loading').length <= loading, 'nothing loaded again');
  t.applyFilter('');
  assert.equal(t.current()[0], big, 'and still, once the others are back');
  assert.notEqual(t.current()[1], t.rows[1], 'the others are new');
});

// ===== The slideshow on rows the list builds again =====

test('a row the list built again plays its slideshow, from the strip it got back', async () => {
  const t = await strips([['Big', 'folder']]);
  rebuild(t);
  const row = t.current()[0];
  assert.notEqual(row, t.rows[0], 'a new row');
  t.seen([row]);
  hover(row.querySelector('.name'));
  t.pg.tick(DWELL);
  assert.ok(show(row), 'it plays');
  assert.equal(frames(row).length, 20 + 10);
  assert.deepEqual(t.asked, ['Big/?preview'], 'without asking the server again');
});

test('a filter that keeps the row keeps its slideshow playing', async () => {
  const t = await strips([['Big', 'folder'], ['Trip', 'folder']]);
  const row = t.rows[0];
  hover(row);
  t.pg.tick(DWELL);
  t.pg.frame();
  const panel = show(row);
  t.applyFilter('big');
  assert.equal(t.current()[0], row);
  assert.ok(panel.classList.contains('hp-open'), 'still open');
  t.pg.tick(STEP);
  assert.equal(panel.querySelector('.strip-track').style.transform, 'translateX(44px)', 'and stepping');
});

test('a slideshow closes when the list drops its row, and stops stepping', async () => {
  const t = await strips([['Big', 'folder'], ['Trip', 'folder']]);
  const row = t.rows[0];
  hover(row);
  t.pg.tick(DWELL);
  t.pg.frame();
  const panel = show(row);
  assert.ok(panel.classList.contains('hp-open'));
  const track = panel.querySelector('.strip-track');
  t.applyFilter('trip'); // a filter drops the row under the pointer
  assert.ok(!panel.classList.contains('hp-open'), 'closed');
  assert.ok(!row.classList.contains('hp-anchor'), 'and the hover panel let go of the row');
  t.pg.tick(STEP * 3);
  assert.equal(track.style.transform || '', '', 'no more steps');
  // The row built in its place opens as any other.
  t.applyFilter('');
  const again = t.current()[0];
  t.seen([again]);
  hover(again);
  t.pg.tick(DWELL);
  assert.ok(show(again));
});

test('a dwell on a row the list drops before it ends opens nothing', async () => {
  const t = await strips([['Big', 'folder']]);
  const row = t.rows[0];
  hover(row);
  t.pg.tick(DWELL / 2);
  rebuild(t);
  t.pg.tick(DWELL);
  assert.equal(show(row), null, 'nothing on the dropped row');
  const again = t.current()[0];
  t.seen([again]);
  hover(again);
  t.pg.tick(DWELL);
  assert.ok(show(again), 'and the new row still opens');
});
