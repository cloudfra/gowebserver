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
const { page, load, plain } = require('../../harness.js');

// The list starts 300 px down the page; a row is 57 px tall (the height
// list.js takes until it can measure one) unless opts.rowH says.
const TOP = 300;

function setup(files, opts) {
  opts = opts || {};
  const pg = page('<p id="summary"></p><p id="no-match" hidden></p><input id="filter"><div id="files-body"></div>', {
    innerHeight: 800,
    globals: { grid: null, GOWS: { sort: 'name', thumbs: false, videoThumbs: false, files, media: [] } },
  });
  const body = pg.document.getElementById('files-body');
  body.getBoundingClientRect = () => ({ top: TOP - pg.scrollY, height: 0 });
  if (opts.rowH) {
    const create = pg.document.createElement.bind(pg.document);
    pg.document.createElement = (tag) => {
      const el = create(tag);
      if (tag === 'div') el.rect.height = opts.rowH;
      return el;
    };
  }
  const d = load(pg, ['custom-index/util.js', 'custom-index/data.js', 'custom-index/folders.js', 'custom-index/list.js'], ['applyFilter', 'fileList']);
  d.applyFilter('');
  const rows = () => body.querySelectorAll('.row');
  return {
    pg, body, rows, applyFilter: d.applyFilter, list: d.fileList,
    names: () => plain(rows().map((r) => r.querySelector('.name').textContent)),
    // The places in the list of the rows that are built.
    built: () => rows().map((r) => Number(r.querySelector('.name').textContent.replace(/\D/g, ''))),
    pad: () => [parseFloat(body.style.paddingTop), parseFloat(body.style.paddingBottom)],
  };
}

const many = (n) => Array.from({ length: n }, (_, i) => [0, 'file-' + i + '.txt', 'file-' + i + '.txt', 'text', i, '2024-01-01']);

test('each kind of entry gets its row and its links', () => {
  const t = setup([
    [2, 'My%20Docs', 'My Docs', 'folder', 4096, '2024-01-01'],
    [3, 'pics.zip', 'pics.zip', 'folder', 2048, '2024-01-02'],
    [1, 'notes.txt', 'notes.txt', 'text', 1500, '2024-01-03 01:02:03PM'],
    [0, 'tool.exe', 'tool.exe', 'terminal', 5, '2024-01-04'],
  ]);
  const r = t.rows();
  const links = (row) => [row.querySelector('.row-icon').getAttribute('href'), row.querySelector('.row-main').getAttribute('href')];
  assert.deepEqual(plain(r.map(links)), [
    ['My%20Docs/', 'My%20Docs/'],
    ['pics.zip', 'pics.zip.d/'],
    ['notes.txt', 'notes.txt?view=rich'],
    ['tool.exe', 'tool.exe'],
  ]);
  assert.deepEqual(plain(r.map((row) => row.hasAttribute('data-dir'))), [true, true, false, false]);
  assert.deepEqual(plain(r.map((row) => row.querySelector('.size').textContent)), ['Folder', '2.0 kB', '1.5 kB', '5 B']);
  assert.equal(r[2].querySelector('.date').textContent, '2024-01-03 01:02:03PM');
  assert.equal(r[3].querySelector('.chip').className, 'chip icon-terminal');
  assert.equal(r[3].querySelector('use').getAttribute('href'), '#icon-terminal');
  assert.equal(r[0].querySelector('.row-icon').getAttribute('aria-label'), 'Open My Docs directly');
  assert.ok(r[0].classList.contains('first') && !r[1].classList.contains('first'), 'only the first row has no border');
});

test('a name is text, never markup', () => {
  const t = setup([[0, '%3Cb%3E', '<b>"x" & \'y\'', 'unknown', 1, '2024-01-01']]);
  const row = t.rows()[0];
  assert.equal(row.querySelector('.name').textContent, '<b>"x" & \'y\'');
  assert.equal(row.querySelector('b'), null);
  assert.equal(row.querySelector('.row-main').getAttribute('title'), '<b>"x" & \'y\'');
});

test('an ordinary folder\'s list is built whole', () => {
  const t = setup(many(200));
  assert.equal(t.rows().length, 200);
  assert.deepEqual(t.pad(), [0, 0]);
});

test('a big folder builds only the rows near the screen, and pads for the rest', () => {
  const t = setup(many(50000));
  const built = t.built();
  // The screen and three quarters of one below it, at 57 px a row.
  assert.deepEqual([built[0], built[built.length - 1]], [0, Math.floor((800 * 1.75 - TOP) / 57)]);
  assert.deepEqual(t.pad(), [0, (50000 - built.length) * 57]);
});

test('scrolling builds the rows coming on screen, in order, and drops the far ones', () => {
  const t = setup(many(50000));
  const y = 1000 * 57 + TOP; // row 1000 at the top of the screen
  t.pg.scroll(y);
  const built = t.built();
  assert.equal(built[0], Math.floor((1000 * 57 - 800 * 0.75) / 57));
  assert.equal(built[built.length - 1], Math.floor((1000 * 57 + 800 * 1.75) / 57));
  assert.deepEqual(built, built.map((_, i) => built[0] + i), 'one run, in order');
  assert.deepEqual(t.pad(), [built[0] * 57, (50000 - 1 - built[built.length - 1]) * 57]);
  assert.ok(t.rows().length < 60);
});

test('a short scroll keeps the rows it has and adds only the new ones', () => {
  const t = setup(many(50000));
  t.pg.scroll(1000 * 57 + TOP);
  const before = t.rows();
  t.pg.scroll(1005 * 57 + TOP);
  const after = t.rows();
  assert.equal(after[0], before[0], 'the rows above are kept a while');
  assert.ok(after.length > before.length);
  t.pg.scroll(1000 * 57 + TOP);
  assert.equal(t.rows()[0], before[0], 'and are there on the way back');
});

test('a jump far away starts again there', () => {
  const t = setup(many(50000));
  t.pg.scroll(40000 * 57 + TOP);
  const built = t.built();
  assert.ok(built[0] < 40000 && built[built.length - 1] > 40000);
  assert.ok(t.rows().length < 60);
});

test('the row height is measured from a row the browser laid out', () => {
  const t = setup(many(50000), { rowH: 64 });
  assert.deepEqual(t.pad(), [0, (50000 - t.rows().length) * 64]);
  t.pg.scroll(1000 * 64 + TOP);
  const built = t.built();
  assert.equal(built[0], Math.floor((1000 * 64 - 800 * 0.75) / 64));
  assert.deepEqual(t.pad(), [built[0] * 64, (50000 - 1 - built[built.length - 1]) * 64]);
});

test('a filter builds the list again from what matches', () => {
  const t = setup(many(50000));
  t.pg.scroll(30000 * 57 + TOP);
  t.applyFilter('file-4999');
  assert.deepEqual(t.names(), ['file-4999.txt', 'file-49990.txt', 'file-49991.txt', 'file-49992.txt', 'file-49993.txt',
    'file-49994.txt', 'file-49995.txt', 'file-49996.txt', 'file-49997.txt', 'file-49998.txt', 'file-49999.txt']);
  assert.ok(t.rows()[0].classList.contains('first'));
  assert.deepEqual(t.pad(), [0, 0]);
  t.applyFilter('zzz');
  assert.equal(t.rows().length, 0);
  assert.equal(t.pg.document.getElementById('summary').textContent, '');
});

test('a filter keeps the rows that still match, where they are, and builds only the rest', () => {
  const t = setup(many(50000));
  const before = t.rows();
  const keep = before.filter((r) => /1/.test(r.querySelector('.name').textContent));
  let moved = 0;
  const insert = t.body.insertBefore.bind(t.body);
  t.body.insertBefore = (el, ref) => { if (before.includes(el)) moved++; return insert(el, ref); };
  t.applyFilter('1');
  const after = t.rows();
  assert.ok(keep.length > 5);
  for (const r of keep.slice(0, 5)) assert.ok(after.includes(r), r.querySelector('.name').textContent + ' kept');
  assert.equal(moved, 0, 'none of them moved');
  assert.deepEqual(after.map((r) => r.querySelector('.name').textContent), after.map((r) => r._file.name), 'in order');
  assert.ok(after.every((r) => /1/.test(r.querySelector('.name').textContent)));
  assert.ok(after[0].classList.contains('first') && after.slice(1).every((r) => !r.classList.contains('first')));
  t.applyFilter('');
  assert.ok(keep.slice(0, 3).every((r) => t.rows().includes(r)), 'and kept again as the rest come back');
});

test('a row whose height changes moves every row to match', () => {
  const t = setup(many(50000), { rowH: 64 });
  t.rows().forEach((r) => { r.rect.height = 70; });
  t.pg.scroll(1000 * 70 + TOP);
  const built = t.built();
  assert.equal(built[0], Math.floor((1000 * 70 - 800 * 0.75) / 70));
  assert.deepEqual(t.pad(), [built[0] * 70, (50000 - 1 - built[built.length - 1]) * 70]);
});

test('the row with the keyboard focus stays in the page, at its place, while the screen is away', () => {
  const t = setup(many(50000));
  const row = t.rows()[3];
  row.querySelector('.row-main').focus();
  t.pg.scroll(30000 * 57 + TOP);
  assert.ok(row.isConnected, 'still in the page');
  assert.ok(row.classList.contains('parked'));
  assert.equal(row.style.top, 3 * 57 + 'px', 'at its place in the list');
  assert.ok(!t.list.rows().includes(row), 'outside the rows built');
  assert.equal(t.pg.document.activeElement, row.querySelector('.row-main'), 'with the focus');
  t.pg.scroll(0);
  assert.equal(t.list.rows()[3], row, 'back among them');
  assert.ok(!row.classList.contains('parked') && row.style.top === '');
  assert.equal(t.pg.document.activeElement, row.querySelector('.row-main'));
});

test('a filter puts the focused row at its new place, or lets it go when it no longer shows', () => {
  const t = setup(many(50000));
  const row = t.rows()[10]; // file-10.txt
  row.querySelector('.row-main').focus();
  t.pg.scroll(30000 * 57 + TOP);
  t.applyFilter('file-1');
  assert.ok(row.isConnected && row.classList.contains('parked'));
  assert.equal(row.style.top, row._file.pos * 57 + 'px');
  t.applyFilter('file-2');
  assert.ok(!row.isConnected, 'gone with its entry');
});

test('a parked row goes once the focus leaves it', () => {
  const t = setup(many(50000));
  const row = t.rows()[3];
  row.querySelector('.row-main').focus();
  t.pg.scroll(30000 * 57 + TOP);
  assert.ok(row.isConnected);
  t.pg.document.getElementById('filter').focus();
  t.body.fire('focusout');
  t.pg.tick(0);
  assert.ok(!row.isConnected);
  assert.equal(t.list.parked(), null);
});

