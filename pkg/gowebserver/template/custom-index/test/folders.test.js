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

// A row of the list as the template writes it: a folder's link ends in a
// slash, an archive's doesn't.
function row(name, kind) {
  const href = kind === 'folder' ? encodeURIComponent(name) + '/' : encodeURIComponent(name);
  return '<div class="row"' + (kind === 'file' ? '' : ' data-dir') + '><a class="row-icon" href="' + href + '"><span class="chip"><svg></svg></span></a>' +
    '<a class="row-main"><span class="name">' + name + '</span><span class="meta"><span class="size">Folder</span></span></a></div>';
}

// A page listing rows, whose server answers ?preview with previews[folder].
// seen(i) brings the i-th observed row near the screen.
function setup(rows, covers, opts) {
  opts = opts || {};
  const asked = [];
  let observer = null;
  const pg = page('<p id="summary"></p><p id="no-match" hidden></p>' + rows.map((r) => row(r[0], r[1])).join(''), {
    globals: {
      GOWS: { sort: 'name', thumbs: opts.thumbs !== false, videoThumbs: opts.videoThumbs !== false, media: [], diag: '' },
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
  const create = pg.document.createElement.bind(pg.document);
  pg.document.createElement = (tag) => {
    const el = create(tag);
    if (tag === 'span') el.clientWidth = opts.room === undefined ? 400 : opts.room;
    return el;
  };
  load(pg, ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/folders.js'], []);
  const rowsEl = pg.document.querySelectorAll('.row');
  return {
    pg, asked, rows: rowsEl,
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
