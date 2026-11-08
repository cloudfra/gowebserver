// ===== Thumbnail loading =====
// A browser opens at most six HTTP/1.1 connections to a server, and a
// thumbnail the server still has to make holds one until it's made.
// Giving every tile its src at once fills all six with the rest
// queued behind them, and a video, a slide or the next page waits at
// the back of that queue for seconds. So every thumbnail on the page
// goes through this one queue, which fetches them a batch at a time,
// always leaving connections free for everything else. HTTP/2 and 3
// share one connection, so there the batch size is only about not
// flooding the server.
// A batch is also shown together: its pictures are handed back in a
// single frame once they have all arrived, so a screen of tiles
// fills in a few steps, not one style change and paint per picture.
// A batch waits at most BATCH_WAIT for its slowest picture; what has
// arrived by then is shown, and the rest joins the next batch.
// A video's thumbnail can take the server seconds to make, where a
// photo's takes a fraction of one, so videos ("heavy" ones) get at most
// half the slots: a folder of videos being thumbnailed for the first
// time never holds up its photos, or the thumbnails already made.
var thumbLoader = (function () {
  var queue = [], arrived = [], loading = 0, heavy = 0, limit = 4, timer = 0, pumping = false;
  var BATCH_WAIT = 1500;
  try {
    var nav = performance.getEntriesByType('navigation')[0];
    if (nav && /^h[23]/.test(nav.nextHopProtocol)) limit = 12;
  } catch (e) { }
  var heavyLimit = Math.max(1, Math.floor(limit / 2));

  // Fills the batch's free slots from the queue, in order, passing over
  // heavy images while they have their share; opens a batch if none is.
  function pump() {
    pumping = false;
    var started = false;
    for (var i = 0; i < queue.length && loading < limit;) {
      if (queue[i]._thumbHeavy && heavy >= heavyLimit) { i++; continue; }
      start(queue.splice(i, 1)[0]);
      started = true;
    }
    if (started && !timer) timer = setTimeout(close, BATCH_WAIT);
  }

  // Pumps once everything being queued right now has been, so a
  // screen of tiles queued one by one doesn't start a batch of one.
  function pumpSoon() {
    if (pumping) return;
    pumping = true;
    Promise.resolve().then(pump);
  }

  function start(img) {
    loading++;
    if (img._thumbHeavy) heavy++;
    img._thumb = 'loading';
    img._thumbEnd = function (e) { arrive(img, e.type === 'load'); };
    img.addEventListener('load', img._thumbEnd);
    img.addEventListener('error', img._thumbEnd);
    img.src = img._thumbURL;
  }

  // Stops listening to img, which is no longer loading.
  function stop(img) {
    img.removeEventListener('load', img._thumbEnd);
    img.removeEventListener('error', img._thumbEnd);
    img._thumbEnd = null;
    loading--;
    if (img._thumbHeavy) heavy--;
  }

  function arrive(img, ok) {
    stop(img);
    img._thumb = 'arrived';
    img._thumbOK = ok;
    arrived.push(img);
    settle();
  }

  // Closes the batch once nothing in it is still loading. With no
  // batch open, img was left over from one that timed out: it's
  // shown now, and its slot goes to the next batch.
  function settle() {
    if (!loading || !timer) close();
  }

  // Ends the batch: shows what arrived, in one frame, and starts the
  // next one. That waits for whatever is canceling images to finish:
  // a row scrolled away cancels its tiles one by one, and starting the
  // next batch in the middle would fetch tiles about to be canceled.
  function close() {
    clearTimeout(timer);
    timer = 0;
    if (arrived.length) {
      var batch = arrived;
      arrived = [];
      requestAnimationFrame(function () {
        batch.forEach(function (img) {
          // Canceled since it arrived.
          if (img._thumb !== 'arrived') return;
          img._thumb = '';
          img._thumbDone(img._thumbOK);
        });
      });
    }
    pumpSoon();
  }

  // Takes img out of the queue; false when it wasn't waiting there.
  function unqueue(img) {
    var i = queue.indexOf(img);
    if (i < 0) return false;
    queue.splice(i, 1);
    img._thumb = '';
    return true;
  }

  return {
    // Loads url into img in an upcoming batch, then calls done with
    // whether it loaded, along with the rest of its batch. front
    // puts it ahead of everything waiting; heavy is a video's
    // thumbnail, which gets at most half the slots.
    load: function (img, url, done, front, heavy) {
      img._thumbURL = url;
      img._thumbHeavy = !!heavy;
      img._thumbDone = done;
      img._thumb = 'queued';
      if (front) queue.unshift(img); else queue.push(img);
      pumpSoon();
    },
    unqueue: unqueue,
    // Stops img loading, whether it's waiting or under way; done
    // isn't called.
    cancel: function (img) {
      if (unqueue(img)) return;
      var was = img._thumb;
      img._thumb = '';
      img.removeAttribute('src');
      if (was === 'loading') { stop(img); settle(); }
    }
  };
})();
