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

// The breadcrumb's folder panel (crumb-panel.js), on both pages that have
// a breadcrumb: the file browser and the text viewer.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { serve, open } = require('../../browser.js');

let srv, b;

test.before(async () => {
  srv = await serve({
    files: {
      'Trips/2023/a.txt': 'a\n',
      'Trips/2024/Rome/notes.txt': 'notes\n',
      'Trips/2025 & later/b.txt': 'b\n',
      'Trips/readme.txt': 'not a folder\n',
      'Music/c.txt': 'c\n',
    },
  });
  b = await open();
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const crumb = (name) => b.page.locator('#crumbs a', { hasText: name });
const entries = () => b.page.locator('#crumb-panel a').evaluateAll((as) => as.map((a) => [a.textContent, a.getAttribute('href'), a.classList.contains('current')]));
const isOpen = () => b.page.locator('#crumb-panel').evaluate((el) => el.classList.contains('hp-open'));

test('resting on a crumb drops its sibling folders under it', async () => {
  await b.page.goto(srv.url + 'Trips/2024/Rome/');
  const before = await crumb('2024').boundingBox();
  await crumb('2024').hover();
  await b.page.waitForSelector('#crumb-panel.hp-open');
  assert.deepEqual(await entries(), [
    ['2023', '/Trips/2023/', false],
    ['2024', '/Trips/2024/', true],
    ['2025 & later', '/Trips/2025%20%26%20later/', false],
  ]);
  const box = await b.page.locator('#crumb-panel').boundingBox();
  const after = await crumb('2024').boundingBox();
  assert.deepEqual(after, before, 'the crumb itself never moves');
  assert.ok(box.y >= after.y + after.height, 'under the crumb');
  assert.ok(Math.abs(box.x - after.x) < 2, 'lined up with it');
  const z = await b.page.locator('#crumb-panel').evaluate((el) => +getComputedStyle(el).zIndex);
  assert.ok(z > 25, 'above the title, the toolbar and the scrubber');
  assert.equal(await crumb('2024').getAttribute('aria-expanded'), 'true');
});

test('leaving both closes it; Escape does too', async () => {
  await b.page.mouse.move(600, 700);
  await b.page.waitForFunction(() => !document.getElementById('crumb-panel').classList.contains('hp-open'));
  await crumb('2024').hover();
  await b.page.waitForSelector('#crumb-panel.hp-open');
  await b.page.keyboard.press('Escape');
  assert.equal(await isOpen(), false);
});

test('a folder in it is a link like any other', async () => {
  await b.page.mouse.move(600, 700);
  await crumb('2024').hover();
  await b.page.waitForSelector('#crumb-panel.hp-open');
  const link = b.page.locator('#crumb-panel a', { hasText: '2023' });
  // Diagonally across the gap, into the panel, and a click.
  await link.hover();
  await Promise.all([b.page.waitForURL(srv.url + 'Trips/2023/'), link.click()]);
  assert.equal(await b.page.locator('#title').textContent(), '2023');
});

test('the keyboard: ArrowDown on a crumb opens it on its own folder', async () => {
  await b.page.goto(srv.url + 'Trips/2024/Rome/');
  await crumb('Trips').focus();
  await b.page.keyboard.press('ArrowDown');
  await b.page.waitForSelector('#crumb-panel.hp-open');
  assert.equal(await b.page.evaluate(() => document.activeElement.textContent), 'Trips');
  assert.equal(await b.page.evaluate(() => document.activeElement.closest('#crumb-panel') !== null), true);
  assert.deepEqual((await entries()).map((e) => e[0]), ['Music', 'Trips']);
});

test('moving along the crumbs leaves no crumb marked as having the panel but its own', async () => {
  await b.page.goto(srv.url + 'Trips/2024/Rome/');
  await crumb('2024').hover();
  await b.page.waitForSelector('#crumb-panel.hp-open');
  await crumb('Trips').hover();
  await b.page.waitForFunction(() => document.querySelector('#crumb-panel a.current').textContent === 'Trips');
  const expanded = await b.page.locator('#crumbs a[aria-expanded]').evaluateAll((as) => as.map((a) => a.textContent));
  assert.deepEqual(expanded, ['Trips']);
});

test('a panel opened from the keyboard stays while the mouse moves, and Tab out of it closes it', async () => {
  await b.page.goto(srv.url + 'Trips/2024/Rome/');
  await crumb('2024').focus();
  await b.page.keyboard.press('ArrowDown');
  await b.page.waitForSelector('#crumb-panel.hp-open');
  await b.page.mouse.move(600, 600);
  await b.page.mouse.move(700, 650);
  await b.page.waitForTimeout(600);
  assert.equal(await isOpen(), true, 'the mouse never went near it');
  // Down to the last folder, then Tab on out.
  await b.page.keyboard.press('ArrowDown');
  await b.page.keyboard.press('Tab');
  assert.equal(await isOpen(), false);
  assert.equal(await b.page.evaluate(() => !!document.activeElement.closest('#crumb-panel')), false);
});

test('Shift+Tab from the panel goes back to the crumbs', async () => {
  await b.page.goto(srv.url + 'Trips/2024/Rome/');
  await crumb('2024').focus();
  await b.page.keyboard.press('ArrowDown');
  await b.page.waitForSelector('#crumb-panel.hp-open');
  // From its own folder up to the first.
  await b.page.keyboard.press('ArrowUp');
  assert.equal(await b.page.evaluate(() => document.activeElement.textContent), '2023');
  await b.page.keyboard.press('Shift+Tab');
  assert.equal(await b.page.evaluate(() => !!document.activeElement.closest('#crumbs')), true);
});

test('the text viewer has it too', async () => {
  await b.page.goto(srv.url + 'Trips/2024/Rome/notes.txt?view=rich');
  await crumb('2024').hover();
  await b.page.waitForSelector('#crumb-panel.hp-open');
  assert.deepEqual((await entries()).map((e) => e[0]), ['2023', '2024', '2025 & later']);
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});

test('serve paths of more than one segment are listed and marked by the home crumb', async () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'gows-mounts-'));
  for (const d of ['ab/x', 'c']) fs.mkdirSync(path.join(base, d), { recursive: true });
  const mounts = await serve({ dir: base, args: ['-path', path.join(base, 'ab') + ',' + path.join(base, 'c'), '-servepath', '/a/b,/c'] });
  const m = await open();
  try {
    await m.page.goto(mounts.url + 'a/b/x/');
    await m.page.locator('#crumbs a').first().hover();
    await m.page.waitForSelector('#crumb-panel.hp-open');
    const got = await m.page.locator('#crumb-panel a').evaluateAll((as) => as.map((a) => [a.textContent, a.getAttribute('href'), a.classList.contains('current')]));
    assert.deepEqual(got, [['a/b', '/a/b/', true], ['c', '/c/', false]]);
    await Promise.all([m.page.waitForURL(mounts.url + 'c/'), m.page.locator('#crumb-panel a', { hasText: 'c' }).click()]);
    assert.deepEqual(m.problems, []);
  } finally {
    await m.close();
    mounts.close();
    fs.rmSync(base, { recursive: true, force: true });
  }
});
