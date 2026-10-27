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

const DAY = 86400;

// A folder of photos then videos, a day apart from 2024-01-01, newest
// first when sorted by date.
function folder(photos, videos) {
  const media = [];
  for (let i = 0; i < photos; i++) media.push([0, 'p' + i + '.jpg', 'p' + i + '.jpg', 1000 + i, 1704110400 + i * DAY]);
  for (let i = 0; i < (videos || 0); i++) media.push([1, 'v' + i + '.mp4', 'v' + i + '.mp4', 5000 + i, 1704110400 + i * DAY]);
  return media;
}

// The grid over media in a window 1280 wide and 800 tall, with the grid
// starting 100px down the page. The parts of the page it talks to but
// that aren't under test are stand-ins.
function setup(media, opts) {
  opts = Object.assign({ sort: 'name', thumbs: true, videoThumbs: true, width: 1280 }, opts);
  const opened = [];
  const pg = page('<p id="summary"></p><p id="no-match"></p><div id="media"></div>', {
    // A mouse, which is what hovers.
    media: { '(hover: hover) and (pointer: fine), (any-hover: hover) and (any-pointer: fine)': opts.mouse !== false },
    globals: {
      GOWS: { sort: opts.sort, thumbs: opts.thumbs, videoThumbs: opts.videoThumbs, media },
      pageAmbient: { show() { } },
      accent: { from() { } },
      scrubber: { measure() { }, place() { }, refresh() { } },
      slideshow: { open: (list, pos) => opened.push([list.length, pos]) },
    },
  });
  const el = pg.document.getElementById('media');
  el.clientWidth = opts.width;
  el.getBoundingClientRect = () => ({ top: 100 - pg.scrollY });
  const g = load(pg, ['custom-index/util.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/grid.js'],
    ['grid', 'applyFilter', 'all', 'fmtDuration']);
  g.applyFilter('');
  const tiles = () => el.querySelectorAll('.tile');
  return Object.assign(g, {
    pg, el, opened, tiles,
    rows: () => el.children,
    names: () => tiles().map((a) => a.getAttribute('aria-label')),
    // Answers every thumbnail request so far, then shows the batch.
    arrive(type) {
      pg.requests.splice(0).forEach((r) => { if (r.el.tagName === 'IMG') r.el.fire(type || 'load'); });
      pg.frame();
    },
  });
}

test('fmtDuration', () => {
  const g = setup([]);
  assert.equal(g.fmtDuration(0), '0:00');
  assert.equal(g.fmtDuration(9.9), '0:09');
  assert.equal(g.fmtDuration(75), '1:15');
  assert.equal(g.fmtDuration(3600), '1:00:00');
  assert.equal(g.fmtDuration(3725), '1:02:05');
});

test('an empty folder has no grid', () => {
  const g = setup([]);
  assert.equal(g.el.hidden, true);
  assert.equal(g.rows().length, 0);
});

test('photos are laid out in square tiles that fill the width', () => {
  const g = setup(folder(20));
  // 1280 wide: six columns of 208px tiles with 4px gaps.
  const first = g.el.querySelectorAll('.m-row')[1];
  assert.equal(first.children.length, 6);
  const w = (1280 - 4 * 5) / 6;
  assert.equal(first.children[0].style.width, w + 'px');
  assert.equal(first.children[0].style.height, w + 'px');
  assert.equal(first.children[1].style.left, (w + 4) + 'px');
  // A heading, then four rows for 20 photos.
  assert.equal(g.el.style.height, (56 + 4 * (w + 4)) + 'px');
  assert.equal(g.rows()[0].querySelector('h2').textContent, 'Photos');
  assert.equal(g.rows()[0].querySelector('.m-count').textContent, '20');
});

test('a phone gets at least three columns', () => {
  const g = setup(folder(20), { width: 320 });
  assert.equal(g.el.querySelectorAll('.m-row')[1].children.length, 3);
});

test('videos come first, in 16:9 tiles', () => {
  const g = setup(folder(4, 5));
  const heads = g.el.querySelectorAll('.m-head h2').map((h) => h.textContent);
  assert.deepEqual(heads, ['Videos', 'Photos']);
  const v = g.el.querySelector('.tile.vid');
  // Three columns with 12px gaps.
  const w = (1280 - 12 * 2) / 3;
  assert.equal(v.style.width, w + 'px');
  assert.equal(v.style.height, Math.round(w * 9 / 16) + 'px');
});

test('sorted by date, photos are grouped by month', () => {
  const g = setup(folder(40), { sort: 'date' });
  const groups = g.el.querySelectorAll('.m-group');
  const fmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' });
  assert.equal(groups[0].firstChild.textContent, fmt.format(new Date(1704110400 * 1000)));
  assert.equal(groups[0].querySelector('.m-count').textContent, '31');
  assert.equal(groups[1].querySelector('.m-count').textContent, '9');
});

test('only the rows near the window are in the page, however big the folder', () => {
  const g = setup(folder(50000));
  const w = (1280 - 4 * 5) / 6;
  assert.equal(g.el.style.height, (56 + Math.ceil(50000 / 6) * (w + 4)) + 'px', 'the page is as tall as the whole folder');
  assert.ok(g.tiles().length <= 80, g.tiles().length + ' tiles mounted');

  g.pg.tick(100000);
  g.pg.scroll(900000);
  assert.ok(g.tiles().length <= 120, g.tiles().length + ' tiles mounted after scrolling');
  const row = Math.floor((900000 - 100 - 56) / (w + 4));
  assert.ok(g.names().includes('p' + row * 6 + '.jpg'), 'the rows under the window are the mounted ones');
  assert.ok(!g.names().includes('p0.jpg'), 'and the first rows are gone');
});

test('thumbnails are fetched a batch at a time and tiles fill in together', async () => {
  const g = setup(folder(200));
  await g.pg.settle();
  assert.deepEqual(g.pg.requests.map((r) => r.url), [0, 1, 2, 3].map((i) => 'p' + i + '.jpg?width=256&height=256&fit=outside'));
  assert.equal(g.el.querySelectorAll('.tile.ready').length, 0);
  g.arrive();
  assert.deepEqual(g.el.querySelectorAll('.tile.ready').map((a) => a.getAttribute('aria-label')), ['p0.jpg', 'p1.jpg', 'p2.jpg', 'p3.jpg']);
  await g.pg.settle();
  assert.equal(g.pg.requests.length, 4, 'the next batch is on its way');
});

test('a thumbnail that fails leaves the placeholder', async () => {
  const g = setup(folder(4));
  await g.pg.settle();
  g.arrive('error');
  assert.equal(g.el.querySelectorAll('.tile.err').length, 4);
  assert.equal(g.el.querySelectorAll('.tile.ready').length, 0);
});

test('tiles scrolled past give up their place in the queue', async () => {
  const g = setup(folder(5000));
  await g.pg.settle();
  const first = g.pg.requests.map((r) => r.el);
  g.pg.requests.length = 0;
  g.pg.tick(100000);
  g.pg.scroll(60000);
  assert.ok(first.every((img) => img.src === ''), 'requests for unmounted tiles are dropped');
  await g.pg.settle();
  const w = (1280 - 4 * 5) / 6;
  const top = Math.floor((60000 - 100 - 56 - 400) / (w + 4)) * 6;
  const got = g.pg.requests.map((r) => Number(/p(\d+)/.exec(r.url)[1]));
  assert.equal(got.length, 4);
  assert.ok(got.every((n) => n >= top - 6), 'the next batch is from what is on screen now: ' + got);
});

test('nothing is fetched during a fling; it loads when the scroll settles', async () => {
  const g = setup(folder(5000));
  await g.pg.settle();
  g.pg.requests.length = 0;
  // 50,000px in 10ms.
  g.pg.tick(10);
  g.pg.scroll(50000);
  await g.pg.settle();
  assert.ok(g.tiles().length > 0, 'tiles are laid out');
  assert.equal(g.pg.requests.length, 0, 'but not loaded');
  g.pg.tick(110);
  g.pg.frame();
  await g.pg.settle();
  assert.equal(g.pg.requests.length, 4);
});

test('a fling keeps the rows it has, and their pictures', async () => {
  const g = setup(folder(5000));
  await g.pg.settle();
  g.arrive();
  const shown = g.el.querySelectorAll('.tile.ready');
  assert.equal(shown.length, 4);
  await g.pg.settle();
  const loading = g.pg.requests.map((r) => r.el);
  assert.equal(loading.length, 4);
  const before = g.rows().length;
  // Fast, but not far: everything that was there is still near.
  g.pg.tick(10);
  g.pg.scroll(300);
  g.pg.frame();
  assert.ok(shown.every((a) => a.isConnected && a.classList.contains('ready')), 'shown tiles stay');
  assert.ok(loading.every((img) => img.isConnected && img.src !== ''), 'pictures on the way are still wanted');
  assert.ok(g.rows().length >= before);
});

test('a video tile is the server\'s thumbnail; the video comes on hover', async () => {
  const g = setup(folder(0, 9));
  await g.pg.settle();
  // Thumbnails, through the same queue as the photos', and no videos.
  assert.equal(g.el.querySelectorAll('video').length, 0);
  assert.deepEqual(g.pg.requests.map((r) => r.url),
    [0, 1, 2, 3].map((i) => 'v' + i + '.mp4?width=512&height=288&fit=outside'));
  const tile = g.el.querySelector('.tile.vid');
  // Nothing to preview until there's a picture to fade from.
  tile.fire('pointerover');
  assert.equal(tile.querySelector('video'), null);
  g.arrive();
  assert.equal(tile.classList.contains('ready'), true);

  // A finger doesn't preview, even where a trackpad is attached too
  // (an iPad): its tap opens the video.
  tile.fire('pointerover', { pointerType: 'touch' });
  assert.equal(tile.querySelector('video'), null);

  // A mouse does, once it stays on the tile.
  tile.fire('pointerover', { pointerType: 'mouse' });
  g.pg.tick(299);
  assert.equal(tile.querySelector('video'), null);
  g.pg.tick(1);
  const v = tile.querySelector('video');
  assert.equal(v.src, 'v0.mp4');
  assert.equal(v.paused, false);
  assert.equal(v.currentTime, undefined, 'it plays from the start');
  assert.equal(tile.firstChild, v, 'under the thumbnail');
  assert.equal(g.el.querySelectorAll('video').length, 1);
  // The thumbnail gives way once the video is really playing.
  assert.equal(tile.classList.contains('vplay'), false);
  v.fire('playing');
  assert.equal(tile.classList.contains('vplay'), true);
  // Its length arrives with the video.
  assert.equal(tile.querySelector('.dur').textContent, '');
  v.duration = 75;
  v.fire('loadedmetadata');
  assert.equal(tile.querySelector('.dur').textContent, '1:15');

  // Leaving brings the thumbnail back, then pauses the same video.
  tile.fire('pointerout', { relatedTarget: null });
  assert.equal(tile.classList.contains('vplay'), false);
  g.pg.tick(400);
  assert.equal(v.paused, true);
  tile.fire('pointerover');
  g.pg.tick(300);
  assert.equal(tile.querySelectorAll('video').length, 1);
  assert.equal(v.paused, false);
});

test('a pointer crossing video tiles on its way elsewhere makes no videos', async () => {
  const g = setup(folder(0, 9));
  await g.pg.settle();
  g.arrive();
  for (const tile of g.el.querySelectorAll('.tile.vid')) {
    tile.fire('pointerover');
    g.pg.tick(120);
    tile.fire('pointerout', { relatedTarget: null });
  }
  g.pg.tick(1000);
  assert.equal(g.el.querySelectorAll('video').length, 0);
});

test('only the last two previews are kept once the pointer leaves', async () => {
  const g = setup(folder(0, 9));
  await g.pg.settle();
  g.arrive();
  const tiles = g.el.querySelectorAll('.tile.vid').slice(0, 3);
  const hover = (tile) => {
    tile.fire('pointerover');
    g.pg.tick(300);
    tile.fire('pointerout', { relatedTarget: null });
    g.pg.tick(400);
  };
  hover(tiles[0]);
  hover(tiles[1]);
  assert.equal(g.el.querySelectorAll('video').length, 2);
  hover(tiles[2]);
  assert.equal(tiles[0].querySelector('video'), null, 'the oldest is unloaded');
  assert.ok(tiles[1].querySelector('video') && tiles[2].querySelector('video'));
  // Back to one still kept: the same video, at once.
  const kept = tiles[1].querySelector('video');
  tiles[1].fire('pointerover');
  g.pg.tick(300);
  assert.equal(tiles[1].querySelector('video'), kept);
  // The slideshow drops them all.
  g.grid.dropPreviews();
  assert.equal(g.el.querySelectorAll('video').length, 0);
});

test('without a mouse a video tile never makes a video', async () => {
  const g = setup(folder(0, 2), { mouse: false });
  await g.pg.settle();
  g.arrive();
  g.el.querySelector('.tile.vid').fire('pointerover');
  assert.equal(g.el.querySelectorAll('video').length, 0);
});

test('a video whose thumbnail fails shows the video alone', async () => {
  const g = setup(folder(0, 1));
  await g.pg.settle();
  g.arrive('error');
  const tile = g.el.querySelector('.tile.vid');
  assert.equal(tile.querySelector('.vthumb'), null);
  assert.equal(tile.classList.contains('err'), false);
  // On screen, so it gets the video with the next frame.
  g.pg.frame();
  assert.equal(tile.querySelector('video').src, 'v0.mp4#t=0.5');
});

test('a tile that is the video makes it only once it is on screen', async () => {
  // Six rows of three, 12px apart: two and a bit fit above the fold.
  const g = setup(folder(0, 18), { videoThumbs: false });
  const made = () => g.tiles().filter((a) => a.querySelector('video')).map((a) => a.getAttribute('aria-label'));
  const ready = () => g.el.querySelectorAll('video').forEach((v) => { if (!v.parentNode.classList.contains('ready')) { v.duration = 9; v.fire('loadedmetadata'); } });
  assert.equal(g.tiles().length, 18, 'every row is near enough to be in the page');
  for (let i = 0; i < 4; i++) ready();
  const first = made();
  assert.ok(first.length >= 6 && first.length < 18, 'only the rows on screen: ' + first);
  assert.deepEqual(first, g.names().slice(0, first.length));

  // Scrolled a row at a time, the next rows get theirs as they arrive.
  g.pg.tick(1000);
  g.pg.scroll(250);
  g.pg.frame();
  for (let i = 0; i < 4; i++) ready();
  assert.ok(made().length > first.length);
  assert.ok(made().length <= 18);
});

test('a video tile scrolled off before its turn gives the turn up', async () => {
  const g = setup(folder(0, 60), { videoThumbs: false });
  // Three are loading; the rest of the screen waits.
  assert.equal(g.el.querySelectorAll('video').length, 3);
  g.pg.tick(100000);
  g.pg.scroll(2500);
  g.pg.frame();
  const loading = g.el.querySelectorAll('video');
  loading.forEach((v) => { v.duration = 9; v.fire('loadedmetadata'); });
  // What loads next is on screen now, not what was waiting back there.
  const top = g.pg.scrollY - 100, bottom = top + 800;
  const next = g.el.querySelectorAll('video').filter((v) => loading.indexOf(v) < 0);
  assert.ok(next.length > 0);
  for (const v of next) {
    const y = Number(/translateY\((.+)px\)/.exec(v.parentNode.parentNode.style.transform)[1]);
    assert.ok(y + 300 > top && y < bottom, 'row at ' + y + ' with the screen at ' + top);
  }
});

test('without server frames a video tile is the video, a few loading at once', () => {
  const g = setup(folder(0, 9), { videoThumbs: false });
  assert.equal(g.el.querySelectorAll('video').length, 3);
  assert.equal(g.el.querySelector('.vthumb'), null);
  const v = g.el.querySelector('video');
  // A frame from just after the start, which the browser picks.
  assert.equal(v.src, 'v0.mp4#t=0.5');
  // Its length arrives with the metadata, and frees the slot.
  v.duration = 75;
  v.fire('loadedmetadata');
  assert.equal(v.currentTime, undefined);
  assert.equal(v.parentNode.querySelector('.dur').textContent, '1:15');
  assert.equal(v.parentNode.classList.contains('ready'), true);
  assert.equal(g.el.querySelectorAll('video').length, 4);
});

test('a picture fades in at rest, and quickly while scrolling', async () => {
  const g = setup(folder(200));
  await g.pg.settle();
  g.arrive();
  const first = g.el.querySelector('.tile.ready');
  assert.ok(first);
  assert.equal(g.el.querySelectorAll('.tile.quick').length, 0);

  // A slow scroll: the grid loads as it goes.
  g.pg.tick(1000);
  g.pg.scroll(400);
  g.pg.frame();
  await g.pg.settle();
  const before = g.el.querySelectorAll('.tile.ready').length;
  g.pg.tick(20);
  g.arrive();
  const shown = g.el.querySelectorAll('.tile.ready').length - before;
  assert.ok(shown > 0);
  assert.equal(g.el.querySelectorAll('.tile.quick').length, shown);
  // Once the scroll has settled the tiles can fade again.
  g.pg.tick(300);
  assert.equal(g.el.querySelectorAll('.tile.quick').length, 0);
  assert.equal(g.el.querySelectorAll('.tile.ready').length, before + shown);
});

test('filtering lays out only what matches', () => {
  const g = setup(folder(200));
  g.applyFilter('p19');
  assert.deepEqual(g.names(), ['p19.jpg', 'p190.jpg', 'p191.jpg', 'p192.jpg', 'p193.jpg', 'p194.jpg', 'p195.jpg', 'p196.jpg', 'p197.jpg', 'p198.jpg', 'p199.jpg']);
  assert.equal(g.rows()[0].querySelector('.m-count').textContent, '11');
});

test('clicking a tile opens the slideshow there', () => {
  const g = setup(folder(20));
  const e = g.tiles()[7].fire('click');
  assert.equal(e.defaultPrevented, true);
  assert.deepEqual(g.opened, [[20, 7]]);
  // A modified click is left to the browser (open in a new tab).
  g.tiles()[8].fire('click', { ctrlKey: true });
  assert.equal(g.opened.length, 1);
});
