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

// All of a video's metadata (metaview.js) on a real file: a synthetic
// MP4 with a timed JSON track of sensor-like readings, an AMF0 track,
// SEI JSON, mdta keys and XMP (internal/gowebserver/testing/media,
// made by sensorVideo). The info panel puts the readings' place on its
// map, and opens the viewer: the structure as a tree with its values
// drawn by what they are, and every raw key by source. With
// META_SCREENSHOTS set to a folder, it saves what it looks like there.

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { serve, open } = require('../../browser.js');

const MEDIA = path.join(__dirname, '..', '..', '..', '..', '..', 'internal', 'gowebserver', 'testing', 'media');
const NAME = 'sensor-data.mp4';
const SHOTS = process.env.META_SCREENSHOTS || '';

let srv, b;

async function shot(p, file) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  await p.waitForTimeout(500);
  await p.screenshot({ path: path.join(SHOTS, (b.name === 'chromium' ? '' : b.name + '-') + file + '.png') });
}

test.before(async () => {
  srv = await serve({ files: { [NAME]: fs.readFileSync(path.join(MEDIA, NAME)) } });
  b = await open({ viewport: { width: 1920, height: 1200 } });
  await b.page.addInitScript(() => localStorage.setItem('gows-slideshow-fullscreen', 'off'));
  await b.page.goto(srv.url);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

async function openInfo(p) {
  await p.locator('.tile[aria-label="' + NAME + '"]').click();
  await p.waitForSelector('dialog#ss[open]');
  await p.waitForTimeout(400);
  await p.keyboard.press('i');
  await p.waitForSelector('.ssi-all');
}

test('the panel stays as it was, with the readings\' place on its map, and a line for all of it', async () => {
  const p = b.page;
  await openInfo(p);
  await p.waitForSelector('.ssi-map.drawn');
  assert.match(await p.textContent('.ssi-where'), /Switzerland/);
  const facts = await p.$$eval('.ssi-body > .ssi-facts dt', (d) => d.map((x) => x.textContent));
  assert.deepEqual(facts.slice(-2), ['Name', 'Size']);
  assert.match(await p.textContent('.ssi-all'), /All metadata.*structured.*raw keys/);
  // The video still plays: the H.264 track the SEI is in is left out.
  const playable = await p.evaluate(() => {
    const v = document.querySelector('.vdock').parentNode.querySelector('video');
    return v.readyState >= 1 && v.videoWidth > 0;
  });
  assert.equal(playable, true);
  await shot(p, 'meta-panel');
  // One list, nothing folded: the details follow the facts.
  assert.equal(await p.locator('#ss-panel-body details').count(), 0);
  const groups = await p.$$eval('.ssi-body > .ssi-group h3', (h) => h.map((x) => x.textContent));
  assert.deepEqual(groups, ['Video', 'Audio', 'Data', 'Description', 'File']);
  await p.evaluate(() => { const b = document.getElementById('ss-panel-body'); b.scrollTop = b.scrollHeight; });
  await shot(p, 'meta-panel-scrolled');
  await p.evaluate(() => { document.getElementById('ss-panel-body').scrollTop = 0; });
});

test('the viewer: the structure as a tree, values drawn by what they are', async () => {
  const p = b.page;
  await p.click('.ssi-all');
  await p.waitForSelector('.mv:not([hidden])');
  const sheet = await p.locator('.mv-sheet').boundingBox();
  assert.ok(sheet.width > 1000 && sheet.height > 1000, 'room for it: ' + JSON.stringify(sheet));
  assert.equal(await p.getAttribute('.mv-tab[data-tab="structure"]', 'aria-selected'), 'true');
  const sources = await p.$$eval('.mv-struct .mv-src', (s) => s.map((x) => x.textContent));
  assert.ok(sources.some((s) => /\/mett$/.test(s)), sources.join(', '));
  assert.ok(sources.some((s) => /^SEI /.test(s)) && sources.some((s) => /XMP/.test(s)) && sources.some((s) => /\/amf0$/.test(s)));
  // The readings: a map, a thermometer and its sparkline, a bar, a date,
  // a swatch, a chip.
  const first = p.locator('.mv-struct').first();
  await p.waitForSelector('.mv-map.ready');
  for (const sel of ['.mv-map', '.mv-thermo', '.mv-spark', '.mv-bar', '.mv-when', '.mv-swatch', '.mv-chip']) {
    assert.ok(await first.locator(sel).count(), sel);
  }
  assert.match(await first.locator('header').textContent(), /messages/);
  await shot(p, 'meta-structure');
  // Every representation, close up: a hot and a cold thermometer among
  // them, and the picture an AMF0 message carries.
  const picks = {
    map: ['gps', 'summary'], 'thermometer-hot': ['temp_c'], 'thermometer-cold': ['outside_temp_c'], sparkline: ['temperature_c'],
    swatch: ['led'], bar: ['battery_pct'], image: ['snapshot'], chip: ['ok'], date: ['timestamp'], duration: ['uptime_s'],
  };
  const found = await p.evaluate((picks) => Object.keys(picks).filter((name) => {
    const [key, part] = picks[name];
    const k = Array.from(document.querySelectorAll('.mv-k')).find((e) => e.textContent === key);
    if (!k) return false;
    let el = k.closest('li');
    if (part) el = el.querySelector(part) || el;
    el.dataset.shot = name;
    return true;
  }), picks);
  assert.deepEqual(found, Object.keys(picks));
  const has = { map: '.mv-map', 'thermometer-hot': '.mv-thermo', 'thermometer-cold': '.mv-thermo', sparkline: '.mv-spark', swatch: '.mv-swatch', bar: '.mv-bar', image: '.mv-img', chip: '.mv-chip', date: '.mv-when', duration: '.mv-when' };
  for (const name of Object.keys(picks)) {
    assert.ok(await p.locator('[data-shot="' + name + '"] ' + has[name]).count(), name);
  }
  const colors = await p.$$eval('[data-shot^="thermometer"] .mv-thermo', (t) => t.map((x) => getComputedStyle(x.querySelector('i')).backgroundColor));
  assert.deepEqual(colors, ['rgb(239, 68, 68)', 'rgb(59, 130, 246)'], 'hot red, cold blue');
  if (SHOTS) {
    for (const name of Object.keys(picks)) {
      const el = p.locator('[data-shot="' + name + '"]');
      await el.scrollIntoViewIfNeeded();
      await p.waitForTimeout(150);
      await el.screenshot({ path: path.join(SHOTS, (b.name === 'chromium' ? '' : b.name + '-') + 'detector-' + name + '.png') });
    }
    await p.evaluate(() => { document.querySelector('.mv-body').scrollTop = 0; });
  }
  // Lists fold with their length, and open.
  const accel = first.locator('.mv-node details').filter({ hasText: 'accel' }).first();
  assert.equal(await accel.getAttribute('open'), null);
  await accel.locator('summary').click();
  assert.equal(await accel.getAttribute('open'), '');
  // The SEI's compressed settings, unpacked.
  await p.fill('.mv-find', 'fov_deg');
  await p.waitForTimeout(200);
  const shown = await p.$$eval('.mv-struct:not([hidden]) .mv-src', (s) => s.map((x) => x.textContent));
  assert.deepEqual(shown.map((s) => /^SEI /.test(s)), [true]);
  await shot(p, 'meta-structure-filtered');
  await p.fill('.mv-find', '');
});

test('the viewer: every raw key by source, copyable, filtered', async () => {
  const p = b.page;
  await p.click('.mv-tab[data-tab="raw"]');
  const kinds = await p.$$eval('.mv-source', (s) => s.map((x) => x.getAttribute('data-source')));
  assert.deepEqual(kinds, ['boxes', 'timed', 'sei', 'xmp', 'tags']);
  assert.ok(await p.locator('.mv-path', { hasText: 'moov/meta/keys' }).count(), 'the mdta keys');
  await shot(p, 'meta-raw');
  await p.fill('.mv-find', 'temperature');
  await p.waitForTimeout(200);
  const rows = await p.$$eval('.mv-row:not([hidden]) dt', (d) => d.map((x) => x.textContent));
  assert.ok(rows.length >= 1 && rows.every((r) => /temperature/.test(r)), rows.join(', '));
  assert.equal(await p.locator('.mv-row:not([hidden]) .mv-copy').count(), rows.length, 'each value copyable');
  // A long value opens.
  const long = p.locator('.mv-row .mv-long').first();
  if (await long.count()) {
    const before = (await long.boundingBox()).height;
    await long.locator('xpath=..').locator('.mv-fold').click();
    assert.ok((await long.boundingBox()).height >= before);
  }
  await shot(p, 'meta-raw-filtered');
  await p.fill('.mv-find', '');
  // Escape closes the viewer, then the panel.
  await p.keyboard.press('Escape');
  await p.waitForSelector('.mv', { state: 'hidden' });
  assert.equal(await p.evaluate(() => document.getElementById('ss').classList.contains('info-on')), true);
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => !document.getElementById('ss').classList.contains('info-on'));
  await p.click('#ss-close');
  await p.waitForFunction(() => !document.getElementById('ss').open);
});

test('on a phone, the viewer takes the screen', async () => {
  const phone = await b.browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: b.name === 'chromium' });
  const p = await phone.newPage();
  await p.addInitScript(() => localStorage.setItem('gows-slideshow-fullscreen', 'off'));
  try {
    await p.goto(srv.url);
    await p.locator('.tile[aria-label="' + NAME + '"]').click();
    await p.waitForSelector('dialog#ss[open]');
    await p.waitForTimeout(400);
    await p.click('#ss-info');
    await p.waitForSelector('.ssi-all');
    await p.waitForTimeout(500);
    await shot(p, 'meta-phone-panel');
    await p.evaluate(() => document.querySelector('.ssi-all').click());
    await p.waitForSelector('.mv:not([hidden])');
    await p.waitForTimeout(500);
    const sheet = await p.locator('.mv-sheet').boundingBox();
    assert.ok(sheet.width >= 389 && sheet.x <= 1, JSON.stringify(sheet));
    await shot(p, 'meta-phone-structure');
    await p.click('.mv-tab[data-tab="raw"]');
    await shot(p, 'meta-phone-raw');
  } finally {
    await phone.close();
  }
});
