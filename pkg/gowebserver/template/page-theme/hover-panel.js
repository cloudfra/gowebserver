// ===== Hover panels =====
// A panel that opens when the pointer rests on something: the
// breadcrumb's folders (crumb-panel.js) and a folder row's slideshow
// over its strip (custom-index/folders.js). They share one dwell, one
// timer, one pointer gate and one reveal, so they behave alike and only
// one is ever open: dwelling on a row closes a breadcrumb's panel.
//
//   var ctl = hoverPanel.attach(container, selector, owner)
//
// watches the elements in container that match selector, its anchors.
// They're looked up as events come, so container's content can be
// rebuilt at will. owner says what to do with them:
//
//   open(anchor, how)  Show the panel for anchor. how is 'pointer'
//                      when the pointer rested on it for DWELL,
//                      'focus' when keyboard focus did (with focus
//                      set), or 'key' for ArrowDown on it, when the
//                      panel should take the focus. While owner's
//                      panel is open, another of its anchors opens at
//                      once: move the panel there.
//   close(anchor)      Hide it.
//   panel()            Optional: the panel, when it isn't inside the
//                      anchor. The pointer or focus in it counts as on
//                      the anchor.
//   when(anchor)       Optional: false leaves anchor alone (a row
//                      without a strip, reduced motion).
//   focus              True: keyboard focus (:focus-visible) and
//                      ArrowDown open it too.
//   grace              How long, in ms, the pointer may be off the
//                      anchor and its panel before it closes, so a
//                      diagonal move into a panel doesn't close it.
//                      GRACE by default.
//
// ctl.isOpen(anchor) says whether owner's panel is open (for anchor,
// when given); ctl.close() closes it. hoverPanel.close() closes
// whatever's open, and hoverPanel.show(panel, on) reveals or hides a
// panel the shared way (.hover-panel in page-theme.css).
//
// The pointer gate is a mouse or trackpad only, never a finger, even on
// a tablet that has both: finePointer, and no touch event. The panel
// closes when the pointer leaves the anchor and its panel, on Escape, on
// a press anywhere else, when the anchor scrolls out of the window and
// when another panel opens.
var hoverPanel = (function () {
  // How long the pointer rests on an anchor before its panel opens:
  // longer than a video tile's preview (HOVER_INTENT, grid.js), as a
  // panel that opens by mistake covers more.
  var DWELL = 600, GRACE = 300;
  // Asked the way custom-index/util.js and the CSS hover rules ask.
  var finePointer = matchMedia('(hover: hover) and (pointer: fine), (any-hover: hover) and (any-pointer: fine)');
  var reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
  var owners = [];
  // The one timer: a dwell before opening (wait.anchor) or the grace
  // before closing (wait.closing).
  var timer = 0, wait = null;
  // What's open: {owner, anchor, how}.
  var active = null;
  var seen = window.IntersectionObserver ? new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (!e.isIntersecting && active && e.target === active.anchor) close();
    });
  }) : null;

  function canHover(e) {
    return !!finePointer.matches && !(e && e.pointerType === 'touch');
  }
  function arm(fn, ms, what) {
    clearTimeout(timer);
    wait = what;
    timer = setTimeout(function () { timer = 0; wait = null; fn(); }, ms);
  }
  function stop() {
    clearTimeout(timer);
    timer = 0;
    wait = null;
  }
  function stopClosing() {
    if (wait && wait.closing) stop();
  }

  // The anchor el is in, and its owner, or null.
  function anchorOf(el) {
    if (!el || !el.closest) return null;
    for (var i = 0; i < owners.length; i++) {
      var o = owners[i], a = el.closest(o.selector);
      if (a && o.container.contains(a)) return { owner: o, anchor: a };
    }
    return null;
  }
  function panelOf(o) {
    return o.panel ? o.panel() : null;
  }
  // Whether el is on the open anchor or in its panel.
  function within(el) {
    if (!active || !el) return false;
    if (active.anchor.contains(el)) return true;
    var p = panelOf(active.owner);
    return !!(p && p.contains(el));
  }

  function open(o, anchor, how) {
    stop();
    if (active && active.owner !== o) close();
    if (active) {
      if (seen) seen.unobserve(active.anchor);
      active.anchor.classList.remove('hp-anchor');
    }
    active = { owner: o, anchor: anchor, how: how };
    anchor.classList.add('hp-anchor');
    if (seen) seen.observe(anchor);
    o.open(anchor, how);
  }
  function close() {
    stop();
    if (!active) return;
    var a = active;
    active = null;
    if (seen) seen.unobserve(a.anchor);
    a.anchor.classList.remove('hp-anchor');
    a.owner.close(a.anchor);
  }

  // The pointer (how 'pointer') or focus ('focus') came to el. may says
  // whether it can open a panel.
  function arrive(el, how, may) {
    if (within(el)) {
      stopClosing();
      return;
    }
    var hit = may ? anchorOf(el) : null;
    if (hit && (how !== 'focus' || hit.owner.focus) && (!hit.owner.when || hit.owner.when(hit.anchor))) {
      if (active && active.owner === hit.owner) {
        open(hit.owner, hit.anchor, how);
      } else if (!(wait && wait.anchor === hit.anchor)) {
        arm(function () { open(hit.owner, hit.anchor, how); }, DWELL, { anchor: hit.anchor });
      }
      return;
    }
    if (!active) {
      stop();
    } else if (how === 'focus' && active.how !== 'pointer') {
      close();
    } else if (how === 'pointer' && !(wait && wait.closing)) {
      var g = active.owner.grace;
      arm(close, g === undefined ? GRACE : g, { closing: true });
    }
  }

  function focusVisible(el) {
    try { return el.matches(':focus-visible'); } catch (e) { return true; }
  }

  document.addEventListener('pointerover', function (e) {
    arrive(e.target, 'pointer', canHover(e));
  });
  // Off the window altogether.
  document.addEventListener('pointerout', function (e) {
    if (!e.relatedTarget) arrive(null, 'pointer', false);
  });
  document.addEventListener('focusin', function (e) {
    arrive(e.target, 'focus', focusVisible(e.target));
  });
  document.addEventListener('pointerdown', function (e) {
    if (active && !within(e.target)) close();
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && active) {
      var back = within(document.activeElement) && active.anchor;
      close();
      if (back && back.focus) back.focus();
      e.preventDefault();
      e.stopPropagation();
    } else if (e.key === 'ArrowDown') {
      var hit = anchorOf(e.target);
      if (hit && hit.owner.focus && (!hit.owner.when || hit.owner.when(hit.anchor))) {
        open(hit.owner, hit.anchor, 'key');
        e.preventDefault();
      }
    }
  });

  return {
    DWELL: DWELL,
    GRACE: GRACE,
    finePointer: finePointer,
    reducedMotion: reducedMotion,
    canHover: canHover,
    close: close,
    show: function (panel, on) {
      panel.classList.toggle('hp-open', !!on);
    },
    attach: function (container, selector, owner) {
      var o = Object.assign({}, owner, { container: container, selector: selector });
      owners.push(o);
      return {
        isOpen: function (anchor) {
          return !!active && active.owner === o && (!anchor || active.anchor === anchor);
        },
        close: function () {
          if (active && active.owner === o) close();
        },
      };
    },
  };
})();
// The file browser's own scripts (folders.js) run in a scope of their own.
window.gowsHoverPanel = hoverPanel;
