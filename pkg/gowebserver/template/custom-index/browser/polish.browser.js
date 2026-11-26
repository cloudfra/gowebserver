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

// The dock and the info panel, polished: the full-screen strip of videos
// in the dock's row still answers after switching videos with it (it was
// placed from the sliding slide's position, off to the side); the wheel
// on the bar brings up the strip of scenes; the strip is edge to edge,
// square, its lit ring not cut off; the shadow goes down from the bar;
// the previous and next buttons come in from beyond the screen's edges;
// the info panel is one solid panel, and its buttons move.

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { serve, open, longVideo } = require('../../browser.js');

let srv, b, video = null;

async function openVideo(page, name) {
  await page.goto('about:blank');
  await page.goto(srv.url + '#v=' + name);
  await page.waitForSelector('dialog#ss[open] #ss-stage .vdock:not([hidden])');
  await page.waitForFunction(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').readyState >= 2);
  await page.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').pause());
}

test.before(async () => {
  b = await open({ viewport: { width: 1280, height: 800 } });
  srv = await serve({ files: {} });
  await b.page.goto(srv.url);
  video = await longVideo(b.page, 6 * 60, 5, 320, 180);
  if (!video) return;
  for (const n of ['a', 'b', 'c', 'd']) fs.writeFileSync(path.join(srv.dir, n + '.webm'), video);
  await openVideo(b.page, 'a.webm');
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const rect = (p, sel) => p.$eval(sel, (e) => {
  const r = e.getBoundingClientRect();
  return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height };
});
const gone = (p) => p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').hidden);

test('the wheel on the bar brings up the strip of scenes, and then scrolls it', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const tl = await rect(p, '#ss-stage .vd-tl');
  await p.mouse.move(tl.left + tl.width * 0.3, tl.top + tl.height / 2, { steps: 3 });
  await p.mouse.wheel(0, 100);
  await p.waitForSelector('#ss-stage .vd-scenes.open');
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').classList.contains('settled'));
  const before = await p.$eval('#ss-stage .vd-scenes-view', (e) => e.scrollLeft);
  await p.mouse.move(tl.left + tl.width * 0.3, tl.top + tl.height / 2);
  await p.mouse.wheel(0, 300);
  await p.waitForFunction((s0) => document.querySelector('#ss-stage .vd-scenes-view').scrollLeft > s0 + 100, before);
});

test('the strip is edge to edge and square, its lit ring whole, running on into the shadow under the bar', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const s = await p.evaluate(() => {
    const el = document.querySelector('#ss-stage .vd-scenes'), dock = document.querySelector('#ss-stage .vdock');
    const panel = getComputedStyle(dock.querySelector('.vd-panel'), '::before');
    const r = el.getBoundingClientRect(), d = dock.getBoundingClientRect(), view = el.querySelector('.vd-scenes-view').getBoundingClientRect();
    const card = el.querySelector('.vd-scene.on .vd-scene-card').getBoundingClientRect();
    const tl = dock.querySelector('.vd-tl').getBoundingClientRect(), p = dock.querySelector('.vd-panel').getBoundingClientRect();
    return {
      left: r.left, right: r.right, dl: d.left, dr: d.right, clip: el.style.clipPath, radius: getComputedStyle(el).borderTopLeftRadius,
      cardBottom: card.bottom, viewBottom: view.bottom, cardTop: card.top, viewTop: view.top,
      shadowTop: p.top + parseFloat(panel.top), tlMid: tl.top + tl.height / 2, shadow: panel.backgroundImage,
    };
  });
  assert.ok(Math.abs(s.left - s.dl) <= 1 && Math.abs(s.right - s.dr) <= 1, 'the dock\'s whole width: ' + JSON.stringify(s));
  assert.doesNotMatch(s.clip, /round [1-9]/, 'square, open: ' + s.clip);
  assert.equal(s.radius, '0px');
  assert.ok(s.viewBottom >= s.cardBottom + 18 && s.viewTop <= s.cardTop - 18, 'room for the ring and the glow, top and bottom: ' + JSON.stringify(s));
  assert.ok(s.shadowTop >= s.tlMid - 1, 'the shadow from the bar down, not above it: ' + JSON.stringify(s));
  // #16181e, however the browser writes it.
  assert.match(s.shadow, /rgba?\(22, 24, 30|srgb 0\.086\d* 0\.094\d* 0\.117\d*/, 'in the strip\'s gray: ' + s.shadow);
  await p.keyboard.press('Escape');
  await gone(p);
});

test('with no chapter under the pointer, the preview shows no time under the bar', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const tl = await rect(p, '#ss-stage .vd-tl');
  await p.mouse.move(tl.left + tl.width * 0.6, tl.top + tl.height / 2, { steps: 3 });
  await p.waitForFunction(() => !document.querySelector('#ss-stage .vd-pop').hidden);
  assert.equal(await p.$eval('#ss-stage .vd-pop-text', (e) => e.hidden), true);
  assert.equal(await p.$eval('#ss-stage .vd-pop-time', (e) => getComputedStyle(e).display), 'none');
  await p.mouse.move(640, 100);
});

test('the info panel is one solid panel, rounded, with a shadow; its close button moves', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.click('#ss-info');
  await p.waitForFunction(() => getComputedStyle(document.getElementById('ss-panel')).opacity === '1');
  // Done coming in.
  await p.waitForTimeout(600);
  const s = await p.evaluate(() => {
    const el = document.getElementById('ss-panel'), cs = getComputedStyle(el);
    return { bg: cs.backgroundColor, radius: cs.borderTopLeftRadius, shadow: cs.boxShadow, before: getComputedStyle(el, '::before').content, blur: cs.backdropFilter };
  });
  assert.equal(s.bg, 'rgb(22, 24, 30)', 'solid, the strip\'s gray');
  assert.equal(s.radius, '18px');
  assert.notEqual(s.shadow, 'none');
  assert.equal(s.before, 'none', 'no layer fading into the slide');
  const x = await rect(p, '#ss-panel-close');
  await p.mouse.move(x.left + x.width / 2, x.top + x.height / 2, { steps: 3 });
  await p.waitForTimeout(450);
  const hover = await p.evaluate(() => {
    const btn = document.getElementById('ss-panel-close');
    return { disc: getComputedStyle(btn, '::before').opacity, turn: getComputedStyle(btn.querySelector('svg')).rotate };
  });
  assert.equal(hover.disc, '1', 'a disc behind it');
  assert.equal(hover.turn, '90deg', 'its X turns');
  assert.deepEqual(await rect(p, '#ss-panel-close'), x, 'its box the same');
  await p.click('#ss-panel-close');
  await p.waitForFunction(() => getComputedStyle(document.getElementById('ss-panel')).visibility === 'hidden');
});

test('in full screen, switching videos with the strip in the row, again and again, it still answers', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.click('#ss-full');
  await p.waitForFunction(() => document.fullscreenElement === document.getElementById('ss-frame'));
  await p.waitForTimeout(600);
  for (const [i, name] of [[1, 'b.webm'], [2, 'c.webm'], [3, 'd.webm'], [0, 'a.webm']]) {
    await p.mouse.move(600, 300);
    await p.mouse.move(620, 320, { steps: 2 });
    await p.waitForFunction(() => document.getElementById('ss').classList.contains('strip-inline') && getComputedStyle(document.querySelector('.ss-bottom')).opacity === '1');
    // The button is where a pointer can press it: on screen, and itself
    // at its middle.
    const hit = await p.evaluate((k) => {
      const btn = Array.from(document.querySelectorAll('#ss-strip button')).find((x) => x._k === k);
      const r = btn.getBoundingClientRect(), at = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
      return { on: r.left >= 0 && r.right <= innerWidth, self: !!at && at.closest('button') === btn };
    }, i);
    assert.deepEqual(hit, { on: true, self: true }, 'switch to ' + name + ': the strip still answers');
    await p.evaluate((k) => Array.from(document.querySelectorAll('#ss-strip button')).find((x) => x._k === k).setAttribute('data-test', 'go'), i);
    await p.click('#ss-strip button[data-test="go"]');
    await p.evaluate(() => document.querySelector('#ss-strip button[data-test]').removeAttribute('data-test'));
    await p.waitForFunction((n) => document.getElementById('ss-name').textContent === n, name);
    await p.waitForTimeout(700);
  }
});

test('in full screen, previous and next come in from beyond the screen\'s edges, and go back out', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.mouse.move(640, 300, { steps: 2 });
  await p.waitForTimeout(500);
  const shown = { prev: await rect(p, '#ss-prev'), next: await rect(p, '#ss-next') };
  assert.ok(shown.prev.left > 0 && shown.next.right < await p.evaluate(() => innerWidth), 'in, on screen');
  // Playing, untouched: the controls go.
  await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').play());
  await p.waitForFunction(() => document.getElementById('ss').classList.contains('chrome-off'), null, { timeout: 8000 });
  await p.waitForTimeout(700);
  const away = { prev: await rect(p, '#ss-prev'), next: await rect(p, '#ss-next'), w: await p.evaluate(() => innerWidth) };
  assert.ok(away.prev.right <= 0, 'previous beyond the left edge: ' + JSON.stringify(away));
  assert.ok(away.next.left >= away.w, 'next beyond the right edge: ' + JSON.stringify(away));
  await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').pause());
  await p.evaluate(() => document.exitFullscreen());
  await p.waitForFunction(() => !document.fullscreenElement);
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
