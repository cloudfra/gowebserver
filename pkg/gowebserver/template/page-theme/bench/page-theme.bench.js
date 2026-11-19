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

const { page, load, bench } = require('../../harness.js');

// The shared header on a scroll: the title's move into the toolbar,
// once a frame. See bench.js for what this does and doesn't time.
(function () {
  const pg = page('<header id="hero"><nav id="crumbs"></nav><h1 id="title" data-from-path>t</h1></header>' +
    '<div id="toolbar"><span id="tb-title">t</span><button id="theme"></button></div>',
    { location: { pathname: '/a/b/c/d/e/f/g/' } });
  const $ = (id) => pg.document.getElementById(id);
  $('title').offsetTop = 120;
  $('title').computed = { fontSize: '40px', lineHeight: '44px' };
  $('tb-title').computed = { fontSize: '20px' };
  $('toolbar').offsetTop = 300;
  $('hero').offsetHeight = 300;
  load(pg, ['page-theme/hover-panel.js', 'page-theme/page-theme.js', 'page-theme/crumb-panel.js'], []);

  let y = 0;
  bench('TitleTravelFrame', () => {
    y = (y + 7) % 400;
    pg.scroll(y);
  });
  // Past the header, which is nearly all of a long page.
  bench('TitleDockedFrame', () => {
    y = 1000 + (y + 7) % 400;
    pg.scroll(y);
  });
})();
