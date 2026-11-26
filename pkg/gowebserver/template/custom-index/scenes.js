// ===== Scenes =====
// The video dock's film strip of scenes (player.js opens it): the
// timeline itself, stretched. While the timeline is held (a mouse press,
// a finger scrubbing), pulling up off it stretches the bar upward into a
// very dark gray strip (the preview's own gray) with the video's colors
// bleeding into it, and the scenes, one a minute, come up out of the bar
// as if pulled: the one nearest the pointer first, then its neighbors on
// either side, one after another (vpChain), as far as the pull has gone.
// It follows the pull with some resistance (vpPull) and, past a point,
// snaps open, the rest of the chain coming up; let go before that and
// it springs back into the bar, the chain run backward, the press a
// plain scrub. Open, the bar is still along
// its bottom, and scrolling the strip (wheel, trackpad, a drag, a
// swipe, the arrow keys) moves a mark along the bar to the time in its
// middle (opts.scan), the reverse of scrubbing; scenes slide in as they
// come into view. The scene nearest the time is ringed in the accent the
// timeline plays in. A scene picked (a click, a tap, Enter) grows out to
// fill the video, which goes there and plays (the dock does that part:
// opts.pick). Swiping or dragging it down folds it back into the bar, as
// do Escape and a press elsewhere: the scenes sink back in the reverse
// of the chain, the farthest first and the nearest last.
//
// A long video has many scenes, so only the ones in view and a few to
// either side are in the page (vpSceneWindow), and only those ask for
// their picture (opts.paint), the nearest first.
//
// The vpScene* and vpPull functions are the parts with no page in them.

// A video of at least VP_SCENE_MIN minutes has a scene each minute; a
// shorter one the longest of VP_SCENE_STEPS (seconds) that still gives
// it VP_SCENE_MIN scenes, and one too short for even that VP_SCENE_MIN
// evenly spaced.
var VP_SCENE_MIN = 5;
var VP_SCENE_STEPS = [30, 20, 15, 10, 5];
// The pull: a press on the timeline that goes VP_PULL_DEAD px above it
// starts stretching it, and VP_PULL_OPEN px further snaps it open. Until
// then the strip shows VP_PULL_GIVE of itself at most, in step with the
// pull, so it lags the pointer as if held back.
var VP_PULL_DEAD = 12;
var VP_PULL_OPEN = 110;
var VP_PULL_GIVE = 0.8;
// How many scenes either side of those in view are kept in the page.
var VP_SCENE_EXTRA = 3;

// The time between scenes for a video lasting dur seconds (0 for none).
function vpSceneStep(dur) {
  if (!(dur > 0)) return 0;
  if (dur >= VP_SCENE_MIN * 60) return 60;
  for (var i = 0; i < VP_SCENE_STEPS.length; i++) {
    if (Math.ceil(dur / VP_SCENE_STEPS[i] - 1e-9) >= VP_SCENE_MIN) return VP_SCENE_STEPS[i];
  }
  return dur / VP_SCENE_MIN;
}

// How many scenes a video lasting dur seconds has, step apart: one at
// 0:00 and each step after it, while before the end.
function vpSceneCount(dur, step) {
  if (!(dur > 0) || !(step > 0)) return 0;
  return Math.max(1, Math.ceil(dur / step - 1e-9));
}

// The scene nearest time t.
function vpSceneNearest(t, step, n) {
  if (!(n > 0)) return -1;
  return Math.max(0, Math.min(n - 1, Math.round((t || 0) / step)));
}

// The scenes to keep in the page with the row scrolled to scroll and
// view wide: the scenes are pitch apart after pad, and extra are kept
// either side of those showing. [lo, hi), within n.
function vpSceneWindow(scroll, view, pitch, pad, n, extra) {
  if (!(n > 0) || !(pitch > 0)) return [0, 0];
  var lo = Math.floor((scroll - pad) / pitch) - extra;
  var hi = Math.ceil((scroll + view - pad) / pitch) + extra;
  return [Math.max(0, Math.min(n, lo)), Math.max(0, Math.min(n, hi))];
}

// The scroll that puts scene i's middle at anchor (px from the row's
// left edge), as near as the row's ends allow: fw wide, pitch apart,
// after pad, with pad after the last too.
function vpSceneScroll(i, n, fw, pitch, pad, anchor, view) {
  var total = pad * 2 + Math.max(0, n - 1) * pitch + fw;
  var x = pad + i * pitch + fw / 2 - anchor;
  return Math.max(0, Math.min(Math.max(0, total - view), Math.round(x)));
}

// The time in the middle of the row scrolled to scroll, view wide (the
// reverse of vpSceneScroll): between two scenes' middles, in step
// between their times, within the video's dur.
function vpSceneScan(scroll, view, fw, pitch, pad, step, dur) {
  var i = (scroll + view / 2 - pad - fw / 2) / pitch;
  return Math.max(0, Math.min(dur, i * step));
}

// A press on the timeline dy px above its top: 'none' (still on it, or
// not far enough to count), 'pulling' with how far along (p, 0 to 1)
// and how much of the strip shows for it (r), or 'open'.
function vpPull(dy, dead, open) {
  var d = dy - dead;
  if (!(d > 0)) return { state: 'none', p: 0, r: 0 };
  if (d >= open) return { state: 'open', p: 1, r: 1 };
  var p = d / open;
  return { state: 'pulling', p: p, r: VP_PULL_GIVE * p };
}

// How far up out of the bar (0 to 1) a scene d scenes from the pointer
// is with the strip r of the way open: the nearest comes first, each a
// little after the one before it (VP_CHAIN_LAG of the pull later), and
// each takes VP_CHAIN_RISE of the pull to come all the way up.
var VP_CHAIN_LAG = 0.12;
var VP_CHAIN_RISE = 0.35;
function vpChain(r, d) {
  return Math.max(0, Math.min(1, (r - Math.max(0, d) * VP_CHAIN_LAG) / VP_CHAIN_RISE));
}

// How long (ms) a scene d scenes from the pointer waits before it moves
// when the strip eases open (out: the nearest first) or closed (the
// farthest, at most far, first), so the chain runs on, or back.
var VP_CHAIN_MS = 40;
// How long folding back takes, the chain's longest wait and its sinking.
var VP_CLOSE_MS = 700;
function vpChainDelay(d, out, far) {
  d = Math.max(0, Math.min(far, d));
  return Math.round((out ? d : far - d) * VP_CHAIN_MS);
}

// The part of a sw x sh picture to draw into dw x dh so it covers it,
// cut evenly from the sides that don't fit: {x, y, w, h} of the source.
function vpCover(sw, sh, dw, dh) {
  if (!(sw > 0 && sh > 0 && dw > 0 && dh > 0)) return { x: 0, y: 0, w: sw || 0, h: sh || 0 };
  var s = Math.max(dw / sw, dh / sh), w = dw / s, h = dh / s;
  return { x: (sw - w) / 2, y: (sh - h) / 2, w: w, h: h };
}

// SceneStrip makes the strip, in host (the dock's panel), behind the
// timeline tl, which stays on top along its bottom. opts are what it
// asks of the dock:
//   duration()        the video's length, in seconds
//   aspect()          its width over its height
//   compact()         whether the dock is the compact one (smaller scenes)
//   label(t)          the chapter at time t, or ''
//   glow(canvas)      draws the video's bottom edge into canvas (its
//                     colors bleed into the strip's top), or doesn't
//   paint(t, canvas, done)  draws the scene at t into canvas, now or
//                     later, and calls done once it has; refresh() asks
//                     again for the ones still waiting
//   cancel(canvas)    the scene in canvas left the page: forget it
//   scan(t)           the strip is scrolled to time t (null: it's gone)
//   pick(t, card)     the scene at t was picked: card is its picture,
//                     on screen, for the dock to grow from
//   closed(why)       the strip folded away ('key', 'pick', 'outside',
//                     'down' or 'now'), or a pull sprang back ('back')
function SceneStrip(host, tl, opts) {
  var el = doc.createElement('div');
  el.className = 'vd-scenes';
  el.hidden = true;
  el.setAttribute('role', 'listbox');
  el.setAttribute('aria-label', 'Scenes');
  el.setAttribute('aria-orientation', 'horizontal');
  el.innerHTML = '<canvas class="vd-scenes-glow" width="48" height="6" aria-hidden="true"></canvas>' +
    '<div class="vd-scenes-view"><div class="vd-scenes-rail"></div></div>';
  host.insertBefore(el, tl);
  var glow = el.firstChild, view = el.lastChild, rail = view.firstChild;

  var GAP = 12, PAD = 16, TOP = 14, BELOW = 6;
  var state = 'closed'; // closed, pulling, open, closing
  var n = 0, step = 0, fw = 0, fh = 0, pitch = 0, sel = -1;
  var base = 0, full = 0, anchorX = NaN, lastScroll = 0;
  var mounted = new Map(); // index -> frame element
  var closeTimer = 0, settleTimer = 0, rafPending = false;
  var drag = null, dragged = false;

  function isOpen() { return state === 'open'; }

  function sceneT(i) { return Math.min(i * step, Math.max(0, opts.duration() - 0.05)); }

  // ---- Laying it out ----
  // The strip is the timeline grown up: its bottom a little below the
  // timeline's, its sides the timeline's, as tall as the bar (base)
  // when folded and the scenes over the bar (full) when open.
  function measure() {
    fw = opts.compact() ? 112 : 160;
    var a = opts.aspect();
    if (!(a > 0)) a = 16 / 9;
    // A tall video's scenes are cut square rather than towering.
    fh = Math.round(Math.max(fw * 0.42, Math.min(fw, fw / a)));
    pitch = fw + GAP;
    var tlH = tl.offsetHeight || 26;
    base = tlH + BELOW * 2;
    full = TOP + fh + base;
    el.style.setProperty('--vd-scene-w', fw + 'px');
    el.style.setProperty('--vd-scene-h', fh + 'px');
    el.style.setProperty('--vd-scenes-base', base + 'px');
    var bw = tl.offsetWidth || tl.getBoundingClientRect().width;
    el.style.left = (tl.offsetLeft - BELOW) + 'px';
    el.style.width = (bw + BELOW * 2) + 'px';
    el.style.bottom = Math.max(0, (host.clientHeight || host.offsetHeight || 0) - tl.offsetTop - tlH - BELOW) + 'px';
    rail.style.width = (PAD * 2 + Math.max(0, n - 1) * pitch + fw) + 'px';
  }

  function viewWidth() {
    return view.clientWidth || view.getBoundingClientRect().width || 0;
  }

  function frame(i) {
    var f = doc.createElement('div');
    f.className = 'vd-scene';
    f.setAttribute('role', 'option');
    f.id = 'vd-scene-' + i;
    f._i = i;
    f.style.left = (PAD + i * pitch) + 'px';
    f.style.setProperty('--d', String(Math.min(Math.abs(i - sel), 6)));
    var t = sceneT(i), title = opts.label(t) || '';
    var long = opts.duration() >= 3600;
    f.innerHTML = '<div class="vd-scene-card"><canvas class="vd-scene-pic"></canvas>' +
      '<span class="vd-scene-chip"><span class="vd-scene-time"></span><span class="vd-scene-title"></span></span></div>';
    f.querySelector('.vd-scene-time').textContent = vpFmtTime(t, long);
    var titleEl = f.querySelector('.vd-scene-title');
    titleEl.textContent = title;
    titleEl.hidden = !title;
    f.setAttribute('aria-label', vpFmtTime(t, long) + (title ? ', ' + title : ''));
    f.setAttribute('aria-selected', String(i === sel));
    if (i === sel) f.classList.add('on');
    var c = f.querySelector('canvas'), dpr = Math.min(window.devicePixelRatio || 1, 2);
    c.width = Math.round(fw * dpr);
    c.height = Math.round(fh * dpr);
    c._t = t;
    return f;
  }

  function paint(f) {
    var c = f.querySelector('canvas');
    if (!c || c._painted) return;
    opts.paint(c._t, c, function () {
      c._painted = true;
      f.classList.add('ready');
    });
  }

  // How far scene f is from x (a client x), in scenes: its place in the
  // chain. Without an x, from the lit scene.
  function place(f) {
    if (!(anchorX > 0)) { f._d = Math.abs(f._i - sel); return; }
    var vr = view.getBoundingClientRect();
    var mid = vr.left + PAD + f._i * pitch + fw / 2 - view.scrollLeft;
    f._d = Math.abs(anchorX - mid) / pitch;
  }

  // Each scene's rise (--p) for the strip r open, and when eased, its
  // wait (--cd), the chain run out or back.
  function chain(r, eased) {
    var far = 0;
    mounted.forEach(function (f) { far = Math.max(far, Math.min(f._d || 0, 8)); });
    mounted.forEach(function (f) {
      var d = f._d || 0;
      f.style.setProperty('--p', String(eased ? (r > 0 ? 1 : 0) : Math.round(vpChain(r, d) * 1000) / 1000));
      f.style.setProperty('--cd', (eased ? vpChainDelay(d, r > 0, far) : 0) + 'ms');
    });
  }

  // The scenes in view and a few either side are in the page, and no
  // others; the new ones ask for their picture, the nearest to the lit
  // one first. Open, a scene coming in slides in from the side the strip
  // is scrolling from.
  function render() {
    if (state === 'closed') return;
    var w = vpSceneWindow(view.scrollLeft, viewWidth(), pitch, PAD, n, VP_SCENE_EXTRA);
    mounted.forEach(function (f, i) {
      if (i >= w[0] && i < w[1]) return;
      var c = f.querySelector('canvas');
      if (c && !c._painted) opts.cancel(c);
      f.remove();
      mounted.delete(i);
    });
    var added = [], dir = view.scrollLeft - lastScroll;
    lastScroll = view.scrollLeft;
    for (var i = w[0]; i < w[1]; i++) {
      if (mounted.has(i)) continue;
      var f = frame(i);
      place(f);
      if (state === 'open') {
        // Open, a scene coming in is already up, and slides in.
        f.style.setProperty('--p', '1');
        if (dir) {
          f.style.setProperty('--ex', (dir > 0 ? 36 : -36) + 'px');
          f.classList.add('enter');
        }
      }
      rail.appendChild(f);
      mounted.set(i, f);
      added.push(f);
    }
    added.sort(function (a, b) { return Math.abs(a._i - sel) - Math.abs(b._i - sel); }).forEach(paint);
    el.dataset.mounted = String(mounted.size);
  }

  // Scrolled: the scenes now near the view, and the bar's mark.
  function scrolled() {
    if (rafPending) return;
    rafPending = true;
    requestAnimationFrame(function () {
      rafPending = false;
      render();
      if (state === 'open') opts.scan(vpSceneScan(view.scrollLeft, viewWidth(), fw, pitch, PAD, step, opts.duration()));
    });
  }

  // ---- Stretching and folding ----
  // How far stretched: r (0 to 1) of the way from the bar to open, at
  // once (with the pull) or eased. The strip makes room a little ahead
  // of the scenes, which come up into it in their chain.
  function show(r, eased) {
    el.classList.toggle('eased', !!eased);
    el.style.setProperty('--u', String(Math.round(r * 1000) / 1000));
    el.style.height = Math.round(base + (full - base) * Math.min(1, r * 1.5)) + 'px';
    chain(r, eased);
  }

  // Lays out the row on time t, with the scene nearest it under client
  // x (or in the middle), folded into the bar: ready to stretch.
  function prepare(t, x) {
    var dur = opts.duration();
    step = vpSceneStep(dur);
    n = vpSceneCount(dur, step);
    if (!n) return false;
    clearTimeout(closeTimer);
    clearTimeout(settleTimer);
    clearMounted();
    sel = vpSceneNearest(t, step, n);
    el.hidden = false;
    el.classList.remove('open', 'settled', 'eased');
    el.dataset.count = String(n);
    el.dataset.step = String(step);
    opts.glow(glow);
    measure();
    var vr = view.getBoundingClientRect();
    anchorX = x == null ? NaN : x;
    var anchor = x == null ? NaN : x - vr.left;
    if (!(anchor > 0 && anchor < viewWidth())) anchor = viewWidth() / 2;
    view.scrollLeft = vpSceneScroll(sel, n, fw, pitch, PAD, anchor, viewWidth());
    lastScroll = view.scrollLeft;
    state = 'pulling';
    render();
    show(0, false);
    return true;
  }

  // The pull is r of the way (see vpPull): the strip follows it. The
  // first pull lays the row out, on time t under x.
  function pull(r, t, x) {
    if (state === 'open') return;
    if (state !== 'pulling' && !(r > 0 && prepare(t, x))) return;
    show(r, false);
  }

  // Let go before it opened: back into the bar.
  function release() {
    if (state !== 'pulling') return;
    state = 'closing';
    show(0, true);
    closeTimer = setTimeout(finish, reducedMotion.matches ? 0 : VP_CLOSE_MS);
    if (opts.closed) opts.closed('back');
  }

  // Opens on time t (scene nearest it under client x), from where a
  // pull left it, or from the bar.
  function open(t, x) {
    if (state === 'open') return true;
    if (state !== 'pulling' && !prepare(t, x)) return false;
    state = 'open';
    void el.offsetWidth;
    show(1, true);
    el.classList.add('open');
    el.setAttribute('aria-activedescendant', 'vd-scene-' + sel);
    opts.scan(sceneT(sel));
    // Once open, the scenes' stagger is dropped, so one lifts at once on
    // hover.
    settleTimer = setTimeout(function () { if (state === 'open') el.classList.add('settled'); }, reducedMotion.matches ? 0 : 700);
    return true;
  }

  function clearMounted() {
    mounted.forEach(function (f) {
      var c = f.querySelector('canvas');
      if (c && !c._painted) opts.cancel(c);
      f.remove();
    });
    mounted.clear();
  }

  // Folds it back into the bar, the stretch run backward. why is told to
  // opts.closed.
  function close(why) {
    if (state === 'closed') return;
    if (state === 'closing') {
      if (why === 'now') finish();
      return;
    }
    state = 'closing';
    clearTimeout(settleTimer);
    drag = null;
    el.removeAttribute('aria-activedescendant');
    // The chain runs back from the lit (or picked) one: it goes last.
    var lit = mounted.get(sel);
    if (lit) {
      var r = lit.getBoundingClientRect();
      anchorX = r.left + r.width / 2;
      mounted.forEach(place);
    }
    el.classList.remove('open', 'settled');
    opts.scan(null);
    if (why === 'now') {
      show(0, false);
      finish();
    } else {
      show(0, true);
      closeTimer = setTimeout(finish, reducedMotion.matches ? 0 : VP_CLOSE_MS);
    }
    if (opts.closed) opts.closed(why);
  }

  function finish() {
    clearTimeout(closeTimer);
    state = 'closed';
    clearMounted();
    el.classList.remove('open', 'settled', 'eased');
    el.hidden = true;
    el.dataset.mounted = '0';
  }

  // ---- Choosing ----
  function light(i, scroll) {
    if (!(n > 0)) return;
    i = Math.max(0, Math.min(n - 1, i));
    var was = mounted.get(sel);
    if (was) { was.classList.remove('on'); was.setAttribute('aria-selected', 'false'); }
    sel = i;
    if (scroll) {
      var left = PAD + i * pitch, vw = viewWidth(), to = view.scrollLeft;
      if (left < to + PAD) to = left - PAD;
      else if (left + fw > to + vw - PAD) to = left + fw - vw + PAD;
      if (to !== view.scrollLeft) {
        if (view.scrollTo && !reducedMotion.matches) view.scrollTo({ left: to, behavior: 'smooth' });
        else view.scrollLeft = to;
      }
      render();
    }
    var f = mounted.get(sel);
    if (f) { f.classList.add('on'); f.setAttribute('aria-selected', 'true'); }
    el.setAttribute('aria-activedescendant', 'vd-scene-' + sel);
    opts.scan(sceneT(sel));
  }

  function pick(i) {
    if (state !== 'open' || !(n > 0)) return;
    light(i, false);
    var f = mounted.get(sel);
    var card = f && f.querySelector('.vd-scene-pic');
    if (f) f.classList.add('picked');
    opts.pick(sceneT(sel), card || null);
    close('pick');
  }

  // A key while it's open: true when it was the strip's.
  function key(e) {
    if (state !== 'open') return false;
    var per = Math.max(1, Math.floor(viewWidth() / pitch));
    switch (e.key) {
      case 'Escape': close('key'); return true;
      case 'ArrowLeft': light(sel - 1, true); return true;
      case 'ArrowRight': light(sel + 1, true); return true;
      case 'PageUp': light(sel - per, true); return true;
      case 'PageDown': light(sel + per, true); return true;
      case 'Home': light(0, true); return true;
      case 'End': light(n - 1, true); return true;
      case 'Enter': case ' ': pick(sel); return true;
      // The timeline's keys and the slideshow's would act under the
      // strip; it's the strip's while it's up.
      case 'ArrowUp': case 'ArrowDown': return true;
    }
    return false;
  }

  // ---- The pointer ----
  view.addEventListener('scroll', scrolled, { passive: true });
  // A mouse wheel scrolls it sideways, as does a trackpad.
  view.addEventListener('wheel', function (e) {
    if (state !== 'open') return;
    if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
      view.scrollLeft += e.deltaY * (e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? viewWidth() : 1);
      scrolled();
    }
    e.preventDefault();
  }, { passive: false });
  // A mouse drags it along (a finger scrolls it natively); either one
  // swiping it down folds it back into the bar.
  view.addEventListener('pointerdown', function (e) {
    if (state !== 'open' || e.button > 0) return;
    drag = { id: e.pointerId, x: e.clientX, y: e.clientY, left: view.scrollLeft, moved: false, mouse: e.pointerType === 'mouse' };
    dragged = false;
  });
  view.addEventListener('pointermove', function (e) {
    if (!drag || drag.id !== e.pointerId) return;
    var dx = e.clientX - drag.x, dy = e.clientY - drag.y;
    if (dy > 40 && dy > Math.abs(dx) * 1.5) {
      drag = null;
      dragged = true;
      close('down');
      return;
    }
    if (!drag.mouse) return;
    if (!drag.moved && Math.abs(dx) > 4) {
      drag.moved = true;
      el.classList.add('dragging');
      try { view.setPointerCapture(e.pointerId); } catch (err) { }
    }
    if (drag.moved) {
      view.scrollLeft = drag.left - dx;
      scrolled();
    }
  });
  function endDrag(e) {
    if (!drag || drag.id !== e.pointerId) return;
    dragged = drag.moved;
    drag = null;
    el.classList.remove('dragging');
  }
  view.addEventListener('pointerup', endDrag);
  view.addEventListener('pointercancel', endDrag);
  rail.addEventListener('click', function (e) {
    if (dragged) { dragged = false; return; }
    var f = e.target.closest && e.target.closest('.vd-scene');
    if (f && state === 'open') pick(f._i);
  });

  // A press anywhere else folds it, as a menu does.
  function press(e) {
    if (state === 'open' && !el.contains(e.target)) close('outside');
  }

  return {
    el: el, open: open, pull: pull, release: release, close: close, key: key, press: press,
    isOpen: isOpen, busy: function () { return state !== 'closed'; },
    pulling: function () { return state === 'pulling'; },
    // The pictures that were waiting may be in now (a sprite loaded).
    refresh: function () { mounted.forEach(paint); },
    lit: function () { return sel; }
  };
}
