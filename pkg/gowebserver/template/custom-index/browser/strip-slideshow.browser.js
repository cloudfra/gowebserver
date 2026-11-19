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

// A folder row's slideshow over its strip (folders.js), opened by resting
// the pointer on the row.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { serve, open, png } = require('../../browser.js');

let srv, b;

test.before(async () => {
  const files = { 'Solo/only.png': png(64, 64, 200, 40, 40) };
  for (let i = 0; i < 14; i++) files['Trip/p' + String(i).padStart(2, '0') + '.png'] = png(96, 96, i * 47, i * 83, i * 29);
  srv = await serve({ files });
  b = await open();
  await b.page.goto(srv.url);
  await b.page.waitForFunction(() => document.querySelectorAll('.row.has-strip').length === 2);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const row = (name) => b.page.locator('.row', { has: b.page.locator('.name', { hasText: name }) });
const shift = () => row('Trip').locator('.strip-track').evaluate((el) => getComputedStyle(el).transform);

test('resting on a folder plays its photos over its strip, in the strip\'s box', async () => {
  const strip = await row('Trip').locator('.row-strip').boundingBox();
  const height = (await row('Trip').boundingBox()).height;
  await row('Trip').locator('.name').hover();
  await b.page.waitForSelector('.strip-show.hp-open');
  const panel = await row('Trip').locator('.strip-show').boundingBox();
  for (const k of ['x', 'y', 'width', 'height']) assert.ok(Math.abs(panel[k] - strip[k]) < 1, k + ': ' + panel[k] + ' vs ' + strip[k]);
  assert.equal((await row('Trip').boundingBox()).height, height, 'the row keeps its height');
  assert.equal(await row('Trip').locator('.strip-show button, .strip-show a').count(), 0, 'no controls');
  const first = await shift();
  await b.page.waitForFunction((was) => getComputedStyle(document.querySelector('.strip-track')).transform !== was, first, { timeout: 3000 });
});

test('the row is still a link under it', async () => {
  // The panel takes no pointer: a click at its middle lands on the row.
  const panel = await row('Trip').locator('.strip-show').boundingBox();
  const hit = await b.page.evaluate(([x, y]) => document.elementFromPoint(x, y).closest('a').className, [panel.x + panel.width / 2, panel.y + panel.height / 2]);
  assert.equal(hit, 'row-main');
});

test('moving off the row closes it, leaving the strip', async () => {
  await b.page.mouse.move(5, 5);
  await b.page.waitForFunction(() => !document.querySelector('.strip-show'));
  assert.ok(await row('Trip').locator('.row-strip img.ready').count() > 0);
});

test('a folder of one photo has nothing to play', async () => {
  await row('Solo').locator('.name').hover();
  await b.page.waitForTimeout(1200);
  assert.equal(await b.page.locator('.strip-show').count(), 0);
});

test('with reduced motion the strip stays still', async () => {
  await b.page.mouse.move(5, 5);
  await b.page.emulateMedia({ reducedMotion: 'reduce' });
  await row('Trip').locator('.name').hover();
  await b.page.waitForTimeout(1200);
  assert.equal(await b.page.locator('.strip-show').count(), 0);
  await b.page.emulateMedia({ reducedMotion: 'no-preference' });
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
