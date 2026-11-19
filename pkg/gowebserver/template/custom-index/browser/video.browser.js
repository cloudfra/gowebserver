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

test('in full screen the filmstrip comes up under a video, not over its scrubber', async () => {
  const p = b.page;
  await p.locator('.tile[aria-label="clip-1.webm"]').click();
  await p.waitForSelector('dialog#ss[open]');
  await p.click('#ss-full');
  await p.waitForFunction(() => document.fullscreenElement === document.getElementById('ss-frame'));
  // The current slide's video, once it knows its size and is laid out
  // for full screen.
  const geometry = () => p.evaluate(() => {
    const v = Array.from(document.querySelectorAll('#ss-stage video')).find((el) => {
      const r = el.getBoundingClientRect();
      return el.videoWidth && r.right > 40 && r.left < innerWidth - 40;
    });
    if (!v) return null;
    const r = v.getBoundingClientRect(), s = document.getElementById('ss-strip').getBoundingClientRect();
    return { videoBottom: r.bottom, stripTop: s.top, x: r.left + r.width / 2 };
  });
  await p.waitForFunction(() => document.querySelector('#ss-stage .slide').style.paddingBottom === '77px');
  await p.waitForFunction(() => Array.from(document.querySelectorAll('#ss-stage video')).some((v) => v.videoWidth));
  const g = await geometry();
  assert.ok(g, 'the video is on screen');
  assert.ok(g.videoBottom <= g.stripTop + 1, 'the video ends above the strip: ' + JSON.stringify(g));

  // With the strip up, the video's bottom edge, where its scrubber is,
  // is still the video's.
  await p.evaluate(() => document.getElementById('ss').classList.add('strip-on'));
  await p.waitForTimeout(500); // past the fade
  const hit = await p.evaluate(({ x, y }) => document.elementFromPoint(x, y).tagName, { x: g.x, y: g.videoBottom - 15 });
  assert.equal(hit, 'VIDEO');
  await p.evaluate(() => document.getElementById('ss').classList.remove('strip-on'));
  await p.keyboard.press('Escape');
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
