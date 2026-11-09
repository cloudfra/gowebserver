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

// The file browser on a folder of 50,000 files and 50,000 photos, the
// size its design is for: see bench.js for what these do and don't time.

'use strict';

const { page, load, bench, markup } = require('../../harness.js');

const N = 50000;
const FILES = ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js', 'custom-index/data.js'];

function media(n) {
  const out = [];
  for (let i = 0; i < n; i++) out.push([i % 50 === 49 ? 1 : 0, 'm' + i, 'IMG_' + i + '.jpg', 3000000 + i, 1704110400 + i * 600]);
  return out;
}

const stubs = () => ({
  pageAmbient: { show() { } },
  Ambient: () => ({ show() { }, reset() { }, clear() { } }),
  accent: { set() { }, clear() { } },
  vividColor: () => null,
  scrubber: { measure() { }, place() { }, refresh() { } },
  slideshow: { open() { } },
});

// Filtering: the list's rows and the media array, with no grid.
(function () {
  let rows = '';
  for (let i = 0; i < N; i++) rows += '<div class="row"><span class="name">file-' + i + '.txt</span></div>';
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + rows, {
    globals: Object.assign(stubs(), { GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media: media(N) }, grid: null }),
  });
  const { applyFilter } = load(pg, FILES, ['applyFilter']);
  applyFilter('');
  let i = 0;
  // Alternates so each call is a new filter: one that hides most rows,
  // then one that brings them back.
  bench('Filter50kRows50kMedia', () => applyFilter(i++ % 2 ? '' : '49'));
})();

// The grid: laying out the whole folder, and following a scroll.
(function () {
  const pg = page('<p id="summary"></p><p id="no-match"></p><div id="media"></div>', {
    globals: Object.assign(stubs(), { GOWS: { sort: 'date', thumbs: true, videoThumbs: true, media: media(N) } }),
  });
  const el = pg.document.getElementById('media');
  el.clientWidth = 1280;
  el.getBoundingClientRect = () => ({ top: 100 - pg.scrollY });
  const g = load(pg, FILES.concat('custom-index/grid.js'), ['grid', 'applyFilter']);
  g.applyFilter('');
  bench('GridLayout50k', () => g.grid.relayout(false));

  // A slow scroll, a row or two a frame, which mounts and loads tiles.
  let y = 0;
  bench('GridScrollFrame50k', () => {
    y = (y + 300) % 1500000;
    pg.tick(100);
    pg.scroll(y);
  });
  // A fling, which lays tiles out without loading them.
  bench('GridFlingFrame50k', () => {
    y = (y + 20000) % 1500000;
    pg.tick(16);
    pg.scroll(y);
  });
})();

// The thumbnail queue when a screen of tiles is queued and then
// scrolled away before any of it loads.
(function () {
  const pg = page('');
  const { thumbLoader } = load(pg, ['custom-index/util.js', 'custom-index/diag.js', 'custom-index/thumbs.js'], ['thumbLoader']);
  const imgs = [];
  for (let i = 0; i < 200; i++) imgs.push(pg.document.createElement('img'));
  const done = () => { };
  bench('ThumbQueueAndCancel200', () => {
    for (let i = 0; i < imgs.length; i++) thumbLoader.load(imgs[i], 't' + i, done);
    for (let i = 0; i < imgs.length; i++) thumbLoader.cancel(imgs[i]);
  });
})();

// The slideshow: one step through the folder.
(function () {
  const pg = page('<p id="summary"></p><p id="no-match"></p>' + markup('custom-index/custom-index.html', '<dialog class="ss"', '</dialog>'), {
    globals: Object.assign(stubs(), { GOWS: { sort: 'name', thumbs: true, videoThumbs: true, media: media(N) }, grid: null }),
  });
  pg.document.getElementById('ss-stage').clientWidth = 1280;
  pg.document.getElementById('ss-stage').clientHeight = 800;
  const s = load(pg, FILES.concat('custom-index/slideshow.js'), ['slideshow', 'all']);
  s.slideshow.open(s.all.filter((it) => !it.video), 0, null, false);
  const dlg = pg.document.getElementById('ss');
  let i = 0;
  bench('SlideshowStep50k', () => {
    dlg.fire('keydown', { key: ++i % 40000 ? 'ArrowRight' : 'Home' });
  });
})();
