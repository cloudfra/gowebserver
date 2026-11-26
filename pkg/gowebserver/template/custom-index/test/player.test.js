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
const { page, load, plain, markup } = require('../../harness.js');

const NAMES = ['vpFmtTime', 'vpParseTime', 'vpHash', 'vpParseHash', 'vpGroupColors', 'vpLayout', 'vpHover', 'vpThumbAt',
  'vpParseThumbsVTT', 'vpStop', 'vpKey', 'vpSliderKey', 'vpRateStep', 'vpSegmentFill', 'VP_COLORS'];

function vp() {
  const pg = page('', {});
  return load(pg, ['custom-index/util.js', 'custom-index/scenes.js', 'custom-index/player.js'], NAMES);
}

// A talk with three chapters, markers in two groups, a range, and one
// marker of a kind the viewer may hide.
const META = {
  duration: 600,
  chapters: [
    { start: 0, end: 200, title: 'Part 1' },
    { start: 200, end: 420, title: 'Part 2' },
    { start: 480, end: 600, title: 'Q&A' },
  ],
  markers: [
    { time: 30, kind: 'slide', label: 'Agenda', group: 'home' },
    { time: 250, kind: 'question', label: 'From the room', group: 'away' },
    { time: 300, end: 360, kind: 'demo', label: 'Live demo' },
    { time: 700, kind: 'slide', label: 'Past the end' },
  ],
  groups: { home: { color: '#2563eb' }, away: {} },
};

test('times are written and read the way people write them', () => {
  const p = vp();
  assert.equal(p.vpFmtTime(0), '0:00');
  assert.equal(p.vpFmtTime(65.9), '1:05');
  assert.equal(p.vpFmtTime(3733), '1:02:13');
  assert.equal(p.vpFmtTime(65, true), '0:01:05', 'as long as the video it is part of');
  assert.equal(p.vpFmtTime(NaN), '0:00');
  assert.equal(p.vpParseTime('1:02:13'), 3733);
  assert.equal(p.vpParseTime('62:13'), 3733);
  assert.equal(p.vpParseTime('3733.5'), 3733.5);
  assert.equal(p.vpParseTime('1h2m13s'), 3733);
  assert.equal(p.vpParseTime('90s'), 90);
  assert.equal(p.vpParseTime('soon'), null);
  assert.equal(p.vpParseTime(''), null);
});

test('the address names the video and the time, in the fragment only', () => {
  const p = vp();
  assert.equal(p.vpHash('Game day #2.mp4', 3733), '#v=Game%20day%20%232.mp4&t=1:02:13');
  assert.equal(p.vpHash('talk.mp4', 0), '#v=talk.mp4');
  assert.deepEqual(plain(p.vpParseHash('#v=Game%20day%20%232.mp4&t=1:02:13')), { v: 'Game day #2.mp4', t: 3733 });
  assert.deepEqual(plain(p.vpParseHash('#t=95&v=talk.mp4')), { v: 'talk.mp4', t: 95 });
  assert.equal(p.vpParseHash('#t=95'), null, 'a time with no video');
  assert.equal(p.vpParseHash(''), null);
  assert.equal(p.vpParseHash('#v=%E0%A4%A'), null, 'malformed');
});

test('chapters are segments, with the time they leave out filled in', () => {
  const p = vp();
  const l = p.vpLayout(META, 600);
  assert.deepEqual(plain(l.segments.map((s) => [s.start, s.end, s.title, s.chapter])), [
    [0, 200, 'Part 1', true], [200, 420, 'Part 2', true], [420, 480, '', false], [480, 600, 'Q&A', true],
  ]);
  assert.equal(l.segments[1].l.toFixed(3), (200 / 6).toFixed(3));
  assert.equal(l.segments[1].w.toFixed(3), (220 / 6).toFixed(3));
  const total = l.segments.reduce((n, s) => n + s.w, 0);
  assert.ok(Math.abs(total - 100) < 1e-9, 'the bar is whole');
});

test('a video without chapters is one plain segment', () => {
  const p = vp();
  const l = p.vpLayout({ chapters: [], markers: [] }, 90);
  assert.deepEqual(plain(l.segments.map((s) => [s.l, s.w, s.chapter])), [[0, 100, false]]);
  assert.deepEqual(plain(p.vpLayout(null, 90).segments.map((s) => [s.l, s.w])), [[0, 100]]);
  assert.deepEqual(plain(p.vpLayout(META, 0)), { segments: [], ticks: [], bands: [] }, 'nothing before the length is known');
});

test('chapters past the end or overlapping are cut to fit', () => {
  const p = vp();
  const l = p.vpLayout({ chapters: [{ start: 0, end: 70, title: 'A' }, { start: 50, end: 200, title: 'B' }, { start: 300, end: 400, title: 'C' }] }, 100);
  assert.deepEqual(plain(l.segments.map((s) => [s.start, s.end, s.title])), [[0, 70, 'A'], [70, 100, 'B']]);
});

test('markers are ticks in their group\'s color, and stretches are bands', () => {
  const p = vp();
  const l = p.vpLayout(META, 600);
  assert.deepEqual(plain(l.ticks.map((m) => [m.time, m.label, m.color, +m.l.toFixed(3)])), [
    [30, 'Agenda', '#2563eb', 5],
    [250, 'From the room', p.VP_COLORS[0], 41.667],
  ], 'one past the end is left out; a group without a color takes the next of the palette');
  assert.deepEqual(plain(l.bands.map((b) => [b.time, b.end, b.l, b.w, b.color])), [[300, 360, 50, 10, '']]);
  const hidden = p.vpLayout(META, 600, { slide: true });
  assert.deepEqual(plain(hidden.ticks.map((m) => m.label)), ['From the room'], 'a hidden kind is not drawn');
});

test('hovering finds the chapter and the nearest marker', () => {
  const p = vp();
  const l = p.vpLayout(META, 600);
  let h = p.vpHover(l, 251.5, 3);
  assert.equal(h.chapter.title, 'Part 2');
  assert.equal(h.marker.label, 'From the room');
  h = p.vpHover(l, 260, 3);
  assert.equal(h.marker, null, 'too far from it');
  assert.equal(p.vpHover(l, 330, 3).marker.label, 'Live demo', 'inside a stretch');
  assert.equal(p.vpHover(l, 450, 3).chapter, null, 'between chapters');
  assert.equal(p.vpHover(l, 599, 3).chapter.title, 'Q&A');
});

test('the preview frame comes from the sprite, or the thumbnails track', () => {
  const p = vp();
  const sprite = { sprite: './talk.mp4?sprite', spriteURL: 'http://h/m/talk.mp4?sprite', interval: 5, columns: 10, count: 120, width: 160, height: 90 };
  assert.deepEqual(plain(p.vpThumbAt(sprite, null, 0)), { url: 'http://h/m/talk.mp4?sprite', x: 0, y: 0, w: 160, h: 90, sheetW: 1600, sheetH: 1080 });
  const t = p.vpThumbAt(sprite, null, 57);
  assert.deepEqual([t.x, t.y], [160, 90], 'the 12th frame: second column, second row');
  assert.deepEqual([p.vpThumbAt(sprite, null, 9999).x, p.vpThumbAt(sprite, null, 9999).y], [1440, 990], 'the last');
  assert.equal(p.vpThumbAt(null, null, 3), null);
  assert.equal(p.vpThumbAt({ vtt: './t.vtt' }, null, 3), null, 'the track not loaded yet');

  const vtt = 'WEBVTT\n\n00:00.000 --> 00:05.000\nsheet.jpg#xywh=0,0,128,72\n\n' +
    '00:05.000 --> 00:10.000\nsheet.jpg#xywh=128,0,128,72\n\n00:00:10.000 --> 00:00:15.000\nother.jpg\n';
  const cues = p.vpParseThumbsVTT(vtt, (u) => 'http://h/m/' + u);
  assert.deepEqual(plain(cues[1]), { start: 5, end: 10, url: 'http://h/m/sheet.jpg', x: 128, y: 0, w: 128, h: 72 });
  assert.equal(p.vpThumbAt(null, cues, 7.5).x, 128);
  assert.equal(p.vpThumbAt(null, cues, 12).url, 'http://h/m/other.jpg');
});

test('previous and next go between markers and chapters', () => {
  const p = vp();
  const times = [30, 250, 300];
  assert.equal(p.vpStop(times, 0, 1), 30);
  assert.equal(p.vpStop(times, 30, 1), 250, 'not the one it is on');
  assert.equal(p.vpStop(times, 300, 1), null);
  assert.equal(p.vpStop(times, 260, -1), 250);
  assert.equal(p.vpStop(times, 250.5, -1), 30, 'just past one goes to the one before');
  assert.equal(p.vpStop(times, 10, -1), null);
});

test('the keyboard model', () => {
  const p = vp();
  const k = (key, more) => p.vpKey(Object.assign({ key }, more));
  assert.equal(k(' '), 'toggle');
  assert.equal(k('k'), 'toggle');
  assert.equal(k('j'), 'seek:-10');
  assert.equal(k('L'), 'seek:10');
  assert.equal(k('ArrowLeft'), 'seek:-5');
  assert.equal(k('ArrowRight'), 'seek:5');
  assert.equal(k(','), 'frame:-1');
  assert.equal(k('.'), 'frame:1');
  assert.equal(k('['), 'marker:-1');
  assert.equal(k(']'), 'marker:1');
  assert.equal(k('{', { shiftKey: true, code: 'BracketLeft' }), 'chapter:-1');
  assert.equal(k('}', { shiftKey: true, code: 'BracketRight' }), 'chapter:1');
  assert.equal(k('ü', { shiftKey: true, code: 'BracketLeft' }), 'chapter:-1', 'whatever the layout types');
  assert.equal(k('<'), 'rate:-1');
  assert.equal(k('>'), 'rate:1');
  assert.equal(k('c'), 'captions');
  assert.equal(k('m'), 'mute');
  assert.equal(k('f'), 'fullscreen');
  assert.equal(k('PageUp'), 'slide:-1');
  assert.equal(k('PageDown'), 'slide:1');
  assert.equal(k('Escape'), null, 'the slideshow\'s');
  assert.equal(k('Home'), null);
  assert.equal(k('l', { ctrlKey: true }), null, 'the browser\'s');

  assert.equal(p.vpSliderKey({ key: 'ArrowRight' }, 100, 600), 105);
  assert.equal(p.vpSliderKey({ key: 'ArrowDown' }, 2, 600), 0);
  assert.equal(p.vpSliderKey({ key: 'PageUp' }, 100, 600), 160);
  assert.equal(p.vpSliderKey({ key: 'PageDown' }, 100, 600), 40);
  assert.equal(p.vpSliderKey({ key: 'End' }, 100, 600), 600);
  assert.equal(p.vpSliderKey({ key: 'Home' }, 100, 600), 0);
  assert.equal(p.vpSliderKey({ key: 'x' }, 100, 600), null);

  assert.equal(p.vpRateStep(1, 1), 1.25);
  assert.equal(p.vpRateStep(2, 1), 2);
  assert.equal(p.vpRateStep(0.25, -1), 0.25);
  assert.equal(p.vpRateStep(1.1, -1), 0.75, 'an odd rate steps from normal');
});

test('a segment shows what is played and buffered', () => {
  const p = vp();
  const seg = { start: 100, end: 200 };
  assert.equal(p.vpSegmentFill(seg, 150, [[0, 180]]),
    'linear-gradient(to right,var(--vd-played) 0,var(--vd-played) 50.00%,var(--vd-track) 50.00%,var(--vd-track) 50.00%,' +
    'var(--vd-buffered) 50.00%,var(--vd-buffered) 80.00%,var(--vd-track) 80.00%,var(--vd-track) 100%)');
  assert.equal(p.vpSegmentFill(seg, 0, []), 'linear-gradient(to right,var(--vd-played) 0,var(--vd-played) 0.00%,var(--vd-track) 0.00%,var(--vd-track) 100%)');
});

// ---- The dock in the slideshow ----

const FILES = ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/scenes.js', 'custom-index/player.js', 'custom-index/metaview.js', 'custom-index/info.js', 'custom-index/slideshow.js'];

// A slideshow over talk-0.mp4 to talk-4.mp4 and a photo, with fetch
// answering ?meta from metas, and a session store.
function setup(opts) {
  opts = opts || {};
  const media = [];
  for (let i = 0; i < 5; i++) media.push([1, 'talk-' + i + '.mp4', 'talk-' + i + '.mp4', 2000, 1704110400]);
  media.push([0, 'photo.jpg', 'photo.jpg', 2000, 1704110400]);
  const fetched = [], session = Object.assign({}, opts.session), replaced = [], images = [];
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + markup('custom-index/custom-index.html', '<dialog class="ss"', '</dialog>'), {
    location: { hash: opts.hash || '', href: 'http://example.test/media/folder/' + (opts.hash || ''), pathname: '/media/folder/' },
    globals: {
      GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media },
      grid: null,
      Ambient: () => ({ show() { }, reset() { }, clear() { } }),
      accent: { set() { }, clear() { } },
      vividColor: () => null,
      // Images the page loads (the preview sprite), which a test lets
      // load or fail.
      Image: class { constructor() { images.push(this); } },
      sessionStorage: {
        getItem: (k) => (k in session ? session[k] : null),
        setItem: (k, v) => { session[k] = String(v); },
        removeItem: (k) => { delete session[k]; },
      },
      fetch: (url) => {
        fetched.push(url);
        const m = (opts.metas || {})[url.replace(/\?meta$/, '')] || { chapters: [], markers: [], groups: {}, subtitles: [] };
        return Promise.resolve({ ok: true, json: () => Promise.resolve(m) });
      },
    },
  });
  pg.history.replaceState = function (state, title, url) { replaced.push(url); };
  const $ = (id) => pg.document.getElementById(id);
  $('ss-stage').clientWidth = 1280;
  $('ss-stage').clientHeight = 800;
  const s = load(pg, FILES, ['slideshow', 'all', 'vis', 'applyFilter']);
  s.applyFilter('');
  const video = () => {
    const d = s.slideshow.dock.current();
    return d;
  };
  return {
    pg, $, s, fetched, session, replaced, images, video,
    dlg: $('ss'),
    key: (k, more) => $('ss').fire('keydown', Object.assign({ key: k }, more)),
    // The current video, once it knows its length.
    ready: (dur) => {
      const v = video();
      v.duration = dur;
      v.readyState = 4;
      v.currentTime = v.currentTime || 0;
      v.paused = true;
      v.play = () => { v.paused = false; v.fire('play'); return Promise.resolve(); };
      v.pause = () => { v.paused = true; v.fire('pause'); };
      v.fire('loadedmetadata');
      return v;
    },
  };
}

test('the dock covers the current video, and asks for its metadata then', async () => {
  const t = setup();
  assert.deepEqual(t.fetched, [], 'nothing is asked for with the listing');
  t.s.slideshow.open(t.s.vis.videos, 1, null, false);
  const v = t.video();
  assert.equal(v.src, 'talk-1.mp4');
  assert.equal(v.controls, false, 'the dock is its controls');
  const dock = t.$('ss-stage').querySelector('.vdock');
  assert.ok(dock && !dock.hidden);
  assert.equal(dock.parentNode, v.parentNode, 'in the video\'s own slide');
  assert.deepEqual(t.fetched, ['talk-1.mp4?meta'], 'only the current video\'s, relative to it');
  t.key('PageDown');
  await t.pg.settle();
  assert.deepEqual(t.fetched, ['talk-1.mp4?meta', 'talk-2.mp4?meta']);
  t.key('PageUp');
  assert.equal(t.fetched.length, 2, 'once per video');
});

test('on a video the keys work the video, and Page Up and Down the slides', async () => {
  const t = setup({
    metas: {
      'talk-0.mp4': {
        duration: 600, chapters: [{ start: 0, end: 200, title: 'Part 1' }, { start: 200, end: 600, title: 'Part 2' }],
        markers: [{ time: 30, label: 'Agenda' }, { time: 250, label: 'Question' }], groups: {}, subtitles: [],
      },
    },
  });
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  await t.pg.settle();
  await t.pg.settle();
  const v = t.ready(600);
  v.currentTime = 100;
  t.key('ArrowRight');
  assert.equal(v.currentTime, 105);
  t.key('j');
  assert.equal(v.currentTime, 95);
  t.key(']');
  assert.equal(v.currentTime, 250, 'the next marker');
  t.key('[');
  assert.equal(v.currentTime, 30);
  t.key('}', { shiftKey: true, code: 'BracketRight' });
  assert.equal(v.currentTime, 200, 'the next chapter');
  t.key('{', { shiftKey: true, code: 'BracketLeft' });
  assert.equal(v.currentTime, 0);
  t.key(' ');
  assert.equal(v.paused, false, 'Space plays the video, not the slideshow');
  assert.equal(t.$('ss-auto').getAttribute('aria-pressed'), 'false');
  t.key('k');
  assert.equal(v.paused, true);
  v.playbackRate = 1;
  t.key('>');
  assert.equal(v.playbackRate, 1.25);
  t.key('m');
  assert.equal(v.muted, true);
  assert.equal(t.$('ss-name').textContent, 'talk-0.mp4', 'still on it');
  t.key('PageDown');
  assert.equal(t.$('ss-name').textContent, 'talk-1.mp4');
});

test('the timeline is a slider', async () => {
  const t = setup({ metas: { 'talk-0.mp4': { duration: 600, chapters: [{ start: 0, end: 600, title: 'All of it' }], markers: [], groups: {}, subtitles: [] } } });
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  await t.pg.settle();
  await t.pg.settle();
  const v = t.ready(600);
  const tl = t.$('ss-stage').querySelector('.vd-tl');
  assert.equal(tl.getAttribute('role'), 'slider');
  assert.equal(tl.getAttribute('aria-valuemax'), '600');
  tl.fire('keydown', { key: 'PageUp' });
  assert.equal(v.currentTime, 60);
  assert.equal(tl.getAttribute('aria-valuenow'), '60');
  assert.equal(tl.getAttribute('aria-valuetext'), '1:00 of 10:00, All of it');
  tl.fire('keydown', { key: 'End' });
  assert.ok(v.currentTime > 599);
});

test('a link to a video and a time opens the slideshow there', () => {
  const t = setup({ hash: '#v=talk-3.mp4&t=1:02' });
  assert.equal(t.s.slideshow.openFromHash(), true);
  assert.equal(t.dlg.open, true);
  assert.equal(t.$('ss-name').textContent, 'talk-3.mp4');
  const v = t.ready(600);
  assert.equal(v.currentTime, 62);
  const none = setup({ hash: '#v=missing.mp4&t=10' });
  assert.equal(none.s.slideshow.openFromHash(), false);
  assert.equal(!!none.dlg.open, false);
});

test('the address follows the video, and each is resumed where it was left', () => {
  const t = setup();
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  assert.deepEqual(t.replaced, ['#v=talk-0.mp4'], 'the fragment only: the path and query are the page\'s');
  const v = t.ready(600);
  v.currentTime = 125;
  v.fire('seeked');
  assert.equal(t.replaced[t.replaced.length - 1], '#v=talk-0.mp4&t=2:05');
  t.key('PageDown');
  assert.equal(t.session['gows-pos:/media/folder/\ntalk-0.mp4'], '125');
  t.key('PageUp');
  const again = t.ready(600);
  assert.equal(again.currentTime, 125, 'resumed');
  // A photo has no time to share.
  t.s.slideshow.open(t.s.vis.photos, 0, null, false);
});

test('subtitles are listed in the captions menu', async () => {
  const t = setup({
    metas: {
      'talk-0.mp4': {
        duration: 600, chapters: [], markers: [], groups: {},
        subtitles: [{ src: './talk-0.en.vtt', lang: 'en', label: 'English' }, { src: './talk-0.fr.srt?vtt', lang: 'fr', label: 'French' }],
      },
    },
  });
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  await t.pg.settle();
  await t.pg.settle();
  const cc = t.$('ss-stage').querySelector('.vd-cc');
  assert.equal(cc.hidden, false);
  const tracks = t.video().querySelectorAll('track');
  assert.equal(tracks.length, 2);
  assert.equal(tracks[1].srclang, 'fr');
  cc.click();
  const menu = t.$('ss-stage').querySelector('.vd-menu');
  assert.equal(menu.hidden, false);
  assert.equal(menu.classList.contains('open'), true, 'fading in');
  assert.equal(menu.querySelector('.vd-menu-title').textContent, 'Subtitles');
  const items = menu.querySelector('.vd-menu-list').children;
  assert.deepEqual(items.map((b) => b.textContent), ['Off', 'English', 'French']);
  assert.deepEqual(items.map((b) => b.getAttribute('role')), ['menuitemradio', 'menuitemradio', 'menuitemradio']);
  assert.equal(items[0].getAttribute('aria-checked'), 'true', 'the current choice is checked');
  assert.equal(cc.getAttribute('aria-expanded'), 'true');
  items[2].click();
  assert.equal(menu.classList.contains('open'), false, 'fading out');
  t.pg.tick(200);
  assert.equal(menu.hidden, true);
  assert.equal(cc.getAttribute('aria-expanded'), 'false');
  assert.equal(cc.getAttribute('aria-pressed'), 'true');
  assert.equal(t.pg.stored['gows-captions'], 'fr', 'remembered');
});

test('a marker kind or group named like a built-in is a kind like any other', () => {
  const p = vp();
  const l = p.vpLayout({ markers: [{ time: 10, kind: 'constructor', group: 'toString', label: 'Odd' }] }, 60);
  assert.equal(l.ticks.length, 1);
  assert.equal(l.ticks[0].color, p.VP_COLORS[0]);
});

test('the volume slider\'s keys stay on the volume', () => {
  const t = setup();
  t.s.slideshow.open(t.s.vis.videos, 2, null, false);
  t.ready(600);
  const volume = t.$('ss-stage').querySelector('.vd-volume');
  ['ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageDown'].forEach((key) => {
    assert.equal(volume.fire('keydown', { key }).stopped, true, key);
  });
  assert.equal(t.$('ss-name').textContent, 'talk-2.mp4');
});

test('the hover lights the timeline from the playhead to the pointer', () => {
  const p = vp();
  const seg = { start: 0, end: 100 };
  assert.equal(p.vpSegmentFill(seg, 20, [], 50),
    'linear-gradient(to right,transparent 20.00%,var(--vd-hover) 20.00%,var(--vd-hover) 50.00%,transparent 50.00%),' + p.vpSegmentFill(seg, 20, []));
  assert.equal(p.vpSegmentFill(seg, 20, [], 10), p.vpSegmentFill(seg, 20, []), 'behind the playhead, nothing');
  assert.equal(p.vpSegmentFill({ start: 60, end: 100 }, 20, [], 50), p.vpSegmentFill({ start: 60, end: 100 }, 20, []), 'nor in a later segment');
});

// The pointer on the timeline at x (of 600 pixels).
function hover(t, x) {
  const tl = t.$('ss-stage').querySelector('.vd-tl');
  tl.rect = { left: 0, top: 0, width: 600, height: 26 };
  tl.fire('pointermove', { clientX: x, clientY: 10, pointerType: 'mouse' });
  const q = (c) => t.$('ss-stage').querySelector(c);
  return {
    pop: !q('.vd-pop').hidden, frame: !q('.vd-pop-frame').hidden, img: !q('.vd-pop-img').hidden,
    bg: q('.vd-pop-img').style.backgroundImage, time: q('.vd-pop-time').textContent, label: q('.vd-pop-label').textContent,
    kind: q('.vdock').dataset.preview,
  };
}

test('the preview never shows an empty box: the sprite once it has loaded, the time until then', async () => {
  const sprite = { sprite: './talk-0.mp4?sprite=ab12', interval: 5, columns: 10, count: 120, width: 160, height: 90 };
  const t = setup({ metas: { 'talk-0.mp4': { duration: 600, chapters: [{ start: 0, end: 600, title: 'Part 1' }], markers: [], groups: {}, subtitles: [], thumbnails: sprite } } });
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  t.ready(600);
  // Before the metadata is in: the time, and no picture.
  let h = hover(t, 300);
  assert.equal(h.pop, true);
  assert.equal(h.time, '5:00');
  assert.equal(h.frame, false, 'no box without a picture');
  await t.pg.settle();
  await t.pg.settle();
  // The metadata names a sprite: it starts loading, and until it has,
  // there's still no box (the stand-in can't draw the video's frames).
  h = hover(t, 300);
  assert.equal(h.label, 'Part 1');
  assert.equal(t.images.length, 1);
  assert.match(t.images[0].src, /talk-0\.mp4\?sprite=ab12$/);
  assert.equal(h.frame, false, 'not a black box while the sprite is made');
  assert.equal(h.kind, 'none');
  // Loaded: the frame for the time, and the box shows it at once, with
  // the pointer still where it was.
  t.images[0].onload();
  h = hover(t, 300);
  assert.equal(h.frame, true);
  assert.equal(h.img, true);
  assert.equal(h.kind, 'image');
  const img = t.$('ss-stage').querySelector('.vd-pop-img');
  // The box is a scene's size (the stand-in has no mouse: the compact
  // dock's, 112 wide), the sheet scaled to it.
  assert.equal(img.style.backgroundPosition, '0px -378px', 'frame 60: first column, seventh row');
  assert.equal(img.style.backgroundSize, '1120px 756px', 'the whole sheet behind the box');
  const frame = t.$('ss-stage').querySelector('.vd-pop-frame');
  assert.deepEqual([frame.style.width, frame.style.height], ['112px', '63px'], 'as big as its scene in the strip');
  assert.equal(t.$('ss-stage').querySelector('.vd-pop').style.getPropertyValue('--vd-pop-h'), '63px', 'the card over the bar by as much');
  assert.equal(t.images.length, 1, 'loaded once');
});

test('a sprite that fails to load leaves the time, not a box', async () => {
  const sprite = { sprite: './talk-0.mp4?sprite=ab12', interval: 5, columns: 10, count: 120, width: 160, height: 90 };
  const t = setup({ metas: { 'talk-0.mp4': { duration: 600, chapters: [], markers: [], groups: {}, subtitles: [], thumbnails: sprite } } });
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  await t.pg.settle();
  await t.pg.settle();
  t.ready(600);
  hover(t, 100);
  t.images[0].onerror();
  const h = hover(t, 120);
  assert.equal(h.pop, true);
  assert.equal(h.frame, false);
  assert.equal(h.time, '2:00');
});

test('the menus: a title, a check on the choice, keys to move, Escape to close', () => {
  const t = setup();
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  const v = t.ready(600);
  v.playbackRate = 1.5;
  const rate = t.$('ss-stage').querySelector('.vd-rate');
  rate.click();
  const menu = t.$('ss-stage').querySelector('.vd-menu');
  const items = menu.querySelector('.vd-menu-list').children;
  assert.equal(menu.querySelector('.vd-menu-title').textContent, 'Speed');
  assert.deepEqual(items.map((b) => b.textContent), ['0.25×', '0.5×', '0.75×', 'Normal', '1.25×', '1.5×', '1.75×', '2×']);
  assert.equal(t.pg.document.activeElement, items[5], 'the focus starts on the choice');
  menu.fire('keydown', { key: 'ArrowDown' });
  assert.equal(t.pg.document.activeElement, items[6]);
  menu.fire('keydown', { key: 'End' });
  assert.equal(t.pg.document.activeElement, items[7]);
  menu.fire('keydown', { key: 'ArrowDown' });
  assert.equal(t.pg.document.activeElement, items[0], 'round to the top');
  assert.equal(t.s.slideshow.dock.holdsChrome(), true, 'an open menu keeps the controls');
  menu.fire('keydown', { key: 'Escape' });
  assert.equal(menu.classList.contains('open'), false);
  assert.equal(t.pg.document.activeElement, rate, 'back on its button');
  items[3].click();
  assert.equal(v.playbackRate, 1);
});

test('a narrow video gets the compact row, and the rest in More', async () => {
  const t = setup({
    metas: {
      'talk-0.mp4': {
        duration: 600, chapters: [{ start: 0, end: 300, title: 'Part 1' }, { start: 300, end: 600, title: 'Part 2' }],
        markers: [{ time: 30, label: 'Agenda' }], groups: {}, subtitles: [],
      },
    },
  });
  t.$('ss-stage').clientWidth = 390;
  t.$('ss-stage').clientHeight = 844;
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  await t.pg.settle();
  await t.pg.settle();
  const v = t.ready(600);
  v.playbackRate = 1;
  v.videoWidth = 1280;
  v.videoHeight = 720;
  v.fire('loadedmetadata');
  const dock = t.$('ss-stage').querySelector('.vdock');
  assert.equal(dock.classList.contains('compact'), true);
  t.$('ss-stage').querySelector('.vd-more').click();
  const items = t.$('ss-stage').querySelector('.vd-menu-list').children;
  assert.deepEqual(items.map((b) => b.textContent),
    ['SpeedNormal', 'Previous chapter', 'Next chapter', 'Previous marker', 'Next marker', 'Show markers']);
  items[2].click();
  assert.equal(v.currentTime, 300, 'the next chapter');
  t.$('ss-stage').querySelector('.vd-more').click();
  t.$('ss-stage').querySelector('.vd-menu-list').children[0].click();
  assert.equal(t.$('ss-stage').querySelector('.vd-menu-title').textContent, 'Speed', 'Speed opens its own menu');
});

test('the controls fade while the video plays, and stay while it is paused', () => {
  const t = setup();
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  const v = t.ready(600);
  t.dlg.fire('pointermove', { pointerType: 'mouse', clientX: 5, clientY: 5 });
  t.pg.tick(3000);
  assert.equal(t.dlg.classList.contains('chrome-off'), false, 'paused');
  v.play();
  t.pg.tick(3000);
  assert.equal(t.dlg.classList.contains('chrome-off'), true, 'playing, untouched');
  t.key('m');
  assert.equal(t.dlg.classList.contains('chrome-off'), false, 'a key brings them back');
  v.pause();
  t.pg.tick(3000);
  assert.equal(t.dlg.classList.contains('chrome-off'), false);
});

test('Escape right after a menu closes is the slideshow\'s, not the fading menu\'s', () => {
  const t = setup();
  t.s.slideshow.open(t.s.vis.videos, 0, null, false);
  const v = t.ready(600);
  v.playbackRate = 1;
  t.$('ss-stage').querySelector('.vd-rate').click();
  const menu = t.$('ss-stage').querySelector('.vd-menu');
  menu.querySelector('.vd-menu-list').children[4].click();
  assert.equal(menu.hidden, false, 'still fading out');
  const e = t.dlg.fire('keydown', { key: 'Escape' });
  assert.equal(e.defaultPrevented, false, 'left to the dialog, which closes');
  assert.equal(t.s.slideshow.dock.holdsChrome(), true, 'paused, not the menu');
  v.play();
  assert.equal(t.s.slideshow.dock.holdsChrome(), false);
});
