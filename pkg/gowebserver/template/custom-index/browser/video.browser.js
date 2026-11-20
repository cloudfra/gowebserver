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

// The current slide's video, the strip, and whether it is up, as laid
// out on screen.
const layout = (p) => p.evaluate(() => {
  const v = Array.from(document.querySelectorAll('#ss-stage video')).find((el) => {
    const r = el.getBoundingClientRect();
    return el.videoWidth && r.right > 40 && r.left < innerWidth - 40;
  });
  if (!v) return null;
  const r = v.getBoundingClientRect(), s = document.getElementById('ss-strip').getBoundingClientRect();
  return {
    top: r.top, bottom: r.bottom, x: r.left + r.width / 2, stripTop: s.top, vh: innerHeight,
    on: document.getElementById('ss').classList.contains('strip-on'),
    opacity: getComputedStyle(document.querySelector('.ss-bottom')).opacity,
  };
});

// Moves the real mouse to y, from a little above, and waits out the
// strip's fades and timers.
async function mouseTo(p, x, y) {
  await p.mouse.move(x, y - 40);
  await p.mouse.move(x, y, { steps: 4 });
  await p.waitForTimeout(800);
  return layout(p);
}

// The mouse on the scrubber leaves the strip down; below the video it
// comes up, under the video, and the scrubber is still the video's.
async function clearOfScrubber(p, mode) {
  let g = await layout(p);
  assert.ok(g, mode + ': the video is on screen');
  assert.ok(g.top >= -1 && g.bottom <= g.stripTop + 1, mode + ': the video ends above the strip: ' + JSON.stringify(g));
  g = await mouseTo(p, g.x, g.bottom - 15);
  assert.equal(g.on, false, mode + ': the mouse on the scrubber leaves the strip down');
  assert.equal(g.opacity, '0', mode);
  g = await mouseTo(p, g.x, (g.bottom + g.vh) / 2);
  assert.equal(g.on, true, mode + ': below the video it comes up');
  assert.equal(g.opacity, '1', mode);
  assert.ok(g.bottom <= g.stripTop + 1, mode + ': and stays under the video: ' + JSON.stringify(g));
  const hit = await p.evaluate(({ x, y }) => document.elementFromPoint(x, y).tagName, { x: g.x, y: g.bottom - 15 });
  assert.equal(hit, 'VIDEO', mode + ': the scrubber is still the video\'s');
  await mouseTo(p, g.x, g.vh / 2);
}

test('in full screen the filmstrip comes up under a video, not over its scrubber', async () => {
  const p = b.page;
  await p.locator('.tile[aria-label="clip-1.webm"]').click();
  await p.waitForSelector('dialog#ss[open]');
  await p.click('#ss-full');
  await p.waitForFunction(() => document.fullscreenElement === document.getElementById('ss-frame'));
  await p.waitForFunction(() => Array.from(document.querySelectorAll('#ss-stage video')).some((v) => v.videoWidth));
  await p.waitForTimeout(300);
  await clearOfScrubber(p, 'fitted');
  // Filling the screen: the clip has the screen's shape, so covering it
  // would put the scrubber back in the strip's band.
  await p.click('#ss-full');
  await p.waitForFunction(() => document.getElementById('ss-full').getAttribute('aria-label') === 'Exit full screen');
  await p.waitForTimeout(300);
  await clearOfScrubber(p, 'filled');
  await p.keyboard.press('Escape');
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
