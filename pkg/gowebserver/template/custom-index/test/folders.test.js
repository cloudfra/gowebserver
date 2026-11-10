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

// A page listing rows, whose server answers ?cover with covers[folder].
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
        const name = decodeURIComponent(url.replace(/\/\?cover$/, ''));
        const c = covers[name];
        return Promise.resolve({ ok: !!c, json: () => Promise.resolve(c) });
      },
    },
  });
  load(pg, ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/folders.js'], []);
  const rowsEl = pg.document.querySelectorAll('.row');
  return {
    pg, asked, rows: rowsEl,
    observed: () => observer ? observer.rows.slice() : [],
    seen: (els) => observer.fn(els.map((target) => ({ target, isIntersecting: true }))),
    settle: async () => { for (let i = 0; i < 5; i++) await pg.settle(); },
  };
}

const COVERS = {
  'Trip': { file: 'a b.jpg', photos: 12, videos: 0, folders: 0 },
  'Clips': { file: 'one.mp4', video: true, photos: 0, videos: 3, folders: 0 },
  'Years': { file: '2024/x.jpg', photos: 0, videos: 0, folders: 2 },
  'Mixed': { file: 'p.jpg', photos: 2, videos: 1, folders: 1 },
  'Empty': { photos: 0, videos: 0, folders: 0 },
};

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
  assert.deepEqual(t.asked, ['Trip/?cover']);
  assert.equal(t.observed().length, 1, 'and is no longer watched');
});

test('the cover loads through the thumbnail queue, and what the folder holds replaces "Folder"', async () => {
  const t = setup([['Trip', 'folder']], COVERS);
  t.seen([t.rows[0]]);
  await t.settle();
  assert.deepEqual(t.pg.requests.map((r) => r.url), ['Trip/a%20b.jpg?width=128&height=128&fit=outside']);
  const size = t.rows[0].querySelector('.size');
  assert.equal(size.textContent, '12 photos');
  const img = t.rows[0].querySelector('img.cover');
  img.fire('load');
  t.pg.frame();
  assert.ok(t.rows[0].querySelector('.chip').classList.contains('has-cover'));
});

test('a cover that fails leaves the icon', async () => {
  const t = setup([['Trip', 'folder']], COVERS);
  t.seen([t.rows[0]]);
  await t.settle();
  t.rows[0].querySelector('img.cover').fire('error');
  t.pg.frame();
  assert.equal(t.rows[0].querySelector('img.cover'), null);
  assert.ok(!t.rows[0].querySelector('.chip').classList.contains('has-cover'));
});

test('a video cover needs video thumbnails; the counts show either way', async () => {
  const t = setup([['Clips', 'folder']], COVERS, { videoThumbs: false });
  t.seen([t.rows[0]]);
  await t.settle();
  assert.deepEqual(t.pg.requests, []);
  assert.equal(t.rows[0].querySelector('.size').textContent, '3 videos');
});

test('a cover from a folder below, and what a folder holds in a few words', async () => {
  const t = setup([['Years', 'folder'], ['Mixed', 'folder'], ['Empty', 'folder']], COVERS);
  t.seen([...t.rows]);
  await t.settle();
  assert.equal(t.pg.requests[0].url, 'Years/2024/x.jpg?width=128&height=128&fit=outside');
  const sizes = t.rows.map((r) => r.querySelector('.size'));
  assert.deepEqual(sizes.map((s) => s.textContent), ['2 folders', '3 items', 'Empty']);
  assert.equal(sizes[1].title, '2 photos, 1 video, 1 folder');
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
