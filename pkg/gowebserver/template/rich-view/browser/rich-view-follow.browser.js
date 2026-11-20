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
const fs = require('node:fs');
const path = require('node:path');
const { serve, open } = require('../../browser.js');

const LOG = 'logs/app.log';
let srv, b;

test.before(async () => {
  srv = await serve({ files: { [LOG]: 'first\nsecond\n', 'src/main.go': 'package main\n' } });
  b = await open();
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

const logFile = () => path.join(srv.dir, LOG);
const shown = () => b.page.locator('#code .cl').allTextContents();
const pressed = () => b.page.locator('#follow').getAttribute('aria-pressed');
const openLog = () => b.page.goto(srv.url + LOG + '?view=rich');
const wait = (n) => b.page.waitForFunction((want) => document.querySelectorAll('#code .cl').length === want, n);

test('a log page offers to be followed; another text file does not', async () => {
  await openLog();
  assert.deepEqual(await shown(), ['first\n', 'second\n']);
  assert.equal(await b.page.locator('#follow').isHidden(), false, 'a log page offers to follow');

  await b.page.goto(srv.url + 'src/main.go?view=rich');
  assert.equal(await b.page.locator('#follow').isHidden(), true, 'a source file has nothing to follow');
});

test('following brings in lines as they are written', async () => {
  await openLog();
  await b.page.click('#follow');
  assert.equal(await pressed(), 'true');

  fs.appendFileSync(logFile(), 'third\nfourth\n');
  await wait(4);
  assert.deepEqual(await shown(), ['first\n', 'second\n', 'third\n', 'fourth\n']);
  assert.equal(await b.page.locator('#code .line').count(), 4, 'the new lines are numbered too');
  assert.equal(await b.page.locator('#L4').count(), 1, 'numbered on from the lines already shown');
  assert.equal(await b.page.locator('#meta-lines').textContent(), '4 lines');
  assert.match(await b.page.locator('#meta-size').textContent(), /[0-9]/);

  await b.page.click('#follow');
  assert.equal(await pressed(), 'false');
});

test('a page reloaded while following follows the log again', async () => {
  await openLog();
  await b.page.click('#follow');
  await b.page.reload();
  assert.equal(await pressed(), 'true', 'following is remembered, like wrapping');

  fs.appendFileSync(logFile(), 'fifth\n');
  await wait(5);
  assert.deepEqual((await shown()).slice(-1), ['fifth\n']);

  await b.page.click('#follow');
  assert.equal(await pressed(), 'false');
});

test('a log that is truncated starts over from its new top', async () => {
  await openLog();
  await b.page.click('#follow');

  // Write something first: seeing it is what says the stream is open, so
  // the truncation below is a truncation of a followed log and not a race
  // with the stream starting.
  fs.appendFileSync(logFile(), 'sixth\n');
  await wait(6);

  fs.writeFileSync(logFile(), 'fresh\n');
  await wait(1);
  assert.deepEqual(await shown(), ['fresh\n']);
  assert.equal(await b.page.locator('#meta-lines').textContent(), '1 line');

  await b.page.click('#follow');
});

test('a log that is deleted while followed says so', async () => {
  await openLog();
  await b.page.click('#follow');
  fs.appendFileSync(logFile(), 'last\n');
  await wait(2);

  fs.unlinkSync(logFile());
  await b.page.waitForFunction(() => document.getElementById('follow-note').textContent !== '');
  assert.equal(await b.page.locator('#follow-note').textContent(), 'This log file is gone.');
  assert.equal(await pressed(), 'false', 'following stopped on its own');
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
