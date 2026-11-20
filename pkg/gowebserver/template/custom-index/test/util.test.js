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

const NAMES = ['fmtBytes', 'plural', 'svgUse', 'store', 'tileThumbURL', 'previewURL', 'hasThumb'];

function util(gows, opts) {
  return load(page('', Object.assign({ globals: { GOWS: gows } }, opts)), ['custom-index/util.js'], NAMES);
}

const photo = { video: false, url: 'a.jpg' };
const video = { video: true, url: 'b.mp4' };

test('fmtBytes matches go-humanize, which renders the list', () => {
  const u = util();
  // From humanize.Bytes().
  const cases = [
    [0, '0 B'], [9, '9 B'], [10, '10 B'], [999, '999 B'], [1000, '1.0 kB'], [1500, '1.5 kB'],
    [82854982, '83 MB'], [1000000000, '1.0 GB'], [5.5e12, '5.5 TB'],
  ];
  for (const [n, want] of cases) assert.equal(u.fmtBytes(n), want, String(n));
});

test('fmtBytes rounds as go-humanize does, once, at the boundaries', () => {
  const u = util();
  // humanize.Bytes() v1.1.0, printed by Go for each.
  const cases = [
    [1049, '1.0 kB'], [1050, '1.1 kB'], [9949, '9.9 kB'], [9950, '10 kB'], [9999, '10 kB'], [10000, '10 kB'],
    [10499, '10 kB'], [10500, '11 kB'], [99499, '99 kB'], [99500, '100 kB'], [99999, '100 kB'],
    [999499, '999 kB'], [999500, '1000 kB'], [999999, '1000 kB'], [1000000, '1.0 MB'], [1234567, '1.2 MB'],
    [9950000, '10 MB'], [3400000000, '3.4 GB'], [1e15, '1.0 PB'], [1e18 - 1, '1.0 EB'], [18446744073709551615, '18 EB'],
  ];
  for (const [n, want] of cases) assert.equal(u.fmtBytes(n), want, String(n));
});

test('plural picks the word and groups the digits', () => {
  const u = util();
  assert.equal(u.plural(1, 'photo', 'photos'), '1 photo');
  assert.equal(u.plural(0, 'photo', 'photos'), '0 photos');
  assert.equal(u.plural(12345, 'photo', 'photos'), new Intl.NumberFormat().format(12345) + ' photos');
});

test('svgUse', () => {
  const u = util();
  assert.equal(u.svgUse('ui-play'), '<svg viewBox="0 0 24 24"><use href="#ui-play"/></svg>');
  assert.equal(u.svgUse('ui-play', 'big'), '<svg class="big" viewBox="0 0 24 24"><use href="#ui-play"/></svg>');
});

test('store reads, writes and removes, and survives storage that throws', () => {
  const pg = page('', { stored: { k: 'v' } });
  const u = load(pg, ['custom-index/util.js'], NAMES);
  assert.equal(u.store('k'), 'v');
  u.store('k', 'w');
  assert.equal(pg.stored.k, 'w');
  u.store('k', null);
  assert.equal('k' in pg.stored, false);
  pg.localStorage.getItem = () => { throw new Error('blocked'); };
  assert.equal(u.store('k'), null);
});

test('without server thumbnails a tile shows the file itself', () => {
  const u = util({ sort: 'name', thumbs: false, videoThumbs: false, media: [] });
  assert.equal(u.tileThumbURL(photo, 200), 'a.jpg');
  assert.equal(u.previewURL(photo), 'a.jpg');
});

test('video thumbnails need the server to grab frames', () => {
  const u = util({ sort: 'name', thumbs: true, videoThumbs: false, media: [] });
  assert.equal(u.hasThumb(photo), true);
  assert.equal(u.hasThumb(video), false);
  assert.equal(u.tileThumbURL(video, 400), 'b.mp4');
});

test('a tile asks for the smallest size that covers it', () => {
  const cases = [[100, 128], [147, 128], [148, 256], [208, 256], [300, 512], [900, 1024], [5000, 1024]];
  for (const [cssW, size] of cases) {
    const u = util({ sort: 'name', thumbs: true, videoThumbs: true, media: [] });
    assert.equal(u.tileThumbURL(photo, cssW), 'a.jpg?width=' + size + '&height=' + size + '&fit=outside', String(cssW));
  }
});

test('a dense screen asks for more pixels, up to 2x', () => {
  for (const [dpr, size] of [[1, 128], [2, 256], [3, 256]]) {
    const u = util({ sort: 'name', thumbs: true, videoThumbs: true, media: [] }, { devicePixelRatio: dpr });
    assert.equal(u.tileThumbURL(photo, 140), 'a.jpg?width=' + size + '&height=' + size + '&fit=outside', String(dpr));
  }
});

test('the size only grows, and is kept per kind of tile', () => {
  const u = util({ sort: 'name', thumbs: true, videoThumbs: true, media: [] });
  assert.match(u.tileThumbURL(photo, 300), /width=512/);
  // A narrower window doesn't fetch a new set.
  assert.match(u.tileThumbURL(photo, 100), /width=512/);
  // The filmstrip passes 0 to reuse whatever the grid has.
  assert.match(u.tileThumbURL(photo, 0), /width=512/);
  // Videos are 16:9 and sized on their own.
  assert.equal(u.tileThumbURL(video, 100), 'b.mp4?width=128&height=72&fit=outside');
});

test('the slideshow preview is one size at the file\'s own shape', () => {
  const u = util({ sort: 'name', thumbs: true, videoThumbs: true, media: [] });
  assert.equal(u.previewURL(photo), 'a.jpg?width=1024&height=1024&fit=inside');
});
