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

// The breadcrumb's folder panel (crumb-panel.js) and the hover panels it
// opens through (hover-panel.js).

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { page, load } = require('../../harness.js');

const HTML = '<header id="hero"><nav id="crumbs"></nav><h1 id="title" data-from-path>root</h1></header>' +
  '<div id="toolbar"><span id="tb-title">root</span><button id="theme"></button></div><main id="away"></main>';
const FINE = '(hover: hover) and (pointer: fine), (any-hover: hover) and (any-pointer: fine)';
const DWELL = 600, GRACE = 300;

// A page at opts.pathname (/Photos/2024/Rome/ by default) on a wide
// screen with a mouse, whose server answers ?folders from opts.dirs.
function setup(opts) {
  opts = opts || {};
  const asked = [];
  const dirs = opts.dirs || {
    '/': [{ name: 'Music' }, { name: 'Photos' }],
    '/Photos/': [{ name: '2023' }, { name: '2024' }, { name: '2025' }],
  };
  let observer = null;
  const pg = page(opts.html || HTML, {
    location: { pathname: opts.pathname || '/Photos/2024/Rome/', search: opts.search || '' },
    media: opts.media || { [FINE]: true, '(min-width: 1100px)': true, '(min-width: 700px)': true },
    globals: {
      fetch(url) {
        asked.push(url);
        const list = dirs[url.replace(/\?.*$/, '')];
        return Promise.resolve({ ok: !!list, json: () => Promise.resolve({ folders: list }) });
      },
      IntersectionObserver: class {
        constructor(fn) { this.fn = fn; observer = this; }
        observe(el) { this.el = el; }
        unobserve() { this.el = null; }
      },
    },
  });
  const $ = (id) => pg.document.getElementById(id);
  load(pg, ['page-theme/hover-panel.js', 'page-theme/page-theme.js', 'page-theme/crumb-panel.js'], []);
  const crumb = (text) => $('crumbs').querySelectorAll('a').find((a) => a.textContent === text);
  const panel = $('crumb-panel');
  return {
    pg, $, asked, crumb, panel,
    open: () => panel.classList.contains('hp-open'),
    entries: () => panel.querySelectorAll('a').map((a) => [a.textContent, a.getAttribute('href')]),
    current: () => panel.querySelectorAll('a.current').map((a) => a.textContent),
    hover: (el, pointerType) => el.fire('pointerover', { pointerType: pointerType || 'mouse' }),
    settle: async () => { for (let i = 0; i < 4; i++) await pg.settle(); },
    scrolledAway: (el) => observer.fn([{ target: el, isIntersecting: false }]),
  };
}

test('resting on a crumb lists the folders beside it, its own marked', async () => {
  const t = setup();
  const crumb = t.crumb('2024');
  t.hover(crumb);
  t.pg.tick(DWELL - 1);
  assert.deepEqual(t.asked, [], 'nothing as the pointer crosses it');
  t.pg.tick(1);
  assert.deepEqual(t.asked, ['/Photos/?folders'], 'its parent is asked, when it opens');
  await t.settle();
  assert.ok(t.open());
  assert.deepEqual(t.entries(), [['2023', '/Photos/2023/'], ['2024', '/Photos/2024/'], ['2025', '/Photos/2025/']]);
  assert.deepEqual(t.current(), ['2024']);
  assert.equal(t.panel.querySelector('.cp-head').textContent, 'Photos');
  assert.equal(t.panel.getAttribute('aria-label'), 'Photos');
  assert.equal(crumb.getAttribute('aria-expanded'), 'true');
  assert.equal(crumb.getAttribute('aria-controls'), 'crumb-panel');
  assert.ok(crumb.classList.contains('hp-anchor'), 'the crumb stays lit');
});

test('the panel sits under its crumb, inside the window, scrolling past the room below', async () => {
  const t = setup();
  const crumb = t.crumb('2024');
  crumb.rect = { top: 40, left: 1200, width: 50, height: 24 };
  t.panel.rect = { width: 240 };
  t.pg.scrollY = 0;
  t.hover(crumb);
  t.pg.tick(DWELL);
  await t.settle();
  assert.equal(t.panel.style.top, '70px');
  assert.equal(t.panel.style.left, (1280 - 240 - 12) + 'px', 'pulled in from the right edge');
  assert.equal(t.panel.style.maxHeight, (800 - 64 - 6 - 12) + 'px');
});

test('the home crumb lists the top level; the ellipsis the folders it hides', async () => {
  const t = setup();
  t.hover(t.crumb('example.test'));
  t.pg.tick(DWELL);
  await t.settle();
  assert.deepEqual(t.asked, ['/?folders']);
  assert.deepEqual(t.entries(), [['Music', '/Music/'], ['Photos', '/Photos/']]);
  assert.deepEqual(t.current(), ['Photos']);

  const deep = setup({ pathname: '/a/b%20c/d/e/f/g/here/' });
  deep.hover(deep.$('crumbs').querySelector('.more'));
  deep.pg.tick(DWELL);
  await deep.settle();
  assert.deepEqual(deep.asked, [], 'nothing to ask: the page has them');
  assert.deepEqual(deep.entries(), [['a', '/a/'], ['b c', '/a/b%20c/']]);
  assert.equal(deep.panel.querySelector('.cp-head').textContent, 'Folders above');
});

test('archives open as folders, and names are escaped', async () => {
  const t = setup({
    pathname: '/Photos/pack.zip.d/x/',
    dirs: { '/Photos/': [{ name: '<b>&' }, { name: 'pack.zip', archive: true }] },
  });
  t.hover(t.crumb('pack.zip'));
  t.pg.tick(DWELL);
  await t.settle();
  assert.deepEqual(t.entries(), [['<b>&', '/Photos/%3Cb%3E%26/'], ['pack.zip', '/Photos/pack.zip.d/']]);
  assert.deepEqual(t.current(), ['pack.zip']);
  assert.doesNotMatch(t.panel.innerHTML, /<b>/);
});

test('the page\'s sort goes along, so the panel is in the order its listing will be', async () => {
  const t = setup({ search: '?sort=date-desc' });
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL);
  assert.deepEqual(t.asked, ['/Photos/?folders&sort=date-desc']);
});

test('no panel for a finger, without a mouse, or when the folder can\'t be listed', async () => {
  const touch = setup();
  touch.hover(touch.crumb('2024'), 'touch');
  touch.pg.tick(DWELL);
  assert.deepEqual(touch.asked, []);

  const noMouse = setup({ media: { '(min-width: 1100px)': true } });
  noMouse.hover(noMouse.crumb('2024'));
  noMouse.pg.tick(DWELL);
  assert.deepEqual(noMouse.asked, []);

  const gone = setup({ dirs: {} });
  gone.hover(gone.crumb('2024'));
  gone.pg.tick(DWELL);
  await gone.settle();
  assert.equal(gone.open(), false);
  assert.equal(gone.crumb('2024').getAttribute('aria-expanded'), null);
});

test('passing over a crumb on the way elsewhere opens nothing', () => {
  const t = setup();
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL - 100);
  t.hover(t.$('away'));
  t.pg.tick(DWELL);
  assert.deepEqual(t.asked, []);
});

test('it stays while the pointer moves into it, and closes after it leaves both', async () => {
  const t = setup();
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL);
  await t.settle();
  t.hover(t.$('hero')); // the gap between them
  t.pg.tick(GRACE - 50);
  t.hover(t.panel.querySelector('a'));
  t.pg.tick(GRACE);
  assert.ok(t.open(), 'a diagonal move into the panel keeps it');
  t.hover(t.$('away'));
  t.pg.tick(GRACE - 1);
  assert.ok(t.open(), 'a moment\'s grace');
  t.pg.tick(1);
  assert.equal(t.open(), false);
  assert.equal(t.crumb('2024').getAttribute('aria-expanded'), null);
  assert.equal(t.crumb('2024').getAttribute('aria-controls'), null);
  assert.ok(!t.crumb('2024').classList.contains('hp-anchor'));
});

test('another crumb moves the open panel there at once', async () => {
  const t = setup();
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL);
  await t.settle();
  t.hover(t.crumb('Photos'));
  await t.settle();
  assert.deepEqual(t.asked, ['/Photos/?folders', '/?folders'], 'no second dwell');
  assert.deepEqual(t.current(), ['Photos']);
  assert.ok(t.open());
  assert.ok(!t.crumb('2024').classList.contains('hp-anchor'));
});

test('an answer is kept a moment, for a pointer going back and forth', async () => {
  const t = setup();
  for (const name of ['2024', 'Photos', '2024']) {
    t.hover(t.crumb(name));
    t.pg.tick(DWELL);
    await t.settle();
  }
  assert.deepEqual(t.asked, ['/Photos/?folders', '/?folders']);
});

test('Escape, a press elsewhere and the crumb scrolling away close it', async () => {
  for (const how of ['escape', 'press', 'scroll']) {
    const t = setup();
    const crumb = t.crumb('2024');
    t.hover(crumb);
    t.pg.tick(DWELL);
    await t.settle();
    if (how === 'escape') {
      t.panel.querySelector('a').focus();
      t.pg.document.fire('keydown', { key: 'Escape' });
      assert.equal(t.pg.document.activeElement, crumb, 'focus goes back to the crumb');
    } else if (how === 'press') {
      t.$('away').fire('pointerdown');
    } else {
      t.scrolledAway(crumb);
    }
    assert.equal(t.open(), false, how);
  }
});

test('from the keyboard: focus on a crumb opens it after the dwell, ArrowDown at once and into it', async () => {
  const t = setup();
  const crumb = t.crumb('2024');
  const matches = crumb.matches.bind(crumb);
  crumb.matches = (s) => s === ':focus-visible' || matches(s);
  crumb.focus();
  crumb.fire('focusin');
  t.pg.tick(DWELL);
  await t.settle();
  assert.ok(t.open(), 'focus counts as resting on it');
  t.$('away').fire('focusin');
  assert.equal(t.open(), false, 'and focus leaving closes it');

  crumb.fire('keydown', { key: 'ArrowDown' });
  await t.settle();
  assert.ok(t.open());
  assert.equal(t.pg.document.activeElement.textContent, '2024', 'on its own folder');
  t.pg.document.activeElement.fire('keydown', { key: 'ArrowDown' });
  assert.equal(t.pg.document.activeElement.textContent, '2025');
  t.pg.document.activeElement.fire('keydown', { key: 'ArrowUp' });
  t.pg.document.activeElement.fire('keydown', { key: 'ArrowUp' });
  t.pg.document.activeElement.fire('keydown', { key: 'ArrowUp' });
  assert.equal(t.pg.document.activeElement, crumb, 'up from the first goes back to the crumb');
});

test('a click on a crumb doesn\'t open it: focus without :focus-visible', () => {
  const t = setup();
  t.crumb('2024').fire('focusin');
  t.pg.tick(DWELL);
  assert.deepEqual(t.asked, []);
});

test('panels share one dwell and one timer: another owner\'s panel replaces this one', async () => {
  const t = setup();
  const hp = t.pg.gowsHoverPanel;
  const log = [];
  const row = t.$('away');
  const ctl = hp.attach(t.pg.document.body, 'main', {
    grace: 0,
    open: (a, how) => log.push('open ' + a.id + ' ' + how),
    close: (a) => log.push('close ' + a.id),
  });
  assert.equal(hp.DWELL, DWELL);
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL);
  await t.settle();
  assert.ok(t.open());
  t.hover(row);
  t.pg.tick(GRACE);
  assert.equal(t.open(), false, 'the crumb panel closes after its grace, while the other waits out its dwell');
  assert.deepEqual(log, []);
  t.pg.tick(DWELL - GRACE);
  assert.deepEqual(log, ['open away pointer']);
  assert.ok(ctl.isOpen(row));
  t.hover(t.$('title'));
  t.pg.tick(0);
  assert.deepEqual(log, ['open away pointer', 'close away']);
  assert.equal(ctl.isOpen(), false);
});

test('the crumbs being rebuilt (a change of screen width) close it', async () => {
  const t = setup();
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL);
  await t.settle();
  t.pg.gowsHoverPanel.close();
  assert.equal(t.open(), false);
});

test('moving along the crumbs takes the panel\'s state off the crumb it leaves', async () => {
  const t = setup();
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL);
  await t.settle();
  t.hover(t.crumb('Photos'));
  await t.settle();
  assert.equal(t.crumb('2024').getAttribute('aria-expanded'), null);
  assert.equal(t.crumb('2024').getAttribute('aria-controls'), null);
  assert.equal(t.crumb('Photos').getAttribute('aria-expanded'), 'true');
});

test('a panel opened from the keyboard stays while the mouse moves elsewhere', async () => {
  const t = setup();
  const crumb = t.crumb('2024');
  crumb.focus();
  crumb.fire('keydown', { key: 'ArrowDown' });
  await t.settle();
  assert.ok(t.open());
  t.hover(t.$('away'));
  t.pg.tick(GRACE * 3);
  assert.ok(t.open(), 'the mouse was never on it');
  // Once the pointer has been on it, leaving closes it as usual.
  t.hover(t.panel.querySelector('a'));
  t.hover(t.$('away'));
  t.pg.tick(GRACE);
  assert.equal(t.open(), false);
});

test('the panel follows the crumbs in the page, and Tab out of it closes it', async () => {
  const t = setup();
  assert.equal(t.$('crumbs').nextSibling, t.panel, 'right after the crumbs, for Tab');
  t.hover(t.crumb('2024'));
  t.pg.tick(DWELL);
  await t.settle();
  const last = t.panel.querySelectorAll('a').pop();
  last.focus();
  last.fire('focusin');
  assert.ok(t.open(), 'Tab into it keeps it');
  t.$('theme').fire('focusin', { relatedTarget: last });
  assert.equal(t.open(), false, 'Tab out of it closes it, though the pointer opened it');
});

test('a serve path of more than one segment links and marks as the page does', async () => {
  const t = setup({ pathname: '/a/b/x/', dirs: { '/': [{ name: 'a/b' }, { name: 'c d' }] } });
  t.hover(t.crumb('example.test'));
  t.pg.tick(DWELL);
  await t.settle();
  assert.deepEqual(t.entries(), [['a/b', '/a/b/'], ['c d', '/c%20d/']]);
  assert.deepEqual(t.current(), ['a/b']);
});

// Embedded at /media/, the home crumb is the mount's: it lists the
// mount's top level, asked of the mount, and every link stays under it.
test('embedded, the home crumb lists the mount\'s top level', async () => {
  const t = setup({
    html: HTML.replace('<nav id="crumbs">', '<nav id="crumbs" data-home="./../../" data-title="Media">'),
    pathname: '/media/Photos/2024/',
    dirs: { '/media/': [{ name: 'Music' }, { name: 'Photos' }], '/media/Photos/': [{ name: '2024' }] },
  });
  t.hover(t.crumb('Media'));
  t.pg.tick(DWELL);
  await t.settle();
  assert.deepEqual(t.asked, ['/media/?folders']);
  assert.deepEqual(t.entries(), [['Music', '/media/Music/'], ['Photos', '/media/Photos/']]);
  assert.deepEqual(t.current(), ['Photos']);
  assert.equal(t.panel.querySelector('.cp-head').textContent, 'Media');
});

test('the link to the host\'s app opens no panel', async () => {
  const t = setup({
    html: HTML.replace('<nav id="crumbs">', '<nav id="crumbs" data-home="./../../" data-title="Media" data-host-href="/" data-host-label="App">'),
    pathname: '/media/Photos/2024/',
    dirs: { '/': [{ name: 'api' }], '/media/': [{ name: 'Photos' }] },
  });
  t.hover(t.crumb('App'));
  t.pg.tick(DWELL);
  await t.settle();
  assert.deepEqual(t.asked, []);
  assert.equal(t.open(), false);
});
