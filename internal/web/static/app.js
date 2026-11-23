// Client helpers called from datastar expressions. Every datastar expression is
// compiled with `el` in scope, so an option's label is read from its data-label
// attribute rather than interpolated into the expression: the label comes from a
// manifest's keywords, and templ escapes an attribute value but could not escape
// it into a JavaScript string literal (FR-055).
//
// Multi-select state travels as a JSON array in a string signal. datastar's
// signal proxy wraps array elements, so Array.prototype methods on a live array
// signal compare wrappers rather than values; a string is unambiguous in both
// directions and is what the server unmarshals.
(function () {
  "use strict";

  function list(raw) {
    try {
      var parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed : [];
    } catch (e) {
      return [];
    }
  }

  // amFuzzy is a subsequence match, not a substring match: the needle's
  // characters must appear in order in the haystack but need not be adjacent.
  // Whitespace is stripped from the needle only. This mirrors fuzzy() at
  // docs/design/agent-manager.dc.html line 964 and web.Fuzzy in Go.
  window.amFuzzy = function (needle, haystack) {
    var n = String(needle == null ? "" : needle).toLowerCase().replace(/\s+/g, "");
    if (n === "") {
      return true;
    }
    var h = String(haystack == null ? "" : haystack).toLowerCase();
    var i = 0;
    for (var j = 0; j < h.length && i < n.length; j++) {
      if (h[j] === n[i]) {
        i++;
      }
    }
    return i === n.length;
  };

  window.amHas = function (raw, value) {
    return list(raw).indexOf(value) !== -1;
  };

  window.amToggle = function (raw, value) {
    var values = list(raw);
    var at = values.indexOf(value);
    if (at === -1) {
      values.push(value);
    } else {
      values.splice(at, 1);
    }
    values.sort();
    return JSON.stringify(values);
  };

  window.amAny = function (raw) {
    return list(raw).length > 0;
  };

  // Rendered through data-text, which assigns textContent, so a label is never
  // parsed as markup.
  window.amSummary = function (raw) {
    var values = list(raw);
    if (values.length === 0) {
      return "Any";
    }
    if (values.length === 1) {
      return values[0];
    }
    return values.length + " selected";
  };

  // amNoMatch backs the menu's "No match" state. The option list only exists in
  // the DOM, so the emptiness of the filtered view is read from there; the
  // needle argument is what makes the expression reactive.
  window.amNoMatch = function (el, needle) {
    var options = el.parentElement.querySelectorAll("[data-label]");
    if (options.length === 0) {
      return false;
    }
    for (var i = 0; i < options.length; i++) {
      if (window.amFuzzy(needle, options[i].dataset.label)) {
        return false;
      }
    }
    return true;
  };
})();

// amEditor backs /catalog/create. Files live here, mirrored into the form's
// hidden `files` field on every change and into localStorage so a reload keeps
// the draft. The DOM is built with textContent only: paths are typed by people
// and file content comes from published packages.
(function () {
  "use strict";

  function storage() {
    try {
      return window.localStorage;
    } catch (e) {
      return null;
    }
  }

  function readDraft(key) {
    var store = storage();
    if (!store) {
      return null;
    }
    try {
      var parsed = JSON.parse(store.getItem(key) || "null");
      return Array.isArray(parsed) && parsed.length ? parsed : null;
    } catch (e) {
      return null;
    }
  }

  function writeDraft(key, files) {
    var store = storage();
    if (!store) {
      return;
    }
    try {
      store.setItem(key, JSON.stringify(files));
    } catch (e) {}
  }

  function dropDraft(key) {
    var store = storage();
    if (!store) {
      return;
    }
    try {
      store.removeItem(key);
    } catch (e) {}
  }

  var segment = /^[A-Za-z0-9_][A-Za-z0-9._-]*$/;

  function validPath(path) {
    if (!path || path.length > 255) {
      return false;
    }
    return path.split("/").every(function (part) {
      return segment.test(part) && part !== "..";
    });
  }

  function init() {
    var root = document.getElementById("am-editor");
    var seedNode = document.getElementById("am-editor-seed");
    if (!root || !seedNode) {
      return;
    }
    var seed = JSON.parse(seedNode.textContent);
    var key = "am-draft:" + seed.key;
    var byPath = function (a, b) {
      return a.path < b.path ? -1 : a.path > b.path ? 1 : 0;
    };
    var pristine = JSON.stringify((seed.files || []).slice().sort(byPath));

    var list = document.getElementById("am-file-list");
    var buffer = document.getElementById("am-buffer");
    var current = document.getElementById("am-current");
    var field = document.getElementById("am-files-field");
    var newPath = document.getElementById("am-new-path");
    var example = document.getElementById("am-example");
    var note = document.getElementById("am-draft-note");

    var files = JSON.parse(pristine);
    var restored = readDraft(key);
    if (restored && JSON.stringify(restored) !== pristine) {
      files = restored;
      note.style.display = "";
    }
    var selected = null;

    function find(path) {
      for (var i = 0; i < files.length; i++) {
        if (files[i].path === path) {
          return i;
        }
      }
      return -1;
    }

    function taken(path) {
      var lower = path.toLowerCase();
      return files.some(function (f) {
        return f.path.toLowerCase() === lower;
      });
    }

    function kind() {
      return find("plugin.json") === -1 ? "skill" : "plugin";
    }

    function sync() {
      files.sort(byPath);
      field.value = JSON.stringify(files);
      if (JSON.stringify(files) === pristine) {
        dropDraft(key);
      } else {
        writeDraft(key, files);
      }
    }

    function renderList() {
      while (list.firstChild) {
        list.removeChild(list.firstChild);
      }
      files.forEach(function (f) {
        var item = document.createElement("li");
        var button = document.createElement("button");
        button.type = "button";
        button.className = "am-mono";
        button.textContent = f.path;
        button.style.cssText =
          "display:block;width:100%;text-align:left;border:0;border-radius:6px;padding:5px 8px;" +
          "font-size:12.5px;cursor:pointer;color:var(--fg);overflow:hidden;text-overflow:ellipsis;" +
          "white-space:nowrap;background:" + (f.path === selected ? "var(--surface2)" : "none");
        if (f.path === selected) {
          button.setAttribute("aria-current", "true");
        }
        button.addEventListener("click", function () {
          select(f.path);
        });
        item.appendChild(button);
        list.appendChild(item);
      });
    }

    function renderExamples() {
      while (example.options.length > 1) {
        example.remove(1);
      }
      seed.examples.forEach(function (e) {
        if (e.kind !== kind()) {
          return;
        }
        var option = document.createElement("option");
        option.value = e.id;
        option.textContent = e.label + " (" + e.path + ")";
        example.appendChild(option);
      });
      example.value = "";
    }

    function select(path) {
      var at = find(path);
      selected = at === -1 ? (files[0] ? files[0].path : null) : path;
      var file = selected === null ? null : files[find(selected)];
      current.textContent = file ? file.path : "No files";
      buffer.value = file ? file.content : "";
      buffer.disabled = !file;
      renderList();
    }

    function refresh(path) {
      sync();
      renderExamples();
      select(path);
    }

    function add(path, content) {
      path = (path || "").trim();
      if (!validPath(path)) {
        window.alert("Use letters, digits, '.', '-' and '_' between slashes, e.g. references/notes.md.");
        return false;
      }
      if (taken(path)) {
        select(path);
        return false;
      }
      files.push({ path: path, content: content || "" });
      refresh(path);
      return true;
    }

    buffer.addEventListener("input", function () {
      var at = find(selected);
      if (at !== -1) {
        files[at].content = buffer.value;
        sync();
      }
    });

    // Tab indents rather than leaving the editor: YAML and markdown lists need it.
    buffer.addEventListener("keydown", function (event) {
      if (event.key !== "Tab" || event.shiftKey || event.ctrlKey || event.metaKey || event.altKey) {
        return;
      }
      event.preventDefault();
      var start = buffer.selectionStart;
      buffer.setRangeText("  ", start, buffer.selectionEnd, "end");
      buffer.dispatchEvent(new Event("input"));
    });

    document.getElementById("am-new-file").addEventListener("click", function () {
      if (add(newPath.value, "")) {
        newPath.value = "";
        buffer.focus();
      }
    });
    newPath.addEventListener("keydown", function (event) {
      if (event.key === "Enter") {
        event.preventDefault();
        document.getElementById("am-new-file").click();
      }
    });

    example.addEventListener("change", function () {
      var chosen = seed.examples.filter(function (e) {
        return e.id === example.value;
      })[0];
      if (chosen) {
        add(chosen.path, seed.bodies[chosen.id]);
      }
      example.value = "";
    });

    document.getElementById("am-rename").addEventListener("click", function () {
      var at = find(selected);
      if (at === -1) {
        return;
      }
      var next = (window.prompt("Rename " + selected + " to", selected) || "").trim();
      if (!next || next === selected) {
        return;
      }
      if (!validPath(next)) {
        window.alert("Use letters, digits, '.', '-' and '_' between slashes, e.g. references/notes.md.");
        return;
      }
      if (next.toLowerCase() !== selected.toLowerCase() && taken(next)) {
        window.alert(next + " already exists.");
        return;
      }
      files[at].path = next;
      refresh(next);
    });

    document.getElementById("am-delete").addEventListener("click", function () {
      var at = find(selected);
      if (at === -1 || !window.confirm("Delete " + selected + "?")) {
        return;
      }
      files.splice(at, 1);
      refresh(null);
    });

    document.getElementById("am-draft-discard").addEventListener("click", function () {
      dropDraft(key);
      files = JSON.parse(pristine);
      note.style.display = "none";
      refresh(null);
    });

    // Switching starter replaces the whole tree, so an edited draft is confirmed first.
    Array.prototype.forEach.call(document.querySelectorAll(".am-starter"), function (link) {
      link.addEventListener("click", function (event) {
        if (JSON.stringify(files) !== pristine && !window.confirm("Replace your draft with this starter?")) {
          event.preventDefault();
          return;
        }
        dropDraft(key);
      });
    });

    window.amDraftClear = function () {
      dropDraft(key);
    };

    var first = find("SKILL.md") !== -1 ? "SKILL.md" : find("plugin.json") !== -1 ? "plugin.json" : null;
    refresh(first);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
