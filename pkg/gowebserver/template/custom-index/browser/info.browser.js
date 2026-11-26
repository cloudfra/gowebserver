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

// The slideshow's info panel (info.js) on real files: a photo with its
// EXIF, GPS, XMP and ICC profile, and a video with lyrics and a location
// in its tags (internal/gowebserver/testing/media). With INFO_SCREENSHOTS
// set to a folder, it saves what it looks like there.

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { serve, open, png } = require('../../browser.js');

const MEDIA = path.join(__dirname, '..', '..', '..', '..', '..', 'internal', 'gowebserver', 'testing', 'media');
const SHOTS = process.env.INFO_SCREENSHOTS || '';

let srv, b;
// Every request the page made, by origin.
const origins = new Set();

async function shot(p, name) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  await p.waitForTimeout(500);
  await p.screenshot({ path: path.join(SHOTS, (b.name === 'chromium' ? '' : b.name + '-') + name + '.png') });
}

test.before(async () => {
  srv = await serve({
    files: {
      'eiffel-tower.jpg': fs.readFileSync(path.join(MEDIA, 'eiffel-tower.jpg')),
      'night-walk.mp4': fs.readFileSync(path.join(MEDIA, 'night-walk.mp4')),
      'plain.png': png(640, 480, 40, 90, 160),
    },
  });
  b = await open({ viewport: { width: 1440, height: 900 } });
  b.page.on('request', (r) => origins.add(new URL(r.url()).origin));
  await b.page.goto(srv.url);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

// Where things are on screen.
const rect = (p, sel) => p.evaluate((s) => {
  const el = document.querySelector(s);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
}, sel);

// The current slide's media.
const media = (p) => p.evaluate(() => {
  const el = Array.from(document.querySelectorAll('#ss-stage img, #ss-stage video')).find((m) => {
    const r = m.getBoundingClientRect();
    return r.width && r.right > 0 && r.left < innerWidth;
  });
  if (!el) return null;
  const r = el.getBoundingClientRect();
  return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width };
});

async function openTile(p, name) {
  await p.locator('.tile[aria-label="' + name + '"]').click();
  await p.waitForSelector('dialog#ss[open]');
  await p.waitForTimeout(500);
}

async function closeSlideshow(p) {
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => !document.getElementById('ss').open);
  await p.waitForTimeout(300);
}

test('a photo\'s place on the map, its date, name and size, with the rest folded away', async () => {
  const p = b.page;
  await openTile(p, 'eiffel-tower.jpg');
  const before = await media(p);
  await p.keyboard.press('i');
  await p.waitForSelector('.ssi-map.drawn');
  await p.waitForTimeout(600);

  assert.equal(await p.getAttribute('#ss-info', 'aria-expanded'), 'true');
  assert.equal(await p.textContent('.ssi-where'), 'Eiffel Tower, Paris, France');
  const facts = await p.$$eval('.ssi-body > .ssi-facts dt', (d) => d.map((x) => x.textContent));
  assert.deepEqual(facts, ['Taken', 'Name', 'Size']);
  assert.match(await p.textContent('.ssi-body > .ssi-facts'), /UTC\+2/);
  assert.equal(await p.locator('#ss-panel-body details').count(), 0, 'nothing folded');

  // Docked, the photo moves over to make room: the panel covers none of it.
  const panel = await rect(p, '#ss-panel'), after = await media(p);
  assert.ok(after.right <= panel.left + 1, 'the photo ends left of the panel: ' + JSON.stringify({ after, panel }));
  assert.ok(after.width <= before.width + 1, 'and is no bigger');
  // The pin is on the map, the map in the panel.
  const pin = await rect(p, '.ssi-pin'), map = await rect(p, '.ssi-map');
  assert.ok(pin.left > map.left && pin.right < map.right && pin.bottom > map.top && pin.bottom <= map.bottom + 1);
  await shot(p, 'info-photo');

  const groups = await p.$$eval('.ssi-group h3', (h) => h.map((x) => x.textContent));
  assert.deepEqual(groups, ['Camera', 'Exposure', 'Image', 'Description', 'File']);
  assert.match(await p.textContent('.ssi-body'), /Display P3/);
  await p.waitForTimeout(400);
  // Scrolled within the panel, not the page.
  await p.evaluate(() => { const b = document.getElementById('ss-panel-body'); b.scrollTop = document.querySelector('.ssi-group').offsetTop - b.offsetTop - 8; });
  await shot(p, 'info-details');

  // Escape closes the panel, and only then the slideshow.
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => !document.getElementById('ss').classList.contains('info-on'));
  assert.equal(await p.evaluate(() => document.getElementById('ss').open), true);
  await p.waitForTimeout(500);
  const back = await media(p);
  assert.ok(Math.abs(back.width - before.width) < 2, 'the photo takes its room back');
  await closeSlideshow(p);
});

test('the map comes from the server: no request leaves it', async () => {
  const mine = new URL(srv.url).origin;
  assert.deepEqual(Array.from(origins).filter((o) => o !== mine && !o.startsWith('data:')), []);
});

test('in full screen the panel is over the stage, beside the photo', async () => {
  const p = b.page;
  await openTile(p, 'eiffel-tower.jpg');
  await p.click('#ss-full');
  await p.waitForFunction(() => document.fullscreenElement === document.getElementById('ss-frame'));
  await p.waitForTimeout(400);
  await p.click('#ss-info');
  await p.waitForSelector('.ssi-map.drawn');
  await p.waitForTimeout(600);
  const panel = await rect(p, '#ss-panel'), m = await media(p);
  assert.ok(panel.width > 300 && panel.right <= 1440);
  assert.ok(m.right <= panel.left + 1, 'the photo is beside it');
  const hit = await p.evaluate(({ x, y }) => !!document.elementFromPoint(x, y).closest('#ss-panel'), { x: panel.left + 40, y: panel.top + 80 });
  assert.equal(hit, true, 'the panel is on top, in the full-screen frame');
  await shot(p, 'info-fullscreen');
  await p.click('#ss-panel-close');
  await p.evaluate(() => document.exitFullscreen());
  await p.waitForFunction(() => !document.fullscreenElement);
  await closeSlideshow(p);
});

test('a video\'s lyrics, and its dock still answers beside the panel', async () => {
  const p = b.page;
  await openTile(p, 'night-walk.mp4');
  await p.keyboard.press('i');
  await p.waitForSelector('.ssi-prose');
  await p.waitForSelector('.ssi-map.drawn');
  assert.match(await p.textContent('.ssi-text h3'), /Lyrics/);
  assert.match(await p.textContent('.ssi-prose'), /Neon on the water/);
  assert.match(await p.textContent('.ssi-where'), /Japan/);
  assert.match(await p.textContent('.ssi-facts'), /Recorded/);
  // The dock's play button is the dock's, not under the panel.
  await p.mouse.move(500, 450);
  await p.waitForTimeout(300);
  const play = await rect(p, '.vdock .vd-play');
  assert.ok(play && play.width, 'the dock is there');
  const panel = await rect(p, '#ss-panel');
  assert.ok(play.right <= panel.left, 'the dock is clear of the panel');
  const hit = await p.evaluate(({ x, y }) => !!document.elementFromPoint(x, y).closest('.vdock'), { x: play.left + play.width / 2, y: play.top + play.height / 2 });
  assert.equal(hit, true);
  await shot(p, 'info-video-lyrics');
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => !document.getElementById('ss').classList.contains('info-on'));
  await p.waitForTimeout(300);
  await closeSlideshow(p);
});

test('on a phone the panel is a sheet over the bottom of the screen', async () => {
  const phone = await b.browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: b.name === 'chromium' });
  const p = await phone.newPage();
  // A phone opens its gallery full screen, which headless Firefox makes
  // its desktop-sized screen: this one stays in the window, the phone's.
  await p.addInitScript(() => localStorage.setItem('gows-slideshow-fullscreen', 'off'));
  try {
    await p.goto(srv.url);
    await p.locator('.tile[aria-label="eiffel-tower.jpg"]').click();
    await p.waitForSelector('dialog#ss[open]');
    await p.waitForTimeout(500);
    await p.click('#ss-info');
    await p.waitForSelector('.ssi-map.drawn');
    await p.waitForTimeout(600);
    assert.equal(await p.evaluate(() => document.getElementById('ss').classList.contains('info-sheet')), true);
    const panel = await rect(p, '#ss-panel');
    assert.ok(Math.abs(panel.bottom - 844) < 2 && panel.left === 0 && Math.abs(panel.width - 390) < 2, JSON.stringify(panel));
    assert.ok(panel.height < 844 * 0.7);
    // The photo moves up, above the sheet.
    await p.waitForTimeout(400);
    const m = await media(p);
    assert.ok(m && m.bottom <= panel.top + 1, 'the photo is above the sheet: ' + JSON.stringify({ m, panel }));
    await shot(p, 'info-phone');
  } finally {
    await phone.close();
  }
});
