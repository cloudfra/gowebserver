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

const FILES = ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js', 'custom-index/scenes.js', 'custom-index/player.js', 'custom-index/metaview.js', 'custom-index/info.js', 'custom-index/slideshow.js'];

// What ?info says of a photo taken by the Eiffel Tower.
const PHOTO = {
  kind: 'image', name: 'p1.jpg', size: 3200000, modified: '2024-06-01T00:00:00Z', format: 'JPEG',
  width: 4032, height: 3024, taken: '2024-05-01T18:45:10+02:00',
  location: { lat: 48.8584, lon: 2.2945, alt: 35.1, direction: 135.3, directionRef: 'T', accuracy: 5, country: 'France' },
  place: 'Eiffel Tower, Paris',
  text: [{ label: 'Lyrics', lang: 'eng', text: 'Line one\nLine <two>' }],
  groups: [
    { title: 'Camera', fields: [{ label: 'Camera', value: 'Apple iPhone 15 Pro' }] },
    { title: 'Exposure', fields: [{ label: 'Aperture', value: 'ƒ/1.8' }, { label: 'Shutter speed', value: '1/250 s' }] },
    { title: 'Image', fields: [{ label: 'ICC profile', value: 'Display P3' }, { label: 'Bit depth', value: '8 bits per channel' }] },
  ],
  tags: [{ group: 'EXIF', name: 'Make', value: 'Apple' }, { group: 'GPS', name: 'GPSLatitudeRef', value: 'N' }],
};

// A world of one square country around Paris.
const WORLD = { q: 1, c: [['France', [0, 44, 8, 0, 0, 8, -8, 0]], ['Elsewhere', [100, 0, 5, 0, 0, 5]]] };

// The slideshow over n photos, with fetch answering ?info from docs (by
// URL) and ?worldmap with WORLD. Every request is in fetched.
function setup(n, opts) {
  opts = opts || {};
  const media = [];
  for (let i = 0; i < n; i++) media.push([0, 'p' + i + '.jpg', 'p' + i + '.jpg', 2000, 1704110400]);
  const fetched = [];
  const docs = opts.docs || {};
  const ambient = { show() { }, reset() { }, clear() { } };
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + markup('custom-index/custom-index.html', '<dialog class="ss"', '</dialog>'), {
    innerWidth: opts.width || 1600,
    globals: {
      GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media },
      grid: null,
      Ambient: () => ambient,
      accent: { set() { }, clear() { } },
      vividColor: () => null,
      fetch: (url) => {
        fetched.push(url);
        if (url === '?worldmap') return Promise.resolve({ ok: true, json: () => Promise.resolve(WORLD) });
        const d = docs[url];
        if (!d) return Promise.resolve({ ok: false, status: 404 });
        return Promise.resolve({ ok: true, json: () => Promise.resolve(d) });
      },
    },
  });
  const $ = (id) => pg.document.getElementById(id);
  const stage = opts.stage || [1600, 900];
  $('ss-stage').clientWidth = stage[0];
  $('ss-stage').clientHeight = stage[1];
  $('ss-strip').offsetHeight = 63;
  $('ss-strip').parentNode.computed = { paddingBottom: '14px' };
  const s = load(pg, FILES, ['slideshow', 'all']);
  return {
    pg, $, all: s.all, slideshow: s.slideshow, fetched,
    dlg: $('ss'), panel: $('ss-panel'), body: $('ss-panel-body'), btn: $('ss-info'),
    key: (k) => $('ss').fire('keydown', { key: k }),
    // Lets the fetches and their callbacks run.
    async flush() {
      pg.tick(200);
      for (let i = 0; i < 6; i++) await pg.settle();
    },
    infos: () => fetched.filter((u) => /\?info$/.test(u)),
  };
}

test('the panel asks for nothing until it is opened, then for the slide on screen', async () => {
  const t = setup(5, { docs: { 'p1.jpg?info': PHOTO } });
  t.slideshow.open(t.all, 1, null, false);
  await t.flush();
  assert.deepEqual(t.fetched, [], 'closed, it fetches nothing');
  assert.equal(t.btn.getAttribute('aria-expanded'), 'false');

  t.btn.click();
  assert.equal(t.dlg.classList.contains('info-on'), true);
  assert.equal(t.btn.getAttribute('aria-expanded'), 'true');
  assert.equal(t.panel.getAttribute('aria-hidden'), 'false');
  // What the listing knows shows at once, the rest when it comes.
  assert.match(t.body.textContent, /p1\.jpg/);
  assert.ok(t.body.querySelector('.ssi-skel'), 'a placeholder while it loads');
  await t.flush();
  assert.deepEqual(t.infos(), ['p1.jpg?info']);
  assert.equal(t.body.querySelector('.ssi-skel'), null);
});

test('it shows the place first, then the date, name and size, and the details after, unfolded', async () => {
  const t = setup(3, { docs: { 'p1.jpg?info': PHOTO } });
  t.slideshow.open(t.all, 1, null, false);
  t.key('i');
  await t.flush();

  const top = t.body.children.map((el) => el.className);
  assert.deepEqual(top, ['ssi-loc', 'ssi-facts', 'ssi-text', 'ssi-group', 'ssi-group', 'ssi-group', 'ssi-all']);
  const labels = t.body.querySelectorAll('.ssi-facts')[0].querySelectorAll('dt').map((d) => d.textContent);
  assert.deepEqual(labels, ['Taken', 'Name', 'Size']);
  assert.equal(t.body.querySelector('.ssi-where').textContent, 'Eiffel Tower, Paris, France');
  assert.match(t.body.querySelector('.ssi-coords').textContent, /48\.85840° N, 2\.29450° E · 35 m above sea level · facing south-east \(135°\) · ± 5 m/);
  // The wall clock where it was taken, and its offset.
  const taken = t.body.querySelector('.ssi-facts dd');
  assert.match(taken.textContent, /2024/);
  assert.match(taken.textContent, /6:45|18:45/);
  assert.match(taken.textContent, /UTC\+2$/);
  assert.match(t.body.querySelectorAll('.ssi-facts')[0].textContent, /3\.2 MB4,?032 × 3,?024 · 12 MP/);
  // The map's only link out is one the viewer follows.
  const osm = t.body.querySelector('.ssi-osm');
  assert.equal(osm.getAttribute('href'), 'https://www.openstreetmap.org/?mlat=48.858400&mlon=2.294500#map=16/48.858400/2.294500');
  assert.equal(osm.getAttribute('rel'), 'noopener noreferrer');
  // Lyrics, as text.
  assert.equal(t.body.querySelector('.ssi-prose').textContent, 'Line one\nLine <two>');
  assert.equal(t.body.querySelector('.ssi-lang').textContent, 'eng');
  // The rest follows, in the server's order, nothing folded; every tag
  // is in the viewer.
  assert.equal(t.body.querySelectorAll('details').length, 0);
  assert.deepEqual(t.body.querySelectorAll('.ssi-group h3').map((h) => h.textContent), ['Camera', 'Exposure', 'Image']);
  assert.match(t.body.querySelector('.ssi-group').textContent, /Apple iPhone 15 Pro/);
  assert.match(t.body.querySelector('.ssi-all').textContent, /All metadata.*2 raw keys/);
});

test('the map is drawn from the server\'s own world map, fetched once', async () => {
  const docs = { 'p0.jpg?info': PHOTO, 'p1.jpg?info': Object.assign({}, PHOTO, { name: 'p1.jpg' }) };
  const t = setup(3, { docs });
  t.slideshow.open(t.all, 0, null, false);
  t.key('i');
  await t.flush();
  const fig = t.body.querySelector('.ssi-map');
  assert.equal(fig.classList.contains('drawn'), true);
  const svg = fig.querySelector('svg');
  assert.ok(svg, 'an SVG map');
  assert.ok(fig.querySelector('.ssi-here').getAttribute('d').length > 4, 'the country it\'s in, marked');
  assert.ok(fig.querySelector('.ssi-fov'), 'the way the camera faced');
  t.key('ArrowRight');
  await t.flush();
  assert.equal(t.fetched.filter((u) => u === '?worldmap').length, 1);
  assert.ok(t.fetched.every((u) => !/^https?:/.test(u)), 'nothing from another server: ' + t.fetched);
  // Zooming redraws it, up to its limits.
  const zin = t.body.querySelector('[data-zoom="1"]');
  zin.click();
  await t.flush();
  assert.equal(t.body.querySelector('[data-zoom="1"]').disabled, true, 'all the way in');
});

test('stepping through slides asks only for the one it stops on, and keeps what it has', async () => {
  const docs = {};
  for (let i = 0; i < 10; i++) docs['p' + i + '.jpg?info'] = Object.assign({}, PHOTO, { name: 'p' + i + '.jpg', location: null, place: '' });
  const t = setup(10, { docs });
  t.slideshow.open(t.all, 0, null, false);
  t.key('i');
  await t.flush();
  for (let i = 0; i < 5; i++) t.key('ArrowRight');
  await t.flush();
  assert.deepEqual(t.infos(), ['p0.jpg?info', 'p5.jpg?info']);
  t.key('Home');
  await t.flush();
  assert.deepEqual(t.infos(), ['p0.jpg?info', 'p5.jpg?info'], 'p0 was kept');
  assert.match(t.body.textContent, /p0\.jpg/);
});

test('a file the server can\'t describe still shows what the listing says', async () => {
  const t = setup(2);
  t.slideshow.open(t.all, 0, null, false);
  t.key('i');
  await t.flush();
  assert.match(t.body.textContent, /Modified/);
  assert.match(t.body.textContent, /p0\.jpg/);
  assert.ok(t.body.querySelector('.ssi-note'));
});

test('names are text, never markup', async () => {
  const evil = Object.assign({}, PHOTO, { place: '<img src=x onerror=alert(1)>', text: [{ label: '<b>x</b>', text: '<script>' }] });
  const t = setup(2, { docs: { 'p0.jpg?info': evil } });
  t.slideshow.open(t.all, 0, null, false);
  t.key('i');
  await t.flush();
  assert.equal(t.body.querySelectorAll('img').length, 0);
  assert.equal(t.body.querySelectorAll('script').length, 0);
  assert.equal(t.body.querySelectorAll('b').length, 0);
});

test('Escape closes the panel before the slideshow', async () => {
  const t = setup(3, { docs: { 'p0.jpg?info': PHOTO } });
  t.slideshow.open(t.all, 0, null, false);
  t.key('i');
  assert.equal(t.dlg.classList.contains('info-on'), true);
  t.key('Escape');
  assert.equal(t.dlg.classList.contains('info-on'), false);
  // The dialog's cancel that comes with that Escape doesn't close it.
  t.dlg.fire('cancel');
  assert.equal(t.dlg.open, true);
  t.pg.tick(1000);
  t.dlg.fire('cancel');
  t.pg.tick(1000);
  assert.equal(t.dlg.open, false, 'the next Escape closes the slideshow');
});

test('a cancel while the panel is open closes the panel only', () => {
  const t = setup(3);
  t.slideshow.open(t.all, 0, null, false);
  t.btn.click();
  t.dlg.fire('cancel');
  assert.equal(t.dlg.classList.contains('info-on'), false);
  assert.equal(t.dlg.open, true);
});

test('docked, the slide makes room for the panel; as a sheet, it doesn\'t', () => {
  let t = setup(3, { stage: [1600, 900] });
  t.slideshow.open(t.all, 0, null, false);
  const slide = () => t.$('ss-stage').children.find((d) => d._k === 0);
  const right = () => parseFloat(slide().style.padding.split(' ')[1]);
  const before = right();
  t.key('i');
  assert.equal(t.dlg.classList.contains('info-docked'), true);
  assert.equal(right(), before + 388);
  assert.equal(t.dlg.style['--ssi-room'], '388px');
  t.key('i');
  assert.equal(right(), before);

  // A phone: a sheet over the photo.
  t = setup(3, { stage: [390, 800], width: 390 });
  t.slideshow.open(t.all, 0, null, false);
  t.key('i');
  assert.equal(t.dlg.classList.contains('info-sheet'), true);
  assert.equal(t.dlg.classList.contains('info-docked'), false);
  assert.equal(right(), 0);
  // The photo moves up above it.
  assert.equal(parseFloat(slide().style.padding.split(' ')[2]), 448);
  assert.equal(t.dlg.style['--ssi-sheet'], '448px');
});

test('closing the slideshow closes the panel, and keys inside it are its own', () => {
  const t = setup(3);
  t.slideshow.open(t.all, 1, null, false);
  t.key('i');
  // End in the lyrics scrolls them; it doesn't go to the last slide.
  const inner = t.$('ss-panel-close');
  inner.fire('keydown', { key: 'End' });
  assert.equal(t.$('ss-name').textContent, 'p1.jpg');
  t.$('ss-close').click();
  t.pg.tick(1000);
  assert.equal(t.dlg.classList.contains('info-on'), false);
});

test('dates read as the camera wrote them', () => {
  const t = setup(1);
  const fmt = t.slideshow.info._taken;
  assert.deepEqual({ ...fmt('2019') }, { text: '2019' });
  assert.match(fmt('2024-05-01').text, /2024/);
  assert.equal(fmt('2024-05-01').zone, undefined);
  // Not moved into the viewer's time zone: 23:59 where it was taken.
  const late = fmt('2024-05-01T23:59:00-07:00');
  assert.match(late.text, /11:59|23:59/);
  assert.equal(late.zone, 'UTC-7');
  assert.equal(fmt('2024-05-01T06:30:00+05:30').zone, 'UTC+5:30');
  assert.equal(fmt('2024-05-01T06:30:00').zone, '', 'no offset: the camera\'s clock');
  assert.equal(fmt('2024-05-01T06:30:00Z').zone, undefined, 'UTC: shown in the viewer\'s time');
});
