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

// The file list of a big folder: only the rows near the window are built.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { serve, open, png } = require('../../browser.js');

const FILES = 3000;
let srv, b;

test.before(async () => {
  const files = {};
  for (let i = 0; i < FILES; i++) files['file-' + String(i).padStart(5, '0') + '.txt'] = 'x'.repeat(i % 50);
  // A folder of photos, for its strip and the strip's slideshow.
  for (let i = 0; i < 8; i++) files['Trip/p' + i + '.png'] = png(96, 96, i * 47, i * 83, i * 29);
  srv = await serve({ files });
  b = await open();
  await b.page.goto(srv.url);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const count = (sel) => b.page.locator(sel).count();

// The rows built, each with where it is on the page; and whether each
// starts where the last ended.
const layout = () => b.page.evaluate(() => {
  const rows = Array.from(document.querySelectorAll('#files-body .row')).map((r) => {
    const rect = r.getBoundingClientRect();
    return { name: r.querySelector('.name').textContent, top: rect.top + scrollY, h: rect.height };
  });
  const body = document.getElementById('files-body').getBoundingClientRect();
  return { rows, height: body.height, gaps: rows.slice(1).filter((r, i) => Math.abs(r.top - (rows[i].top + rows[i].h)) > 0.5).length };
});

test('every entry is counted, but only the rows near the window are built', async () => {
  assert.equal(await b.page.locator('#summary').textContent(), '1 folder, 3,000 files');
  const n = await count('.row');
  assert.ok(n > 5 && n < 60, n + ' rows built');
  const l = await layout();
  assert.equal(l.rows[0].name, 'Trip');
  assert.equal(l.gaps, 0, 'each row starts where the last ends');
  // The list is as tall as all of its rows.
  assert.ok(Math.abs(l.height - (FILES + 1) * l.rows[1].h) < 2, l.height + ' px for rows of ' + l.rows[1].h);
});

test('the rows link where the template\'s did', async () => {
  const href = (name, cls) => b.page.locator('.row:has(.name:text-is("' + name + '")) .' + cls).getAttribute('href');
  assert.equal(await href('Trip', 'row-main'), 'Trip/');
  assert.equal(await href('file-00000.txt', 'row-main'), 'file-00000.txt?view=rich');
  assert.equal(await href('file-00000.txt', 'row-icon'), 'file-00000.txt');
});

test('scrolling builds the rows that come into view, where they belong', async () => {
  const first = await layout();
  const pitch = first.rows[1].h;
  await b.page.evaluate((y) => scrollTo(0, y), first.rows[0].top + 2000 * pitch);
  await b.page.waitForFunction(() => Array.from(document.querySelectorAll('.row .name')).some((n) => n.textContent === 'file-01999.txt'));
  const l = await layout();
  const row = l.rows.find((r) => r.name === 'file-01999.txt');
  assert.ok(Math.abs(row.top - (first.rows[0].top + 2000 * pitch)) < 2, 'at its place in the list');
  assert.equal(l.gaps, 0);
  assert.ok(l.rows.length < 80, l.rows.length + ' rows built');
  assert.equal(await count('.row .name:text-is("Trip")'), 0, 'the first rows are gone');
});

test('the last row is reached at the bottom', async () => {
  await b.page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
  await b.page.waitForSelector('.row .name:text-is("file-02999.txt")');
});

test('the filter finds rows far down the list', async () => {
  await b.page.evaluate(() => scrollTo(0, 0));
  await b.page.fill('#filter', '-0299');
  await b.page.waitForFunction(() => document.getElementById('summary').textContent === '10 files');
  assert.equal(await count('.row'), 10);
  assert.equal(await b.page.locator('.row .name').first().textContent(), 'file-02990.txt');
  await b.page.fill('#filter', '');
  await b.page.waitForFunction(() => document.getElementById('summary').textContent === '1 folder, 3,000 files');
});

// The folder's row, as the list has it now.
const trip = () => b.page.locator('#files-body .row', { has: b.page.locator('.name', { hasText: /^Trip$/ }) });

test('a folder row scrolled away and back gets its strip, and its slideshow plays', async () => {
  await b.page.evaluate(() => scrollTo(0, 0));
  await trip().locator('.row-strip img.ready').first().waitFor();
  const before = await trip().elementHandle();
  await b.page.evaluate(() => scrollTo(0, 1500 * 57));
  await b.page.waitForFunction(() => !Array.from(document.querySelectorAll('.row .name')).some((n) => n.textContent === 'Trip'));
  await b.page.evaluate(() => scrollTo(0, 0));
  await trip().locator('.row-strip').waitFor();
  assert.equal(await before.evaluate((el) => el.isConnected), false, 'a row built again, not the old one');
  await trip().locator('.name').hover();
  await b.page.waitForSelector('#files-body .strip-show.hp-open');
  assert.equal(await trip().locator('.strip-show').count(), 1, 'on the new row');
});

test('a filter that drops the row under a playing slideshow closes it', async () => {
  await b.page.focus('#filter');
  await b.page.keyboard.type('file-0000');
  await b.page.waitForFunction(() => document.getElementById('summary').textContent === '10 files');
  assert.equal(await b.page.locator('.strip-show').count(), 0);
  assert.equal(await b.page.locator('.hp-anchor').count(), 0, 'the hover panel let go of it');
  await b.page.fill('#filter', '');
  await b.page.waitForFunction(() => document.getElementById('summary').textContent === '1 folder, 3,000 files');
  await b.page.mouse.move(5, 5);
  await trip().locator('.row-strip').waitFor();
  await trip().locator('.name').hover();
  await b.page.waitForSelector('#files-body .strip-show.hp-open');
  await b.page.mouse.move(5, 5);
  await b.page.waitForFunction(() => !document.querySelector('.strip-show'));
});

test('on a phone, rows are a height of their own, and still one after another', async () => {
  await b.page.setViewportSize({ width: 390, height: 844 });
  await b.page.evaluate(() => scrollTo(0, 1000 * 60));
  await b.page.waitForTimeout(200);
  const l = await layout();
  assert.equal(l.gaps, 0);
  const pitch = l.rows[l.rows.length - 1].h;
  assert.ok(Math.abs(l.height - (FILES + 1) * pitch) < 2, l.height + ' px for rows of ' + pitch);
  await b.page.setViewportSize({ width: 1280, height: 800 });
});
