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

// The strip of scenes a video's timeline stretches into (scenes.js), on
// twelve minutes of video the browser encodes itself (longVideo): each minute a
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
  // A second video, so the slideshow has a filmstrip (in full screen, up
  // from the bottom edge).
  fs.writeFileSync(path.join(srv.dir, 'zz-again.webm'), video);
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

const panel = (p) => p.evaluate(() => {
  const el = document.querySelector('#ss-stage .vd-scenes');
  const dock = document.querySelector('#ss-stage .vdock');
  const v = dock.parentNode.querySelector('video');
  const tl = dock.querySelector('.vd-tl'), scan = dock.querySelector('.vd-scan');
  const lit = el.querySelector('.vd-scene.on');
  const r = el.getBoundingClientRect(), vr = v.getBoundingClientRect(), tr = tl.getBoundingClientRect(), dr = dock.getBoundingClientRect();
  return {
    open: !el.hidden && el.classList.contains('open'), hidden: el.hidden, count: +el.dataset.count,
    mounted: el.querySelectorAll('.vd-scene').length, entering: el.querySelectorAll('.vd-scene.enter').length,
    lit: lit && lit.getAttribute('aria-label'), scroll: el.querySelector('.vd-scenes-view').scrollLeft,
    t: v.currentTime, paused: v.paused, dialog: !!document.querySelector('dialog#ss[open]'),
    zoom: !!document.querySelector('#ss-stage .vd-zoom'),
    top: Math.round(r.top), bottom: Math.round(r.bottom), left: Math.round(r.left), width: Math.round(r.width), height: Math.round(r.height),
    tlTop: Math.round(tr.top), tlBottom: Math.round(tr.bottom), tlLeft: Math.round(tr.left), tlWidth: Math.round(tr.width),
    video: [Math.round(vr.top), Math.round(vr.bottom)], dockBottom: Math.round(dr.bottom),
    scan: scan.hidden ? null : scan.style.left,
    stripOn: document.getElementById('ss').classList.contains('strip-on'),
  };
});

const settled = (p) => p.waitForFunction(() => {
  const el = document.querySelector('#ss-stage .vd-scenes');
  return el.classList.contains('settled') && el.querySelectorAll('.vd-scene').length === el.querySelectorAll('.vd-scene.ready').length;
}, null, { timeout: 20000 });
const gone = (p) => p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').hidden);

// Takes the timeline at fraction f of it with the mouse and pulls up by
// dy, holding on.
async function pullAt(p, f, dy) {
  const tl = await p.locator('#ss-stage .vd-tl').boundingBox();
  const x = tl.x + tl.width * f, y = tl.y + tl.height / 2;
  await p.mouse.move(x - 30, y);
  await p.mouse.move(x, y, { steps: 3 });
  await p.mouse.down();
  await p.mouse.move(x, tl.y - dy, { steps: 8 });
  return { x, tl };
}

test('hovering alone brings nothing up, and the preview rises out of the bar', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const tl = await p.locator('#ss-stage .vd-tl').boundingBox();
  await p.mouse.move(tl.x + tl.width / 2, tl.y + 10, { steps: 3 });
  await p.waitForSelector('#ss-stage .vd-pop.rise:not([hidden])');
  // Its box is the dark gray it always was, not the accent.
  assert.equal(await p.$eval('#ss-stage .vd-pop-frame', (e) => getComputedStyle(e).backgroundColor), 'rgb(12, 13, 16)');
  await p.mouse.move(tl.x + tl.width / 2, tl.y - 200, { steps: 6 });
  assert.equal((await panel(p)).hidden, true);
});

test('the timeline held and pulled up stretches into the strip, a scene a minute; the video stays put', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const before = await panel(p);
  // Half way: the bar stretches up, not yet open.
  const { x, tl } = await pullAt(p, 0.5, 60);
  let s = await panel(p);
  assert.equal(s.hidden, false);
  assert.equal(s.open, false, 'not yet');
  assert.ok(s.bottom >= s.tlBottom && s.top < s.tlTop, 'grown up out of the bar: ' + JSON.stringify(s));
  const halfHeight = s.height;
  await p.mouse.move(x, tl.y - 150, { steps: 4 });
  await p.mouse.up();
  await p.waitForSelector('#ss-stage .vd-scenes.open');
  await settled(p);
  s = await panel(p);
  assert.ok(s.height > halfHeight + 20, 'taller open than half way');
  assert.equal(s.count, 12);
  assert.equal(s.lit, '6:00, The deep', 'the scene nearest where the pull started');
  assert.ok(Math.abs(s.left - (s.tlLeft - 6)) <= 1 && Math.abs(s.width - (s.tlWidth + 12)) <= 1, 'as wide as the bar: ' + JSON.stringify(s));
  assert.ok(s.bottom > s.tlBottom && s.bottom - s.tlBottom <= 8, 'the bar along its bottom');
  assert.deepEqual(s.video, before.video, 'the video doesn\'t move');
  assert.equal(s.scan, '50%', 'a mark on the bar at 6:00');
  // The lit ring is the bar's played color: --vd-played, as a ring of
  // it is drawn, compared as pixels (the browser may write the two the
  // same color in different notations).
  const ring = await p.evaluate(() => {
    const el = document.querySelector('#ss-stage .vd-scenes');
    const probe = document.createElement('div');
    probe.style.boxShadow = '0 0 0 2.5px var(--vd-played)';
    el.appendChild(probe);
    const want = getComputedStyle(probe).boxShadow.split(' 0px')[0];
    probe.remove();
    const got = getComputedStyle(el.querySelector('.vd-scene.on .vd-scene-card')).boxShadow.split(' 0px')[0];
    const px = (color) => {
      const c = document.createElement('canvas');
      c.width = c.height = 1;
      const g = c.getContext('2d');
      g.fillStyle = color;
      g.fillRect(0, 0, 1, 1);
      return Array.from(g.getImageData(0, 0, 1, 1).data);
    };
    return { want: px(want), got: px(got), text: [want, got] };
  });
  // The accent follows the picture, easing, and the ring eases after it,
  // so they can be a step apart.
  assert.ok(ring.got.every((v, i) => Math.abs(v - ring.want[i]) <= 24), 'ringed in the played color: ' + JSON.stringify(ring));
  // Each scene's picture: the video's own frame at its time.
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
  assert.equal(await p.$eval('#ss-stage .vd-scenes-glow', (c) => c.classList.contains('drawn')), true, 'the video\'s colors bleed in');
  assert.ok(Math.abs(s.t - 360) < 1.5 && s.paused, 'where the press put it, and paused: ' + s.t);
});

test('scrolling the strip moves the bar\'s mark, and scenes slide in', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const before = await panel(p);
  const view = await p.locator('#ss-stage .vd-scenes-view').boundingBox();
  await p.mouse.move(view.x + view.width / 2, view.y + view.height / 2, { steps: 2 });
  await p.mouse.wheel(0, 2000);
  await p.waitForFunction((s0) => document.querySelector('#ss-stage .vd-scenes-view').scrollLeft > s0 + 100, before.scroll);
  await p.waitForFunction((m) => document.querySelector('#ss-stage .vd-scan').style.left !== m, before.scan);
  const s = await panel(p);
  assert.equal(s.open, true, 'still up');
  assert.ok(parseFloat(s.scan) > parseFloat(before.scan), 'the mark moved on with it: ' + before.scan + ' to ' + s.scan);
  assert.ok(await p.locator('#ss-stage .vd-scene[aria-label^="11:00"]').count(), 'the last scene is in the page');
  await p.waitForFunction(() => {
    const el = document.querySelector('#ss-stage .vd-scenes');
    return el.querySelectorAll('.vd-scene').length === el.querySelectorAll('.vd-scene.ready').length;
  }, null, { timeout: 20000 });
});

test('a scene clicked grows over the video, which goes there and plays', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const box = await p.locator('#ss-stage .vd-scene[aria-label^="9:00"]').boundingBox();
  await p.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 2 });
  await p.mouse.down();
  await p.mouse.up();
  assert.equal((await panel(p)).zoom, true, 'the scene, growing');
  await p.waitForFunction(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return !document.querySelector('#ss-stage .vd-zoom') && !v.paused && v.currentTime >= 540 && v.currentTime < 545;
  }, null, { timeout: 10000 });
  await gone(p);
  const s = await panel(p);
  assert.equal(s.scan, null, 'the mark goes with it');
  assert.equal(s.dialog, true);
  await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').pause());
});

test('let go before it opens and it springs back into the bar, the press a scrub', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.mouse.move(640, 100);
  const { tl } = await pullAt(p, 0.25, 50);
  assert.equal((await panel(p)).hidden, false);
  await p.mouse.move(tl.x + tl.width * 0.25, tl.y - 50);
  await p.mouse.up();
  await gone(p);
  await p.waitForFunction(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return !v.seeking && Math.abs(v.currentTime - 180) < 2;
  });
  await p.mouse.move(640, 100);
});

test('swiped down, the strip folds back into the bar', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const { x, tl } = await pullAt(p, 0.5, 150);
  await p.mouse.up();
  await settled(p);
  const view = await p.locator('#ss-stage .vd-scenes-view').boundingBox();
  await p.mouse.move(view.x + 40, view.y + 10, { steps: 2 });
  await p.mouse.down();
  await p.mouse.move(view.x + 44, view.y + 80, { steps: 6 });
  await p.mouse.up();
  await gone(p);
  assert.equal((await panel(p)).dialog, true);
  void x;
  void tl;
});

test('S and Escape: up and away, and the slideshow stays open', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.mouse.move(640, 100);
  await p.focus('#ss');
  await p.keyboard.press('s');
  await p.waitForSelector('#ss-stage .vd-scenes.open');
  assert.equal((await panel(p)).lit, '6:00, The deep', 'opened by the key on the current time');
  await p.keyboard.press('ArrowLeft');
  assert.equal((await panel(p)).lit, '5:00, The deep');
  assert.ok(Math.abs(parseFloat((await panel(p)).scan) - 5 / 12 * 100) < 0.01, 'the mark follows the lit scene');
  await p.keyboard.press('Escape');
  await gone(p);
  assert.equal((await panel(p)).dialog, true, 'the slideshow is still open');
});

test('in full screen: the filmstrip stays put while it\'s up, and it stretches above the filmstrip', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.click('#ss-full');
  await p.waitForFunction(() => document.fullscreenElement === document.getElementById('ss-frame'));
  await p.waitForTimeout(500);
  const before = await panel(p);
  // Open: the bottom edge doesn't bring the filmstrip up under it.
  const { x, tl } = await pullAt(p, 0.5, 60);
  await p.mouse.move(x, tl.y - 160, { steps: 4 });
  await p.mouse.up();
  await settled(p);
  let s = await panel(p);
  assert.deepEqual(s.video, before.video, 'the video doesn\'t move');
  // The screen's very bottom edge (its size is the browser's own).
  const edge = await p.evaluate(() => Math.round(document.getElementById('ss-frame').getBoundingClientRect().bottom) - 2);
  await p.mouse.move(x, edge, { steps: 4 });
  await p.waitForTimeout(300);
  assert.equal((await panel(p)).stripOn, false, 'no filmstrip while it\'s up');
  await p.click('#ss-name');
  await gone(p);
  // The filmstrip up (from the bottom edge), the dock lifts above it,
  // and the strip with the dock.
  await p.mouse.move(x, edge - 40, { steps: 2 });
  await p.mouse.move(x, edge, { steps: 4 });
  await p.waitForFunction(() => document.getElementById('ss').classList.contains('strip-on'));
  await p.waitForTimeout(500);
  const band = await p.$eval('.ss-bottom', (e) => Math.round(e.getBoundingClientRect().top));
  // The keys from the timeline: in full screen the dialog itself is
  // outside what's shown, and can't take the focus.
  await p.focus('#ss-stage .vd-tl');
  await p.keyboard.press('s');
  await settled(p);
  s = await panel(p);
  assert.ok(s.bottom <= band + 1, 'above the filmstrip: ' + s.bottom + ' over ' + band);
  assert.equal(s.stripOn, true, 'and the filmstrip stays');
  await p.click('#ss-name');
  await gone(p);
  await p.evaluate(() => document.exitFullscreen());
  await p.waitForFunction(() => !document.fullscreenElement);
});

test('on a phone, a finger stretches the bar the same way, and a tap picks', async (t) => {
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
    // A finger on the timeline, dragged along it and then pulled up off it.
    await p.evaluate(() => {
      const tl = document.querySelector('#ss-stage .vd-tl'), r = tl.getBoundingClientRect();
      const at = (type, x, y) => tl.dispatchEvent(new PointerEvent(type, {
        bubbles: true, cancelable: true, pointerId: 7, pointerType: 'touch', isPrimary: true, clientX: x, clientY: y,
      }));
      const y = r.top + r.height / 2;
      at('pointerdown', r.left + r.width * 0.3, y);
      at('pointermove', r.left + r.width * 0.5, y);
      at('pointermove', r.left + r.width * 0.5, r.top - 60);
      at('pointermove', r.left + r.width * 0.5, r.top - 160);
      at('pointerup', r.left + r.width * 0.5, r.top - 160);
    });
    await p.waitForSelector('#ss-stage .vd-scenes.open');
    assert.equal((await panel(p)).lit, '6:00, The deep', 'where it was scrubbed to');
    const w = await p.$eval('#ss-stage .vd-scene', (e) => e.offsetWidth);
    assert.equal(Math.round(w), 112, 'the compact dock\'s smaller scenes');
    const scene = p.locator('#ss-stage .vd-scene[aria-label^="7:00"]');
    await scene.waitFor();
    await scene.tap();
    await p.waitForFunction(() => {
      const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
      return !document.querySelector('#ss-stage .vd-zoom') && v.currentTime >= 420 && v.currentTime < 425;
    }, null, { timeout: 10000 });
    await gone(p);
  } finally {
    await ctx.close();
  }
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
