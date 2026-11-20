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

// What the browser tests (*/browser/*.browser.js), browserbench.js and
// browse.js share: a real server over a folder made for the occasion, and
// a headless browser looking at it. Makefile_javascript.mk installs the
// browsers and says where they and the server are; run these through it
// (make test-browser, benchmark-browser, browse).
//
// The tests in */test run on harness.js's stand-in instead, which is
// instant and needs nothing installed, but has no layout, network or
// painting. These are for what only a browser can show.

'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const zlib = require('node:zlib');

// A w by h PNG of a gradient from the given color, so every file differs
// and the server has real pixels to resize.
function png(w, h, r, g, b) {
  const raw = Buffer.alloc(h * (1 + w * 3));
  for (let y = 0; y < h; y++) {
    const row = y * (1 + w * 3);
    for (let x = 0; x < w; x++) {
      const o = row + 1 + x * 3;
      raw[o] = (r + x) & 255;
      raw[o + 1] = (g + y) & 255;
      raw[o + 2] = (b + x + y) & 255;
    }
  }
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    const out = Buffer.alloc(8 + data.length + 4);
    out.writeUInt32BE(data.length, 0);
    body.copy(out, 4);
    out.writeUInt32BE(zlib.crc32(body) >>> 0, 8 + data.length);
    return out;
  };
  const head = Buffer.alloc(13);
  head.writeUInt32BE(w, 0);
  head.writeUInt32BE(h, 4);
  head.set([8, 2, 0, 0, 0], 8);
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk('IHDR', head), chunk('IDAT', zlib.deflateSync(raw, { level: 1 })), chunk('IEND', Buffer.alloc(0)),
  ]);
}

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.on('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const port = s.address().port;
      s.close(() => resolve(port));
    });
  });
}

// fill makes the folder dir for serve and serveEmbedded: opts.photos
// PNGs, photo-00000.png onward, of opts.photoSize, and opts.files.
function fill(dir, opts) {
    fs.mkdirSync(dir);
    const size = opts.photoSize || [640, 480];
    for (let i = 0; i < (opts.photos || 0); i++) {
      const name = 'photo-' + String(i).padStart(5, '0') + '.png';
      fs.writeFileSync(path.join(dir, name), png(size[0], size[1], i * 37, i * 91, i * 13));
    }
    for (const [name, content] of Object.entries(opts.files || {})) {
      fs.mkdirSync(path.dirname(path.join(dir, name)), { recursive: true });
      fs.writeFileSync(path.join(dir, name), content);
    }
}

// serve starts the server (the binary named by GOWEBSERVER) and resolves
// once it answers, to {url, dir, uploads, close}. It serves opts.dir, or
// else a new folder holding:
//   opts.photos  that many PNGs, photo-00000.png onward
//   opts.files   {name: content} for anything else
// opts.args are more flags for the server.
// close() stops the server and removes what serve made.
async function serve(opts) {
  opts = opts || {};
  const bin = process.env.GOWEBSERVER;
  if (!bin || !fs.existsSync(bin)) throw new Error('GOWEBSERVER must name the server binary; run this through make');
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'gows-browser-'));
  const dir = opts.dir ? path.resolve(opts.dir) : path.join(tmp, 'served');
  const work = path.join(tmp, 'work'), uploads = path.join(tmp, 'uploads');
  [work, uploads].forEach((d) => fs.mkdirSync(d));
  if (!opts.dir) fill(dir, opts);

  const port = await freePort(), tlsPort = await freePort();
  // It writes its certificate where it runs, so that's a folder of its own.
  const proc = spawn(bin, [
    '-path', dir, '-http.port', String(port), '-https.port', String(tlsPort), '-enhancedindex=true',
    '-thumbnails.cachepath', 'memory:', '-upload.path', uploads,
  ].concat(opts.args || []), { cwd: work, stdio: ['ignore', 'ignore', 'pipe'] });
  let log = '';
  proc.stderr.on('data', (d) => { log = (log + d).slice(-4000); });
  const close = () => {
    proc.kill();
    fs.rmSync(tmp, { recursive: true, force: true });
  };
  const url = 'http://127.0.0.1:' + port + '/';
  for (let i = 0; ; i++) {
    if (proc.exitCode !== null || i > 200) {
      close();
      throw new Error('the server did not start:\n' + log);
    }
    try {
      await fetch(url);
      break;
    } catch (e) {
      await new Promise((r) => setTimeout(r, 50));
    }
  }
  return { url, dir, uploads, close, log: () => log };
}

// serveEmbedded starts the host program that embeds gowebserver (the
// binary named by GOWEBSERVER_EMBED_HOST; see
// internal/gowebserver/testing/embedhost) and resolves once it answers,
// to {url, media, dir, close}: its app is at url, with its API at
// url + 'api/', and gowebserver's file browser of opts.dir, or a folder
// made as serve makes one, at media (url + 'media/'). opts.args are more
// flags for it.
async function serveEmbedded(opts) {
  opts = opts || {};
  const bin = process.env.GOWEBSERVER_EMBED_HOST;
  if (!bin || !fs.existsSync(bin)) throw new Error('GOWEBSERVER_EMBED_HOST must name the host program; run this through make');
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'gows-embedded-'));
  const dir = opts.dir ? path.resolve(opts.dir) : path.join(tmp, 'served');
  if (!opts.dir) fill(dir, opts);
  const port = await freePort();
  const proc = spawn(bin, ['-path', dir, '-http.port', String(port)].concat(opts.args || []),
    { cwd: tmp, stdio: ['ignore', 'ignore', 'pipe'] });
  let log = '';
  proc.stderr.on('data', (d) => { log = (log + d).slice(-4000); });
  const close = () => {
    proc.kill();
    fs.rmSync(tmp, { recursive: true, force: true });
  };
  const url = 'http://127.0.0.1:' + port + '/';
  for (let i = 0; ; i++) {
    if (proc.exitCode !== null || i > 200) {
      close();
      throw new Error('the host did not start:\n' + log);
    }
    try {
      await fetch(url);
      break;
    } catch (e) {
      await new Promise((r) => setTimeout(r, 50));
    }
  }
  return { url, media: url + 'media/', dir, tmp, close, log: () => log };
}

// open starts the browser named by BROWSER (chromium, firefox or webkit)
// and resolves to {browser, page, problems, name, close}. problems
// collects what a healthy page doesn't do: script errors, console
// errors, failed requests and error responses. Requests the page itself
// canceled (a thumbnail scrolled away) aren't problems.
async function open(opts) {
  opts = opts || {};
  const name = process.env.BROWSER || 'chromium';
  const playwright = require('playwright');
  if (!playwright[name] || !playwright[name].launch) throw new Error('BROWSER=' + name + ': want chromium, firefox or webkit');
  const browser = await playwright[name].launch();
  const context = await browser.newContext({ viewport: opts.viewport || { width: 1280, height: 800 } });
  const page = await context.newPage();
  const problems = [];
  page.on('pageerror', (e) => problems.push('error: ' + e.message));
  page.on('console', (m) => { if (m.type() === 'error') problems.push('console: ' + m.text()); });
  page.on('requestfailed', (r) => {
    const why = (r.failure() || {}).errorText || '';
    if (!/ABORTED|cancel+ed/i.test(why)) problems.push('failed: ' + r.url() + ' ' + why);
  });
  page.on('response', (r) => { if (r.status() >= 400) problems.push(r.status() + ': ' + r.url()); });
  return { browser, page, problems, name, close: () => browser.close() };
}

module.exports = { serve, serveEmbedded, open, png };
