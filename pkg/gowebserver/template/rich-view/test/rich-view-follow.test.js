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

// A page for a log the server offers to stream, as rich-view.html builds one.
const LOG = '<span id="meta-lines">2 lines</span><span id="meta-size">12 B</span>' +
  '<select id="code-theme"></select><button id="wrap" aria-pressed="false"></button>' +
  '<button id="follow" type="button" aria-pressed="false" hidden></button>' +
  '<div id="code" data-follow-url="app.log?view=rich&amp;follow=1" data-follow-offset="12" data-follow-line="3">' +
  '<pre class="chroma"><code>' +
  '<span class="line"><span class="ln">1</span><span class="cl">first\n</span></span>' +
  '<span class="line"><span class="ln">2</span><span class="cl">second\n</span></span>' +
  '</code></pre></div><p id="follow-note"></p>';

function line(n, text) {
  return `<span class="line"><span class="ln"><a href="#L${n}">${n}</a></span><span class="cl">${text}\n</span></span>`;
}

// The code as a reader sees it: one line of text each, without the numbers.
function shown(code) {
  return code.children.map((el) => el.querySelector('.cl').textContent);
}

// A stream that only remembers what it was asked for, so a test can send it
// events and see where the page connected to.
function sources() {
  const opened = [];
  const EventSource = class {
    constructor(url) {
      this.url = url;
      this.readyState = 1;
      this.listeners = {};
      opened.push(this);
    }
    addEventListener(name, fn) { (this.listeners[name] = this.listeners[name] || []).push(fn); }
    emit(name, data, id) { (this.listeners[name] || []).forEach((fn) => fn({ data, lastEventId: id })); }
    send(chunk, id) { this.emit('lines', JSON.stringify(chunk), id); }
    close() { this.closed = true; this.readyState = 2; }
  };
  return { opened, EventSource };
}

function setup(html, opts) {
  const stream = sources();
  const pg = page(html || LOG, Object.assign({ globals: { EventSource: stream.EventSource } }, opts));
  load(pg, ['rich-view/rich-view.js'], []);
  const root = pg.document.documentElement;
  return { pg, stream, last: () => stream.opened[stream.opened.length - 1], root, $: (id) => pg.document.getElementById(id) };
}

test('a log offers to be followed, and starts off not following it', () => {
  const t = setup();
  assert.equal(t.$('follow').hidden, false);
  assert.equal(t.$('follow').getAttribute('aria-pressed'), 'false');
  assert.equal(t.stream.opened.length, 0);
});

test('a file that is not a log has nothing to follow', () => {
  const t = setup(LOG.replace(/ data-follow-[a-z-]+="[^"]*"/g, ''));
  assert.equal(t.$('follow').hidden, true);
  assert.equal(t.stream.opened.length, 0);
});

test('following opens a stream from where the page stopped reading', () => {
  const t = setup();
  t.$('follow').click();
  assert.equal(t.stream.opened.length, 1);
  assert.equal(t.last().url, 'app.log?view=rich&follow=1&offset=12&line=3');
  assert.equal(t.$('follow').getAttribute('aria-pressed'), 'true');
});

test('followed lines join the code, numbered on from what the page showed', () => {
  const t = setup();
  t.$('follow').click();
  t.last().send({ start: 3, count: 2, size: '40 B', lines: line(3, 'third') + line(4, 'fourth') }, '28:5');
  const code = t.$('code').querySelector('code');
  assert.deepEqual(shown(code), ['first\n', 'second\n', 'third\n', 'fourth\n']);
  assert.equal(t.$('meta-lines').textContent, '4 lines');
  assert.equal(t.$('meta-size').textContent, '40 B');
});

test('a log that was rotated in place starts the page over', () => {
  const t = setup();
  t.$('follow').click();
  t.last().send({ start: 1, count: 1, lines: line(1, 'fresh') }, '6:2');
  const code = t.$('code').querySelector('code');
  assert.deepEqual(shown(code), ['fresh\n']);
  assert.equal(t.$('meta-lines').textContent, '1 line');
});

test('a stream that goes on resuming starts the next one where the last left off', () => {
  const t = setup();
  t.$('follow').click();
  t.last().send({ start: 3, count: 1, lines: line(3, 'third') }, '30:9');
  t.$('follow').click();
  t.$('follow').click();
  assert.equal(t.last().url, 'app.log?view=rich&follow=1&offset=30&line=9');
});

test('following is remembered, and turning it off forgets it', () => {
  const t = setup();
  t.$('follow').click();
  assert.equal(t.pg.stored['gows-follow'], '1');
  t.$('follow').click();
  assert.equal(t.last().closed, true);
  assert.equal('gows-follow' in t.pg.stored, false);
  assert.equal(t.$('follow').getAttribute('aria-pressed'), 'false');

  const again = setup(undefined, { stored: { 'gows-follow': '1' } });
  assert.equal(again.stream.opened.length, 1);
  assert.equal(again.$('follow').getAttribute('aria-pressed'), 'true');
});

test('a log that is gone says so and stops', () => {
  const t = setup();
  t.$('follow').click();
  t.last().emit('gone', '{}');
  assert.equal(t.$('follow-note').textContent, 'This log file is gone.');
  assert.equal(t.last().closed, true);
  assert.equal(t.$('follow').getAttribute('aria-pressed'), 'false');
});

test('a stream that died says so once, rather than reopening itself', () => {
  const t = setup();
  t.$('follow').click();
  const source = t.last();
  source.readyState = 2;
  source.onerror();
  assert.equal(t.$('follow-note').textContent, 'Following stopped.');
  assert.equal(t.$('follow').getAttribute('aria-pressed'), 'false');
  assert.equal(t.stream.opened.length, 1);
});

test('a broken event changes nothing', () => {
  const t = setup();
  t.$('follow').click();
  t.last().emit('lines', 'not json');
  const code = t.$('code').querySelector('code');
  assert.deepEqual(shown(code), ['first\n', 'second\n']);
});

test('the page follows along when its reader is at the bottom, and not otherwise', () => {
  const t = setup();
  t.root.scrollHeight = 900;
  t.$('follow').click();
  t.pg.scrollY = 850;
  t.last().send({ start: 3, count: 1, lines: line(3, 'third') }, '20:4');
  assert.equal(t.pg.scrollY, 900);

  const away = setup();
  away.root.scrollHeight = 4000;
  away.$('follow').click();
  away.pg.scrollY = 100;
  away.last().send({ start: 3, count: 1, lines: line(3, 'third') }, '20:4');
  assert.equal(away.pg.scrollY, 100);
});
