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

// What page-theme.html says a page must have.
const HTML = '<header id="hero"><nav id="crumbs"></nav><h1 id="title" data-from-path>root</h1></header>' +
  '<div id="toolbar"><span id="tb-title">root</span><button id="theme"></button></div>';

function setup(opts) {
  opts = opts || {};
  const pg = page(opts.html || HTML, opts);
  const $ = (id) => pg.document.getElementById(id);
  if (opts.theme) pg.document.documentElement.dataset.theme = opts.theme;
  // The title sits 120px down a 300px header; the toolbar title is
  // 40px in, in a font half the size.
  $('title').offsetTop = 120;
  $('title').computed = { fontSize: '40px', lineHeight: '44px' };
  $('tb-title').offsetLeft = 40;
  $('tb-title').offsetTop = 310;
  $('tb-title').offsetHeight = 20;
  $('tb-title').computed = { fontSize: '20px' };
  $('toolbar').offsetTop = 300;
  $('hero').offsetHeight = 300;
  load(pg, ['page-theme/hover-panel.js', 'page-theme/page-theme.js', 'page-theme/crumb-panel.js'], []);
  return {
    pg, $, root: pg.document.documentElement,
    crumbs: () => $('crumbs').querySelectorAll('a').map((a) => [a.textContent, a.href]),
  };
}

test('the theme button cycles automatic, light, dark and remembers the choice', () => {
  const t = setup();
  const btn = t.$('theme');
  assert.equal(btn.getAttribute('aria-label'), 'Theme: automatic');
  assert.equal(t.root.dataset.theme, undefined);

  btn.click();
  assert.equal(t.root.dataset.theme, 'light');
  assert.equal(t.pg.stored['gows-theme'], 'light');
  assert.equal(btn.title, 'Theme: light');

  btn.click();
  assert.equal(t.root.dataset.theme, 'dark');
  assert.equal(t.pg.stored['gows-theme'], 'dark');

  btn.click();
  assert.equal(t.root.dataset.theme, undefined, 'automatic leaves it to the system');
  assert.equal('gows-theme' in t.pg.stored, false);
  assert.match(btn.innerHTML, /^<svg/);
});

test('the button starts from the theme the page already has', () => {
  const t = setup({ theme: 'dark' });
  assert.equal(t.$('theme').getAttribute('aria-label'), 'Theme: dark');
  t.$('theme').click();
  assert.equal(t.root.dataset.theme, undefined);
});

test('breadcrumbs link each folder above this one', () => {
  const t = setup({ location: { pathname: '/e/photos/2024/', host: 'nas:8080' }, media: { '(min-width: 1100px)': true } });
  assert.deepEqual(t.crumbs(), [['nas:8080', '/'], ['e', '/e/'], ['photos', '/e/photos/']]);
});

test('a deep path collapses its start into one link, by screen width', () => {
  const pathname = '/a/b/c/d/e/f/here/';
  const wide = setup({ location: { pathname }, media: { '(min-width: 1100px)': true, '(min-width: 700px)': true } });
  assert.deepEqual(wide.crumbs().map((c) => c[0]), ['example.test', '…', 'c', 'd', 'e', 'f']);
  const more = wide.$('crumbs').querySelector('.more');
  assert.equal(more.href, '/a/b/', 'the ellipsis goes to the nearest hidden folder');
  assert.equal(more.getAttribute('title'), 'a / b');
  assert.equal(more.getAttribute('aria-label'), 'Up to b');

  const medium = setup({ location: { pathname }, media: { '(min-width: 700px)': true } });
  assert.deepEqual(medium.crumbs().map((c) => c[0]), ['example.test', '…', 'd', 'e', 'f']);
  const phone = setup({ location: { pathname } });
  assert.deepEqual(phone.crumbs().map((c) => c[0]), ['example.test', '…', 'e', 'f']);
});

test('names are decoded for reading and escaped for the page', () => {
  const t = setup({ location: { pathname: '/a%20%3Cb%3E/R%26D.zip.d/100%/x/' }, media: { '(min-width: 1100px)': true } });
  assert.deepEqual(t.crumbs().slice(1), [['a <b>', '/a%20%3Cb%3E/'], ['R&D.zip', '/a%20%3Cb%3E/R%26D.zip.d/'], ['100%', '/a%20%3Cb%3E/R%26D.zip.d/100%/']]);
  assert.match(t.$('crumbs').innerHTML, /a &lt;b&gt;/);
  assert.doesNotMatch(t.$('crumbs').innerHTML, /<b>/);
});

test('a folder is titled from the URL, which has the serve path the server never sees', () => {
  const t = setup({ location: { pathname: '/e/My%20Photos/' } });
  assert.equal(t.$('title').textContent, 'My Photos');
  assert.equal(t.$('tb-title').textContent, 'My Photos');
  assert.equal(t.pg.document.title, 'My Photos');
  // An opened archive loses its .d.
  assert.equal(setup({ location: { pathname: '/e/pack.zip.d/' } }).$('title').textContent, 'pack.zip');
  // The top folder is named for the host.
  assert.equal(setup({ location: { pathname: '/', host: 'nas:8080' } }).$('title').textContent, 'nas:8080');
});

test('a page that names itself keeps its title', () => {
  const t = setup({ html: HTML.replace(' data-from-path', ''), location: { pathname: '/e/file.go' } });
  assert.equal(t.$('title').textContent, 'root');
});

test('the title travels into the toolbar as the page scrolls', () => {
  const t = setup();
  const title = t.$('title'), hero = t.$('hero'), toolbar = t.$('toolbar');
  // It starts when the title nears the top (112) and ends when the
  // toolbar sticks (300).
  assert.equal(hero.style['--title-p'], '0');
  assert.equal(title.style.transform, '');

  t.pg.scroll(112 + 94);
  assert.equal(hero.style['--title-p'], '0.5');
  assert.equal(toolbar.style['--title-p'], '0.5');
  // Halfway: half the way across, at three quarters of its size.
  assert.match(title.style.transform, /^translate\(20\.00px,[-\d.]+px\) scale\(0\.7500\)$/);
  assert.equal(hero.classList.contains('title-docked'), false);

  t.pg.scroll(300);
  assert.equal(hero.classList.contains('title-docked'), true);
  assert.equal(toolbar.classList.contains('title-docked'), true);
  assert.equal(title.style.transform, '', 'docked, the toolbar\'s own title takes over');

  t.pg.scroll(0);
  assert.equal(hero.classList.contains('title-docked'), false);
  assert.equal(hero.style['--title-p'], '0');
});

test('scrolling writes styles once a frame, and not at all when docked', () => {
  const t = setup();
  let writes = 0;
  const set = t.$('hero').style.setProperty;
  t.$('hero').style.setProperty = function (n, v) { writes++; set.call(this, n, v); };
  for (let y = 150; y < 160; y++) { t.pg.scrollY = y; t.pg.fire('scroll'); }
  assert.equal(t.pg.frame(), 1, 'ten scroll events, one frame');
  assert.equal(writes, 1);
  t.pg.scroll(5000);
  t.pg.scroll(6000);
  assert.equal(writes, 2, 'past the header there is nothing to update');
});
