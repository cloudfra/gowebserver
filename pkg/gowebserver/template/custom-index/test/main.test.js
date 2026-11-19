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
const { page, load } = require('../../harness.js');

function setup(value) {
  const html = '<p id="summary"></p><p id="no-match"></p><input id="filter" value="' + (value || '') + '">';
  const files = ['alpha', 'beta'].map((n) => [0, n, n, 'unknown', 1, '2024-01-01']);
  const pg = page(html, { globals: { grid: null, GOWS: { sort: 'name', thumbs: false, videoThumbs: false, files, media: [] } } });
  const input = pg.document.getElementById('filter');
  input.value = value || '';
  load(pg, ['custom-index/util.js', 'custom-index/data.js', 'custom-index/main.js'], []);
  return { pg, input, summary: () => pg.document.getElementById('summary').textContent };
}

test('the page starts with everything counted', () => {
  assert.equal(setup().summary(), '2 files');
});

test('a filter the browser restored is applied on load', () => {
  assert.equal(setup('alp').summary(), '1 file');
});

test('typing filters once the typing pauses', () => {
  const t = setup();
  t.input.value = 'b';
  t.input.fire('input');
  t.input.value = 'be';
  t.input.fire('input');
  t.pg.tick(119);
  assert.equal(t.summary(), '2 files');
  t.pg.tick(1);
  assert.equal(t.summary(), '1 file');
});

test('Escape clears the filter, and is left alone when there is none', () => {
  const t = setup('alp');
  assert.equal(t.input.fire('keydown', { key: 'Escape' }).stopped, true);
  assert.equal(t.input.value, '');
  assert.equal(t.summary(), '2 files');
  assert.equal(t.input.fire('keydown', { key: 'Escape' }).stopped, false, 'so it can close what is behind');
});

test('slash focuses the filter, unless typing or in a dialog', () => {
  const t = setup();
  const doc = t.pg.document;
  assert.equal(doc.body.fire('keydown', { key: '/' }).defaultPrevented, true);
  assert.equal(doc.activeElement, t.input);
  assert.equal(t.input.fire('keydown', { key: '/' }).defaultPrevented, false, 'a slash typed into the filter is a slash');

  doc.activeElement = doc.body;
  assert.equal(doc.body.fire('keydown', { key: '/', ctrlKey: true }).defaultPrevented, false);
  doc.body.innerHTML = '<dialog open></dialog>';
  assert.equal(doc.body.fire('keydown', { key: '/' }).defaultPrevented, false);
});
