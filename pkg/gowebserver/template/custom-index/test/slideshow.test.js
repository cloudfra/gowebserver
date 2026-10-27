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
const { page, load, markup } = require('../../harness.js');

const FILES = ['custom-index/util.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/slideshow.js'];

// The slideshow over a folder of n photos (every tenth item a video
// when videos is set), on the template's own dialog markup.
function setup(n, opts) {
  opts = opts || {};
  const media = [];
  for (let i = 0; i < n; i++) {
    const v = opts.videos && i % 10 === 9;
    media.push([v ? 1 : 0, 'm' + i, 'm' + i + (v ? '.mp4' : '.jpg'), 2000, 1704110400]);
  }
  const ambient = { show() { }, reset() { }, clear() { } };
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + markup('custom-index/custom-index.html', '<dialog class="ss"', '</dialog>'), {
    innerWidth: opts.width || 1600,
    globals: {
      GOWS: { sort: 'name', thumbs: opts.thumbs !== false, videoThumbs: true, media },
      grid: null,
      Ambient: () => ambient,
      accent: { set() { }, clear() { } },
      vividColor: () => null,
    },
  });
  const $ = (id) => pg.document.getElementById(id);
  $('ss-stage').clientWidth = 1280;
  $('ss-stage').clientHeight = 800;
  const s = load(pg, FILES, ['slideshow', 'all']);
  return {
    pg, $, all: s.all, slideshow: s.slideshow,
    dlg: $('ss'),
    slides: () => $('ss-stage').children,
    strip: () => $('ss-strip').children,
    key: (k) => $('ss').fire('keydown', { key: k }),
  };
}

test('only the current slide and its neighbors exist', () => {
  const t = setup(10000);
  t.slideshow.open(t.all, 500, null, false);
  assert.equal(t.dlg.open, true);
  assert.equal(t.slides().length, 3);
  assert.equal(t.$('ss-name').textContent, 'm500.jpg');
  assert.equal(t.$('ss-sub').firstChild.textContent, '501 of ' + new Intl.NumberFormat().format(10000));

  for (let i = 0; i < 40; i++) t.key('ArrowRight');
  assert.equal(t.slides().length, 3, 'however far it goes');
  assert.equal(t.$('ss-name').textContent, 'm540.jpg');
  assert.ok(t.pg.document.querySelectorAll('*').length < 200, 'the page stays small');
});

test('the filmstrip holds only the thumbnails around the current slide', () => {
  const t = setup(10000);
  t.slideshow.open(t.all, 500, null, false);
  assert.equal(t.strip().length, 25);
  const cur = t.strip().filter((b) => b.hasAttribute('aria-current'));
  assert.deepEqual(cur.map((b) => b.getAttribute('aria-label')), ['m500.jpg']);
  t.key('End');
  assert.equal(t.strip().length, 13);
  assert.equal(t.$('ss-name').textContent, 'm9999.jpg');
});

test('the filmstrip holds as many thumbnails as fit across the window', () => {
  // 60px each; the current one stays in the middle.
  for (const [width, want] of [[1600, 25], [1280, 19], [390, 5], [200, 3]]) {
    const t = setup(1000, { width });
    t.slideshow.open(t.all, 500, null, false);
    assert.equal(t.strip().length, want, width + 'px');
    assert.equal(t.strip()[(want - 1) / 2].hasAttribute('aria-current'), true);
  }
});

test('the ends of the folder stop the arrows', () => {
  const t = setup(3);
  t.slideshow.open(t.all, 0, null, false);
  assert.equal(t.slides().length, 2);
  assert.equal(t.$('ss-prev').disabled, true);
  assert.equal(t.$('ss-next').disabled, false);
  t.key('ArrowLeft');
  assert.equal(t.$('ss-name').textContent, 'm0.jpg');
  t.key('End');
  assert.equal(t.$('ss-next').disabled, true);
  t.key('Home');
  assert.equal(t.$('ss-name').textContent, 'm0.jpg');
});

test('a slide shows the server\'s preview, and the links are to the original', () => {
  const t = setup(5);
  t.slideshow.open(t.all, 2, null, false);
  const srcs = t.pg.requests.filter((r) => r.el.parentNode && r.el.parentNode.parentNode === t.$('ss-stage')).map((r) => r.url);
  assert.ok(srcs.includes('m2?width=1024&height=1024&fit=inside'), srcs.join());
  assert.equal(t.$('ss-open').href, 'm2');
  assert.equal(t.$('ss-dl').getAttribute('download'), 'm2.jpg');
});

test('a filmstrip thumbnail that fails becomes the placeholder', async () => {
  const t = setup(5);
  t.slideshow.open(t.all, 2, null, false);
  await t.pg.settle();
  const reqs = t.pg.requests.filter((r) => r.el.parentNode && r.el.parentNode.parentNode === t.$('ss-strip'));
  assert.equal(reqs.length, 4, 'one batch');
  reqs.forEach((r, i) => r.el.fire(i === 0 ? 'error' : 'load'));
  t.pg.frame();
  const failed = t.strip().filter((b) => b.classList.contains('err'));
  assert.equal(failed.length, 1);
  assert.equal(failed[0].querySelector('img'), null);
  assert.equal(t.strip().filter((b) => b.querySelector('img')).length, 4);
});

test('without server thumbnails there is no filmstrip', () => {
  const t = setup(50, { thumbs: false });
  t.slideshow.open(t.all, 5, null, false);
  assert.equal(t.strip().length, 0);
  assert.equal(t.slides().length, 3);
});

test('a video slide loads before its neighbors\' videos', () => {
  const t = setup(100, { videos: true });
  // Only the videos (m9, m19, ...), from the fourth: m39, between m29
  // and m49.
  t.slideshow.open(t.all.filter((it) => it.video), 3, null, false);
  const vs = t.slides().map((d) => d.firstChild);
  assert.equal(vs.length, 3);
  const loaded = () => vs.filter((v) => v.src).map((v) => v.src).sort();
  assert.deepEqual(loaded(), ['m39'], 'only the current video loads');
  vs.find((v) => v.src === 'm39').fire('loadeddata');
  assert.deepEqual(loaded(), ['m29', 'm39', 'm49'], 'then its neighbors');
});

test('the neighbors load anyway if the current video is slow to show', () => {
  const t = setup(100, { videos: true });
  t.slideshow.open(t.all.filter((it) => it.video), 3, null, false);
  const vs = t.slides().map((d) => d.firstChild);
  assert.equal(vs.filter((v) => v.src).length, 1);
  t.pg.tick(3000);
  assert.equal(vs.filter((v) => v.src).length, 3);
});

test('stepping to a neighbor loads it now', () => {
  const t = setup(100, { videos: true });
  t.slideshow.open(t.all.filter((it) => it.video), 3, null, false);
  t.key('ArrowRight');
  const cur = t.slides().map((d) => d.firstChild).find((v) => v.src === 'm49');
  assert.ok(cur, 'm49 is loading as soon as it is current');
});

test('closing takes everything back out of the page', async () => {
  const t = setup(100, { videos: true });
  t.slideshow.open(t.all, 9, null, false);
  await t.pg.settle();
  assert.equal(t.slides().some((d) => d.firstChild.tagName === 'VIDEO'), true);
  t.$('ss-close').click();
  t.pg.frame();
  t.pg.tick(2000);
  t.pg.frame();
  t.pg.tick(2000);
  assert.equal(t.dlg.open, false);
  assert.equal(t.slides().length, 0);
  assert.equal(t.strip().length, 0);
  assert.equal(t.pg.document.documentElement.classList.contains('ss-lock'), false);
  // And it opens again.
  t.slideshow.open(t.all, 50, null, false);
  assert.equal(t.slides().length, 3);
});
