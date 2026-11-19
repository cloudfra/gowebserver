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

// What the templates' JavaScript tests and benchmarks run on: Node alone,
// with no packages to install.
//
// A template's scripts are the body of the function its HTML wraps them
// in, so they share one scope and export nothing. load() runs them the
// same way, in the order given, against a small stand-in for the browser
// (page()), and hands back the names a test asks for.
//
// The stand-in has only what the scripts use: elements with classes,
// attributes, styles and listeners, simple selectors, and a clock,
// timers and animation frames that move only when a test moves them.
// There is no layout: a test sets the sizes and positions it needs.

'use strict';

const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const VOID = new Set(['img', 'input', 'br', 'hr', 'meta', 'link', 'use', 'path', 'circle', 'polyline', 'rect']);

class ClassList {
  constructor(el) { this.el = el; this.set = new Set(); }
  add(...names) { names.forEach((n) => this.set.add(n)); }
  remove(...names) { names.forEach((n) => this.set.delete(n)); }
  contains(name) { return this.set.has(name); }
  toggle(name, on) {
    if (on === undefined) on = !this.set.has(name);
    if (on) this.set.add(name); else this.set.delete(name);
    return on;
  }
  toString() { return Array.from(this.set).join(' '); }
}

class Style {
  setProperty(name, value) { this[name] = String(value); }
  getPropertyValue(name) { return this[name] === undefined ? '' : this[name]; }
  removeProperty(name) { delete this[name]; }
}

class Node {
  constructor() {
    this.childNodes = [];
    this.parentNode = null;
    this.listeners = {};
  }

  addEventListener(type, fn, opts) {
    (this.listeners[type] = this.listeners[type] || []).push({ fn, once: !!(opts && opts.once) });
  }
  removeEventListener(type, fn) {
    this.listeners[type] = (this.listeners[type] || []).filter((l) => l.fn !== fn);
  }
  // Calls the listeners for an event of this type, here and then on
  // each ancestor, as a bubbling event would. Returns the event.
  fire(type, props) {
    const e = Object.assign({
      type, target: this, defaultPrevented: false, stopped: false, button: 0,
      preventDefault() { if (this.cancelable !== false) this.defaultPrevented = true; },
      stopPropagation() { this.stopped = true; },
    }, props);
    for (let n = this; n && !e.stopped; n = n.parentNode || n.defaultView) {
      const ls = n.listeners[type] || [];
      ls.slice().forEach((l) => {
        if (l.once) n.removeEventListener(type, l.fn);
        l.fn.call(n, e);
      });
    }
    return e;
  }
  dispatchEvent(e) { this.fire(e.type, e); return true; }

  get children() { return this.childNodes.filter((n) => n instanceof Element); }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get nextSibling() {
    if (!this.parentNode) return null;
    const s = this.parentNode.childNodes;
    return s[s.indexOf(this) + 1] || null;
  }
  get isConnected() {
    for (let n = this; n; n = n.parentNode) if (n instanceof Document) return true;
    return false;
  }
  contains(other) {
    for (let n = other; n; n = n.parentNode) if (n === this) return true;
    return false;
  }
  insertBefore(child, before) {
    if (child instanceof Fragment) {
      child.childNodes.slice().forEach((c) => this.insertBefore(c, before));
      return child;
    }
    child.remove();
    const i = before ? this.childNodes.indexOf(before) : -1;
    if (i < 0) this.childNodes.push(child); else this.childNodes.splice(i, 0, child);
    child.parentNode = this;
    return child;
  }
  appendChild(child) { return this.insertBefore(child, null); }
  remove() {
    if (!this.parentNode) return;
    const s = this.parentNode.childNodes;
    s.splice(s.indexOf(this), 1);
    this.parentNode = null;
  }
  get textContent() { return this.childNodes.map((n) => n.textContent).join(''); }
  set textContent(v) {
    this.childNodes.forEach((n) => { n.parentNode = null; });
    this.childNodes = [];
    this._html = undefined;
    if (v !== '') this.appendChild(new Text(String(v)));
  }
  // Every element below this one, in document order.
  descendants(out) {
    out = out || [];
    this.childNodes.forEach((n) => {
      if (n instanceof Element) { out.push(n); n.descendants(out); }
    });
    return out;
  }
  querySelectorAll(sel) { return this.descendants().filter((el) => el.matches(sel)); }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

class Text extends Node {
  constructor(text) { super(); this.text = text; }
  get textContent() { return this.text; }
  set textContent(v) { this.text = String(v); }
}

class Fragment extends Node { }

class Element extends Node {
  constructor(tag, ownerDocument) {
    super();
    this.tagName = tag.toUpperCase();
    this.ownerDocument = ownerDocument;
    this.attrs = {};
    this.classList = new ClassList(this);
    this.style = new Style();
    this.dataset = {};
    this.hidden = false;
    this.value = '';
    this.draggable = false;
    // Layout a test sets; nothing computes it.
    this.rect = { top: 0, left: 0, width: 0, height: 0 };
    this.clientWidth = 0;
    this.offsetLeft = 0;
    this.offsetTop = 0;
    this.offsetHeight = 0;
    this.offsetParent = null;
  }

  get id() { return this.attrs.id || ''; }
  set id(v) { this.attrs.id = v; }
  get className() { return this.classList.toString(); }
  set className(v) { this.classList.set = new Set(String(v).split(/\s+/).filter(Boolean)); }
  get offsetWidth() { return this.rect.width; }
  get name() { return this.attrs.name || ''; }
  set name(v) { this.attrs.name = String(v); }
  get type() { return this.attrs.type || ''; }
  set type(v) { this.attrs.type = String(v); }
  get method() { return this.attrs.method || 'get'; }
  get action() { return this.attrs.action || ''; }

  setAttribute(name, value) {
    value = String(value);
    if (name === 'class') this.className = value;
    else if (name.indexOf('data-') === 0) this.dataset[name.slice(5)] = value;
    else this.attrs[name] = value;
  }
  getAttribute(name) {
    if (name === 'class') return this.className;
    if (name.indexOf('data-') === 0) return name.slice(5) in this.dataset ? this.dataset[name.slice(5)] : null;
    return name in this.attrs ? this.attrs[name] : null;
  }
  hasAttribute(name) { return this.getAttribute(name) !== null; }
  removeAttribute(name) {
    if (name === 'src') this._src = undefined;
    if (name.indexOf('data-') === 0) delete this.dataset[name.slice(5)]; else delete this.attrs[name];
  }

  // src and href are plain attributes; requests is every src an element
  // was given, which is how a test sees what a page fetched.
  get src() { return this._src || ''; }
  set src(v) {
    this._src = String(v);
    if (this.ownerDocument) this.ownerDocument.requests.push({ el: this, url: this._src });
  }
  get href() { return this.attrs.href || ''; }
  set href(v) { this.attrs.href = String(v); }

  get innerHTML() { return this._html || ''; }
  set innerHTML(html) {
    this.textContent = '';
    this._html = String(html);
    parseInto(this, this._html);
  }

  getBoundingClientRect() {
    const r = this.rect;
    return { top: r.top, left: r.left, width: r.width, height: r.height, bottom: r.top + r.height, right: r.left + r.width };
  }
  closest(sel) {
    for (let n = this; n instanceof Element; n = n.parentNode) if (n.matches(sel)) return n;
    return null;
  }
  matches(sel) {
    return sel.split(',').some((s) => matchChain(this, s.trim().split(/\s+/)));
  }
  focus() { if (this.ownerDocument) this.ownerDocument.activeElement = this; }
  click() { this.fire('click'); }

  // Dialogs.
  showModal() { this.open = true; }
  close() { this.open = false; this.fire('close'); }

  // Media elements.
  load() { }
  pause() { this.paused = true; }
  play() { this.paused = false; return Promise.resolve(); }
  setPointerCapture() { }
}

// Whether el matches the last of the compound selectors in chain and has
// ancestors matching the ones before it.
function matchChain(el, chain) {
  if (!matchCompound(el, chain[chain.length - 1])) return false;
  if (chain.length === 1) return true;
  const rest = chain.slice(0, -1);
  for (let n = el.parentNode; n instanceof Element; n = n.parentNode) {
    if (matchChain(n, rest)) return true;
  }
  return false;
}

// One compound selector: tag, #id, .class, [attr], [attr=value] and
// :not() of those.
function matchCompound(el, sel) {
  const re = /:not\(([^)]+)\)|\[([\w-]+)(?:=["']?([^"'\]]*)["']?)?\]|([#.]?)([\w-]+)|\*/g;
  let m;
  while ((m = re.exec(sel))) {
    if (m[1] !== undefined) { if (matchCompound(el, m[1])) return false; }
    else if (m[2] !== undefined) {
      const v = el.getAttribute(m[2]);
      if (v === null || (m[3] !== undefined && v !== m[3])) return false;
    }
    else if (m[4] === '#') { if (el.id !== m[5]) return false; }
    else if (m[4] === '.') { if (!el.classList.contains(m[5])) return false; }
    else if (m[5] !== undefined) { if (el.tagName !== m[5].toUpperCase()) return false; }
  }
  return true;
}

// Builds the elements and text that html describes under parent. It
// reads the markup the scripts and the tests write, not HTML at large.
function parseInto(parent, html) {
  const doc = parent instanceof Document ? parent : parent.ownerDocument;
  const re = /<\/([\w-]+)\s*>|<([\w-]+)((?:\s+[\w-:]+(?:=(?:"[^"]*"|'[^']*'|[^\s>]+))?)*)\s*(\/?)>|([^<]+)/g;
  let cur = parent, m;
  html = html.replace(/<!--[\s\S]*?-->/g, '');
  while ((m = re.exec(html))) {
    if (m[1]) {
      if (cur !== parent) cur = cur.parentNode;
    } else if (m[2]) {
      const el = new Element(m[2], doc);
      const attrRe = /([\w-:]+)(?:=(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g;
      let a;
      while ((a = attrRe.exec(m[3]))) {
        const v = a[2] !== undefined ? a[2] : a[3] !== undefined ? a[3] : a[4] !== undefined ? a[4] : '';
        if (a[1] === 'hidden') el.hidden = true; else el.setAttribute(a[1], unescapeHTML(v));
      }
      cur.appendChild(el);
      if (!m[4] && !VOID.has(m[2].toLowerCase())) cur = el;
    } else if (m[5].trim()) {
      cur.appendChild(new Text(unescapeHTML(m[5])));
    }
  }
}

function unescapeHTML(s) {
  return s.replace(/&(amp|lt|gt|quot|#39);/g, (_, e) => ({ amp: '&', lt: '<', gt: '>', quot: '"', '#39': "'" }[e]));
}

class Document extends Node {
  constructor() {
    super();
    this.requests = [];
    this.title = '';
    this.documentElement = this.appendChild(new Element('html', this));
    this.body = this.documentElement.appendChild(new Element('body', this));
    this.activeElement = this.body;
  }
  createElement(tag) { return new Element(tag, this); }
  createDocumentFragment() { return new Fragment(); }
  createEvent() {
    return { initEvent(type, bubbles, cancelable) { this.type = type; this.cancelable = !!cancelable; } };
  }
  getElementById(id) { return this.descendants().find((el) => el.id === id) || null; }
}

// page(html) is a browser window showing html (the content of <body>),
// as the object the scripts see as their global scope. Besides the
// browser's own names it has the controls a test drives it with:
//
//   page.tick(ms)    moves the clock, running the timers that come due
//   page.frame()     runs the animation frames requested so far
//   page.settle()    lets promise callbacks run (await it)
//   page.requests    every {el, url} given to an element's src
//   page.stored      what's in localStorage
function page(html, opts) {
  opts = opts || {};
  const document = new Document();
  if (html) parseInto(document.body, html);

  let now = 0, timerID = 0, timers = [], frames = [];
  const win = new Node();
  const stored = Object.assign({}, opts.stored);
  const location = { pathname: '/', host: 'example.test', hash: '', href: '' };
  Object.assign(location, opts.location);

  Object.assign(win, {
    document, location, stored,
    requests: document.requests,
    navigator: opts.navigator || {},
    devicePixelRatio: opts.devicePixelRatio || 1,
    innerWidth: opts.innerWidth || 1280,
    innerHeight: opts.innerHeight || 800,
    scrollX: 0,
    scrollY: 0,
    isSecureContext: true,
    console,
    localStorage: {
      getItem: (k) => (k in stored ? stored[k] : null),
      setItem: (k, v) => { stored[k] = String(v); },
      removeItem: (k) => { delete stored[k]; },
    },
    // media maps a query to whether it matches; the rest don't.
    matchMedia: (q) => ({ matches: !!(opts.media && opts.media[q]), addEventListener() { } }),
    getComputedStyle: (el) => Object.assign({ getPropertyValue: (n) => el.style.getPropertyValue(n) }, el.computed),
    performance: {
      now: () => now,
      getEntriesByType: () => (opts.protocol ? [{ nextHopProtocol: opts.protocol }] : []),
    },
    setTimeout(fn, ms) { timers.push({ id: ++timerID, at: now + (ms || 0), fn }); return timerID; },
    clearTimeout(id) { timers = timers.filter((t) => t.id !== id); },
    requestAnimationFrame(fn) { frames.push(fn); return frames.length; },
    cancelAnimationFrame() { },
    scrollTo(x, y) { win.scrollY = y; },
    history: {
      state: null,
      pushState(state) { this.state = state; },
      back() { this.state = null; win.fire('popstate'); },
    },
    ResizeObserver: class { observe() { } disconnect() { } },

    tick(ms) {
      const until = now + ms;
      for (;;) {
        const due = timers.filter((t) => t.at <= until).sort((a, b) => a.at - b.at || a.id - b.id)[0];
        if (!due) break;
        timers = timers.filter((t) => t !== due);
        now = Math.max(now, due.at);
        due.fn();
      }
      now = until;
    },
    frame() {
      const run = frames;
      frames = [];
      run.forEach((fn) => fn(now));
      return run.length;
    },
    settle: () => new Promise((resolve) => setImmediate(resolve)),
    // Scrolls the window as a person would: a scroll event, then a frame.
    scroll(y) {
      win.scrollY = y;
      win.fire('scroll');
      win.frame();
    },
  });
  // The scripts call these bare, with no this.
  ['addEventListener', 'removeEventListener', 'fire'].forEach((m) => { win[m] = Node.prototype[m].bind(win); });
  document.defaultView = win;
  win.window = win;
  Object.assign(win, opts.globals);
  return vm.createContext(win);
}

// load runs the template's scripts in page, in order, in one scope, and
// returns the values of the names in want. files are paths under the
// template directory, such as 'custom-index/util.js'.
function load(pg, files, want) {
  const src = files.map((f) => fs.readFileSync(path.join(__dirname, f), 'utf8')).join('\n');
  const body = '(function () {\n\'use strict\';\n' + src + '\nreturn {' + (want || []).join(', ') + '};\n})()';
  return new vm.Script(body, { filename: files[files.length - 1] }).runInContext(pg);
}

// plain copies a value made inside a page into this file's own objects
// and arrays, which is what assert.deepStrictEqual compares.
function plain(v) {
  return JSON.parse(JSON.stringify(v));
}

// bench times fn and prints a line in the shape of a Go benchmark's. It
// runs fn until that has taken long enough to trust, at least a few
// times, after a warm-up. bench.js runs the files that call it.
function bench(name, fn) {
  if (name.indexOf(process.env.BENCH_FILTER || '') < 0) return;
  for (let i = 0; i < 3; i++) fn();
  let n = 0;
  const start = process.hrtime.bigint();
  let elapsed = 0n;
  while (n < 5 || elapsed < 300000000n) {
    fn();
    n++;
    elapsed = process.hrtime.bigint() - start;
  }
  const ns = Number(elapsed) / n;
  console.log(('Benchmark' + name).padEnd(44) + String(n).padStart(8) + (ns.toFixed(0) + ' ns/op').padStart(18));
}

// markup returns the lines of a template's HTML from the first that
// contains from to the first after it that contains to, so a test runs
// a script against the page's real markup.
function markup(file, from, to) {
  const html = fs.readFileSync(path.join(__dirname, file), 'utf8');
  const a = html.indexOf(from);
  const b = html.indexOf(to, a);
  if (a < 0 || b < 0) throw new Error(file + ' has no ' + from + ' ... ' + to);
  return html.slice(a, b + to.length);
}

module.exports = { page, load, plain, bench, markup };
