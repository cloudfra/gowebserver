// ===== Hover panels =====
// A panel that opens when the pointer rests on something: the
// breadcrumb's folders (crumb-panel.js) and a folder row's slideshow
// over its strip (custom-index/folders.js). They share one dwell, one
// pointer gate and one reveal, so they behave alike, and only one is
// ever open: dwelling on a row closes a breadcrumb's panel.
//
//   var ctl = hoverPanel.attach(container, selector, owner)
//
// watches the elements in container that match selector, its anchors.
// They're looked up as events come, so container's content can be
// rebuilt at will. owner says what to do with them:
//
//   open(anchor, how, from)
//                      Show the panel for anchor. how is 'pointer'
//                      when the pointer rested on it for DWELL,
//                      'focus' when keyboard focus did (with focus
//                      set), or 'key' for ArrowDown on it, when the
//                      panel should take the focus. from is the
//                      owner's anchor the panel is moving from, if
//                      it's moving: it's no longer open.
//   close(anchor)      Hide it.
//   panel()            Optional: the panel, when it isn't inside the
//                      anchor. The pointer or focus in it counts as on
//                      the anchor.
//   when(anchor)       Optional: false leaves anchor alone (a row
//                      without a strip, reduced motion).
//   focus              True: keyboard focus (:focus-visible) and
//                      ArrowDown open it too.
//   move               True: while the panel is open, another of the
//                      owner's anchors takes it at once, without a
//                      dwell (a menu bar's way, for the crumbs). Left
//                      out where anchors touch and each panel costs
//                      something (a row's slideshow): a sweep across
//                      them would open every one.
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
// closes when the pointer leaves the anchor and its panel (one opened
// from the keyboard only once the pointer has been on it), when focus
// leaves them, on Escape, on a press anywhere else, when the anchor
// scrolls out of the window and when another panel opens.
var hoverPanel = (function () {
  // How long the pointer rests on an anchor before its panel opens:
  // longer than a video tile's preview (HOVER_INTENT, grid.js), as a
  // panel that opens by mistake covers more.
  var DWELL = 600, GRACE = 300;
  // Asked the way custom-index/util.js and the CSS hover rules ask.
  var finePointer = matchMedia('(hover: hover) and (pointer: fine), (any-hover: hover) and (any-pointer: fine)');
  var reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
  var owners = [];
  // The dwell before an anchor opens (pending is that anchor), and the
  // grace before the open panel closes. Apart, so the pointer resting on
  // a new anchor doesn't keep the panel it left open.
  var openTimer = 0, pending = null, closeTimer = 0;
  // What's open: {owner, anchor, how, pointed}, pointed once the pointer
  // has been on it.
  var active = null;
  var seen = window.IntersectionObserver ? new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (!e.isIntersecting && active && e.target === active.anchor) closeAll();
    });
  }) : null;

  function canHover(e) {
    return !!finePointer.matches && !(e && e.pointerType === 'touch');
  }
  function stopOpening() {
    clearTimeout(openTimer);
    openTimer = 0;
    pending = null;
  }
  function stopClosing() {
    clearTimeout(closeTimer);
    closeTimer = 0;
  }
  function armOpen(hit, how) {
    stopOpening();
    pending = hit.anchor;
    openTimer = setTimeout(function () {
      openTimer = 0;
      pending = null;
      open(hit.owner, hit.anchor, how);
    }, DWELL);
  }
  function armClose() {
    if (closeTimer) return;
    var g = active.owner.grace;
    closeTimer = setTimeout(function () { closeTimer = 0; close(); }, g === undefined ? GRACE : g);
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
  // Whether anchor of owner o may open now.
  function allowed(o, anchor) {
    return !o.when || o.when(anchor);
  }

  function open(o, anchor, how) {
    stopOpening();
    stopClosing();
    var from = null;
    if (active && active.owner !== o) close();
    if (active) {
      from = active.anchor;
      if (seen) seen.unobserve(from);
      from.classList.remove('hp-anchor');
    }
    active = { owner: o, anchor: anchor, how: how, pointed: how === 'pointer' };
    anchor.classList.add('hp-anchor');
    if (seen) seen.observe(anchor);
    o.open(anchor, how, from === anchor ? null : from);
  }
  function close() {
    stopClosing();
    if (!active) return;
    var a = active;
    active = null;
    if (seen) seen.unobserve(a.anchor);
    a.anchor.classList.remove('hp-anchor');
    a.owner.close(a.anchor);
  }
  // Closes what's open, and what was about to.
  function closeAll() {
    stopOpening();
    close();
  }

  // The pointer (how 'pointer') or focus ('focus') came to el. may says
  // whether it can open a panel; from is where focus came from.
  function arrive(el, how, may, from) {
    if (within(el)) {
      if (how === 'pointer') active.pointed = true;
      stopOpening();
      stopClosing();
      return;
    }
    var hit = may ? anchorOf(el) : null;
    if (hit && (how !== 'focus' || hit.owner.focus) && allowed(hit.owner, hit.anchor)) {
      if (active && active.owner === hit.owner && hit.owner.move) {
        open(hit.owner, hit.anchor, how);
        return;
      }
      if (pending !== hit.anchor) armOpen(hit, how);
    } else {
      stopOpening();
    }
    // Away from what's open.
    if (!active) return;
    if (how === 'focus') {
      if (active.how !== 'pointer' || within(from)) close();
    } else if (active.pointed) {
      armClose();
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
    arrive(e.target, 'focus', focusVisible(e.target), e.relatedTarget);
  });
  document.addEventListener('pointerdown', function (e) {
    if (active && !within(e.target)) closeAll();
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && active) {
      var back = within(document.activeElement) && active.anchor;
      closeAll();
      if (back && back.focus) back.focus();
      e.preventDefault();
      e.stopPropagation();
    } else if (e.key === 'ArrowDown') {
      var hit = anchorOf(e.target);
      if (hit && hit.owner.focus && allowed(hit.owner, hit.anchor)) {
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
    close: closeAll,
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
