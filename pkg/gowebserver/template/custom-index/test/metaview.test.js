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

// The metadata viewer (metaview.js): its detectors, its tree of the
// structures, the raw keys by source, the filter, and how the info
// panel opens it.

const test = require('node:test');
const assert = require('node:assert/strict');
const { page, load, markup } = require('../../harness.js');

const NAMES = ['mvType', 'mvNumber', 'mvLeaf', 'mvEpoch', 'mvDuration', 'mvTempColor', 'mvSparkline', 'mvLatLon', 'mvISO6709',
  'mvDetect', 'mvRawGroups', 'mvRawOf', 'mvSourceOf', 'mvMatch', 'MetaView', 'MV_DETECTORS'];
const FILES = ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/scenes.js',
  'custom-index/player.js', 'custom-index/metaview.js', 'custom-index/info.js', 'custom-index/slideshow.js'];

function mv() {
  const pg = page('<div id="host"></div>', {});
  return Object.assign(load(pg, ['custom-index/util.js', 'custom-index/data.js', 'custom-index/player.js', 'custom-index/metaview.js'], NAMES), { pg });
}

// What a detector made of a value: its names.
const names = (s, path, v, ctx) => [...s.mvDetect(path, v, ctx).map((d) => d.name)];

// A made-up recording's metadata: readings sent ten times a second, an
// XMP packet, and raw keys from each kind of source.
const DOC = {
  kind: 'video', name: 'run.mp4', size: 4000000,
  structures: [
    {
      source: 'moov/trak[2]/mett', count: 95, start: 0, end: 9.4, timed: true,
      value: {
        gps: { lat: 46.5191, lon: 6.6323, alt_m: 372.5 }, temperature_c: 18.5, counter: 0, battery_pct: 98,
        timestamp: 1741944600000, device: { id: 'sensor-01', status: { ok: true, led: '#2bd2ff', uptime_s: 3600 } }, accel: [0, 1, 0.98],
      },
      fields: {
        temperature_c: { values: 60, first: '18.5', last: '25.4', min: 18.5, max: 25.7, series: [[0, 18.5], [4, 22], [9.4, 25.4]] },
        counter: { values: 95, first: '0', last: '94', min: 0, max: 94, series: [[0, 0], [9.4, 94]] },
      },
    },
    { source: 'uuid/be7acfcb (XMP)', name: 'XMP', count: 1, start: 0, end: 0, value: { 'dc:title': 'Morning test run', 'dc:subject': ['test', 'sensor'] } },
  ],
  raw: [
    { path: 'ftyp', key: 'major brand', value: 'isom' },
    { path: 'moov/trak[2]/mett text', key: 'temperature_c', value: '18.5 … 25.4 (60 values)' },
    { path: 'SEI 5ea1c0de', key: 'frame', value: '1 … 9' },
    { path: 'uuid/be7acfcb (XMP)', key: 'dc:title', value: 'Morning test run' },
    { path: 'moov/trak[1]/mdia/hdlr', key: 'name', value: 'VideoHandler' },
    { path: 'moov/trak[2]/mett text', key: 'note', value: 'x'.repeat(400) },
  ],
};

test('values are read by what they look like, only when it is clear', () => {
  const s = mv();
  assert.deepEqual(names(s, 'gps', { lat: 46.5, lon: 6.6 }), ['location']);
  assert.deepEqual(names(s, 'gps', { lat: 0, lon: 0 }), [], 'null island is no place');
  assert.deepEqual(names(s, 'pos', { latitude: '91', longitude: '1' }), []);
  assert.deepEqual(names(s, 'meta.location', '+48.8584+002.2945/'), ['location']);
  assert.deepEqual(names(s, 'name', '+48.8584+002.2945/'), []);
  assert.deepEqual(names(s, 'temperature_c', 21.5), ['temperature']);
  assert.deepEqual(names(s, 'cpu.temp', '70 °F'), ['temperature']);
  assert.deepEqual(names(s, 'count', 21.5), []);
  assert.deepEqual(names(s, 'temp', 5000), [], 'out of range');
  assert.deepEqual(names(s, 'timestamp', 1741944600000), ['time']);
  assert.deepEqual(names(s, 'captured_us', '1741944600000000'), ['time']);
  assert.deepEqual(names(s, 'created', 1741944600), ['time']);
  assert.deepEqual(names(s, 'count', 1741944600000), [], 'a big number is not a time by itself');
  assert.deepEqual(names(s, 'when', '2025-03-14T09:30:00Z'), ['time']);
  assert.deepEqual(names(s, 'uptime_s', 3725), ['duration']);
  assert.deepEqual(names(s, 'duration_ms', 1500), ['duration']);
  assert.deepEqual(names(s, 'led', '#2bd2ff'), ['color']);
  assert.deepEqual(names(s, 'c', 'rgb(1, 2, 3)'), ['color']);
  assert.deepEqual(names(s, 'id', '#hashtag'), []);
  assert.deepEqual(names(s, 'battery_pct', 87), ['percent']);
  assert.deepEqual(names(s, 'fill_ratio', 0.25), ['percent']);
  assert.deepEqual(names(s, 'battery_pct', 187), []);
  assert.deepEqual(names(s, 'ok', true), ['boolean']);
  assert.deepEqual(names(s, 'img', 'data:image/png;base64,iVBORw0KGgo='), ['image']);
  assert.deepEqual(names(s, 'img', 'data:text/html;base64,PGI+'), []);
  // A number over time: what it is, and its sparkline.
  const field = { series: [[0, 1], [1, 2], [2, 3]] };
  assert.deepEqual(names(s, 'temperature_c', 18.5, { field }), ['temperature', 'series']);
  assert.deepEqual(names(s, 'counter', 3, { field }), ['series']);
  assert.ok(s.MV_DETECTORS.length >= 9, 'a table to add to');
});

test('the parts the detectors are made of', () => {
  const s = mv();
  assert.equal(s.mvEpoch(1741944600), 1741944600000);
  assert.equal(s.mvEpoch(1741944600123), 1741944600123);
  assert.equal(s.mvEpoch(1741944600123456), 1741944600123.456);
  assert.equal(s.mvEpoch(12345), null);
  assert.equal(s.mvDuration(3725), '1:02:05');
  assert.equal(s.mvDuration(65), '1:05');
  assert.equal(s.mvDuration(1.5), '0:01.5');
  assert.equal(s.mvDuration(-1), '');
  assert.equal(s.mvTempColor(-10), 'rgb(59,130,246)');
  assert.equal(s.mvTempColor(40), 'rgb(239,68,68)');
  assert.equal(s.mvLeaf('a.b[2]'), 'b');
  assert.equal(s.mvLeaf('Gps.Lat'), 'lat');
  assert.equal(s.mvNumber('12.5'), 12.5);
  assert.equal(s.mvNumber('12 C'), null);
  assert.equal(s.mvType([1]), 'array');
  assert.equal(s.mvType(null), 'null');
  assert.deepEqual({ ...s.mvISO6709('-33.8688+151.2093/') }, { lat: -33.8688, lon: 151.2093 });
  assert.match(s.mvSparkline([[0, 1], [1, 3], [2, 2]], 96, 22), /<polyline points="1\.0,21\.0 48\.0,1\.0 95\.0,11\.0"\/>/);
  assert.equal(s.mvSparkline([[0, 1]], 96, 22), '');
  assert.equal(s.mvSourceOf('moov/trak[3]/amf0 event'), 'timed');
  assert.equal(s.mvSourceOf('moov/trak[3]/mett'), 'timed');
  assert.equal(s.mvSourceOf('SEI 0123'), 'sei');
  assert.equal(s.mvSourceOf('uuid/be7a (XMP)'), 'xmp');
  assert.equal(s.mvSourceOf('moov/mvhd'), 'boxes');
  assert.equal(s.mvMatch('MOOV hdlr', 'moov/trak[1]/mdia/hdlr\nname'), true);
  assert.equal(s.mvMatch('moov zebra', 'moov/trak[1]/mdia/hdlr'), false);
  const groups = s.mvRawGroups(DOC.raw);
  assert.deepEqual([...groups.map((g) => g.path + ':' + g.rows.length)], ['ftyp:1', 'moov/trak[2]/mett text:2', 'SEI 5ea1c0de:1', 'uuid/be7acfcb (XMP):1', 'moov/trak[1]/mdia/hdlr:1']);
});

test('the structure: a tree of keys and types, lists folded, changes beside their fields', () => {
  const s = mv();
  const maps = [];
  const v = s.MetaView({ host: s.pg.document.getElementById('host'), map: (art, loc) => maps.push(loc) });
  v.open(DOC, 'run.mp4');
  const el = v.el;
  assert.equal(el.hidden, false);
  assert.equal(el.querySelector('.mv-name').textContent, 'run.mp4');
  assert.equal(v.tab(), 'structure');
  const tabs = el.querySelectorAll('.mv-tab');
  assert.equal(tabs[0].getAttribute('aria-selected'), 'true');
  assert.equal(tabs[0].querySelector('.mv-count').textContent, '2');
  assert.equal(tabs[1].querySelector('.mv-count').textContent, '6');

  const cards = el.querySelectorAll('.mv-struct');
  assert.equal(cards.length, 2);
  assert.match(cards[0].querySelector('header').textContent, /moov\/trak\[2\]\/mett.*95 messages · 0:00–0:09/);
  // Top-level keys, sorted; an object open, a list folded with its count.
  const top = cards[0].querySelector('.mv-tree').children;
  assert.deepEqual(top.map((li) => li.querySelector('.mv-k').textContent), ['accel', 'battery_pct', 'counter', 'device', 'gps', 'temperature_c', 'timestamp']);
  const accel = top[0].querySelector('details');
  assert.equal(accel.hasAttribute('open'), false);
  assert.match(accel.querySelector('summary').textContent, /accel\[3\]\[0,1,0\.98\]/);
  assert.equal(top[3].querySelector('details').hasAttribute('open'), true);
  // Smart representations.
  assert.ok(top[1].querySelector('.mv-bar'), 'battery: a bar');
  assert.ok(top[4].querySelector('.mv-map'), 'gps: a map');
  assert.ok(top[5].querySelector('.mv-thermo'), 'temperature: a thermometer');
  assert.ok(top[5].querySelector('.mv-spark'), 'and its sparkline');
  assert.match(top[5].querySelector('.mv-change').textContent, /18\.5 to 25\.7 · 60 values/);
  assert.ok(top[6].querySelector('.mv-when'), 'timestamp: a date');
  assert.match(top[6].querySelector('.mv-when').textContent, /2025/);
  const status = top[3].querySelectorAll('.mv-leaf');
  assert.ok(status.some((li) => li.querySelector('.mv-swatch')), 'a color: a swatch');
  assert.ok(status.some((li) => li.querySelector('.mv-chip.yes')), 'true: a chip');
  assert.ok(status.some((li) => /1:00:00/.test(li.textContent)), 'uptime: h:mm:ss');
  assert.equal(top[2].querySelector('.mv-t').textContent, 'number');
  // The map is asked for once it shows.
  assert.equal(maps.length, 1);
  assert.equal(maps[0].lat, 46.5191);

  // The filter hides the structures that don't match.
  const find = el.querySelector('.mv-find');
  find.value = 'morning';
  find.fire('input');
  assert.deepEqual(cards.map((c) => c.hidden), [true, false]);
  find.value = 'nothing like this';
  find.fire('input');
  assert.equal(el.querySelector('.mv-none').hidden, false);
});

test('the raw keys: by kind of source, then where; copyable; long values folded', () => {
  const s = mv();
  const v = s.MetaView({ host: s.pg.document.getElementById('host') });
  v.open(DOC, 'run.mp4');
  v.setTab('raw');
  const el = v.el;
  assert.equal(el.querySelector('[data-pane="raw"]').hidden, false);
  assert.equal(el.querySelector('[data-pane="structure"]').hidden, true);
  const sources = el.querySelectorAll('.mv-source');
  assert.deepEqual(sources.map((x) => x.getAttribute('data-source')), ['boxes', 'timed', 'sei', 'xmp']);
  assert.deepEqual(sources[0].querySelectorAll('.mv-path').map((p) => p.textContent), ['ftyp', 'moov/trak[1]/mdia/hdlr']);
  assert.match(sources[1].querySelector('h3').textContent, /Timed metadata\s*2/);
  const rows = el.querySelectorAll('.mv-row');
  assert.equal(rows.length, 6);
  assert.ok(rows.every((r) => r.querySelector('.mv-copy')), 'each value copyable');
  const long = el.querySelectorAll('.mv-v.mv-long');
  assert.equal(long.length, 1);
  const fold = long[0].parentNode.querySelector('.mv-fold');
  fold.click();
  assert.equal(long[0].classList.contains('full'), true);
  assert.equal(fold.textContent, 'Show less');
  // Filtering by path, key or value.
  const find = el.querySelector('.mv-find');
  find.value = 'hdlr';
  find.fire('input');
  assert.deepEqual(el.querySelectorAll('.mv-box').map((b) => b.hidden), [true, false, true, true, true]);
  assert.deepEqual(sources.map((x) => x.hidden), [false, true, true, true]);
  find.value = '';
  find.fire('input');
  assert.ok(el.querySelectorAll('.mv-box').every((b) => !b.hidden));
  // Escape closes it; its keys don't reach what's under it.
  let reached = 0;
  s.pg.document.getElementById('host').addEventListener('keydown', () => reached++);
  const e = el.fire('keydown', { key: 'Escape' });
  assert.equal(e.defaultPrevented, true);
  assert.equal(reached, 0);
  assert.equal(v.isOpen(), false);
});

test('a file\'s tags are raw keys too, under their group, apart from the boxes', () => {
  const s = mv();
  const doc = { raw: [{ path: 'XMP', key: 'dc:title', value: 'T' }], tags: [{ group: 'EXIF', name: 'Make', value: 'Example' }, { group: 'XMP', name: 'dc:title', value: 'T' }] };
  assert.equal(s.mvRawOf(doc).length, 3);
  assert.equal(s.mvRawGroups(s.mvRawOf(doc)).length, 3, 'a tag group named as a box is its own');
  const v = s.MetaView({ host: s.pg.document.getElementById('host') });
  v.open(doc, 'p.jpg');
  assert.equal(v.tab(), 'raw', 'nothing structured: it opens on the raw keys');
  const sources = v.el.querySelectorAll('.mv-source');
  assert.deepEqual(sources.map((x) => x.getAttribute('data-source')), ['xmp', 'tags']);
  assert.deepEqual(sources[1].querySelectorAll('.mv-path').map((p) => p.textContent), ['EXIF', 'XMP']);
  assert.equal(v.el.querySelectorAll('.mv-tab')[1].querySelector('.mv-count').textContent, '3');
});

test('values are text, never markup', () => {
  const s = mv();
  const v = s.MetaView({ host: s.pg.document.getElementById('host') });
  v.open({
    structures: [{ source: '<b>s</b>', count: 1, value: { '<i>k</i>': '<img src=x onerror=alert(1)>', c: 'red;background:url(x)' } }],
    raw: [{ path: '<b>p</b>', key: '<i>k</i>', value: '<script>x</script>' }],
  }, '<name>');
  const el = v.el;
  assert.equal(el.querySelector('.mv-src').textContent, '<b>s</b>');
  assert.equal(el.querySelectorAll('img').length, 0);
  assert.equal(el.querySelectorAll('.mv-swatch').length, 0, 'only a color that is surely one is drawn');
  v.setTab('raw');
  assert.equal(el.querySelector('.mv-path').textContent, '<b>p</b>');
  assert.equal(el.querySelector('.mv-row .mv-v').textContent, '<script>x</script>');
  assert.equal(el.querySelectorAll('script').length, 0);
});

test('the info panel opens it from a line at its end, and Escape closes it before the panel', async () => {
  const media = [[0, 'v.mp4', 'v.mp4', 2000, 1704110400]];
  const docs = { 'v.mp4?info': DOC };
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + markup('custom-index/custom-index.html', '<dialog class="ss"', '</dialog>'), {
    innerWidth: 1600,
    globals: {
      GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media },
      grid: null, Ambient: () => ({ show() { }, reset() { }, clear() { } }), accent: { set() { }, clear() { } }, vividColor: () => null,
      fetch: (url) => {
        if (url === '?worldmap') return Promise.resolve({ ok: true, json: () => Promise.resolve({ q: 1, c: [] }) });
        const d = docs[url];
        return Promise.resolve(d ? { ok: true, json: () => Promise.resolve(d) } : { ok: false, status: 404 });
      },
    },
  });
  const $ = (id) => pg.document.getElementById(id);
  $('ss-stage').clientWidth = 1600;
  $('ss-stage').clientHeight = 900;
  $('ss-strip').offsetHeight = 63;
  $('ss-strip').parentNode.computed = { paddingBottom: '14px' };
  const s = load(pg, FILES, ['slideshow', 'all']);
  s.slideshow.open(s.all, 0, null, false);
  $('ss').fire('keydown', { key: 'i' });
  pg.tick(200);
  for (let i = 0; i < 6; i++) await pg.settle();
  const body = $('ss-panel-body');
  const all = body.querySelector('.ssi-all');
  assert.ok(all, 'a line for all of it');
  assert.match(all.textContent, /All metadata.*2 structured · 6 raw keys/);
  all.click();
  const view = s.slideshow.info.viewer();
  assert.equal(view.isOpen(), true);
  assert.equal(view.el.parentNode, $('ss-frame'), 'over the slideshow, in its frame (so in full screen too)');
  // An Escape from outside it closes it, and leaves the panel open.
  const e = $('ss').fire('keydown', { key: 'Escape' });
  assert.equal(e.defaultPrevented, true);
  assert.equal(view.isOpen(), false);
  assert.equal($('ss').classList.contains('info-on'), true);
});
