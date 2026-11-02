/* tidebot WebUI — vanilla JS, no framework. All dynamic text goes through esc(). */
(function () {
  "use strict";
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  // Build version from our own <script src="/static/app.js?v=…">: other static URLs reuse it
  // so the browser can keep them cached until the next build.
  var assetV = (function () {
    var m = /[?&]v=([0-9a-f]+)/.exec((document.currentScript && document.currentScript.src) || "");
    return m ? "?v=" + m[1] : "";
  })();
  var state = { lang: "en", langPref: "auto", dict: {}, csrf: "", tz: "UTC", version: "", es: null, idleTimer: null, lastInput: Date.now(), notifSeen: localStorage.getItem("tb_notif_seen") || "" };
  var IDLE_MS = 30 * 60 * 1000;

  function esc(v) {
    return String(v == null ? "" : v).replace(/[&<>"'`]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;", "`": "&#96;" }[c];
    });
  }
  function enc(v) { return encodeURIComponent(String(v == null ? "" : v)); }

  // ---------- IME (Chinese / Japanese / Korean input) ----------
  // While a candidate window is open, Enter confirms the candidate and Esc cancels it: they
  // belong to the input method, not to us. One capture listener on window runs before every
  // other key handler (palette, dialogs, drawers, pickers, inline edits, list keys), so
  // confirming 你好 with Enter no longer runs the palette's first entry or saves a half-typed
  // name, and Esc no longer closes the dialog. Safari reports the confirming Enter as
  // keyCode 229 with isComposing already false, hence both checks.
  function imeKey(e) { return !!(e && (e.isComposing || e.keyCode === 229)); }
  window.addEventListener("keydown", function (e) { if (imeKey(e)) e.stopImmediatePropagation(); }, true);
  // imeInput: search-as-you-type that waits for the input method. Pinyin letters typed on the
  // way to 天气 never reach the search; the text is committed once on compositionend (some
  // engines fire it before the last input event, so the field is read then), clearing the
  // box applies at once, other edits after ms of quiet. The same text is never sent twice.
  // Idea: CherryHQ/cherry-studio src/renderer/components/GlobalSearch/
  // useImeAwareDebouncedValue.ts (frozen while composing, flushed on compositionend).
  function imeInput(el, ms, fn) {
    var t, composing = false, last = el.value;
    var fire = function () { clearTimeout(t); if (el.value === last) return; last = el.value; fn(el.value); };
    el.addEventListener("compositionstart", function () { composing = true; clearTimeout(t); });
    el.addEventListener("compositionend", function () { composing = false; fire(); });
    el.addEventListener("input", function (e) {
      if (composing || e.isComposing) return;
      clearTimeout(t);
      if (!ms || !el.value) fire(); else t = setTimeout(fire, ms);
    });
  }

  // ---------- keys: one modifier per platform, labels as each OS writes them ----------
  // macOS uses ⌘ for app shortcuts and keeps Control for text editing (Ctrl+K deletes to the end
  // of the line in every text field), Windows and Linux use Ctrl (the Windows / Super key belongs
  // to the OS). modKey() therefore takes only the platform's own modifier, and never Ctrl+Alt:
  // that is AltGr on Windows keyboards (Polish ą, German @), not a shortcut. Labels follow the
  // platform too: ⌘ ⌥ ⇧ ⌃ written together on a Mac (⌘K, ⌘Enter), Ctrl+ / Alt+ / Shift+
  // elsewhere (Ctrl+K); screen readers get the long names. Ideas: microsoft/vscode
  // src/vs/base/common/keybindingLabels.ts (UILabelProvider / AriaLabelProvider per OS) and
  // n8n-io/n8n packages/frontend/@n8n/composables/src/useDeviceSupport.ts (isCtrlKeyPressed:
  // metaKey on macOS, ctrlKey elsewhere).
  var KB_MAC = (function () {
    var n = typeof navigator === "undefined" ? {} : navigator;
    var p = (n.userAgentData && n.userAgentData.platform) || n.platform || n.userAgent || "";
    return /mac|iphone|ipad|ipod/i.test(p);
  })();
  var KEY_NAMES = {
    mac: { Mod: "⌘", Ctrl: "⌃", Alt: "⌥", Shift: "⇧", sep: "" },
    pc: { Mod: "Ctrl", Ctrl: "Ctrl", Alt: "Alt", Shift: "Shift", sep: "+" },
    macAria: { Mod: "Command", Ctrl: "Control", Alt: "Option", Shift: "Shift", sep: "+" },
    pcAria: { Mod: "Control", Ctrl: "Control", Alt: "Alt", Shift: "Shift", sep: "+" }
  };
  // keyLabel("Mod+Enter") → "⌘Enter" on a Mac, "Ctrl+Enter" elsewhere; the last part is the key.
  function keyLabel(combo, aria, mac) {
    var m = KEY_NAMES[(mac == null ? KB_MAC : mac) ? (aria ? "macAria" : "mac") : (aria ? "pcAria" : "pc")];
    var parts = String(combo).split("+"), key = parts.pop();
    return parts.map(function (x) { return m[x] || x; }).join(m.sep) + (parts.length ? m.sep : "") + key;
  }
  // A space separates keys pressed one after another ("g h"): one <kbd> each.
  function kbdHtml(combo) { return String(combo).split(" ").map(function (c) { return '<kbd aria-label="' + esc(keyLabel(c, true)) + '">' + esc(keyLabel(c)) + "</kbd>"; }).join(""); }
  // The platform's shortcut modifier alone (Shift allowed): ⌘ on a Mac, Ctrl elsewhere.
  function modKey(e, mac) {
    if (e.altKey) return false;
    return (mac == null ? KB_MAC : mac) ? !!e.metaKey && !e.ctrlKey : !!e.ctrlKey && !e.metaKey;
  }
  // ⌘/Ctrl+Enter saves or runs a text box. Either modifier is taken here: neither OS gives
  // Enter with it a meaning in a text field, and earlier hints said Ctrl+Enter on every OS.
  function submitKey(e) { return e.key === "Enter" && !!(e.metaKey || e.ctrlKey) && !e.altKey; }
  // The letter a shortcut means, whatever the layout: Caps Lock gives "J", a Russian or Greek
  // layout gives "о" for the same key; e.code still says KeyJ. Other keys pass through.
  function kbLetter(e) {
    var k = e.key || "";
    if (/^[a-z]$/i.test(k)) return e.shiftKey ? k : k.toLowerCase();
    if (k.length === 1 && /[^\x00-\x7f]/.test(k) && /^Key[A-Z]$/.test(e.code || "")) {
      var c = e.code.slice(3);
      return e.shiftKey ? c : c.toLowerCase();
    }
    return k;
  }

