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

// The strip of scenes a video's timeline stretches into (scenes.js,
// opened by player.js).

const test = require('node:test');
const assert = require('node:assert/strict');
const { page, load, plain, markup } = require('../../harness.js');

const NAMES = ['vpSceneStep', 'vpSceneCount', 'vpSceneNearest', 'vpSceneWindow', 'vpSceneScroll', 'vpSceneScan', 'vpSceneSize', 'vpPull', 'vpChain', 'vpChainDelay', 'vpCover',
  'VP_PULL_DEAD', 'VP_PULL_OPEN', 'VP_PULL_GIVE'];

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

test('the scenes come up in a chain: the nearest first, its neighbors after', () => {
  const p = sc();
  // A little way into the pull: only the nearest is moving.
  assert.ok(p.vpChain(0.1, 0) > 0);
  assert.equal(p.vpChain(0.1, 1), 0);
  // Further: the nearest is up, the next coming, the far ones waiting.
  const at = [0, 0.5, 1, 2, 3, 5].map((d) => p.vpChain(0.4, d));
  assert.equal(at[0], 1, 'the nearest, all the way up');
  for (let i = 1; i < at.length; i++) assert.ok(at[i] <= at[i - 1], 'each no further than the one nearer: ' + at);
  assert.ok(at[2] > 0 && at[2] < 1, 'the next on its way');
  assert.equal(at[5], 0, 'the far ones still in the bar');
  assert.equal(p.vpChain(p.VP_PULL_GIVE, 8), 0, 'so far out it waits for the snap');
  assert.equal(p.vpChain(0, 0), 0, 'nothing before the pull');
  // Eased: out, the nearest goes first; back, the farthest does.
  assert.deepEqual([0, 1, 2, 4].map((d) => p.vpChainDelay(d, true, 4)), [0, 40, 80, 160]);
  assert.deepEqual([0, 1, 2, 4].map((d) => p.vpChainDelay(d, false, 4)), [160, 120, 80, 0]);
  assert.equal(p.vpChainDelay(9, true, 4), 160, 'no longer than the farthest in view');
});

test('a picture covers its box, cut evenly', () => {
  const p = sc();
  assert.deepEqual(plain(p.vpCover(320, 180, 160, 90)), { x: 0, y: 0, w: 320, h: 180 });
  assert.deepEqual(plain(p.vpCover(400, 180, 160, 90)), { x: 40, y: 0, w: 320, h: 180 }, 'wider: the sides go');
  assert.deepEqual(plain(p.vpCover(180, 320, 160, 160)), { x: 0, y: 70, w: 180, h: 180 }, 'taller: top and bottom');
  assert.deepEqual(plain(p.vpCover(0, 0, 160, 90)), { x: 0, y: 0, w: 0, h: 0 });
});


test('pulling: nothing until past the dead zone, then in step, then open', () => {
  const p = sc();
  const D = p.VP_PULL_DEAD, O = p.VP_PULL_OPEN;
  assert.equal(p.vpPull(0, D, O).state, 'none', 'on the timeline');
  assert.equal(p.vpPull(-30, D, O).state, 'none', 'below it');
  assert.equal(p.vpPull(D, D, O).state, 'none', 'a wobble above it is still a scrub');
  const half = p.vpPull(D + O / 2, D, O);
  assert.equal(half.state, 'pulling');
  assert.equal(half.p, 0.5);
  assert.equal(half.r, p.VP_PULL_GIVE * 0.5, 'the strip lags the pull');
  assert.ok(half.r < half.p);
  assert.deepEqual(plain(p.vpPull(D + O, D, O)), { state: 'open', p: 1, r: 1 });
});

test('scrolling the strip is scrubbing backward: the time in its middle', () => {
  const p = sc();
  // 160 wide, 172 apart, after 16, in a view 600 wide: scene 5's middle
  // under the view's middle at 16 + 5 * 172 + 80 - 300 = 656.
  assert.equal(p.vpSceneScan(656, 600, 160, 172, 16, 60, 600), 300);
  assert.equal(p.vpSceneScan(656 + 86, 600, 160, 172, 16, 60, 600), 330, 'half way to the next');
  assert.equal(p.vpSceneScan(0, 100, 160, 172, 16, 60, 600), 0, 'never before the start');
  assert.equal(p.vpSceneScan(99999, 600, 160, 172, 16, 60, 600), 600, 'nor past the end');
  assert.equal(p.vpSceneScroll(5, 10, 160, 172, 16, 300, 600), 656, 'the reverse of vpSceneScroll');
});

// ---- On the page ----

const FILES = ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js',
  'custom-index/scenes.js', 'custom-index/player.js', 'custom-index/metaview.js', 'custom-index/info.js', 'custom-index/slideshow.js'];

// The strip's height in the stand-in, which has no layout: the bar
// (26) and 6 either side of it, and the compact dock's 63px scenes and 20
// over them. Its clip when it's all out, and when it's the preview's
// card (the lit scene's box, and 6 around it).
const FULL = 38 + 63 + 20, OPEN_CLIP = 'inset(0px 0px 0px 0px round 0px)';
const insets = (clip) => (clip.match(/-?[\d.]+px/g) || []).slice(0, 4).map(parseFloat);

// A slideshow over two videos, talk-0.mp4 lasting dur seconds, with its
// metadata (meta) and the canvases' drawing recorded (draws).
function setup(opts) {
  opts = opts || {};
  const images = [], draws = [];
  const media = [[1, 'talk-0.mp4', 'talk-0.mp4', 2000, 1704110400], [1, 'talk-1.mp4', 'talk-1.mp4', 2000, 1704110400]];
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + markup('custom-index/custom-index.html', '<dialog class="ss"', '</dialog>'), {
    location: { hash: '', href: 'http://example.test/v/', pathname: '/v/' },
    globals: {
      GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media },
      grid: null,
      Ambient: () => ({ show() { }, reset() { }, clear() { } }),
      accent: { set() { }, clear() { } },
      vividColor: () => null,
      Image: class { constructor() { images.push(this); } },
      sessionStorage: { getItem: () => null, setItem() { }, removeItem() { } },
      fetch: () => Promise.resolve({
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
  const q = (c) => $('ss-frame').querySelector(c);
  return {
    pg, $, s, q, images, draws,
    dlg: $('ss'),
    key: (k, more) => $('ss').fire('keydown', Object.assign({ key: k }, more)),
    // Opens talk-0.mp4, lasting dur, laid out: the frame 800 high, the
    // video (and its dock) down to 700, its timeline 600 wide at 500,
    // the strip's row 600 wide.
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
      $('ss-frame').rect = { left: 0, top: 0, width: 1280, height: 800 };
      q('.vdock').rect = { left: 0, top: 160, width: 600, height: 540 };
      q('.vd-tl').rect = { left: 0, top: 500, width: 600, height: 26 };
      q('.vd-pop').rect = { left: 0, top: 0, width: 176, height: 140 };
      const view = q('.vd-scenes-view');
      view.clientWidth = 600;
      view.rect = { left: 0, top: 690, width: 600, height: 100 };
      return v;
    },
    panel: () => {
      const el = q('.vd-scenes');
      const scenes = el.querySelectorAll('.vd-scene');
      return {
        open: !el.hidden && el.classList.contains('open'), hidden: el.hidden, eased: el.classList.contains('eased'),
        height: el.style.height, clip: el.style.clipPath, gone: el.classList.contains('gone'), u: el.style.getPropertyValue('--u'), count: +el.dataset.count, mounted: scenes.map((f) => f._i),
        labels: scenes.map((f) => f.getAttribute('aria-label')),
        lit: scenes.filter((f) => f.classList.contains('on')).map((f) => f._i),
        active: el.getAttribute('aria-activedescendant'),
        // The video never moves for it.
        still: !s.slideshow.dock.current().style.translate && !q('.vdock').style.translate,
        up: $('ss').classList.contains('scenes-up'),
        // The bar's mark, where the strip is scrolled to.
        scan: q('.vd-scan').hidden ? null : q('.vd-scan').style.left,
        inside: el.parentNode === q('.vd-panel') && el.nextSibling === q('.vd-tl'),
      };
    },
    // The pointer on the timeline: down at x, then moves, then up.
    tl: (type, x, y, kind) => q('.vd-tl').fire(type, { clientX: x, clientY: y, pointerType: kind || 'mouse', pointerId: 4 }),
  };
}

test('S stretches the bar into the strip on the current time, a scene a minute, with the nearest lit', async () => {
  const t = setup();
  const v = await t.open(600);
  v.currentTime = 125;
  t.key('s');
  const s = t.panel();
  assert.equal(s.open, true);
  assert.equal(s.count, 10);
  assert.deepEqual(s.lit, [2], '2:00 is nearest 2:05');
  assert.equal(s.active, 'vd-scene-2');
  assert.deepEqual(s.labels.slice(0, 3), ['0:00', '1:00', '2:00']);
  assert.equal(s.height, FULL + 'px', 'all the way up');
  assert.equal(s.clip, OPEN_CLIP, 'all the way out');
  assert.equal(s.u, '1');
  assert.equal(s.eased, true, 'easing up, not jumping');
  assert.equal(s.inside, true, 'the timeline grown: in the dock, behind the bar');
  assert.equal(s.still, true, 'the video doesn\'t move');
  assert.equal(s.scan, '20%', 'the bar\'s mark on the lit scene, 2:00 of 10:00');
  assert.equal(s.up, true, 'the filmstrip stays put while it\'s up');
  assert.equal(t.s.slideshow.dock.holdsChrome(), true, 'the controls stay while it\'s up');
});

test('a long video keeps only the scenes near the view in the page', async () => {
  const t = setup();
  const v = await t.open(3 * 3600);
  v.currentTime = 90 * 60;
  t.key('s');
  let s = t.panel();
  assert.equal(s.count, 180);
  assert.deepEqual(s.lit, [90]);
  assert.ok(s.mounted.length <= 14, 'a window of them, not 180: ' + s.mounted.length);
  assert.ok(s.mounted.includes(90));
  const view = t.q('.vd-scenes-view');
  view.scrollLeft = 124 * 10;
  view.fire('scroll');
  t.pg.frame();
  s = t.panel();
  assert.ok(s.mounted.includes(10) && !s.mounted.includes(90), 'scrolled: the ones now in view: ' + s.mounted);
  assert.ok(s.mounted.length <= 14);
  assert.deepEqual(s.lit, [], 'the lit one is out of the page');
  // The wheel, up and down, scrolls it sideways.
  const e = view.fire('wheel', { deltaY: 248, deltaX: 0, deltaMode: 0 });
  assert.equal(e.defaultPrevented, true);
  assert.equal(view.scrollLeft, 124 * 12);
});

test('the keys move the lit scene and the bar\'s mark; Escape folds the strip and leaves the slideshow open', async () => {
  const t = setup();
  const v = await t.open(600);
  v.currentTime = 60;
  t.key('s');
  t.key('ArrowRight');
  t.key('ArrowRight');
  assert.deepEqual(t.panel().lit, [3]);
  assert.equal(t.panel().scan, '30%');
  assert.equal(v.currentTime, 60, 'the arrows are the strip\'s, not the video\'s');
  t.key('Home');
  assert.deepEqual(t.panel().lit, [0]);
  const e = t.key('Escape');
  assert.equal(e.defaultPrevented, true, 'the dialog doesn\'t close');
  const back = (i) => parseInt(t.q('#vd-scene-' + i).style.getPropertyValue('--cd'), 10);
  assert.ok(back(0) > back(2), 'folding, the farthest goes first and the lit one last: ' + [back(0), back(2)]);
  assert.equal(t.q('#vd-scene-0').style.getPropertyValue('--p'), '0');
  assert.equal(t.dlg.open, true);
  let s = t.panel();
  assert.equal(s.open, false);
  assert.ok(insets(s.clip)[3] > 0, 'narrowing back to the card: ' + s.clip);
  assert.equal(s.gone, true, 'and going');
  assert.equal(s.u, '0');
  assert.equal(s.scan, null, 'the mark goes');
  assert.equal(s.up, false);
  t.pg.tick(800);
  s = t.panel();
  assert.equal(s.hidden, true);
  assert.deepEqual(s.mounted, []);
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
  assert.equal(zoom.parentNode, t.q('.vdock'), 'over the video');
  assert.equal(zoom.classList.contains('grow'), true);
  assert.equal(t.panel().open, false, 'the strip folds');
  assert.notEqual(t.panel().clip, OPEN_CLIP);
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
  t.q('#vd-scene-4').querySelector('.vd-scene-time').fire('click');
  assert.equal(v.currentTime, 240);
  // A seek that never comes back doesn't leave the picture up.
  t.pg.tick(2200);
  assert.equal(t.q('.vd-zoom'), null);
  assert.equal(v.paused, false);
});

test('the timeline held and pulled up stretches into the strip, and past a point opens it', async () => {
  const t = setup();
  const v = await t.open(600);
  const D = 12, O = 110;
  t.tl('pointerdown', 300, 510);
  t.tl('pointermove', 300, 505);
  assert.equal(v.currentTime, 300, 'a scrub, so far');
  assert.equal(t.panel().hidden, true);
  // Half way: the strip is 0.4 of the way up, and nothing seeks.
  t.tl('pointermove', 360, 500 - D - O / 2);
  let s = t.panel();
  assert.equal(s.hidden, false);
  assert.equal(s.open, false);
  assert.equal(s.eased, false, 'with the pointer, not eased');
  // The card grown out sideways 0.64 of the way (a little ahead of the
  // scenes): 0.36 of its insets left.
  const [top, , , left] = insets(s.clip);
  assert.equal(top, Math.round(14 * 0.36), 'the card\'s top, nearly the strip\'s');
  assert.ok(left > 0, 'not all the way out: ' + s.clip);
  assert.equal(s.u, '0.4', 'the scenes unfolding as far');
  assert.equal(s.still, true);
  assert.equal(t.q('.vdock').classList.contains('pulling'), true, 'the preview gives way to the strip');
  assert.equal(v.currentTime, 300, 'pulling doesn\'t seek');
  assert.deepEqual(s.lit, [5], 'on the time the pull started from');
  // The chain, out of the bar: the scene under the pointer is the
  // preview, up all along; its neighbors first, the farther ones after.
  const rise = (i) => parseFloat(t.q('#vd-scene-' + i).style.getPropertyValue('--p'));
  assert.equal(rise(5), 1, 'the preview\'s scene, up all along');
  assert.ok(rise(4) > 0 && Math.abs(rise(4) - rise(6)) < 0.05, 'its neighbors first, alike');
  assert.ok(rise(3) < rise(4) && rise(2) < rise(3) && rise(7) < rise(6), 'the next ones behind them');
  assert.equal(rise(0), 0, 'the far ones still in the bar');
  // Past the point: open.
  t.tl('pointermove', 360, 500 - D - O);
  s = t.panel();
  assert.equal(s.open, true);
  assert.equal(s.height, FULL + 'px');
  assert.equal(s.eased, true, 'it snaps the rest of the way');
  assert.equal(t.q('.vdock').classList.contains('scrubbing'), false, 'the scrub is over');
  // The rest of the chain comes up, each after the one nearer.
  const wait = (i) => parseInt(t.q('#vd-scene-' + i).style.getPropertyValue('--cd'), 10);
  assert.equal(rise(0), 1, 'all on their way up');
  assert.equal(s.clip, OPEN_CLIP);
  assert.ok(wait(5) < wait(4) && wait(4) < wait(3) && wait(3) < wait(2), 'the nearest first');
  t.tl('pointerup', 360, 300);
  assert.equal(v.currentTime, 300, 'letting go doesn\'t seek');
  assert.equal(t.panel().open, true);
});

test('let go before it opens and it springs back into the bar, the press a scrub', async () => {
  const t = setup();
  const v = await t.open(600);
  t.tl('pointerdown', 300, 510);
  t.tl('pointermove', 300, 430);
  assert.equal(t.panel().hidden, false);
  t.tl('pointerup', 120, 430);
  let s = t.panel();
  assert.notEqual(s.clip, OPEN_CLIP, 'back to the card');
  assert.equal(s.eased, true);
  assert.equal(v.currentTime, 120, 'seeks where it was let go, as a scrub');
  t.pg.tick(800);
  assert.equal(t.panel().hidden, true);
  // Pulled, then back down below the line: back down, and scrubbing on.
  t.tl('pointerdown', 300, 510);
  t.tl('pointermove', 300, 440);
  t.tl('pointermove', 240, 510);
  assert.notEqual(t.panel().clip, OPEN_CLIP);
  assert.equal(v.currentTime, 240, 'scrubbing again');
  t.tl('pointerup', 240, 510);
});

test('a mouse only hovering never brings it up', async () => {
  const t = setup();
  await t.open(600);
  t.tl('pointermove', 300, 510);
  t.tl('pointermove', 300, 300);
  t.tl('pointerleave', 300, 300);
  assert.equal(t.panel().hidden, true);
});

test('a finger stretches it the same way, and the video plays on after', async () => {
  const t = setup();
  const v = await t.open(600);
  v.play();
  t.tl('pointerdown', 150, 510, 'touch');
  assert.equal(v.paused, true, 'scrubbing');
  t.tl('pointermove', 180, 505, 'touch');
  t.tl('pointermove', 180, 360, 'touch');
  assert.deepEqual(t.panel().lit, [3], 'on the time it was scrubbed to');
  assert.equal(t.panel().open, true);
  t.tl('pointerup', 180, 360, 'touch');
  // A tap anywhere else folds it, and the video goes on.
  t.q('.vd-play').fire('pointerdown', { pointerType: 'touch' });
  assert.equal(t.panel().open, false);
  assert.equal(v.paused, false);
});

test('swiped down, the strip folds back into the bar', async () => {
  const t = setup();
  await t.open(600);
  t.key('s');
  const view = t.q('.vd-scenes-view');
  view.fire('pointerdown', { clientX: 300, clientY: 700, pointerType: 'touch', pointerId: 9 });
  view.fire('pointermove', { clientX: 305, clientY: 730, pointerType: 'touch', pointerId: 9 });
  assert.equal(t.panel().open, true, 'not yet');
  view.fire('pointermove', { clientX: 310, clientY: 760, pointerType: 'touch', pointerId: 9 });
  assert.equal(t.panel().open, false);
  assert.notEqual(t.panel().clip, OPEN_CLIP);
});

test('scrolled, the bar\'s mark follows the time in the middle, and new scenes slide in', async () => {
  const t = setup();
  await t.open(3 * 3600);
  t.key('s');
  const view = t.q('.vd-scenes-view');
  // 112 wide, 124 apart: scene 30's middle in the view's middle.
  view.scrollLeft = 16 + 30 * 124 + 56 - 300;
  view.fire('scroll');
  t.pg.frame();
  const s = t.panel();
  assert.equal(s.scan, (30 * 60 / (3 * 3600) * 100) + '%');
  const fresh = t.q('#vd-scene-30');
  assert.ok(fresh.classList.contains('enter'), 'slides in');
  assert.equal(fresh.style.getPropertyValue('--ex'), '36px', 'from the side it scrolls from');
});

test('the preview rises out of the bar when it comes', async () => {
  const t = setup();
  await t.open(600);
  const pop = t.q('.vd-pop');
  t.tl('pointermove', 300, 510);
  assert.equal(pop.hidden, false);
  assert.equal(pop.classList.contains('rise'), true);
  pop.classList.remove('rise');
  t.tl('pointermove', 320, 510);
  assert.equal(pop.classList.contains('rise'), false, 'once, as it comes, not on every move');
});

test('the video\'s colors bleed into its top', async () => {
  const t = setup();
  await t.open(600);
  t.key('s');
  const glow = t.q('.vd-scenes-glow');
  const d = t.draws.find((x) => x.canvas === glow);
  assert.ok(d, 'drawn from the video');
  assert.deepEqual(d.args.slice(0, 4), [0, 720 * 0.8, 1280, 720 * 0.2], 'its bottom fifth');
  assert.equal(glow.classList.contains('drawn'), true);
});

test('a short video gets a few scenes, evenly', async () => {
  const t = setup();
  await t.open(90);
  t.key('s');
  assert.deepEqual(t.panel().labels, ['0:00', '0:20', '0:40', '1:00', '1:20']);
});

test('chapters name their scenes', async () => {
  const t = setup({ meta: { duration: 600, chapters: [{ start: 0, end: 240, title: 'Intro' }, { start: 240, end: 600, title: 'Main' }] } });
  await t.open(600);
  t.key('s');
  assert.deepEqual(t.panel().labels.slice(3, 5), ['3:00, Intro', '4:00, Main']);
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
  assert.equal(t.panel().hidden, true);
  assert.equal(t.dlg.classList.contains('scenes-up'), false);
});

test('the timeline, the time and each control come and go in order', async () => {
  const t = setup();
  await t.open(600);
  const dock = t.q('.vdock');
  const risers = dock.querySelectorAll('.vd-rise');
  const order = risers.map((x) => +x.style.getPropertyValue('--vd-i'));
  assert.deepEqual(order, order.map((_, i) => i), 'a step each, in the page\'s order');
  assert.ok(risers[0].classList.contains('vd-tl'), 'the timeline first');
  assert.ok(risers[1].classList.contains('vd-play'), 'then play');
  assert.ok(risers[2].classList.contains('vd-time'), 'then the time');
  assert.ok(risers.some((x) => x.classList.contains('vd-full')), 'and every control, to full screen');
  assert.equal(dock.style.getPropertyValue('--vd-n'), String(risers.length - 1), 'going, the last first');
});

test('a scene and the preview are one size', () => {
  const p = sc();
  assert.deepEqual(plain(p.vpSceneSize(false, 16 / 9)), { w: 160, h: 90 });
  assert.deepEqual(plain(p.vpSceneSize(true, 16 / 9)), { w: 112, h: 63 }, 'the compact dock\'s');
  assert.deepEqual(plain(p.vpSceneSize(false, 9 / 16)), { w: 160, h: 160 }, 'a tall video\'s cut square');
  assert.deepEqual(plain(p.vpSceneSize(false, 4)), { w: 160, h: 67 }, 'a very wide one not too thin');
  assert.deepEqual(plain(p.vpSceneSize(false, 0)), { w: 160, h: 90 });
});

test('from the first pixel of the pull, the preview\'s scene is up, with the preview\'s picture', async () => {
  const sprite = { sprite: './talk-0.mp4?sprite=ab12', interval: 5, columns: 10, count: 120, width: 160, height: 90 };
  const t = setup({ meta: { duration: 600, thumbnails: sprite } });
  await t.open(600);
  t.tl('pointermove', 300, 510);
  t.images[0].onload();
  t.tl('pointerdown', 300, 510);
  t.tl('pointermove', 300, 505);
  const drawn = t.draws.length;
  t.tl('pointermove', 300, 500 - 12 - 2);
  const lit = t.q('#vd-scene-5');
  assert.equal(lit.style.getPropertyValue('--p'), '1', 'already up, where the preview is');
  assert.equal(t.q('#vd-scene-4').style.getPropertyValue('--p') < '0.2', true, 'its neighbors only starting');
  const seeded = t.draws.slice(drawn).find((d) => d.canvas === lit.querySelector('canvas'));
  assert.ok(seeded, 'its picture the preview\'s');
});

test('pulled open and still held, pulling back down folds it, nothing picked', async () => {
  const t = setup();
  const v = await t.open(600);
  t.tl('pointerdown', 300, 510);
  t.tl('pointermove', 300, 505);
  t.tl('pointermove', 300, 360);
  assert.equal(t.panel().open, true);
  // The pointer is the document's now (the timeline let it go).
  t.pg.document.fire('pointermove', { clientX: 300, clientY: 420, pointerId: 4, pointerType: 'mouse' });
  assert.equal(t.panel().open, true, 'still above the line');
  t.pg.document.fire('pointermove', { clientX: 300, clientY: 505, pointerId: 4, pointerType: 'mouse' });
  assert.equal(t.panel().open, false, 'back down: folding');
  assert.equal(v.currentTime, 300, 'nothing picked');
  t.pg.document.fire('pointerup', { clientX: 300, clientY: 505, pointerId: 4, pointerType: 'mouse' });
});

test('the wheel on the timeline brings the strip up on the time under the pointer, and then scrolls it', async () => {
  const t = setup();
  await t.open(600);
  t.tl('pointermove', 240, 510);
  const e = t.q('.vd-tl').fire('wheel', { deltaY: 40, deltaX: 0, deltaMode: 0, clientX: 240, clientY: 510 });
  assert.equal(e.defaultPrevented, true, 'not the page\'s');
  assert.equal(t.panel().open, true);
  assert.deepEqual(t.panel().lit, [4], '4:00, under the pointer');
  const view = t.q('.vd-scenes-view'), at = view.scrollLeft;
  t.q('.vd-tl').fire('wheel', { deltaY: 100, deltaX: 0, deltaMode: 0 });
  assert.equal(view.scrollLeft, at + 100, 'then it scrolls');
});

test('under the bar, the preview shows a chapter, never the time', async () => {
  const t = setup({ meta: { duration: 600, chapters: [{ start: 0, end: 300, title: 'Intro' }] } });
  await t.open(600);
  t.tl('pointermove', 120, 510);
  assert.equal(t.q('.vd-pop-text').hidden, false);
  assert.equal(t.q('.vd-pop-label').textContent, 'Intro');
  t.tl('pointermove', 450, 510);
  assert.equal(t.q('.vd-pop-text').hidden, true, 'no chapter: nothing under the bar');
});
