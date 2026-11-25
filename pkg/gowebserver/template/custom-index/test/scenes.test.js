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

// The strip of scenes over a video's timeline (scenes.js, opened by
// player.js).

const test = require('node:test');
const assert = require('node:assert/strict');
const { page, load, plain, markup } = require('../../harness.js');

const NAMES = ['vpSceneStep', 'vpSceneCount', 'vpSceneNearest', 'vpSceneWindow', 'vpSceneScroll', 'vpLift', 'vpCover',
  'VP_LIFT_OPEN', 'VP_LIFT_TOUCH', 'VP_LIFT_SIDE'];

function sc() {
  return load(page('', {}), ['custom-index/util.js', 'custom-index/scenes.js', 'custom-index/player.js'], NAMES);
}

test('a scene a minute, and a few for a short video', () => {
  const p = sc();
  const scenes = (dur) => {
    const step = p.vpSceneStep(dur);
    return [step, p.vpSceneCount(dur, step)];
  };
  assert.deepEqual(scenes(600), [60, 10], 'ten minutes, ten scenes');
  assert.deepEqual(scenes(3 * 3600), [60, 180]);
  assert.deepEqual(scenes(5 * 60), [60, 5], 'five minutes is the least with a scene a minute');
  assert.deepEqual(scenes(5 * 60 + 1), [60, 6], 'a second into the sixth minute is a scene');
  assert.deepEqual(scenes(299), [30, 10], 'shorter: the longest round step giving five or more');
  assert.deepEqual(scenes(120), [20, 6]);
  assert.deepEqual(scenes(90), [20, 5]);
  assert.deepEqual(scenes(60), [10, 6]);
  assert.deepEqual(scenes(30), [5, 6]);
  assert.deepEqual(scenes(20), [4, 5], 'too short even for 5 seconds: five evenly spaced');
  assert.deepEqual(scenes(2), [0.4, 5]);
  assert.deepEqual(scenes(0), [0, 0], 'no length, no scenes');
  assert.deepEqual(scenes(NaN), [0, 0]);
});

test('the lit scene is the one nearest the time', () => {
  const p = sc();
  assert.equal(p.vpSceneNearest(125, 60, 10), 2);
  assert.equal(p.vpSceneNearest(155, 60, 10), 3, 'nearer the next');
  assert.equal(p.vpSceneNearest(595, 60, 10), 9, 'never past the last');
  assert.equal(p.vpSceneNearest(-3, 60, 10), 0);
  assert.equal(p.vpSceneNearest(5, 60, 0), -1);
});

test('only the scenes in view and a few either side are kept', () => {
  const p = sc();
  // 160 wide, 172 apart, after 16: 600 pixels show scenes 0 to 3.
  assert.deepEqual(plain(p.vpSceneWindow(0, 600, 172, 16, 180, 3)), [0, 7]);
  assert.deepEqual(plain(p.vpSceneWindow(172 * 100, 600, 172, 16, 180, 3)), [96, 107]);
  assert.deepEqual(plain(p.vpSceneWindow(172 * 178, 600, 172, 16, 180, 3)), [174, 180], 'not past the last');
  assert.deepEqual(plain(p.vpSceneWindow(0, 600, 172, 16, 0, 3)), [0, 0]);
});

test('the lit scene opens under the preview, as far as the ends allow', () => {
  const p = sc();
  // Scene 5's middle (16 + 5 * 172 + 80 = 956) under x = 300.
  assert.equal(p.vpSceneScroll(5, 180, 160, 172, 16, 300, 600), 656);
  assert.equal(p.vpSceneScroll(0, 180, 160, 172, 16, 300, 600), 0, 'the first can\'t move right');
  // 10 scenes are 16 + 9 * 172 + 160 + 16 = 1740 wide: at most 1140 in.
  assert.equal(p.vpSceneScroll(9, 10, 160, 172, 16, 100, 600), 1140);
  assert.equal(p.vpSceneScroll(2, 3, 160, 172, 16, 300, 600), 0, 'a strip narrower than the view stays put');
});

test('rising from the timeline to the preview', () => {
  const p = sc();
  assert.equal(p.vpLift(0, 0, 88, 56).state, 'rail');
  assert.equal(p.vpLift(-10, 0, 88, 56).state, 'rail', 'below its top');
  const half = p.vpLift(28, 10, 88, 56);
  assert.equal(half.state, 'rising');
  assert.equal(half.p, 0.5);
  assert.equal(p.vpLift(56, 0, 88, 56).state, 'open');
  assert.equal(p.vpLift(30, 88 + p.VP_LIFT_SIDE, 88, 56).state, 'rising', 'a little off the side is still on the way');
  assert.equal(p.vpLift(30, 88 + p.VP_LIFT_SIDE + 1, 88, 56).state, 'away');
  assert.equal(p.vpLift(200, -300, 88, 56).state, 'away');
  assert.ok(p.VP_LIFT_TOUCH > p.VP_LIFT_OPEN, 'a finger goes further');
});

test('a picture covers its box, cut evenly', () => {
  const p = sc();
  assert.deepEqual(plain(p.vpCover(320, 180, 160, 90)), { x: 0, y: 0, w: 320, h: 180 });
  assert.deepEqual(plain(p.vpCover(400, 180, 160, 90)), { x: 40, y: 0, w: 320, h: 180 }, 'wider: the sides go');
  assert.deepEqual(plain(p.vpCover(180, 320, 160, 160)), { x: 0, y: 70, w: 180, h: 180 }, 'taller: top and bottom');
  assert.deepEqual(plain(p.vpCover(0, 0, 160, 90)), { x: 0, y: 0, w: 0, h: 0 });
});

// ---- On the page ----

const FILES = ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js',
  'custom-index/scenes.js', 'custom-index/player.js', 'custom-index/info.js', 'custom-index/slideshow.js'];

// A slideshow over two videos, talk-0.mp4 lasting dur seconds, with its
// metadata (meta) and the canvases' drawing recorded (draws).
function setup(opts) {
  opts = opts || {};
  const images = [], draws = [];
  const media = [[1, 'talk-0.mp4', 'talk-0.mp4', 2000, 1704110400], [1, 'talk-1.mp4', 'talk-1.mp4', 2000, 1704110400]];
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + markup('custom-index/custom-index.html', '<dialog class="ss"', '</dialog>'), {
    location: { hash: '', href: 'http://example.test/v/', pathname: '/v/' },
    media: opts.media,
    globals: {
      GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media },
      grid: null,
      Ambient: () => ({ show() { }, reset() { }, clear() { } }),
      accent: { set() { }, clear() { } },
      vividColor: () => null,
      Image: class { constructor() { images.push(this); } },
      sessionStorage: { getItem: () => null, setItem() { }, removeItem() { } },
      fetch: (url) => Promise.resolve({
        ok: true, json: () => Promise.resolve(Object.assign({ chapters: [], markers: [], groups: {}, subtitles: [] }, opts.meta)),
      }),
    },
  });
  pg.history.replaceState = function () { };
  // A canvas draws: what it's asked to draw is kept.
  const proto = Object.getPrototypeOf(pg.document.createElement('canvas'));
  proto.getContext = function () {
    const c = this;
    return { drawImage: (...a) => draws.push({ canvas: c, args: a.slice(1) }) };
  };
  const $ = (id) => pg.document.getElementById(id);
  $('ss-stage').clientWidth = 1280;
  $('ss-stage').clientHeight = 800;
  const s = load(pg, FILES, ['slideshow', 'vis', 'applyFilter']);
  s.applyFilter('');
  const q = (c) => $('ss-stage').querySelector(c);
  return {
    pg, $, s, q, images, draws,
    dlg: $('ss'),
    key: (k, more) => $('ss').fire('keydown', Object.assign({ key: k }, more)),
    // Opens talk-0.mp4, lasting dur, laid out: the timeline 600 wide at
    // the bottom of the window, the strip's view 600 wide over it.
    open: async (dur) => {
      s.slideshow.open(s.vis.videos, 0, null, false);
      await pg.settle();
      await pg.settle();
      const v = s.slideshow.dock.current();
      v.duration = dur;
      v.readyState = 4;
      v.currentTime = 0;
      v.paused = true;
      v.videoWidth = 1280;
      v.videoHeight = 720;
      v.play = () => { v.paused = false; v.fire('play'); return Promise.resolve(); };
      v.pause = () => { v.paused = true; v.fire('pause'); };
      v.fire('loadedmetadata');
      q('.vd-tl').rect = { left: 0, top: 500, width: 600, height: 26 };
      q('.vd-pop').rect = { left: 0, top: 0, width: 176, height: 140 };
      const view = q('.vd-scenes-view');
      view.clientWidth = 600;
      view.rect = { left: 0, top: 350, width: 600, height: 130 };
      return v;
    },
    strip: () => {
      const el = q('.vd-scenes');
      const scenes = el.querySelectorAll('.vd-scene');
      return {
        open: !el.hidden && el.classList.contains('open'), hidden: el.hidden, closing: el.classList.contains('closing'),
        count: +el.dataset.count, mounted: scenes.map((f) => f._i),
        labels: scenes.map((f) => f.getAttribute('aria-label')),
        lit: scenes.filter((f) => f.classList.contains('on')).map((f) => f._i),
        active: el.getAttribute('aria-activedescendant'),
      };
    },
  };
}

test('S opens the strip on the current time, a scene a minute, with the nearest lit', async () => {
  const t = setup();
  const v = await t.open(600);
  v.currentTime = 125;
  t.key('s');
  const s = t.strip();
  assert.equal(s.open, true);
  assert.equal(s.count, 10);
  assert.deepEqual(s.lit, [2], '2:00 is nearest 2:05');
  assert.equal(s.active, 'vd-scene-2');
  assert.deepEqual(s.labels.slice(0, 3), ['0:00', '1:00', '2:00']);
  assert.equal(t.q('.vdock').classList.contains('scenes-open'), true);
  assert.equal(t.s.slideshow.dock.holdsChrome(), true, 'the controls stay while it\'s up');
});

test('a long video keeps only the scenes near the view in the page', async () => {
  const t = setup();
  const v = await t.open(3 * 3600);
  v.currentTime = 90 * 60;
  t.key('s');
  let s = t.strip();
  assert.equal(s.count, 180);
  assert.deepEqual(s.lit, [90]);
  assert.ok(s.mounted.length <= 12, 'a window of them, not 180: ' + s.mounted.length);
  assert.ok(s.mounted.includes(90));
  const view = t.q('.vd-scenes-view');
  view.scrollLeft = 172 * 10;
  view.fire('scroll');
  t.pg.frame();
  s = t.strip();
  assert.ok(s.mounted.includes(10) && !s.mounted.includes(90), 'scrolled: the ones now in view: ' + s.mounted);
  assert.ok(s.mounted.length <= 12);
  assert.deepEqual(s.lit, [], 'the lit one is out of the page');
  // The wheel, up and down, scrolls it sideways.
  const e = view.fire('wheel', { deltaY: 344, deltaX: 0, deltaMode: 0 });
  assert.equal(e.defaultPrevented, true);
  assert.equal(view.scrollLeft, 172 * 12);
});

test('the keys move the lit scene; Escape folds the strip and leaves the slideshow open', async () => {
  const t = setup();
  const v = await t.open(600);
  v.currentTime = 60;
  t.key('s');
  t.key('ArrowRight');
  t.key('ArrowRight');
  assert.deepEqual(t.strip().lit, [3]);
  assert.equal(v.currentTime, 60, 'the arrows are the strip\'s, not the video\'s');
  t.key('Home');
  assert.deepEqual(t.strip().lit, [0]);
  const e = t.key('Escape');
  assert.equal(e.defaultPrevented, true, 'the dialog doesn\'t close');
  assert.equal(t.dlg.open, true);
  assert.equal(t.strip().closing, true, 'folding away');
  t.pg.tick(500);
  assert.equal(t.strip().hidden, true);
  assert.deepEqual(t.strip().mounted, []);
  assert.equal(t.q('.vdock').classList.contains('scenes-open'), false);
  t.key('ArrowRight');
  assert.equal(v.currentTime, 65, 'the arrows seek again');
});

test('a scene picked grows over the video, which goes there and plays', async () => {
  const t = setup();
  const v = await t.open(600);
  t.key('s');
  t.key('ArrowRight');
  t.key('ArrowRight');
  t.key('Enter');
  assert.equal(v.currentTime, 120);
  const zoom = t.q('.vd-zoom');
  assert.ok(zoom, 'the picked scene, growing');
  assert.equal(zoom.classList.contains('grow'), true);
  assert.equal(t.strip().closing, true);
  assert.equal(t.q('.vd-scenes').classList.contains('drop'), true, 'the rest drop out of its way');
  assert.equal(v.paused, true, 'not yet');
  v.fire('seeked');
  t.pg.tick(560);
  assert.equal(v.paused, false, 'there, and playing');
  assert.equal(zoom.classList.contains('fade'), true);
  t.pg.tick(400);
  assert.equal(t.q('.vd-zoom'), null);
});

test('a click on a scene picks it', async () => {
  const t = setup();
  const v = await t.open(600);
  t.key('s');
  const f = t.q('#vd-scene-4');
  f.querySelector('.vd-scene-time').fire('click');
  assert.equal(v.currentTime, 240);
  // A seek that never comes back doesn't leave the picture up.
  t.pg.tick(2200);
  assert.equal(t.q('.vd-zoom'), null);
  assert.equal(v.paused, false);
});

test('the mouse rising from the timeline lifts the preview, then unfolds the strip', async () => {
  const t = setup();
  await t.open(600);
  const tl = t.q('.vd-tl'), pop = t.q('.vd-pop');
  tl.fire('pointermove', { clientX: 300, clientY: 510, pointerType: 'mouse' });
  assert.equal(pop.hidden, false);
  // Out of the top, toward the preview: it stays, and lifts.
  tl.fire('pointerleave', { clientX: 300, clientY: 499, pointerType: 'mouse' });
  assert.equal(pop.hidden, false);
  t.pg.document.fire('pointermove', { clientX: 305, clientY: 472, pointerType: 'mouse' });
  assert.equal(pop.classList.contains('lifting'), true);
  assert.equal(pop.style.getPropertyValue('--lift'), '0.500');
  assert.equal(t.strip().hidden, true);
  // Up into it: the strip, on the time that was previewed.
  t.pg.document.fire('pointermove', { clientX: 305, clientY: 500 - 56, pointerType: 'mouse' });
  const s = t.strip();
  assert.equal(s.open, true);
  assert.deepEqual(s.lit, [5], '5:00, under the pointer at the timeline\'s middle');
  assert.equal(pop.hidden, true, 'the preview became the strip');
  assert.equal(pop.classList.contains('lifting'), false);
  // Back down toward the timeline: it folds.
  t.pg.document.fire('pointermove', { clientX: 305, clientY: 490, pointerType: 'mouse' });
  assert.equal(t.strip().closing, true);
  t.pg.tick(500);
  assert.equal(t.strip().hidden, true);
});

test('leaving the timeline any other way puts the preview away', async () => {
  const t = setup();
  await t.open(600);
  const tl = t.q('.vd-tl'), pop = t.q('.vd-pop');
  tl.fire('pointermove', { clientX: 300, clientY: 510, pointerType: 'mouse' });
  tl.fire('pointerleave', { clientX: 300, clientY: 527, pointerType: 'mouse' });
  assert.equal(pop.hidden, true, 'out of the bottom');
  tl.fire('pointermove', { clientX: 300, clientY: 510, pointerType: 'mouse' });
  tl.fire('pointerleave', { clientX: 300, clientY: 499, pointerType: 'mouse' });
  t.pg.document.fire('pointermove', { clientX: 300 + 88 + 40, clientY: 480, pointerType: 'mouse' });
  assert.equal(pop.hidden, true, 'off to the side');
  assert.equal(t.strip().hidden, true);
  // Back on the timeline, the preview follows it again.
  tl.fire('pointermove', { clientX: 120, clientY: 510, pointerType: 'mouse' });
  assert.equal(pop.hidden, false);
  assert.equal(t.q('.vd-pop-time').textContent, '2:00');
});

test('straying off the strip folds it after a moment, unless the pointer comes back', async () => {
  const t = setup();
  await t.open(600);
  t.key('s');
  const doc = t.pg.document;
  doc.fire('pointermove', { clientX: 300, clientY: 250, pointerType: 'mouse' });
  t.pg.tick(100);
  doc.fire('pointermove', { clientX: 300, clientY: 400, pointerType: 'mouse' });
  t.pg.tick(400);
  assert.equal(t.strip().open, true, 'back in time');
  doc.fire('pointermove', { clientX: 300, clientY: 250, pointerType: 'mouse' });
  t.pg.tick(300);
  assert.equal(t.strip().open, false);
  assert.equal(t.dlg.open, true);
});

test('a finger scrubbing drags up off the timeline for the strip, and the video plays on after', async () => {
  const t = setup();
  const v = await t.open(600);
  v.play();
  const tl = t.q('.vd-tl');
  tl.fire('pointerdown', { clientX: 150, clientY: 510, pointerType: 'touch', pointerId: 3 });
  assert.equal(v.paused, true, 'scrubbing');
  tl.fire('pointermove', { clientX: 180, clientY: 505, pointerType: 'touch', pointerId: 3 });
  assert.equal(v.currentTime, 180);
  tl.fire('pointermove', { clientX: 180, clientY: 470, pointerType: 'touch', pointerId: 3 });
  assert.equal(t.strip().hidden, true, 'not yet: a finger goes further');
  assert.equal(t.q('.vd-pop').classList.contains('lifting'), true);
  tl.fire('pointermove', { clientX: 180, clientY: 500 - 72, pointerType: 'touch', pointerId: 3 });
  assert.deepEqual(t.strip().lit, [3], 'on the time it was scrubbed to');
  assert.equal(t.q('.vdock').classList.contains('scrubbing'), false, 'the scrub is over');
  tl.fire('pointerup', { clientX: 180, clientY: 400, pointerType: 'touch', pointerId: 3 });
  assert.equal(v.currentTime, 180, 'lifting the finger doesn\'t seek');
  // A tap anywhere else folds it, and the video goes on.
  t.q('.vd-play').fire('pointerdown', { pointerType: 'touch' });
  assert.equal(t.strip().closing, true);
  assert.equal(v.paused, false);
});

test('a short video gets a few scenes, evenly', async () => {
  const t = setup();
  await t.open(90);
  t.key('s');
  assert.deepEqual(t.strip().labels, ['0:00', '0:20', '0:40', '1:00', '1:20']);
});

test('chapters name their scenes', async () => {
  const t = setup({ meta: { duration: 600, chapters: [{ start: 0, end: 240, title: 'Intro' }, { start: 240, end: 600, title: 'Main' }] } });
  await t.open(600);
  t.key('s');
  assert.deepEqual(t.strip().labels.slice(3, 5), ['3:00, Intro', '4:00, Main']);
});

test('the scenes are drawn from the sprite once it has loaded', async () => {
  const sprite = { sprite: './talk-0.mp4?sprite=ab12', interval: 5, columns: 10, count: 120, width: 160, height: 90 };
  const t = setup({ meta: { duration: 600, thumbnails: sprite } });
  await t.open(600);
  t.key('s');
  assert.equal(t.images.length, 1, 'the sprite, asked for once');
  assert.equal(t.q('.vd-scene.ready'), null, 'nothing drawn while it loads');
  t.images[0].onload();
  const at2 = t.draws.find((d) => d.canvas._t === 120);
  assert.ok(at2, 'scene 2:00 drawn');
  // Frame 24 of the sheet: fifth column, third row, all of it, into
  // the scene (the stand-in has no mouse, so the dock is the compact one,
  // with smaller scenes).
  assert.deepEqual(at2.args, [640, 180, 160, 90, 0, 0, 112, 63]);
  assert.ok(t.q('#vd-scene-2').classList.contains('ready'));
  assert.equal(t.images.length, 1);
});

test('moving to another slide folds the strip at once', async () => {
  const t = setup();
  await t.open(600);
  t.key('s');
  t.$('ss-next').click();
  assert.equal(t.$('ss-name').textContent, 'talk-1.mp4');
  assert.equal(t.strip().hidden, true);
});
