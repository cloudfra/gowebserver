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

// The video dock (player.js) on a real video with its metadata: chapters,
// markers in two groups and a stretch, English subtitles, and the preview
// sprite the server makes. The video is a minute of test pattern made
// with ffmpeg, VP9 in MP4 (which every browser here plays), with its
// chapters in the MP4 itself. Without ffmpeg the browser records a few
// seconds of video instead, with its chapters in a file beside it, and
// the parts that need a seekable video or a sprite are skipped.

const test = require('node:test');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { serve, open } = require('../../browser.js');

let srv, b, D = 60, ffmpeg = true;

// Seconds as WebVTT writes them.
function vttTime(s) {
  const m = Math.floor(s / 60), r = s - m * 60;
  return String(m).padStart(2, '0') + ':' + r.toFixed(3).padStart(6, '0');
}

function makeVideo() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'gows-player-'));
  try {
    const meta = path.join(dir, 'chapters.ffmeta'), out = path.join(dir, 'talk.mp4');
    fs.writeFileSync(meta, ';FFMETADATA1\n' +
      '[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=20000\ntitle=Part 1\n' +
      '[CHAPTER]\nTIMEBASE=1/1000\nSTART=20000\nEND=40000\ntitle=Part 2\n' +
      '[CHAPTER]\nTIMEBASE=1/1000\nSTART=40000\nEND=60000\ntitle=Q&A\n');
    execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'lavfi', '-i', 'testsrc2=size=320x180:rate=10:duration=60',
      '-i', meta, '-map', '0:v', '-map_metadata', '1', '-map_chapters', '1',
      '-c:v', 'libvpx-vp9', '-deadline', 'realtime', '-cpu-used', '8', '-b:v', '100k', '-g', '10', '-pix_fmt', 'yuv420p',
      '-movflags', '+faststart', out], { stdio: 'pipe' });
    return fs.readFileSync(out);
  } catch (e) {
    return null;
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// A few seconds of WebM, recorded from a canvas by the browser itself.
async function recordVideo(page, seconds) {
  const bytes = await page.evaluate(async (ms) => {
    const c = document.createElement('canvas');
    c.width = 320;
    c.height = 180;
    const g = c.getContext('2d');
    const rec = new MediaRecorder(c.captureStream(30), { mimeType: 'video/webm' });
    const chunks = [];
    rec.ondataavailable = (e) => chunks.push(e.data);
    const stopped = new Promise((r) => { rec.onstop = r; });
    rec.start();
    const t0 = performance.now();
    await new Promise((done) => {
      (function draw() {
        const t = performance.now() - t0;
        g.fillStyle = 'hsl(' + (t / 10) + ',70%,50%)';
        g.fillRect(0, 0, c.width, c.height);
        if (t < ms) requestAnimationFrame(draw); else done();
      })();
    });
    rec.stop();
    await stopped;
    return Array.from(new Uint8Array(await new Blob(chunks).arrayBuffer()));
  }, seconds * 1000);
  return Buffer.from(bytes);
}

test.before(async () => {
  b = await open({ viewport: { width: 1280, height: 720 } });
  let video = makeVideo();
  const files = {};
  if (!video) {
    ffmpeg = false;
    D = 4;
    await b.page.goto('about:blank');
    video = await recordVideo(b.page, D);
    files['talk.chapters.vtt'] = 'WEBVTT\n\n' + ['Part 1', 'Part 2', 'Q&amp;A'].map((t, i) =>
      vttTime(i * D / 3) + ' --> ' + vttTime((i + 1) * D / 3) + '\n' + t).join('\n\n') + '\n';
  }
  files['talk.mp4'] = video;
  files['talk.markers.json'] = JSON.stringify({
    markers: [
      { time: D * 0.1, kind: 'score', label: 'Home 1–0', group: 'home' },
      { time: D * 0.45, end: D * 0.5, kind: 'stoppage', label: 'Timeout' },
      { time: D * 0.55, kind: 'score', label: 'Away 1–1', group: 'away' },
    ],
    groups: { home: { color: '#2563eb' }, away: { color: '#dc2626' } },
  });
  files['talk.en.vtt'] = 'WEBVTT\n\n' + vttTime(0) + ' --> ' + vttTime(D) + '\nHello from the subtitles\n';
  files['notes.txt'] = 'not a sidecar';
  srv = await serve({ files });
  await b.page.goto(srv.url);
});

test.after(async () => {
  if (b) await b.close();
  if (srv) srv.close();
});

// The current video, as the page has it.
const state = (p) => p.evaluate(() => {
  const dock = document.querySelector('#ss-stage .vdock');
  const v = dock && dock.parentNode.querySelector('video');
  return v && {
    t: v.currentTime, paused: v.paused, dur: v.duration,
    segs: dock.querySelectorAll('.vd-seg').length, ticks: dock.querySelectorAll('.vd-tick').length,
    bands: dock.querySelectorAll('.vd-band').length, colors: Array.from(dock.querySelectorAll('.vd-tick')).map((e) => getComputedStyle(e).backgroundColor),
    chapter: dock.querySelector('.vd-chap').textContent, cap: dock.querySelector('.vd-cap').hidden ? '' : dock.querySelector('.vd-cap').textContent,
    controls: v.controls,
  };
});

async function seekPaused(p, t) {
  await p.evaluate((t) => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    v.pause();
    v.currentTime = t;
  }, t);
  await p.waitForFunction((t) => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return Math.abs(v.currentTime - t) < 0.3 && !v.seeking;
  }, t);
}

test('the listing folds the sidecars into the video\'s tile', async () => {
  const p = b.page;
  const rows = await p.$$eval('#files .row .name', (els) => els.map((e) => e.textContent));
  assert.deepEqual(rows, ['notes.txt'], 'the subtitles and markers are not files of their own');
  const badges = await p.$$eval('.tile[aria-label="talk.mp4"] .side-badge', (els) => els.map((e) => e.textContent));
  assert.deepEqual(badges, ffmpeg ? ['CC', 'Markers'] : ['CC', 'Chapters', 'Markers']);
});

test('a video plays in the dock, with its chapters and markers on the timeline', async () => {
  const p = b.page;
  await p.locator('.tile[aria-label="talk.mp4"]').click();
  await p.waitForSelector('dialog#ss[open] #ss-stage .vdock:not([hidden])');
  await p.waitForFunction(() => document.querySelectorAll('#ss-stage .vd-tick').length === 2);
  if (ffmpeg) await p.waitForFunction(() => document.querySelectorAll('#ss-stage .vd-seg').length === 3);
  const s = await state(p);
  assert.equal(s.controls, false, 'the browser\'s own controls are off');
  assert.equal(s.ticks, 2);
  assert.equal(s.bands, 1, 'the stretch is a band');
  assert.deepEqual(s.colors, ['rgb(37, 99, 235)', 'rgb(220, 38, 38)'], 'in their group\'s color');
  // Played by the dock's button.
  await seekPaused(p, 0);
  await p.click('#ss-stage .vd-play');
  await p.waitForFunction(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return !v.paused && v.currentTime > 0.2;
  });
  assert.equal(await p.getAttribute('#ss-stage .vd-play', 'aria-label'), 'Pause');
  await p.click('#ss-stage .vd-play');
  assert.equal((await state(p)).paused, true);
});

// What the preview box shows now: which picture (the sprite's "image",
// the video's own "frame", or "none"), where it is, and how much is in
// it, from its pixels: the canvas's for a frame, and for the sprite the
// part of the sheet the box shows, drawn as the box draws it.
const preview = (p) => p.evaluate(async () => {
  const dock = document.querySelector('#ss-stage .vdock');
  const kind = dock.dataset.preview, frame = dock.querySelector('.vd-pop-frame');
  if (frame.hidden) return { kind, shown: false };
  const r = frame.getBoundingClientRect();
  let c = dock.querySelector('.vd-pop-canvas');
  if (kind === 'image') {
    const cs = getComputedStyle(dock.querySelector('.vd-pop-img'));
    const im = new Image();
    im.src = cs.backgroundImage.slice(5, -2);
    await im.decode();
    const [x, y] = cs.backgroundPosition.split(' ').map(parseFloat);
    const [w, h] = cs.backgroundSize.split(' ').map(parseFloat);
    c = document.createElement('canvas');
    c.width = Math.round(r.width);
    c.height = Math.round(r.height);
    c.getContext('2d').drawImage(im, x, y, w, h);
  }
  const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
  let lo = 255, hi = 0, sum = 0;
  for (let i = 0; i < d.length; i += 4) {
    const l = (d[i] * 3 + d[i + 1] * 6 + d[i + 2]) / 10;
    lo = Math.min(lo, l);
    hi = Math.max(hi, l);
    sum += l;
  }
  return {
    kind, shown: true, mean: sum / (d.length / 4), spread: hi - lo,
    inside: r.left >= 0 && r.top >= 0 && r.right <= innerWidth && r.bottom <= innerHeight && r.width > 40 && r.height > 20,
  };
});

// Puts the pointer on the timeline at fraction f of it.
async function hoverAt(p, f) {
  const tl = await p.locator('#ss-stage .vd-tl').boundingBox();
  const y = tl.y + tl.height / 2;
  await p.mouse.move(tl.x + tl.width * (f - 0.05), y);
  await p.mouse.move(tl.x + tl.width * f, y, { steps: 4 });
}

function assertPicture(s, what) {
  assert.ok(s.shown, what + ': the box shows: ' + JSON.stringify(s));
  assert.ok(s.inside, what + ': inside the window: ' + JSON.stringify(s));
  assert.ok(s.mean > 8 && s.spread > 24, what + ': and something in it, not a blank box: ' + JSON.stringify(s));
}

test('with a cold cache the preview is the video\'s own frame, then the sprite', async (t) => {
  if (!ffmpeg) return t.skip('needs the server\'s sprite');
  const p = b.page;
  // The sprite as it is while the server makes it: not there yet.
  let release;
  const held = new Promise((r) => { release = r; });
  await p.route(/\?sprite=/, async (route) => { await held; await route.continue(); });
  await seekPaused(p, 1);
  await hoverAt(p, 0.6);
  await p.waitForSelector('#ss-stage .vd-pop:not([hidden])');
  await p.waitForFunction(() => document.querySelector('#ss-stage .vdock').dataset.preview === 'frame', null, { timeout: 15000 });
  assertPicture(await preview(p), 'cold');
  release();
  await p.waitForFunction(() => document.querySelector('#ss-stage .vdock').dataset.preview === 'image', null, { timeout: 30000 });
  await hoverAt(p, 0.62);
  assertPicture(await preview(p), 'warm');
  await p.unroute(/\?sprite=/);
  await p.mouse.move(640, 200);
});

test('without ffmpeg the preview is the video\'s own frame', async () => {
  // The same folder, served by a server that can't find ffmpeg.
  const path_ = process.env.PATH;
  process.env.PATH = path_.split(path.delimiter).filter((d) => !fs.existsSync(path.join(d, 'ffmpeg'))).join(path.delimiter);
  let srv2;
  try {
    srv2 = await serve({ dir: srv.dir });
  } finally {
    process.env.PATH = path_;
  }
  const page = await b.page.context().newPage();
  const sprites = [];
  page.on('request', (r) => { if (/\?sprite/.test(r.url())) sprites.push(r.url()); });
  try {
    await page.goto(srv2.url);
    await page.locator('.tile[aria-label="talk.mp4"]').click();
    await page.waitForSelector('#ss-stage .vdock:not([hidden])');
    await page.waitForFunction(() => document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').readyState >= 1);
    await page.waitForTimeout(300); // the metadata
    await hoverAt(page, 0.4);
    await page.waitForFunction(() => document.querySelector('#ss-stage .vdock').dataset.preview === 'frame', null, { timeout: 15000 });
    assertPicture(await preview(page), 'no ffmpeg');
    assert.deepEqual(sprites, [], 'no sprite is asked for');
  } finally {
    await page.close();
    srv2.close();
  }
});

test('scrubbing the timeline seeks, with a preview and the chapter', async (t) => {
  if (!ffmpeg) return t.skip('needs a seekable video and a sprite');
  const p = b.page;
  await seekPaused(p, 1);
  const tl = await p.locator('#ss-stage .vd-tl').boundingBox();
  const y = tl.y + tl.height / 2;
  // Hovering shows where, before anything moves.
  await p.mouse.move(tl.x + tl.width * 0.2, y);
  await p.mouse.move(tl.x + tl.width * 0.75, y, { steps: 4 });
  await p.waitForSelector('#ss-stage .vd-pop:not([hidden])');
  assert.equal(await p.textContent('#ss-stage .vd-pop-label'), 'Q&A');
  assert.equal(await p.textContent('#ss-stage .vd-pop-time'), '0:45');
  // The preview is a frame of the server's sprite.
  await p.waitForFunction(() => document.querySelector('#ss-stage .vdock').dataset.preview === 'image');
  const bg = await p.$eval('#ss-stage .vd-pop-img', (e) => !e.hidden && e.style.backgroundImage);
  assert.match(bg, /talk\.mp4\?sprite/);
  const sprite = await p.evaluate(async () => {
    const r = await fetch('talk.mp4?sprite');
    return [r.status, r.headers.get('content-type')];
  });
  assert.deepEqual(sprite, [200, 'image/jpeg']);
  // Dragging seeks.
  await p.mouse.down();
  await p.mouse.move(tl.x + tl.width * 0.3, y, { steps: 5 });
  await p.mouse.up();
  await p.waitForFunction(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return !v.seeking && Math.abs(v.currentTime - 18) < 1;
  });
  assert.equal((await state(p)).chapter, 'Part 1');
  await p.mouse.move(640, 200);
});

test('the keys jump to the next marker and chapter', async (t) => {
  if (!ffmpeg) return t.skip('needs a seekable video');
  const p = b.page;
  await seekPaused(p, 1);
  await p.focus('#ss');
  await p.keyboard.press(']');
  await p.waitForFunction(() => Math.abs(document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').currentTime - 6) < 0.3);
  await p.keyboard.press(']');
  await p.waitForFunction(() => Math.abs(document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').currentTime - 27) < 0.3);
  await p.keyboard.press('Shift+BracketRight');
  await p.waitForFunction(() => Math.abs(document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').currentTime - 40) < 0.3);
  assert.equal((await state(p)).chapter, 'Q&A');
  await p.keyboard.press('Shift+BracketLeft');
  await p.waitForFunction(() => Math.abs(document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').currentTime - 20) < 0.3);
  await p.keyboard.press('j');
  await p.waitForFunction(() => Math.abs(document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').currentTime - 10) < 0.3);
  await p.keyboard.press('ArrowRight');
  await p.waitForFunction(() => Math.abs(document.querySelector('#ss-stage .vdock').parentNode.querySelector('video').currentTime - 15) < 0.3);
  assert.equal(await p.textContent('#ss-name'), 'talk.mp4', 'the arrows seek, not change slides');
});

test('the captions menu turns the subtitles on', async () => {
  const p = b.page;
  await p.waitForSelector('#ss-stage .vd-cc:not([hidden])');
  await p.click('#ss-stage .vd-cc');
  const items = await p.$$eval('#ss-stage .vd-menu [role=menuitemradio]', (els) => els.map((e) => e.textContent));
  assert.deepEqual(items, ['Off', 'English']);
  await p.click('#ss-stage .vd-menu [role=menuitemradio]:nth-child(2)');
  assert.equal(await p.getAttribute('#ss-stage .vd-cc', 'aria-pressed'), 'true');
  if (ffmpeg) await seekPaused(p, 12);
  await p.waitForFunction(() => !document.querySelector('#ss-stage .vd-cap').hidden);
  assert.equal((await state(p)).cap, 'Hello from the subtitles');
  // Off again with C.
  await p.focus('#ss');
  await p.keyboard.press('c');
  await p.waitForFunction(() => document.querySelector('#ss-stage .vd-cap').hidden);
});

test('the address carries the video and time, and a link opens it there', async (t) => {
  if (!ffmpeg) return t.skip('needs a seekable video');
  const p = b.page;
  await seekPaused(p, 33);
  await p.waitForFunction(() => location.hash === '#v=talk.mp4&t=0:33');
  assert.equal(new URL(p.url()).search, '', 'the query is left alone');
  await p.keyboard.press('Escape');
  await p.waitForFunction(() => !document.querySelector('dialog#ss[open]'));
  assert.equal(new URL(p.url()).hash, '', 'closing takes it off');

  // A link: a new page opened at the address.
  const page = await b.page.context().newPage();
  await page.goto(srv.url + '#v=talk.mp4&t=0:42');
  await page.waitForSelector('dialog#ss[open] #ss-stage .vdock:not([hidden])');
  await page.waitForFunction(() => {
    const v = document.querySelector('#ss-stage .vdock').parentNode.querySelector('video');
    return v.readyState >= 1 && Math.abs(v.currentTime - 42) < 0.5;
  }, null, { polling: 100 }); // a page behind another gets no animation frames
  assert.equal(await page.textContent('#ss-name'), 'talk.mp4');
  await page.close();
});

test('nothing went wrong along the way', () => {
  assert.deepEqual(b.problems, []);
});
