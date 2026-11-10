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

const FILES = ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/slideshow.js'];

// util.js's question about the input device, spelled as it asks it.
const FINE_POINTER = '(hover: hover) and (pointer: fine), (any-hover: hover) and (any-pointer: fine)';
const FULLSCREEN_KEY = 'gows-slideshow-fullscreen';

// The stand-in has no full screen, so a test that wants one asks for it
// before the page's scripts run: requestFullscreen and exitFullscreen
// set what the document reports and fire the change event the page
// listens for.
function fullScreen(pg, $) {
  const doc = pg.document;
  doc.fullscreenEnabled = true;
  doc.fullscreenElement = null;
  const show = (el) => {
    doc.fullscreenElement = el;
    doc.fire('fullscreenchange');
  };
  $('ss-frame').requestFullscreen = function () {
    show(this);
    return Promise.resolve();
  };
  doc.exitFullscreen = function () { show(null); };
}

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
    stored: opts.stored,
    // Only a desktop test says a mouse is in use.
    media: opts.mouse ? { [FINE_POINTER]: true } : undefined,
    globals: {
      GOWS: { sort: 'name', thumbs: opts.thumbs !== false, videoThumbs: true, media },
      grid: null,
      Ambient: () => ambient,
      accent: { set() { }, clear() { } },
      vividColor: () => null,
    },
  });
  const $ = (id) => pg.document.getElementById(id);
  // The stage is the room the slideshow draws in; a phone test gives it
  // a phone's, portrait or landscape.
  const stage = opts.stage || [1280, 800];
  $('ss-stage').clientWidth = stage[0];
  $('ss-stage').clientHeight = stage[1];
  if (opts.fullscreen) fullScreen(pg, $);
  const s = load(pg, FILES, ['slideshow', 'all']);
  return {
    pg, $, all: s.all, slideshow: s.slideshow,
    dlg: $('ss'),
    slides: () => $('ss-stage').children,
    strip: () => $('ss-strip').children,
    key: (k) => $('ss').fire('keydown', { key: k }),
    full: () => pg.document.fullscreenElement || null,
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

// The browser paints its own control panel, scrubber included, along the
// bottom of a video, so the filmstrip hangs under the bar at the top of
// the frame instead - which leaves a video's strip nothing to dodge.
test('a video keeps its filmstrip, on a phone too', () => {
  const phone = setup(30, { videos: true, stage: [844, 390], fullscreen: true });
  phone.slideshow.open(phone.all, 8, null, false);
  assert.equal(phone.$('ss-strip').hidden, false, 'a photo has its strip');
  phone.key('ArrowRight');
  assert.equal(phone.$('ss-strip').hidden, false, "and so does a video's");

  const desk = setup(30, { videos: true, mouse: true });
  desk.slideshow.open(desk.all, 9, null, false);
  assert.equal(desk.$('ss-strip').hidden, false, 'where there is a mouse it stays');
});

// The strip used to sit at the bottom of the frame, where a video's
// scrubber does too, so the area that summoned it was there.
test('the area that summons the filmstrip is at the top', () => {
  const t = setup(30, { videos: true, mouse: true, fullscreen: true, stage: [1280, 800] });
  t.slideshow.open(t.all, 8, null, false);
  t.$('ss-full').click();
  t.$('ss-frame').rect = { top: 0, left: 0, width: 1280, height: 800 };
  const on = () => t.dlg.classList.contains('strip-on');
  // 200px up from the bottom edge, over where the strip used to sit.
  t.dlg.fire('pointermove', { clientY: 600, pointerType: 'mouse' });
  assert.equal(on(), false, 'that is no longer where it lives');
  t.dlg.fire('pointermove', { clientY: 100, pointerType: 'mouse' });
  assert.equal(on(), true, 'the top edge summons it');
  // A full-screen frame on a notched phone starts below the window's top
  // edge, and the band goes with it.
  const notch = setup(30, { videos: true, mouse: true, fullscreen: true, stage: [1280, 800] });
  notch.slideshow.open(notch.all, 9, null, false);
  notch.$('ss-full').click();
  notch.$('ss-frame').rect = { top: 40, left: 0, width: 1280, height: 760 };
  notch.dlg.fire('pointermove', { clientY: 200, pointerType: 'mouse' });
  assert.equal(notch.dlg.classList.contains('strip-on'), false, 'past the band, wherever it sits');
  notch.dlg.fire('pointermove', { clientY: 180, pointerType: 'mouse' });
  assert.equal(notch.dlg.classList.contains('strip-on'), true, 'the band moves with the frame');

  // Windowed, the strip is part of the chrome and needs no summoning.
  const win = setup(30, { videos: true, mouse: true, stage: [1280, 800] });
  win.slideshow.open(win.all, 9, null, false);
  win.dlg.fire('pointermove', { clientY: 700, pointerType: 'mouse' });
  assert.equal(win.$('ss-strip').hidden, false);
  assert.equal(win.dlg.classList.contains('strip-on'), false, 'nothing to summon');
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

test('a phone opens the gallery full screen', () => {
  const t = setup(30, { stage: [390, 844], fullscreen: true });
  t.slideshow.open(t.all, 5, null, false);
  assert.equal(t.full(), t.$('ss-frame'), 'the frame, never the dialog');
  assert.equal(t.dlg.classList.contains('is-fs'), true);
  assert.equal(t.slides()[0].style.padding, '0px 0px 0px 0px', 'the slide has the whole stage');
});

test('a landscape phone opens full screen too', () => {
  const t = setup(30, { stage: [844, 390], fullscreen: true });
  t.slideshow.open(t.all, 5, null, false);
  assert.equal(t.full(), t.$('ss-frame'));
});

test('a phone with no full screen still gets the whole stage', () => {
  // iOS Safari: there is no full screen to ask for, so the windowed
  // slide has to fill the phone by itself.
  const t = setup(30, { stage: [844, 390] });
  t.slideshow.open(t.all, 5, null, false);
  assert.equal(t.full(), null);
  assert.equal(t.slides()[0].style.padding, '0px 0px 0px 0px');
});

test('a desktop or a tablet opens the gallery in a window', () => {
  const desktop = setup(30, { stage: [1280, 800], fullscreen: true, mouse: true });
  desktop.slideshow.open(desktop.all, 5, null, false);
  assert.equal(desktop.full(), null);
  assert.equal(desktop.slides()[0].style.padding, '150px 96px 64px 96px', 'room for the bar and the filmstrip under it');

  const tablet = setup(30, { stage: [1024, 768], fullscreen: true });
  tablet.slideshow.open(tablet.all, 5, null, false);
  assert.equal(tablet.full(), null);
});

test('autoplay on a desktop still goes full screen', () => {
  const t = setup(30, { stage: [1280, 800], fullscreen: true, mouse: true });
  t.slideshow.open(t.all, 5, null, true);
  assert.equal(t.full(), t.$('ss-frame'));
});

test('a phone shown full screen once stays windowed after it declines', () => {
  const t = setup(30, { stage: [390, 844], fullscreen: true });
  t.slideshow.open(t.all, 5, null, false);
  assert.equal(t.full(), t.$('ss-frame'));
  // One press fills the screen, the next leaves full screen: the way to
  // say no, which is remembered.
  t.$('ss-full').click();
  assert.equal(t.full(), t.$('ss-frame'), 'still full screen, filling it');
  t.$('ss-full').click();
  assert.equal(t.full(), null);
  assert.equal(t.pg.stored[FULLSCREEN_KEY], 'off');

  const next = setup(30, { stage: [390, 844], fullscreen: true, stored: { [FULLSCREEN_KEY]: 'off' } });
  next.slideshow.open(next.all, 5, null, false);
  assert.equal(next.full(), null, 'the next gallery opens the way it was left');
});

test('asking for full screen forgets a phone declining it', () => {
  const t = setup(30, { stage: [1280, 800], fullscreen: true, mouse: true, stored: { [FULLSCREEN_KEY]: 'off' } });
  t.slideshow.open(t.all, 5, null, false);
  assert.equal(t.full(), null);
  t.$('ss-full').click();
  assert.equal(t.full(), t.$('ss-frame'));
  assert.equal(FULLSCREEN_KEY in t.pg.stored, false);
});
