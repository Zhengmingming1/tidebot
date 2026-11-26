  // ---------- tooltips ----------
  // [data-tip] text used to be shown only by a CSS ::after on the element itself, so the window
  // edge or any ancestor with overflow hidden (the home KPI strip, cards, table scrollers) cut
  // it off: a sweep of the main pages at 1024×655 (scripts/tip-check.py) found 13 such tips per
  // language. One fixed layer on <body> now shows the text instead. It is never clipped by a
  // container, opens above the element (or below where the page's CSS put that tip below),
  // flips when there is no room and shifts to stay 8px inside the window. The ::after stays as
  // the fallback until this has run. Ideas: radix-ui/primitives
  // packages/react/tooltip/src/tooltip.tsx (content portalled out of the trigger's tree, closed
  // when the trigger scrolls) and floating-ui/floating-ui packages/core/src/middleware/flip.ts
  // and shift.ts (flip on collision, clamp into the boundary).
  var TIP_GAP = 6, TIP_PAD = 8;
  // tipPlace: top-left corner of a w×h tip for an element at rect r in a vw×vh window.
  function tipPlace(r, w, h, vw, vh, below) {
    var up = r.top - TIP_GAP - h >= TIP_PAD, down = r.bottom + TIP_GAP + h <= vh - TIP_PAD;
    if (below ? !down && up : !up && down) below = !below;
    var x = r.left + r.width / 2 - w / 2, y = below ? r.bottom + TIP_GAP : r.top - TIP_GAP - h;
    x = Math.max(TIP_PAD, Math.min(x, vw - TIP_PAD - w));
    y = Math.max(TIP_PAD, Math.min(y, vh - TIP_PAD - h));
    return { x: Math.round(x), y: Math.round(y), below: below };
  }
  var tipSt = { el: null, on: null, timer: null, mo: null, aria: false };
  function tipKeyFocus(t) { return t === document.activeElement && t.matches(":focus-visible"); }
  function tipTarget(n) {
    var t = n && n.nodeType === 1 ? n : n && n.parentElement;
    t = t && t.closest ? t.closest("[data-tip]") : null;
    return t && t.id !== "tip-layer" ? t : null;
  }
  function tipHide() {
    if (!tipSt.on) return;
    if (tipSt.aria) tipSt.on.removeAttribute("aria-describedby");
    tipSt.on = null; tipSt.aria = false;
    clearInterval(tipSt.timer); tipSt.timer = null;
    if (tipSt.mo) { tipSt.mo.disconnect(); tipSt.mo = null; }
    if (tipSt.el) tipSt.el.hidden = true;
  }
  function tipShow(t) {
    var text = t.getAttribute("data-tip") || "";
    var cs = getComputedStyle(t, "::after");
    // no tip over a button whose own menu or popover is open (it would cover the menu)
    if (!text.trim() || t.getAttribute("aria-expanded") === "true") { tipHide(); return; }
    if (tipSt.on !== t) {
      tipHide();
      tipSt.on = t;
      if (!t.hasAttribute("aria-describedby")) { t.setAttribute("aria-describedby", "tip-layer"); tipSt.aria = true; }
      // the text can change while shown (copied, live counts); a re-render can drop the element
      if (typeof MutationObserver === "function") {
        tipSt.mo = new MutationObserver(function () { if (tipSt.on === t) tipShow(t); });
        tipSt.mo.observe(t, { attributes: true, attributeFilter: ["data-tip", "aria-expanded"] });
      }
      tipSt.timer = setInterval(function () {
        if (!t.isConnected || !(t.matches(":hover") || tipKeyFocus(t))) tipHide();
      }, 400);
    }
    var L = tipSt.el;
    if (!L) {
      L = tipSt.el = document.createElement("div");
      L.id = "tip-layer"; L.className = "tip-layer"; L.setAttribute("role", "tooltip");
      document.body.appendChild(L);
    }
    var vw = document.documentElement.clientWidth || window.innerWidth, vh = window.innerHeight;
    var mw = parseFloat(cs.maxWidth);
    L.style.maxWidth = Math.min(isNaN(mw) || mw <= 0 ? 280 : mw, vw - 2 * TIP_PAD) + "px";
    L.style.whiteSpace = cs.whiteSpace === "nowrap" ? "nowrap" : "pre-line";
    L.textContent = text;
    L.style.left = "0px"; L.style.top = "0px"; L.hidden = false;
    if (L.scrollWidth > L.clientWidth + 1) L.style.whiteSpace = "pre-line"; // one line does not fit
    var p = tipPlace(t.getBoundingClientRect(), L.offsetWidth, L.offsetHeight, vw, vh, cs.top !== "auto" && cs.bottom === "auto");
    L.style.left = p.x + "px"; L.style.top = p.y + "px";
    L.setAttribute("data-side", p.below ? "below" : "above");
  }
  document.documentElement.classList.add("tip-js");
  document.addEventListener("pointerover", function (e) {
    if (e.pointerType === "touch") return;
    var t = tipTarget(e.target);
    if (t) { if (t !== tipSt.on) tipShow(t); } else if (tipSt.on && !tipKeyFocus(tipSt.on)) tipHide();
  });
  document.addEventListener("pointerout", function (e) {
    if (tipSt.on && !e.relatedTarget && !tipKeyFocus(tipSt.on)) tipHide(); // left the window
  });
  document.addEventListener("focusin", function (e) {
    var t = tipTarget(e.target);
    if (t && t === e.target && t.matches(":focus-visible")) tipShow(t);
  });
  document.addEventListener("focusout", function (e) { if (tipSt.on && e.target === tipSt.on) tipHide(); });
  // the element moved with a scroll of the page or of a box around it (a log tailing elsewhere
  // does not count): follow it while the pointer or keyboard focus is still on it, else close
  var tipRaf = 0;
  window.addEventListener("scroll", function (e) {
    var s = e.target;
    if (!tipSt.on || tipRaf || !(s === document || s === window || (s.contains && s.contains(tipSt.on)))) return;
    tipRaf = requestAnimationFrame(function () {
      tipRaf = 0;
      var t = tipSt.on;
      if (t && t.isConnected && (t.matches(":hover") || tipKeyFocus(t))) tipShow(t); else tipHide();
    });
  }, true);
  window.addEventListener("resize", tipHide);
  window.addEventListener("hashchange", tipHide);
  document.addEventListener("keydown", function (e) { if (e.key === "Escape") tipHide(); });
