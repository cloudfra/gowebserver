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

// A page with the loader and n images queued in it; shown collects the
// images handed back, as 'N+' (loaded) or 'N-' (failed).
function setup(n, opts) {
  const pg = page('', opts);
  const { thumbLoader } = load(pg, ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js'], ['thumbLoader']);
  const shown = [];
  const imgs = [];
  const queue = (front) => {
    const i = imgs.length;
    const img = pg.document.createElement('img');
    imgs.push(img);
    thumbLoader.load(img, 't' + i, (ok) => shown.push(i + (ok ? '+' : '-')), front);
    return img;
  };
  for (let i = 0; i < n; i++) queue();
  const fetched = () => pg.requests.map((r) => r.url);
  return { pg, thumbLoader, shown, imgs, queue, fetched };
}

test('images queued one by one start as a single batch', async () => {
  const t = setup(10);
  assert.deepEqual(t.fetched(), [], 'nothing starts until the queueing is done');
  await t.pg.settle();
  assert.deepEqual(t.fetched(), ['t0', 't1', 't2', 't3']);
});

test('HTTP/2 and 3 share a connection, so a batch is bigger', async () => {
  for (const [protocol, size] of [['http/1.1', 4], ['h2', 12], ['h3', 12]]) {
    const t = setup(30, { protocol });
    await t.pg.settle();
    assert.equal(t.fetched().length, size, protocol);
  }
});

test('a batch is shown together, in one frame, once all of it has arrived', async () => {
  const t = setup(10);
  await t.pg.settle();
  t.imgs[0].fire('load');
  t.imgs[1].fire('error');
  t.imgs[2].fire('load');
  t.pg.frame();
  assert.deepEqual(t.shown, [], 'three of four arrived: nothing shown yet');
  assert.equal(t.fetched().length, 4, 'and the next batch has not started');

  t.imgs[3].fire('load');
  assert.deepEqual(t.shown, [], 'shown in a frame, not in the load event');
  assert.equal(t.pg.frame(), 1, 'one frame for the whole batch');
  assert.deepEqual(t.shown, ['0+', '1-', '2+', '3+']);
  await t.pg.settle();
  assert.deepEqual(t.fetched().slice(4), ['t4', 't5', 't6', 't7'], 'then the next batch starts');
});

test('a slow image holds its batch back only so long, then joins the next', async () => {
  const t = setup(10);
  await t.pg.settle();
  [0, 1, 2].forEach((i) => t.imgs[i].fire('load'));
  t.pg.tick(1499);
  t.pg.frame();
  assert.deepEqual(t.shown, []);
  t.pg.tick(1);
  t.pg.frame();
  assert.deepEqual(t.shown, ['0+', '1+', '2+']);
  await t.pg.settle();
  // Image 3 still holds a connection, so the next batch is one short.
  assert.deepEqual(t.fetched().slice(4), ['t4', 't5', 't6']);

  [4, 5, 6].forEach((i) => t.imgs[i].fire('load'));
  t.pg.frame();
  assert.deepEqual(t.shown, ['0+', '1+', '2+'], 'batch two waits for the straggler it took in');
  t.imgs[3].fire('load');
  t.pg.frame();
  assert.deepEqual(t.shown, ['0+', '1+', '2+', '4+', '5+', '6+', '3+']);
});

test('a straggler that arrives with no batch open is shown at once', async () => {
  const t = setup(4);
  await t.pg.settle();
  [0, 1, 2].forEach((i) => t.imgs[i].fire('load'));
  t.pg.tick(1500);
  t.pg.frame();
  t.imgs[3].fire('load');
  t.pg.frame();
  assert.deepEqual(t.shown, ['0+', '1+', '2+', '3+']);
});

test('images queued while a batch is open take its free slots', async () => {
  const t = setup(2);
  await t.pg.settle();
  t.queue();
  t.queue();
  t.queue();
  await t.pg.settle();
  assert.deepEqual(t.fetched(), ['t0', 't1', 't2', 't3']);
});

test('front puts an image ahead of everything waiting', async () => {
  const t = setup(6);
  await t.pg.settle();
  t.queue(true);
  [0, 1, 2, 3].forEach((i) => t.imgs[i].fire('load'));
  await t.pg.settle();
  assert.deepEqual(t.fetched().slice(4), ['t6', 't4', 't5']);
});

test('unqueue takes out an image that has not started, and only that', async () => {
  const t = setup(6);
  await t.pg.settle();
  assert.equal(t.thumbLoader.unqueue(t.imgs[5]), true);
  assert.equal(t.thumbLoader.unqueue(t.imgs[5]), false);
  assert.equal(t.thumbLoader.unqueue(t.imgs[0]), false, 'already loading');
  [0, 1, 2, 3].forEach((i) => t.imgs[i].fire('load'));
  await t.pg.settle();
  assert.deepEqual(t.fetched().slice(4), ['t4']);
});

test('cancel stops an image wherever it is and never shows it', async () => {
  const t = setup(9);
  await t.pg.settle();
  // Waiting.
  t.thumbLoader.cancel(t.imgs[8]);
  // Loading: the request is dropped and its late events are ignored.
  t.thumbLoader.cancel(t.imgs[0]);
  assert.equal(t.imgs[0].src, '');
  t.imgs[0].fire('load');
  // Arrived, waiting for its frame.
  t.imgs[1].fire('load');
  t.imgs[2].fire('load');
  t.imgs[3].fire('load');
  t.thumbLoader.cancel(t.imgs[1]);
  t.pg.frame();
  assert.deepEqual(t.shown, ['2+', '3+']);
  await t.pg.settle();
  assert.deepEqual(t.fetched().slice(4), ['t4', 't5', 't6', 't7']);
});

test('cancelling a whole batch moves on to the next', async () => {
  const t = setup(8);
  await t.pg.settle();
  [0, 1, 2, 3].forEach((i) => t.thumbLoader.cancel(t.imgs[i]));
  await t.pg.settle();
  assert.deepEqual(t.fetched().slice(4), ['t4', 't5', 't6', 't7']);
  t.pg.frame();
  assert.deepEqual(t.shown, []);
});

test('cancelling everything at once fetches none of what was waiting', async () => {
  // A row scrolled away cancels its tiles one by one: the batch that
  // was loading, then the ones behind it.
  const t = setup(40);
  await t.pg.settle();
  t.imgs.forEach((img) => t.thumbLoader.cancel(img));
  await t.pg.settle();
  assert.equal(t.fetched().length, 4);
});

test('cancel leaves an image the loader never had alone', () => {
  const t = setup(0);
  const img = t.pg.document.createElement('img');
  t.thumbLoader.cancel(img);
  assert.equal(img.src, '');
});

test('the queue drains completely', async () => {
  const t = setup(25);
  for (let guard = 0; t.shown.length < 25 && guard < 100; guard++) {
    await t.pg.settle();
    t.pg.requests.forEach((r) => r.el.fire('load'));
    t.pg.frame();
  }
  assert.equal(t.shown.length, 25);
  assert.equal(t.fetched().length, 25, 'each fetched once');
});

test('video thumbnails get at most half the slots, so photos behind them still load', async () => {
  const pg = page('', { protocol: 'h2' });
  const { thumbLoader } = load(pg, ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js'], ['thumbLoader']);
  const imgs = [];
  const queue = (name, heavy) => {
    const img = pg.document.createElement('img');
    imgs.push(img);
    thumbLoader.load(img, name, () => { }, false, heavy);
  };
  // A folder of videos, then its photos.
  for (let i = 0; i < 20; i++) queue('v' + i, true);
  for (let i = 0; i < 10; i++) queue('p' + i, false);
  await pg.settle();
  const fetched = () => pg.requests.map((r) => r.url);
  assert.deepEqual(fetched(), ['v0', 'v1', 'v2', 'v3', 'v4', 'v5', 'p0', 'p1', 'p2', 'p3', 'p4', 'p5'],
    'six of the twelve slots to videos, the rest to the photos queued after them');

  // The next batch, once all of this one is in: the next six videos,
  // and the photos that are left.
  imgs.forEach((img) => { if (img._thumb === 'loading') img.fire('load'); });
  pg.frame();
  await pg.settle();
  assert.deepEqual(fetched().slice(12), ['v6', 'v7', 'v8', 'v9', 'v10', 'v11', 'p6', 'p7', 'p8', 'p9']);
});
