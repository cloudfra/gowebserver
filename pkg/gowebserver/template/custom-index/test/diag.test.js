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

// A page with diagnostics sent to endpoint ('' for off). sent collects
// each batch the page sends: its URL and its events.
function setup(endpoint) {
  const sent = [];
  const pg = page('', {
    location: { href: 'https://example.test/e/Holiday/' },
    navigator: {
      sendBeacon(url, blob) { sent.push({ url, events: blob.parts.join('').split('\n').map((l) => JSON.parse(l)) }); return true; },
    },
    globals: {
      GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media: [[0, 'a.jpg', 'a.jpg', 1, 1]], diag: endpoint },
      Blob: class { constructor(parts) { this.parts = parts; } },
      setInterval() { return 0; },
    },
  });
  const d = load(pg, ['custom-index/util.js', 'custom-index/diag.js'], ['diag']);
  return { pg, sent, diag: d.diag };
}

test('without diagnostics nothing is sent', () => {
  const t = setup('');
  t.diag('thumb', { url: 'a.jpg?width=256', ok: true });
  t.pg.fire('error', { error: { name: 'TypeError', stack: '' }, lineno: 3, colno: 4 });
  t.pg.tick(10000);
  t.pg.fire('pagehide');
  assert.deepEqual(t.sent, []);
});

test('events go to the server in batches, at most every two seconds', () => {
  const t = setup('./../__diag');
  t.diag('thumb', { url: 'a.jpg?width=256', ok: true, ms: 12 });
  t.diag('thumb', { url: 'b.jpg?width=256', ok: false, ms: 30 });
  assert.equal(t.sent.length, 0, 'held for the batch');
  t.pg.tick(2000);
  assert.equal(t.sent.length, 1);
  assert.equal(t.sent[0].url, './../__diag');
  assert.deepEqual(t.sent[0].events.map((e) => e.ev), ['page', 'thumb', 'thumb']);
  assert.equal(t.sent[0].events[0].n, 1, 'the page says how many photos and videos it has');
  assert.ok(t.sent[0].events.every((e) => typeof e.pt === 'number'), 'each event has its time on the page');
});

test('what is waiting is sent when the page goes away', () => {
  const t = setup('./__diag');
  t.diag('thumb', { url: 'a.jpg?width=256', ok: true });
  t.pg.fire('pagehide');
  assert.equal(t.sent.length, 1);
});

test('a script error is sent by kind, place and function names, never its message', () => {
  const t = setup('./__diag');
  t.pg.fire('error', {
    message: 'Cannot read properties of null (reading "Holiday.jpg")',
    error: { name: 'TypeError', message: 'Holiday.jpg', stack: 'TypeError: Holiday.jpg\n    at loadTile (https://example.test/e/Holiday/:6172:19)\n    at render (https://example.test/e/Holiday/:6200:3)' },
    lineno: 6172, colno: 19,
  });
  t.pg.tick(2000);
  const err = t.sent[0].events.find((e) => e.ev === 'js-error');
  assert.equal(err.name, 'TypeError');
  assert.equal(err.stack, 'loadTile<render');
  assert.equal(err.col, 19);
  assert.ok(!JSON.stringify(err).includes('Holiday'), 'no message, no URL');
});
