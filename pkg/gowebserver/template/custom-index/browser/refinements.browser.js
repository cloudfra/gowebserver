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

// The video dock's finer points, in a real browser: one shadow from the
// bar; the preview a card through the bar, its picture where its scene
// in the strip will be, so pulling up doesn't move it; pulling back down
// while still held puts the strip away; a scene hovered glows and stays
// put; the speed button keeps its size; the volume stands up.

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { serve, open, longVideo } = require('../../browser.js');

let srv, b, video = null;

test.before(async () => {
  b = await open({ viewport: { width: 1280, height: 800 } });
  srv = await serve({ files: {} });
  await b.page.goto(srv.url);
  video = await longVideo(b.page, 6 * 60, 5, 320, 180);
  if (!video) return;
  fs.writeFileSync(path.join(srv.dir, 'clip.webm'), video);
  await b.page.goto('about:blank');
  await b.page.goto(srv.url + '#v=clip.webm&t=0:30');
  await b.page.waitForSelector('dialog#ss[open] #ss-stage .vdock:not([hidden])');
  await b.page.waitForFunction(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').readyState >= 2);
  await b.page.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').pause());
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const rect = (p, sel) => p.$eval(sel, (e) => {
  const r = e.getBoundingClientRect();
  return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height };
});

test('one shadow, from the bar across the whole dock, none behind each control', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const s = await b.page.evaluate(() => {
    const dock = document.querySelector('#ss-stage .vdock');
    const panel = getComputedStyle(dock.querySelector('.vd-panel'), '::before');
    const p = dock.querySelector('.vd-panel').getBoundingClientRect(), tl = dock.querySelector('.vd-tl').getBoundingClientRect();
    return {
      panel: panel.content, image: panel.backgroundImage, top: p.top + parseFloat(panel.top), tlBottom: tl.bottom, tlTop: tl.top,
      clusters: Array.from(dock.querySelectorAll('.vd-cluster')).map((c) => getComputedStyle(c, '::before').content),
      bar: getComputedStyle(dock.querySelector('.vd-tl'), '::before').content,
    };
  });
  assert.notEqual(s.panel, 'none', 'the dock casts one');
  assert.match(s.image, /linear-gradient/);
  assert.ok(s.top <= s.tlBottom, 'starting at the bar');
  assert.deepEqual(s.clusters, ['none', 'none'], 'none behind the controls');
  assert.equal(s.bar, 'none', 'nor a band of its own behind the bar');
});

test('the preview is a card through the bar: its picture where its scene will be, no time under the bar', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const tl = await rect(p, '#ss-stage .vd-tl');
  const x = tl.left + tl.width * 0.5, y = tl.top + tl.height / 2;
  await p.mouse.move(x - 30, y);
  await p.mouse.move(x, y, { steps: 3 });
  await p.waitForFunction(() => document.querySelector('#ss-stage .vdock').dataset.preview === 'frame', null, { timeout: 15000 });
  await p.waitForTimeout(400);
  const frame = await rect(p, '#ss-stage .vd-pop-frame'), card = await rect(p, '#ss-stage .vd-pop');
  const colors = await p.evaluate(() => ['.vd-pop', '.vd-pop-text'].map((c) => getComputedStyle(document.querySelector('#ss-stage ' + c)).backgroundColor));
  assert.deepEqual([Math.round(frame.width), Math.round(frame.height)], [160, 90], 'a scene\'s size');
  assert.ok(Math.abs(frame.bottom - (tl.top - 6)) <= 1, 'its picture 6px over the bar: ' + frame.bottom + ' / ' + tl.top);
  // The time is at the bottom left already; with no chapter here, nothing
  // under the bar but the card's edge.
  assert.equal(await p.$eval('#ss-stage .vd-pop-text', (e) => e.hidden), true, 'no bubble without a chapter');
  assert.equal(await p.$eval('#ss-stage .vd-pop-time', (e) => getComputedStyle(e).display), 'none', 'never the time');
  assert.ok(card.bottom <= tl.bottom + 8, 'the card ends just under the bar: ' + card.bottom + ' / ' + tl.bottom);
  assert.equal(colors[0], colors[1], 'card and bubble one solid gray');
  // Taken and pulled up: the strip grows around it, its scene where the
  // picture was.
  await p.mouse.down();
  await p.mouse.move(x, tl.top - 12 - 20, { steps: 6 });
  const lit = await p.evaluate(() => {
    const f = document.querySelector('#ss-stage .vd-scene.on .vd-scene-card').getBoundingClientRect();
    return { left: f.left, top: f.top, width: f.width, height: f.height, p: document.querySelector('#ss-stage .vd-scene.on').style.getPropertyValue('--p') };
  });
  assert.equal(lit.p, '1', 'up from the first');
  assert.ok(Math.abs(lit.left - frame.left) <= 1.5 && Math.abs(lit.top - frame.top) <= 1.5 && Math.abs(lit.width - frame.width) <= 1,
    'where the preview was, its size: ' + JSON.stringify([lit, frame]));
  // Open, and still held: back down to the bar, and it goes.
  await p.mouse.move(x, tl.top - 150, { steps: 4 });
  await p.waitForSelector('#ss-stage .vd-scenes.open');
  await p.mouse.move(x, y, { steps: 6 });
  await p.waitForFunction(() => !document.querySelector('#ss-stage .vd-scenes').classList.contains('open'));
  await p.mouse.up();
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').hidden);
});

test('a scene hovered glows in the accent and stays put', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  await p.mouse.move(640, 100);
  await p.focus('#ss-stage .vd-tl');
  await p.keyboard.press('s');
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').classList.contains('settled'));
  // The lit scene's neighbor, in view.
  const sel = await p.evaluate(() => {
    const i = document.querySelector('#ss-stage .vd-scene.on')._i;
    return '#vd-scene-' + (document.getElementById('vd-scene-' + (i + 1)) ? i + 1 : i - 1) + ' .vd-scene-card';
  });
  const before = await rect(p, sel);
  await p.mouse.move(before.left + before.width / 2, before.top + before.height / 2, { steps: 3 });
  await p.waitForTimeout(400);
  const after = await rect(p, sel);
  assert.deepEqual(after, before, 'not moved, not grown');
  const glow = await p.$eval(sel, (e) => getComputedStyle(e).boxShadow);
  assert.match(glow, /0px 0px 18px 3px/, 'a soft glow: ' + glow);
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-scenes').hidden);
});

test('the speed keeps its size highlighted, and the volume stands up over its button', async (t) => {
  if (!video) return t.skip('this browser can\'t encode the video');
  const p = b.page;
  const before = await rect(p, '#ss-stage .vd-rate-text');
  const rate = await rect(p, '#ss-stage .vd-rate');
  await p.mouse.move(rate.left + rate.width / 2, rate.top + rate.height / 2, { steps: 3 });
  await p.waitForTimeout(400);
  assert.deepEqual(await rect(p, '#ss-stage .vd-rate-text'), before, 'the same size, in the same place');
  const mute = await rect(p, '#ss-stage .vd-mute');
  await p.mouse.move(mute.left + mute.width / 2, mute.top + mute.height / 2, { steps: 3 });
  await p.waitForTimeout(400);
  const slider = await rect(p, '#ss-stage .vd-volume');
  assert.ok(slider.height > slider.width * 2, 'upright: ' + JSON.stringify(slider));
  assert.ok(slider.bottom <= mute.top, 'over the button');
  // Dragged up, louder; down, quieter.
  await p.mouse.move(slider.left + slider.width / 2, slider.top + slider.height / 2, { steps: 4 });
  await p.mouse.down();
  await p.mouse.move(slider.left + slider.width / 2, slider.bottom - 4, { steps: 4 });
  await p.mouse.up();
  const low = await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').volume);
  assert.ok(low < 0.2, 'down is quieter: ' + low);
  await p.mouse.down();
  await p.mouse.move(slider.left + slider.width / 2, slider.top + 4, { steps: 4 });
  await p.mouse.up();
  const high = await p.evaluate(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').volume);
  assert.ok(high > 0.8, 'up is louder: ' + high);
  await p.mouse.move(640, 100);
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
