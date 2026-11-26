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

// How the video dock comes and goes (the timeline first, then each
// control after it, and back in reverse), and how the strip of scenes
// comes up out of the bar (the nearest first, in a chain) on the
// preview's own dark gray, on six minutes of video the browser encodes
// itself (longVideo).

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { serve, open, longVideo } = require('../../browser.js');

let srv, b, video = null;

async function openVideo(page) {
  // A new page load: on the same page, a new fragment alone isn't one.
  await page.goto('about:blank');
  await page.goto(srv.url + '#v=clip.webm&t=0:30');
  await page.waitForSelector('dialog#ss[open] #ss-stage .vdock:not([hidden])');
  await page.waitForFunction(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').readyState >= 2);
}

test.before(async () => {
  b = await open({ viewport: { width: 1280, height: 800 } });
  srv = await serve({ files: {} });
  await b.page.goto(srv.url);
  video = await longVideo(b.page, 6 * 60, 5, 320, 180);
  if (!video) return;
  fs.writeFileSync(path.join(srv.dir, 'clip.webm'), video);
  await openVideo(b.page);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

// Each riser's state: its step, its opacity and offset, and when it starts
// to move.
const risers = (p) => p.evaluate(() => Array.from(document.querySelectorAll('#ss-stage .vd-rise')).map((x) => {
  const cs = getComputedStyle(x);
  return {
    cls: x.className.split(' ')[0], i: +x.style.getPropertyValue('--vd-i'), opacity: +cs.opacity, translate: cs.translate,
    delay: Math.max.apply(null, cs.transitionDelay.split(',').map(parseFloat)),
  };
}));

test('the dock goes in reverse, the controls first and the timeline last, and comes back the other way', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.mouse.move(640, 200);
  // Playing, untouched: the controls go.
  await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').play());
  await p.waitForFunction(() => document.getElementById('ss').classList.contains('chrome-off'), null, { timeout: 8000 });
  await p.waitForTimeout(900);
  let r = await risers(p);
  assert.ok(r.length >= 6, 'the timeline, the time and the controls: ' + r.map((x) => x.cls));
  assert.equal(r[0].cls, 'vd-tl');
  r.forEach((x) => {
    assert.equal(x.opacity, 0, x.cls + ' faded out');
    assert.equal(x.translate, '0px 14px', x.cls + ' down by the same distance');
  });
  for (let i = 1; i < r.length; i++) assert.ok(r[i].delay < r[i - 1].delay, 'going, each before the one before it: ' + r.map((x) => x.delay));
  // Summoned: up and in, the timeline first.
  await p.mouse.move(660, 220, { steps: 2 });
  await p.waitForFunction(() => !document.getElementById('ss').classList.contains('chrome-off'));
  r = await risers(p);
  for (let i = 1; i < r.length; i++) assert.ok(r[i].delay > r[i - 1].delay, 'coming, each after the one before it: ' + r.map((x) => x.delay));
  await p.waitForTimeout(900);
  r = await risers(p);
  r.forEach((x) => {
    assert.equal(x.opacity, 1, x.cls + ' in');
    assert.ok(x.translate === 'none' || x.translate === '0px', x.cls + ' in place: ' + x.translate);
  });
  await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').pause());
});

test('with reduced motion they only fade', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const ctx = await b.browser.newContext({ viewport: { width: 1280, height: 800 }, reducedMotion: 'reduce' });
  const p = await ctx.newPage();
  try {
    await openVideo(p);
    await p.mouse.move(640, 200);
    await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').play());
    await p.waitForFunction(() => document.getElementById('ss').classList.contains('chrome-off'), null, { timeout: 8000 });
    await p.waitForTimeout(600);
    const r = await risers(p);
    r.forEach((x) => {
      assert.equal(x.opacity, 0, x.cls + ' faded out');
      assert.equal(x.translate, 'none', x.cls + ' where it was');
      assert.equal(x.delay, 0, x.cls + ' all at once');
    });
  } finally {
    await ctx.close();
  }
});

test('pulled up, the scenes come out of the bar nearest first, on the preview\'s own gray', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.evaluate(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    v.pause();
    v.currentTime = 30;
  });
  const tl = await p.locator('#ss-stage .vd-tl').boundingBox();
  const x = tl.x + tl.width * 0.5, y = tl.y + tl.height / 2;
  await p.mouse.move(x - 30, y);
  await p.mouse.move(x, y, { steps: 3 });
  await p.waitForSelector('#ss-stage .vd-pop:not([hidden])');
  const previewBg = await p.$eval('#ss-stage .vd-pop-text', (e) => getComputedStyle(e).backgroundColor);
  assert.equal(await p.$eval('#ss-stage .vd-pop', (e) => getComputedStyle(e).backgroundColor), previewBg, 'the preview\'s card the gray of its time bubble');
  await p.mouse.down();
  await p.mouse.move(x, tl.y - 12 - 25, { steps: 8 });
  // Part way: the one under the pointer furthest up, the rest behind it
  // the farther out they are.
  const rises = await p.evaluate((x) => Array.from(document.querySelectorAll('#ss-stage .vd-scene')).map((f) => {
    const r = f.getBoundingClientRect();
    return { d: Math.abs(r.left + r.width / 2 - x), p: parseFloat(f.style.getPropertyValue('--p')) };
  }).sort((a, b) => a.d - b.d), x);
  assert.equal(rises[0].p, 1, 'the one under the pointer, the preview, up all along: ' + JSON.stringify(rises));
  assert.ok(rises[1].p > 0 && rises[1].p > rises[rises.length - 1].p, 'then its neighbors: ' + JSON.stringify(rises));
  for (let i = 1; i < rises.length; i++) assert.ok(rises[i].p <= rises[i - 1].p + 0.01, 'nearer first: ' + JSON.stringify(rises));
  assert.equal(rises[rises.length - 1].p, 0, 'the farthest still in the bar');
  await p.mouse.move(x, tl.y - 160, { steps: 4 });
  await p.mouse.up();
  await p.waitForFunction(() => {
    const el = document.querySelector('#ss-stage .vd-scenes');
    return el.classList.contains('settled') && el.querySelectorAll('.vd-scene').length === el.querySelectorAll('.vd-scene.ready').length;
  }, null, { timeout: 20000 });
  const s = await p.evaluate(() => {
    const el = document.querySelector('#ss-stage .vd-scenes');
    return {
      bg: getComputedStyle(el).backgroundColor, image: getComputedStyle(el).backgroundImage,
      up: Array.from(el.querySelectorAll('.vd-scene')).every((f) => getComputedStyle(f).opacity === '1'),
    };
  });
  assert.equal(s.bg, previewBg, 'the strip is the gray of the preview\'s card and bubble');
  assert.match(s.image, /^linear-gradient\(/, 'with the video\'s color bleeding in at the top');
  assert.equal((s.image.match(/linear-gradient/g) || []).length, 1, 'only at the top');
  assert.equal(s.up, true, 'all up once open');
  // Folded: the chain back into the bar, the farthest first.
  await p.keyboard.press('Escape');
  const waits = await p.evaluate((x) => Array.from(document.querySelectorAll('#ss-stage .vd-scene')).map((f) => {
    const r = f.getBoundingClientRect();
    return { d: Math.abs(r.left + r.width / 2 - x), wait: parseInt(f.style.getPropertyValue('--cd'), 10) };
  }).sort((a, b) => a.d - b.d), x);
  for (let i = 1; i < waits.length; i++) assert.ok(waits[i].wait <= waits[i - 1].wait, 'the farther, the sooner: ' + JSON.stringify(waits));
  assert.ok(waits[0].wait > waits[waits.length - 1].wait, 'the nearest last');
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').hidden);
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
