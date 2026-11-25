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
  if (!opts.dir) {
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

// ---- A long video, made quickly ----
// A browser records video only as fast as it plays, which is too slow
// for minutes of it, and ffmpeg may not be installed. So the browser
// encodes the frames itself (WebCodecs, VP8, every frame a keyframe, so
// any time seeks at once), as fast as it can, and they're put in a WebM
// here, with the cues a player seeks by.

// An EBML element: its ID's bytes, its size, then its body.
function ebml(id, body) {
  body = Buffer.isBuffer(body) ? body : Buffer.concat(body);
  const n = body.length;
  let len = 1;
  while (len < 8 && n >= 2 ** (7 * len) - 1) len++;
  const size = Buffer.alloc(len);
  let v = n;
  for (let i = len - 1; i >= 0; i--) {
    size[i] = v % 256;
    v = Math.floor(v / 256);
  }
  size[0] |= 1 << (8 - len);
  const idBytes = [];
  for (let x = id; x > 0; x = Math.floor(x / 256)) idBytes.unshift(x % 256);
  return Buffer.concat([Buffer.from(idBytes), size, body]);
}

// An unsigned integer, in width bytes (or as few as it needs).
function uint(n, width) {
  const out = [];
  for (let x = n; x > 0 || out.length === 0; x = Math.floor(x / 256)) out.unshift(x % 256);
  while (width && out.length < width) out.unshift(0);
  return Buffer.from(out);
}

function float64(x) {
  const b = Buffer.alloc(8);
  b.writeDoubleBE(x);
  return b;
}

// frames are [{ms, data}], keyframes in order; the video lasts dur ms.
function webm(frames, w, h, dur) {
  const head = ebml(0x1A45DFA3, [
    ebml(0x4286, uint(1)), ebml(0x42F7, uint(1)), ebml(0x42F2, uint(4)), ebml(0x42F3, uint(8)),
    ebml(0x4282, Buffer.from('webm')), ebml(0x4287, uint(2)), ebml(0x4285, uint(2)),
  ]);
  const info = ebml(0x1549A966, [
    ebml(0x2AD7B1, uint(1000000)), ebml(0x4D80, Buffer.from('gows')), ebml(0x5741, Buffer.from('gows')), ebml(0x4489, float64(dur)),
  ]);
  const tracks = ebml(0x1654AE6B, [ebml(0xAE, [
    ebml(0xD7, uint(1)), ebml(0x73C5, uint(1)), ebml(0x83, uint(1)), ebml(0x86, Buffer.from('V_VP8')),
    ebml(0xE0, [ebml(0xB0, uint(w)), ebml(0xBA, uint(h))]),
  ])]);
  // A cluster for each frame, so each has a cue of its own.
  const clusters = frames.map((f) => ebml(0x1F43B675, [
    ebml(0xE7, uint(f.ms)),
    ebml(0xA3, Buffer.concat([Buffer.from([0x81, 0, 0, 0x80]), f.data])),
  ]));
  // The cues come before the clusters, with their positions written
  // in a fixed width, so their own size is known before the positions.
  const cues = (at) => ebml(0x1C53BB6B, frames.map((f, i) => ebml(0xBB, [
    ebml(0xB3, uint(f.ms)), ebml(0xB7, [ebml(0xF7, uint(1)), ebml(0xF1, uint(at[i], 8))]),
  ])));
  const zeros = frames.map(() => 0);
  let pos = info.length + tracks.length + cues(zeros).length;
  const at = clusters.map((c) => { const p = pos; pos += c.length; return p; });
  return Buffer.concat([head, ebml(0x18538067, [info, tracks, cues(at)].concat(clusters))]);
}

// A WebM video lasting seconds, a frame every step seconds, w x h, made
// in page (any page will do). Each minute is a scene of its own color,
// with its minute in large type and the time beneath, so a frame shows
// which time it is. null when the browser can't encode VP8.
async function longVideo(page, seconds, step, w, h) {
  const frames = await page.evaluate(async ([seconds, step, w, h]) => {
    if (!window.VideoEncoder) return null;
    const c = new OffscreenCanvas(w, h), g = c.getContext('2d');
    const out = [];
    let failed = null;
    const enc = new VideoEncoder({
      output: (chunk) => {
        const b = new Uint8Array(chunk.byteLength);
        chunk.copyTo(b);
        let s = '';
        for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode.apply(null, b.subarray(i, i + 0x8000));
        out.push({ ms: Math.round(chunk.timestamp / 1000), data: btoa(s) });
      },
      error: (e) => { failed = e; },
    });
    const config = { codec: 'vp8', width: w, height: h, bitrate: 600000, framerate: 1 / step };
    try {
      if (!(await VideoEncoder.isConfigSupported(config)).supported) return null;
    } catch (e) {
      return null;
    }
    enc.configure(config);
    for (let t = 0; t < seconds; t += step) {
      const m = Math.floor(t / 60), s = t % 60, hue = (m * 47) % 360;
      const grad = g.createLinearGradient(0, 0, w, h);
      grad.addColorStop(0, 'hsl(' + hue + ',75%,22%)');
      grad.addColorStop(1, 'hsl(' + ((hue + 60) % 360) + ',85%,52%)');
      g.fillStyle = grad;
      g.fillRect(0, 0, w, h);
      for (let k = 0; k < 7; k++) {
        const a = (k / 7) * Math.PI * 2 + s / 60 * Math.PI * 2;
        g.fillStyle = 'hsla(' + ((hue + k * 40) % 360) + ',90%,70%,0.55)';
        g.beginPath();
        g.arc(w / 2 + Math.cos(a) * w * 0.3, h / 2 + Math.sin(a) * h * 0.3, h * (0.08 + k * 0.012), 0, Math.PI * 2);
        g.fill();
      }
      g.fillStyle = '#fff';
      g.textAlign = 'center';
      g.font = 'bold ' + Math.round(h * 0.32) + 'px sans-serif';
      g.fillText('Scene ' + (m + 1), w / 2, h * 0.52);
      g.font = Math.round(h * 0.11) + 'px monospace';
      g.fillText(m + ':' + String(Math.floor(s)).padStart(2, '0'), w / 2, h * 0.75);
      const frame = new VideoFrame(c, { timestamp: Math.round(t * 1e6), duration: Math.round(step * 1e6) });
      enc.encode(frame, { keyFrame: true });
      frame.close();
      if (enc.encodeQueueSize > 8) await new Promise((r) => setTimeout(r, 0));
    }
    await enc.flush();
    enc.close();
    return failed ? null : out;
  }, [seconds, step, w, h]);
  if (!frames || !frames.length) return null;
  return webm(frames.map((f) => ({ ms: f.ms, data: Buffer.from(f.data, 'base64') })), w, h, seconds * 1000);
}

module.exports = { serve, open, png, longVideo };
