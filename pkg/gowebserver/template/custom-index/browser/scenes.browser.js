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

// The strip of scenes over a video's timeline (scenes.js), on twelve
// minutes of video the browser encodes itself (longVideo): each minute a
// scene of its own color. Its pictures are the video's own frames here,
// grabbed by the preview's copy of it, as without ffmpeg there's no
// sprite.

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { serve, open, longVideo } = require('../../browser.js');

let srv, b, video = null;

test.before(async () => {
  b = await open({ viewport: { width: 1280, height: 800 } });
  srv = await serve({
    files: {
      'voyage.chapters.vtt': 'WEBVTT\n\n00:00.000 --> 04:00.000\nDeparture\n\n04:00.000 --> 12:00.000\nThe deep\n',
    },
  });
  // WebCodecs wants a secure context, as the server's page is.
  await b.page.goto(srv.url);
  video = await longVideo(b.page, 12 * 60, 5, 320, 180);
  if (!video) return;
  fs.writeFileSync(path.join(srv.dir, 'voyage.webm'), video);
  await b.page.goto(srv.url);
  await b.page.locator('.tile[aria-label="voyage.webm"]').click();
  await b.page.waitForSelector('dialog#ss[open] #ss-stage .vdock:not([hidden])');
  await b.page.waitForFunction(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return v.readyState >= 1 && v.duration > 700;
  });
  // Held still, so what's checked doesn't move on.
  await b.page.evaluate(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    v.pause();
    v.currentTime = 30;
  });
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const strip = (p) => p.evaluate(() => {
  const el = document.querySelector('#ss-stage .vd-scenes');
  const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
  const lit = el.querySelector('.vd-scene.on');
  return {
    open: !el.hidden && el.classList.contains('open'), hidden: el.hidden, count: +el.dataset.count,
    mounted: el.querySelectorAll('.vd-scene').length, ready: el.querySelectorAll('.vd-scene.ready').length,
    lit: lit && lit.getAttribute('aria-label'), scroll: el.querySelector('.vd-scenes-view').scrollLeft,
    t: v.currentTime, paused: v.paused, dialog: !!document.querySelector('dialog#ss[open]'),
    zoom: !!document.querySelector('#ss-stage .vd-zoom'),
  };
});

// The mouse on the timeline at fraction f of it, then up off it by dy.
async function liftAt(p, f, dy) {
  const tl = await p.locator('#ss-stage .vd-tl').boundingBox();
  const x = tl.x + tl.width * f, y = tl.y + tl.height / 2;
  await p.mouse.move(x - 30, y);
  await p.mouse.move(x, y, { steps: 3 });
  await p.waitForSelector('#ss-stage .vd-pop:not([hidden])');
  await p.mouse.move(x, tl.y - 4, { steps: 2 });
  await p.mouse.move(x, tl.y - dy, { steps: 4 });
  return { x, tl };
}

test('rising from the timeline into the preview unfolds the scenes, a minute apart', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await liftAt(p, 0.5, 30);
  assert.equal(await p.evaluate(() => document.querySelector('#ss-stage .vd-pop').classList.contains('lifting')), true, 'half way, the preview lifts');
  assert.equal((await strip(p)).hidden, true);
  const tl = await p.locator('#ss-stage .vd-tl').boundingBox();
  await p.mouse.move(tl.x + tl.width * 0.5, tl.y - 70, { steps: 3 });
  await p.waitForSelector('#ss-stage .vd-scenes.open');
  const s = await strip(p);
  assert.equal(s.count, 12);
  assert.equal(s.lit, '6:00, The deep', 'the scene nearest the time previewed');
  assert.ok(s.mounted < s.count, 'only those near the view: ' + s.mounted);
  // Each scene's picture: the video's own frame at its time.
  await p.waitForFunction(() => {
    const el = document.querySelector('#ss-stage .vd-scenes');
    return el.querySelectorAll('.vd-scene').length === el.querySelectorAll('.vd-scene.ready').length;
  }, null, { timeout: 20000 });
  // Drawn, not a blank box: scene 6:00 is the seventh's color.
  const px = await p.evaluate(() => {
    const c = document.querySelector('#ss-stage .vd-scene.on canvas');
    const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
    let lo = 255, hi = 0;
    for (let i = 0; i < d.length; i += 4) {
      const l = (d[i] * 3 + d[i + 1] * 6 + d[i + 2]) / 10;
      lo = Math.min(lo, l);
      hi = Math.max(hi, l);
    }
    return hi - lo;
  });
  assert.ok(px > 40, 'a picture in it: ' + px);
  // The video wasn't touched.
  assert.ok(Math.abs(s.t - 30) < 0.5 && s.paused);
});

test('the strip scrolls with the wheel, and the scenes come in as they near', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const before = await strip(p);
  const view = await p.locator('#ss-stage .vd-scenes-view').boundingBox();
  await p.mouse.move(view.x + view.width / 2, view.y + view.height / 2, { steps: 2 });
  await p.mouse.wheel(0, 2000);
  await p.waitForFunction((s0) => document.querySelector('#ss-stage .vd-scenes-view').scrollLeft > s0 + 100, before.scroll);
  const after = await strip(p);
  assert.equal(after.open, true, 'still up');
  assert.ok(await p.locator('#ss-stage .vd-scene[aria-label^="11:00"]').count(), 'the last scene is in the page');
  assert.equal(await p.locator('#ss-stage .vd-scene[aria-label^="0:00"]').count(), 0, 'the first left it');
  await p.waitForFunction(() => {
    const el = document.querySelector('#ss-stage .vd-scenes');
    return el.querySelectorAll('.vd-scene').length === el.querySelectorAll('.vd-scene.ready').length;
  }, null, { timeout: 20000 });
});

test('a scene clicked grows over the video, which goes there and plays', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const scene = p.locator('#ss-stage .vd-scene[aria-label^="9:00"]');
  const box = await scene.boundingBox();
  await p.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 2 });
  await p.mouse.down();
  await p.mouse.up();
  assert.equal((await strip(p)).zoom, true, 'the scene, growing');
  await p.waitForFunction(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return !document.querySelector('#ss-stage .vd-zoom') && !v.paused && v.currentTime >= 540 && v.currentTime < 545;
  }, null, { timeout: 10000 });
  const s = await strip(p);
  assert.equal(s.hidden, true, 'the strip folded away');
  assert.equal(s.dialog, true);
  await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').pause());
});

test('Escape folds the strip and leaves the slideshow open', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.mouse.move(640, 100);
  await p.focus('#ss');
  await p.keyboard.press('s');
  await p.waitForSelector('#ss-stage .vd-scenes.open');
  assert.equal((await strip(p)).lit, '9:00, The deep', 'opened by the key on the current time');
  await p.keyboard.press('ArrowLeft');
  assert.equal((await strip(p)).lit, '8:00, The deep');
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').hidden);
  assert.equal((await strip(p)).dialog, true, 'the slideshow is still open');
});

test('moving back down to the timeline folds the strip', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const { x, tl } = await liftAt(p, 0.25, 70);
  await p.waitForSelector('#ss-stage .vd-scenes.open');
  assert.equal((await strip(p)).lit, '3:00, Departure');
  await p.mouse.move(x, tl.y + tl.height / 2, { steps: 4 });
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').hidden);
  // And the preview is back, under the pointer.
  await p.mouse.move(x + 5, tl.y + tl.height / 2);
  await p.waitForSelector('#ss-stage .vd-pop:not([hidden])');
  await p.mouse.move(640, 100);
});

test('on a phone, a finger scrubbing drags up off the timeline for the strip, and a tap picks', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const ctx = await b.browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true });
  const p = await ctx.newPage();
  try {
    await p.goto(srv.url + '#v=voyage.webm&t=0:30');
    await p.waitForSelector('dialog#ss[open] #ss-stage .vdock:not([hidden])');
    await p.waitForFunction(() => {
      const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
      return v.readyState >= 1 && v.duration > 700;
    });
    // A finger on the timeline, dragged along it and then up off it.
    await p.evaluate(() => {
      const tl = document.querySelector('#ss-stage .vd-tl'), r = tl.getBoundingClientRect();
      const at = (type, x, y) => tl.dispatchEvent(new PointerEvent(type, {
        bubbles: true, cancelable: true, pointerId: 7, pointerType: 'touch', isPrimary: true, clientX: x, clientY: y,
      }));
      const y = r.top + r.height / 2;
      at('pointerdown', r.left + r.width * 0.3, y);
      at('pointermove', r.left + r.width * 0.5, y);
      at('pointermove', r.left + r.width * 0.5, r.top - 40);
      at('pointermove', r.left + r.width * 0.5, r.top - 90);
      at('pointerup', r.left + r.width * 0.5, r.top - 90);
    });
    await p.waitForSelector('#ss-stage .vd-scenes.open');
    assert.equal((await strip(p)).lit, '6:00, The deep', 'where it was scrubbed to');
    const w = await p.$eval('#ss-stage .vd-scene', (e) => e.offsetWidth);
    assert.equal(Math.round(w), 112, 'the compact dock\'s smaller scenes');
    const scene = p.locator('#ss-stage .vd-scene[aria-label^="7:00"]');
    await scene.waitFor();
    await scene.tap();
    await p.waitForFunction(() => {
      const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
      return !document.querySelector('#ss-stage .vd-zoom') && v.currentTime >= 420 && v.currentTime < 425;
    }, null, { timeout: 10000 });
    assert.equal((await strip(p)).hidden, true);
  } finally {
    await ctx.close();
  }
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
