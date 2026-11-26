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
const { serve, open } = require('../../browser.js');

let srv, b;

// A second of WebM, recorded from a canvas by the browser itself, so the
// test needs no ffmpeg and plays in every browser it runs in.
async function recordVideo(page) {
  const bytes = await page.evaluate(async () => {
    const c = document.createElement('canvas');
    c.width = 640;
    c.height = 360;
    const g = c.getContext('2d');
    const rec = new MediaRecorder(c.captureStream(30), { mimeType: 'video/webm' });
    const chunks = [];
    rec.ondataavailable = (e) => chunks.push(e.data);
    const stopped = new Promise((r) => { rec.onstop = r; });
    rec.start();
    const t0 = performance.now();
    await new Promise((done) => {
      (function draw() {
        const t = performance.now() - t0;
        g.fillStyle = 'hsl(' + (t / 5) + ',70%,50%)';
        g.fillRect(0, 0, c.width, c.height);
        if (t < 1000) requestAnimationFrame(draw); else done();
      })();
    });
    rec.stop();
    await stopped;
    return Array.from(new Uint8Array(await new Blob(chunks).arrayBuffer()));
  });
  return Buffer.from(bytes);
}

test.before(async () => {
  b = await open({ viewport: { width: 1280, height: 720 } });
  await b.page.goto('about:blank');
  const video = await recordVideo(b.page);
  const files = {};
  for (let i = 0; i < 4; i++) files['clip-' + i + '.webm'] = video;
  srv = await serve({ files });
  await b.page.goto(srv.url);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

// The current slide's video, the strip, the dock's timeline and
// buttons, and whether the strip is up, as laid out on screen.
const layout = (p) => p.evaluate(() => {
  const v = Array.from(document.querySelectorAll('#ss-stage video')).find((el) => {
    const r = el.getBoundingClientRect();
    return el.videoWidth && r.right > 40 && r.left < innerWidth - 40;
  });
  if (!v) return null;
  const r = v.getBoundingClientRect(), s = document.getElementById('ss-strip').getBoundingClientRect();
  const tl = document.querySelector('.vd-tl').getBoundingClientRect();
  const row = document.querySelector('.vd-row').getBoundingClientRect();
  const lc = document.querySelector('.vd-left').getBoundingClientRect(), rc = document.querySelector('.vd-right').getBoundingClientRect();
  return {
    left: r.left, top: r.top, right: r.right, bottom: r.bottom, x: r.left + r.width / 2,
    stripTop: s.top, stripBottom: s.bottom, stripLeft: s.left, stripRight: s.right, tlTop: tl.top, tlBottom: tl.bottom,
    rowTop: row.top, rowBottom: row.bottom, leftEnd: lc.right, rightStart: rc.left,
    inline: document.getElementById('ss').classList.contains('strip-inline'),
    vw: innerWidth, vh: innerHeight,
    on: document.getElementById('ss').classList.contains('strip-on'),
    opacity: getComputedStyle(document.querySelector('.ss-bottom')).opacity,
  };
});

// Moves the real mouse to y, from a little above, and waits out the
// strip's fades and timers, and the dock's move.
async function mouseTo(p, x, y) {
  await p.mouse.move(x, y - 40);
  await p.mouse.move(x, y, { steps: 4 });
  await p.waitForTimeout(800);
  return layout(p);
}

// The video covers the screen. The strip is in the dock's own row,
// between the play button and time and the controls at the right; the
// screen's bottom edge doesn't bring it up from there, and nothing moves
// the dock or its bar up.
async function coversScreen(p, mode) {
  let g = await layout(p);
  assert.ok(g, mode + ': the video is on screen');
  assert.ok(g.left <= 0.5 && g.top <= 0.5 && g.right >= g.vw - 0.5 && g.bottom >= g.vh - 0.5,
    mode + ': the video covers the screen: ' + JSON.stringify(g));
  g = await mouseTo(p, g.vw - 60, (g.rowTop + g.rowBottom) / 2);
  assert.equal(g.inline, true, mode + ': the strip is in the row');
  assert.equal(g.opacity, '1', mode + ': there with the dock');
  assert.ok(g.stripTop >= g.rowTop - 4 && g.stripBottom <= g.rowBottom + 4 && g.stripLeft >= g.leftEnd && g.stripRight <= g.rightStart,
    mode + ': between the time and the controls: ' + JSON.stringify(g));
  const tlTop = g.tlTop;
  g = await mouseTo(p, g.x, g.vh - 1);
  assert.equal(g.on, false, mode + ': the bottom edge doesn\'t bring it up from there');
  assert.ok(Math.abs(g.tlTop - tlTop) < 1, mode + ': and the bar stays where it was: ' + JSON.stringify(g));
  const hit = await p.evaluate(() => {
    const b = document.querySelector('.vd-play').getBoundingClientRect();
    return !!document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2).closest('.vd-play');
  });
  assert.equal(hit, true, mode + ': the dock\'s buttons are still there to press');
  await mouseTo(p, g.x, g.vh / 2);
}

test('in full screen a video covers the screen, and the filmstrip sits in the dock\'s row', async () => {
  const p = b.page;
  await p.locator('.tile[aria-label="clip-1.webm"]').click();
  await p.waitForSelector('dialog#ss[open]');
  await p.click('#ss-full');
  await p.waitForFunction(() => document.fullscreenElement === document.getElementById('ss-frame'));
  await p.waitForFunction(() => Array.from(document.querySelectorAll('#ss-stage video')).some((v) => v.videoWidth));
  await p.waitForTimeout(300);
  // The page's scrollbar gutter is no stripe down the screen's edge.
  const frame = await p.evaluate(() => document.getElementById('ss-frame').getBoundingClientRect().width);
  assert.equal(frame, await p.evaluate(() => innerWidth), 'the frame is the whole screen wide');
  // The clip is half the screen's size: fitted, it still fills it.
  await coversScreen(p, 'fitted');
  await p.click('#ss-full');
  await p.waitForFunction(() => document.getElementById('ss-full').getAttribute('aria-label') === 'Exit full screen');
  await p.waitForTimeout(300);
  await coversScreen(p, 'filled');
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => !document.fullscreenElement);
  // Some browsers pass the key on to the slideshow too, which closes.
  await p.waitForTimeout(600);
});

test('the accent follows the video\'s picture as it plays', async () => {
  const p = b.page;
  if (!(await p.evaluate(() => document.getElementById('ss').classList.contains('open')))) {
    await p.locator('.tile[aria-label="clip-1.webm"]').click();
    await p.waitForSelector('dialog#ss[open]');
  }
  // The clip's hue turns as it plays: red at its start, green-blue
  // near its end. The current slide's video is the one on screen; its
  // neighbors are loaded too.
  const onScreen = () => Array.from(document.querySelectorAll('#ss-stage video')).find((x) => {
    const r = x.getBoundingClientRect();
    return x.readyState >= 2 && r.right > 40 && r.left < innerWidth - 40;
  });
  const seek = (t) => p.evaluate(({ t, find }) => new Promise((done) => {
    const v = (0, eval)('(' + find + ')')();
    v.pause();
    v.addEventListener('seeked', done, { once: true });
    v.currentTime = t;
  }), { t, find: onScreen.toString() });
  const hue = (from) => p.waitForFunction((from) => {
    const h = parseFloat(document.documentElement.style.getPropertyValue('--amb-h'));
    return document.documentElement.classList.contains('amb-accent') && Math.abs(h - from) > 40 && h;
  }, from, { timeout: 5000 }).then((r) => r.jsonValue());
  await p.waitForFunction('!!(' + onScreen.toString() + ')()');
  await seek(0.8);
  await p.waitForTimeout(300);
  const late = await p.evaluate(() => parseFloat(document.documentElement.style.getPropertyValue('--amb-h')));
  await seek(0.02);
  const early = await hue(late);
  await seek(0.8);
  await hue(early);
  await p.keyboard.press('Escape');
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
