// ===== Scenes =====
// The video dock's film strip of scenes (player.js opens it): a row of
// frames over the timeline, one a minute, that the preview over the
// timeline unfolds into when the pointer rises from the timeline up into
// it (vpLift), or a finger scrubbing the timeline drags up off it. It
// fans out of the preview, centered on the time that was previewed, with
// the scene nearest it lit. It scrolls (wheel, trackpad, a drag, a swipe,
// the arrow keys); a scene picked (a click, a tap, Enter) grows out to
// fill the video, which goes there and plays (the dock does that part:
// opts.pick). Moving back down to the timeline, or away, folds it back.
//
// A long video has many scenes, so only the ones in view and a few to
// either side are in the page (vpSceneWindow), and only those ask for
// their picture (opts.paint), the nearest first.
//
// The vpScene* functions are the parts with no page in them.

// A video of at least VP_SCENE_MIN minutes has a scene each minute; a
// shorter one the longest of VP_SCENE_STEPS (seconds) that still gives
// it VP_SCENE_MIN scenes, and one too short for even that VP_SCENE_MIN
// evenly spaced.
var VP_SCENE_MIN = 5;
var VP_SCENE_STEPS = [30, 20, 15, 10, 5];
// How far (px) above the timeline's top the pointer goes before the
// preview unfolds: into the preview's lower part, which sits 10px above
// the timeline. A finger scrubbing has to go further, so a scrub that
// wanders up a little stays a scrub. Sideways, the pointer may stray
// VP_LIFT_SIDE past the preview's edge.
var VP_LIFT_OPEN = 56;
var VP_LIFT_TOUCH = 72;
var VP_LIFT_SIDE = 32;
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

// The scenes to keep in the page with the strip scrolled to scroll and
// view wide: the scenes are pitch apart after pad, and extra are kept
// either side of those showing. [lo, hi), within n.
function vpSceneWindow(scroll, view, pitch, pad, n, extra) {
  if (!(n > 0) || !(pitch > 0)) return [0, 0];
  var lo = Math.floor((scroll - pad) / pitch) - extra;
  var hi = Math.ceil((scroll + view - pad) / pitch) + extra;
  return [Math.max(0, Math.min(n, lo)), Math.max(0, Math.min(n, hi))];
}

// The scroll that puts scene i's middle at anchor (px from the strip's
// left edge), as near as the strip's ends allow: fw wide, pitch apart,
// after pad, with pad after the last too.
function vpSceneScroll(i, n, fw, pitch, pad, anchor, view) {
  var total = pad * 2 + Math.max(0, n - 1) * pitch + fw;
  var x = pad + i * pitch + fw / 2 - anchor;
  return Math.max(0, Math.min(Math.max(0, total - view), Math.round(x)));
}

// Where the pointer is on its way from the timeline to the preview: dy
// px above the timeline's top, dx px from the preview's middle, which is
// half px wide either side. 'rail' on (or below) the timeline, 'away'
// off to the side, 'open' once high enough (at least open px), and
// 'rising' on the way, with how far along (0 to 1).
function vpLift(dy, dx, half, open) {
  if (!(dy > 0)) return { state: 'rail', p: 0 };
  if (Math.abs(dx) > half + VP_LIFT_SIDE) return { state: 'away', p: 0 };
  if (dy >= open) return { state: 'open', p: 1 };
  return { state: 'rising', p: dy / open };
}

// The part of a sw x sh picture to draw into dw x dh so it covers it,
// cut evenly from the sides that don't fit: {x, y, w, h} of the source.
function vpCover(sw, sh, dw, dh) {
  if (!(sw > 0 && sh > 0 && dw > 0 && dh > 0)) return { x: 0, y: 0, w: sw || 0, h: sh || 0 };
  var s = Math.max(dw / sw, dh / sh), w = dw / s, h = dh / s;
  return { x: (sw - w) / 2, y: (sh - h) / 2, w: w, h: h };
}

// SceneStrip makes the strip, in host (the dock's panel), over the
// timeline tl. opts are what it asks of the dock:
//   duration()        the video's length, in seconds
//   aspect()          its width over its height
//   compact()         whether the dock is the compact one (smaller scenes)
//   label(t)          the chapter at time t, or ''
//   paint(t, canvas, done)  draws the scene at t into canvas, now or
//                     later, and calls done once it has; refresh() asks
//                     again for the ones still waiting
//   cancel(canvas)    the scene in canvas left the page: forget it
//   pick(t, card)     the scene at t was picked: card is its picture,
//                     on screen, for the dock to grow from
//   closed(why)       the strip folded away ('down', 'away', 'key',
//                     'pick', 'outside' or 'now')
function SceneStrip(host, tl, opts) {
  var el = doc.createElement('div');
  el.className = 'vd-scenes';
  el.hidden = true;
  el.setAttribute('role', 'listbox');
  el.setAttribute('aria-label', 'Scenes');
  el.setAttribute('aria-orientation', 'horizontal');
  el.innerHTML = '<div class="vd-scenes-view"><div class="vd-scenes-rail"></div></div>';
  host.insertBefore(el, tl);
  var view = el.firstChild, rail = view.firstChild;

  var GAP = 12, PAD = 16;
  var state = 'closed'; // closed, open, closing
  var n = 0, step = 0, fw = 0, fh = 0, pitch = 0, sel = -1, anchorX = 0;
  var mounted = new Map(); // index -> frame element
  var closeTimer = 0, awayTimer = 0, settleTimer = 0, rafPending = false;
  var drag = null, dragged = false;

  function isOpen() { return state === 'open'; }

  function sceneT(i) { return Math.min(i * step, Math.max(0, opts.duration() - 0.05)); }

  // ---- Laying it out ----
  function measure() {
    fw = opts.compact() ? 112 : 160;
    var a = opts.aspect();
    if (!(a > 0)) a = 16 / 9;
    // A tall video's scenes are cut square rather than towering.
    fh = Math.round(Math.max(fw * 0.42, Math.min(fw, fw / a)));
    pitch = fw + GAP;
    el.style.setProperty('--vd-scene-w', fw + 'px');
    el.style.setProperty('--vd-scene-h', fh + 'px');
    // Over the timeline, as wide as it, where the preview was.
    el.style.left = tl.offsetLeft + 'px';
    el.style.width = (tl.offsetWidth || tl.getBoundingClientRect().width) + 'px';
    el.style.bottom = Math.max(0, (host.clientHeight || host.offsetHeight || 0) - tl.offsetTop + 8) + 'px';
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

  // The scenes in view and a few either side are in the page, and no
  // others; the new ones ask for their picture, the nearest to the lit
  // one first.
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
    var added = [];
    for (var i = w[0]; i < w[1]; i++) {
      if (mounted.has(i)) continue;
      var f = frame(i);
      rail.appendChild(f);
      mounted.set(i, f);
      added.push(f);
    }
    added.sort(function (a, b) { return Math.abs(a._i - sel) - Math.abs(b._i - sel); }).forEach(paint);
    el.dataset.mounted = String(mounted.size);
  }

  function schedule() {
    if (rafPending) return;
    rafPending = true;
    requestAnimationFrame(function () { rafPending = false; render(); });
  }

  // ---- Opening and closing ----
  // Opens on time t, with the scene nearest it under x (a client x: the
  // preview's middle), unfolding out of the preview.
  function open(t, x) {
    var dur = opts.duration();
    step = vpSceneStep(dur);
    n = vpSceneCount(dur, step);
    if (!n) return false;
    clearTimeout(closeTimer);
    clearTimeout(settleTimer);
    clearMounted();
    state = 'open';
    sel = vpSceneNearest(t, step, n);
    el.hidden = false;
    el.classList.remove('open', 'closing', 'settled');
    el.dataset.count = String(n);
    el.dataset.step = String(step);
    measure();
    var vr = view.getBoundingClientRect();
    anchorX = x;
    var anchor = x - vr.left;
    if (!(anchor > 0)) anchor = viewWidth() / 2;
    view.scrollLeft = vpSceneScroll(sel, n, fw, pitch, PAD, anchor, viewWidth());
    render();
    // Each scene starts folded into the preview, under the pointer, and
    // fans out to its place: the lit one first, then outward.
    fanFrom(anchorX, 1);
    void el.offsetWidth;
    el.classList.add('open');
    el.setAttribute('aria-activedescendant', 'vd-scene-' + sel);
    // Once out, the stagger is dropped, so a scene lifts at once on hover.
    settleTimer = setTimeout(function () { if (state === 'open') el.classList.add('settled'); }, reducedMotion.matches ? 0 : 700);
    return true;
  }

  // Sets each scene's way back into x: how far it is from there, and how
  // long after the first it moves (dir 1 out from the middle, -1 in).
  function fanFrom(x, dir) {
    var span = 0, rows = [];
    mounted.forEach(function (f, i) {
      var r = f.getBoundingClientRect(), d = Math.abs(i - sel);
      span = Math.max(span, d);
      rows.push([f, (x - (r.left + r.width / 2)) || (sel - i) * pitch, d, i - sel]);
    });
    rows.forEach(function (row) {
      var d = Math.min(row[2], 6);
      row[0].style.setProperty('--fx', Math.round(row[1]) + 'px');
      // Tilted the way it fans, as cards from a hand.
      row[0].style.setProperty('--fr', Math.max(-15, Math.min(15, row[3] * 5)) + 'deg');
      row[0].style.setProperty('--d', String(dir > 0 ? d : Math.min(span, 6) - d));
    });
  }

  function clearMounted() {
    mounted.forEach(function (f) {
      var c = f.querySelector('canvas');
      if (c && !c._painted) opts.cancel(c);
      f.remove();
    });
    mounted.clear();
  }

  // Folds it back: into where it came from, or (a pick) down out of the
  // way of the scene growing out of it. why is told to opts.closed.
  function close(why) {
    if (state !== 'open') {
      if (why === 'now' && state === 'closing') finish();
      return;
    }
    state = 'closing';
    clearTimeout(awayTimer);
    clearTimeout(settleTimer);
    drag = null;
    el.removeAttribute('aria-activedescendant');
    if (why === 'now' || reducedMotion.matches) {
      finish();
    } else {
      fanFrom(why === 'pick' ? NaN : anchorX, -1);
      el.classList.remove('settled');
      el.classList.add('closing');
      el.classList.toggle('drop', why === 'pick');
      el.classList.remove('open');
      closeTimer = setTimeout(finish, 420);
    }
    if (opts.closed) opts.closed(why);
  }

  function finish() {
    clearTimeout(closeTimer);
    state = 'closed';
    clearMounted();
    el.classList.remove('open', 'closing', 'drop', 'settled');
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
  view.addEventListener('scroll', schedule, { passive: true });
  // A mouse wheel scrolls it sideways, as does a trackpad.
  view.addEventListener('wheel', function (e) {
    if (state !== 'open') return;
    if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
      view.scrollLeft += e.deltaY * (e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? viewWidth() : 1);
      schedule();
    }
    e.preventDefault();
  }, { passive: false });
  // A mouse drags it along; a finger scrolls it natively.
  view.addEventListener('pointerdown', function (e) {
    if (state !== 'open' || e.pointerType !== 'mouse' || e.button > 0) return;
    drag = { id: e.pointerId, x: e.clientX, left: view.scrollLeft, moved: false };
    dragged = false;
  });
  view.addEventListener('pointermove', function (e) {
    if (!drag || drag.id !== e.pointerId) return;
    var dx = e.clientX - drag.x;
    if (!drag.moved && Math.abs(dx) > 4) {
      drag.moved = true;
      el.classList.add('dragging');
      try { view.setPointerCapture(e.pointerId); } catch (err) { }
    }
    if (drag.moved) {
      view.scrollLeft = drag.left - dx;
      schedule();
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

  // Moving back down to the timeline folds it at once; straying off it
  // does after a moment, so a pointer overshooting an end isn't punished.
  function pointer(e) {
    if (state !== 'open' || e.pointerType !== 'mouse' || drag) return;
    var r = view.getBoundingClientRect();
    if (!(r.width > 0)) return;
    if (e.clientY > r.bottom + 6 && e.clientX >= r.left - 24 && e.clientX <= r.right + 24) {
      close('down');
      return;
    }
    var out = e.clientX < r.left - 56 || e.clientX > r.right + 56 || e.clientY < r.top - 56 || e.clientY > r.bottom + 6;
    if (!out) { clearTimeout(awayTimer); awayTimer = 0; return; }
    if (!awayTimer) awayTimer = setTimeout(function () { awayTimer = 0; close('away'); }, 240);
  }
  // A press anywhere else folds it, as a menu does.
  function press(e) {
    if (state === 'open' && !el.contains(e.target)) close('outside');
  }

  return {
    el: el, open: open, close: close, key: key, pointer: pointer, press: press,
    isOpen: isOpen, busy: function () { return state !== 'closed'; },
    // The pictures that were waiting may be in now (a sprite loaded).
    refresh: function () { mounted.forEach(paint); },
    lit: function () { return sel; }
  };
}
