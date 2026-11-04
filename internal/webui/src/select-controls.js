  // Keep native selects as the form/value/validation source. Only the popup is custom:
  // no mirrored values, hidden required fields, or rebuilt controls on every render.
  var selectPopup = null, selectPopupSeq = 0;
  function closeSelectPopup() {
    if (!selectPopup) return;
    var p = selectPopup; selectPopup = null;
    p.observer.disconnect(); p.box.remove();
    p.select.removeAttribute("aria-controls");
    p.select.removeAttribute("aria-activedescendant");
    p.select.removeAttribute("aria-expanded");
  }
  function popupSelect(el) {
    return el && el.tagName === "SELECT" && !el.multiple && el.size <= 1 && !el.disabled && !el.closest("[data-native-select]");
  }
  function openSelectPopup(select) {
    closeSelectPopup();
    var options = Array.from(select.options), enabled = options.filter(function (o) { return !o.disabled && !o.hidden && !(o.parentElement.tagName === "OPTGROUP" && o.parentElement.disabled); });
    if (!enabled.length) return;
    var box = document.createElement("div"), id = "select-pop-" + (++selectPopupSeq);
    box.id = id; box.className = "select-popup"; box.setAttribute("role", "listbox");
    box.setAttribute("aria-label", select.getAttribute("aria-label") || Array.from(select.labels || []).map(function (l) { return l.textContent.trim(); }).join(" ") || select.getAttribute("title") || select.name || (select.selectedOptions[0] && select.selectedOptions[0].label) || "");
    var active = Math.max(0, enabled.indexOf(select.selectedOptions[0])), rows = [], group = null;
    options.forEach(function (o) {
      if (o.hidden) return;
      if (o.parentElement.tagName === "OPTGROUP" && group !== o.parentElement) {
        group = o.parentElement; var heading = document.createElement("div"); heading.className = "select-popup-group"; heading.textContent = group.label; box.appendChild(heading);
      }
      var row = document.createElement("div"); row.id = id + "-" + options.indexOf(o); row.className = "select-popup-option";
      row.setAttribute("role", "option"); row.setAttribute("aria-selected", String(o.selected));
      row.setAttribute("aria-disabled", String(enabled.indexOf(o) < 0));
      var mark = document.createElement("span"); mark.className = "select-popup-check"; mark.setAttribute("aria-hidden", "true"); mark.textContent = o.selected ? "✓" : "";
      var label = document.createElement("span"); label.textContent = o.label;
      row.appendChild(mark); row.appendChild(label); box.appendChild(row); rows.push({ option: o, row: row });
      row.addEventListener("pointermove", function () { if (enabled.indexOf(o) >= 0) highlight(enabled.indexOf(o), false); });
      row.addEventListener("click", function (e) { if (enabled.indexOf(o) >= 0) choose(o, e.isTrusted); });
    });
    function highlight(index, scroll) {
      active = index;
      rows.forEach(function (r) { var on = r.option === enabled[active]; r.row.classList.toggle("active", on); if (on) { select.setAttribute("aria-activedescendant", r.row.id); if (scroll) r.row.scrollIntoView({ block: "nearest" }); } });
    }
    function choose(o, userAction) {
      var changed = select.value !== o.value;
      select.value = o.value;
      // Real pointer/keyboard choices must participate in the dialog's dirty guard.
      if (changed && userAction) select._tbTouched = true;
      closeSelectPopup(); select.focus({ preventScroll: true });
      if (changed) { ["input", "change"].forEach(function (type) { var event = new Event(type, { bubbles: true }); event._tbUserSelect = !!userAction; select.dispatchEvent(event); }); }
    }
    // Mount in the current dialog to keep its focus trap and accessibility scope intact.
    (select.closest("[role=dialog]") || document.body).appendChild(box);
    var r = select.getBoundingClientRect(), gap = 6, margin = 8;
    box.style.minWidth = Math.min(r.width, window.innerWidth - margin * 2) + "px";
    box.style.maxWidth = (window.innerWidth - margin * 2) + "px";
    var below = window.innerHeight - r.bottom - gap - margin, above = r.top - gap - margin;
    var up = below < Math.min(280, box.scrollHeight) && above > below;
    box.style.maxHeight = Math.max(40, Math.min(320, up ? above : below)) + "px";
    box.style.left = Math.max(margin, Math.min(r.left, window.innerWidth - box.offsetWidth - margin)) + "px";
    box.style.top = (up ? Math.max(margin, r.top - gap - box.offsetHeight) : r.bottom + gap) + "px";
    select.setAttribute("aria-expanded", "true"); select.setAttribute("aria-controls", id);
    var observer = new MutationObserver(function (records) { if ((records || []).some(function (record) { return select.contains(record.target); }) || !document.contains(select) || select.disabled || options.length !== select.options.length || options.some(function (o, i) { return select.options[i] !== o; })) closeSelectPopup(); });
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["disabled", "hidden", "label"] });
    selectPopup = { select: select, box: box, observer: observer, key: function (e) {
      if (e.key === "Escape") { e.preventDefault(); e.stopImmediatePropagation(); closeSelectPopup(); }
      else if (e.key === "Tab") closeSelectPopup();
      else if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopImmediatePropagation(); choose(enabled[active], e.isTrusted); }
      else if (["ArrowDown", "ArrowUp", "Home", "End"].indexOf(e.key) >= 0) {
        e.preventDefault(); e.stopImmediatePropagation(); highlight(e.key === "Home" ? 0 : e.key === "End" ? enabled.length - 1 : (active + (e.key === "ArrowDown" ? 1 : -1) + enabled.length) % enabled.length, true);
      } else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
        e.preventDefault();
        var now = Date.now(); prefix = now - typedAt > 700 ? e.key : prefix + e.key; typedAt = now;
        var needle = prefix.toLocaleLowerCase(), index = enabled.findIndex(function (o) { return o.label.toLocaleLowerCase().indexOf(needle) === 0; });
        if (index >= 0) highlight(index, true);
      }
    } };
    var prefix = "", typedAt = 0;
    highlight(active, true);
    box.addEventListener("pointerdown", function (e) { e.preventDefault(); });
  }
  document.addEventListener("pointerdown", function (e) {
    if (e.button !== 0) return;
    var s = e.target.closest("select");
    if (popupSelect(s)) { e.preventDefault(); var same = selectPopup && selectPopup.select === s; closeSelectPopup(); s.focus({ preventScroll: true }); if (!same) openSelectPopup(s); }
    else if (selectPopup && !selectPopup.box.contains(e.target)) closeSelectPopup();
  });
  document.addEventListener("keydown", function (e) {
    if (imeKey(e)) return;
    if (selectPopup) { selectPopup.key(e); return; }
    if (popupSelect(e.target) && ["ArrowDown", "ArrowUp", "Enter", " ", "Home", "End"].indexOf(e.key) >= 0) {
      e.preventDefault(); e.stopImmediatePropagation(); openSelectPopup(e.target);
      if (selectPopup && (e.key === "Home" || e.key === "End")) selectPopup.key(e);
    }
  }, true);
  document.addEventListener("focusin", function (e) { if (selectPopup && e.target !== selectPopup.select && !selectPopup.box.contains(e.target)) closeSelectPopup(); });
  window.addEventListener("resize", closeSelectPopup);
  window.addEventListener("blur", closeSelectPopup);
  window.addEventListener("scroll", function (e) { if (selectPopup && !selectPopup.box.contains(e.target)) closeSelectPopup(); }, true);
