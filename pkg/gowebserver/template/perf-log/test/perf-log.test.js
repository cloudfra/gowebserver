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

// A page with the performance log's script, loaded from src (as the
// server adds it), with what it posts in sent.
function setup(html, opts) {
  opts = opts || {};
  const sent = [];
  const session = Object.assign({}, opts.session);
  const pg = page(html, {
    location: { pathname: '/photos/', search: '' },
    navigator: { userAgent: 'test', maxTouchPoints: 5 },
    globals: {
      fetch: (url, init) => { sent.push({ url, batch: JSON.parse(init.body) }); return Promise.resolve(); },
      sessionStorage: {
        getItem: (k) => (k in session ? session[k] : null),
        setItem: (k, v) => { session[k] = String(v); },
        removeItem: (k) => { delete session[k]; },
      },
      // Kept away from the test's own console, which the script wraps.
      console: { error() { }, warn() { }, log() { } },
    },
  });
  pg.setInterval = (fn, ms) => {
    const again = () => { fn(); pg.setTimeout(again, ms); };
    return pg.setTimeout(again, ms);
  };
  pg.document.visibilityState = 'visible';
  pg.document.currentScript = { src: opts.src || 'https://example.test/proxy/8182/__perf.js' };
  load(pg, ['perf-log/perf-log.js']);
  const events = () => sent.flatMap((s) => plain(s.batch.events));
  return { pg, sent, session, events, of: (type) => events().filter((e) => e.type === type) };
}

test('it posts batches beside its script, so a proxy\'s prefix is kept', () => {
  const t = setup('');
  assert.equal(t.sent.length, 0, 'nothing until the first batch is due');
  t.pg.tick(2000);
  assert.equal(t.sent.length, 1);
  assert.equal(t.sent[0].url, 'https://example.test/proxy/8182/__perf');
  assert.equal(t.sent[0].batch.page, '/photos/');
  const start = t.of('start')[0];
  assert.equal(start.ua, 'test');
  assert.equal(start.touch, 5);
  // The server ties the page's own requests to it by this cookie, kept
  // to the part of the site it's on.
  assert.match(t.pg.document.cookie, new RegExp('^perfsid=' + t.sent[0].batch.sid + '; path=/proxy/8182/;'));
  // The frame rate is summed up each second, and sent with the rest.
  t.pg.tick(2000);
  assert.equal(t.sent.length, 2, 'and every couple of seconds after');
  assert.equal(t.of('fps').length, 3, 'the last second\'s goes with the next batch');
  assert.equal(t.of('unsupported')[0].what, 'resource timing', 'what the browser can\'t tell it is said');
});

test('loaded twice, it records once', () => {
  const t = setup('');
  load(t.pg, ['perf-log/perf-log.js']);
  t.pg.tick(2000);
  assert.equal(t.of('start').length, 1);
});

test('a video\'s events say which video, where it is and how far it has got', () => {
  const t = setup('<a class="tile"><video id="tv"></video></a><dialog open><video id="dv"></video></dialog>');
  const tile = t.pg.document.getElementById('tv');
  const slide = t.pg.document.getElementById('dv');
  slide.src = 'https://example.test/proxy/8182/clip.mp4';
  slide.readyState = 1;
  slide.fire('loadedmetadata');
  tile.fire('playing');
  slide.fire('waiting');
  t.pg.tick(2000);
  const v = t.of('video');
  assert.deepEqual(v.map((e) => [e.id, e.ev, e.where]), [[1, 'loadedmetadata', 'dialog'], [2, 'playing', 'tile'], [1, 'waiting', 'dialog']]);
  assert.equal(v[0].rs, 1);
  assert.equal(v[0].url, 'https://example.test/proxy/8182/clip.mp4');
});

test('the mark button logs a marker with the page\'s state, and sends it at once', () => {
  const t = setup('<video></video><img><img>');
  const b = t.pg.document.getElementById('__perfmark');
  assert.ok(b, 'the button is on the page');
  b.click();
  assert.equal(t.sent.length, 1, 'without waiting for the next batch');
  const m = t.of('mark')[0];
  assert.deepEqual([m.n, m.videos, m.imgs, m.dialog], [1, 1, 2, false]);
  assert.equal(t.of('tap').length, 0, 'not a tap on the page');
});

test('the button moves into an open dialog, which covers the rest of the page', () => {
  const t = setup('<dialog id="ss"></dialog>');
  const dlg = t.pg.document.getElementById('ss');
  const b = t.pg.document.getElementById('__perfmark');
  assert.equal(b.parentNode, t.pg.document.body);
  dlg.setAttribute('open', '');
  t.pg.tick(300);
  assert.equal(b.parentNode, dlg);
  dlg.removeAttribute('open');
  t.pg.tick(300);
  assert.equal(b.parentNode, t.pg.document.body);
});

test('a page that ended without closing (iOS killed it) is noted by the next', () => {
  const first = setup('');
  first.pg.tick(2000);
  const sid = first.sent[0].batch.sid;
  // The same tab's next page, which finds the first one's marker.
  const next = setup('', { session: first.session });
  next.pg.tick(2000);
  assert.equal(next.of('unclean-exit')[0].prevSid, sid);
  // One that closed properly leaves no marker.
  next.pg.fire('pagehide', { persisted: false });
  const after = setup('', { session: next.session });
  after.pg.tick(2000);
  assert.equal(after.of('unclean-exit').length, 0);
});

test('errors on the page are logged', () => {
  const t = setup('');
  t.pg.fire('error', { target: t.pg, message: 'boom', filename: 'https://example.test/app.js', lineno: 3, colno: 7 });
  t.pg.tick(2000);
  const e = t.of('err')[0];
  assert.equal(e.msg, 'boom');
  assert.match(e.at, /:3:7$/);
});
