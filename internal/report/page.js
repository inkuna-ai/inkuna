// inkunactl diff's page script: the Unified/Split toggle, Viewed marks, the
// file filter, and the j, k, and v keys. It makes no requests; what it
// remembers stays in this browser's storage, when the browser allows it.
(function () {
  "use strict";
  var body = document.body;
  var run = body.getAttribute("data-run") || "";
  var files = Array.prototype.slice.call(document.querySelectorAll("section.file"));
  var links = Array.prototype.slice.call(document.querySelectorAll("nav.tree a"));
  var current = 0;
  // When a page ends before the file j or k chose can reach the top, the
  // scroll that jump causes must not choose another.
  var jumpedAt = 0;

  function remember(key, value) {
    try {
      if (value === null) window.localStorage.removeItem(key);
      else window.localStorage.setItem(key, value);
    } catch (error) {
      // A page opened from disk may have no storage; the choice holds until it closes.
    }
  }
  function recall(key) {
    try {
      return window.localStorage.getItem(key);
    } catch (error) {
      return null;
    }
  }

  // Layout: the page carries both; the body's class shows one. A page
  // written with --split asked for split, so only an unchosen page opens
  // in the layout last picked here.
  function setLayout(layout) {
    body.classList.toggle("layout-split", layout === "split");
    body.classList.toggle("layout-unified", layout !== "split");
    remember("inkuna.diff.mode", layout);
  }
  var stored = recall("inkuna.diff.mode");
  if (!body.classList.contains("layout-split") && (stored === "split" || stored === "unified")) setLayout(stored);
  Array.prototype.forEach.call(document.querySelectorAll("[data-layout]"), function (button) {
    button.addEventListener("click", function () {
      setLayout(button.getAttribute("data-layout"));
    });
  });

  // Viewed: kept per run, path, and content, so a file that changes reads as not viewed.
  function viewedKey(file) {
    return "inkuna.diff.viewed:" + run + ":" + file.getAttribute("data-path") + ":" + file.getAttribute("data-blob");
  }
  function linkFor(file) {
    return links.filter(function (link) {
      return link.getAttribute("href") === "#" + file.id;
    })[0];
  }
  function countViewed() {
    var viewed = files.filter(function (file) {
      return file.querySelector("[data-viewed]").checked;
    }).length;
    var count = document.getElementById("viewed");
    if (count) count.textContent = viewed + " of " + files.length + " viewed";
  }
  function markViewed(file, viewed) {
    file.querySelector("[data-viewed]").checked = viewed;
    file.querySelector("details").open = !viewed;
    var link = linkFor(file);
    if (link) link.classList.toggle("viewed-file", viewed);
    remember(viewedKey(file), viewed ? "1" : null);
    countViewed();
  }
  files.forEach(function (file) {
    var box = file.querySelector("[data-viewed]");
    if (recall(viewedKey(file)) === "1") markViewed(file, true);
    box.addEventListener("change", function () {
      markViewed(file, box.checked);
    });
  });
  countViewed();

  // Filter: every word must appear in the path or a rename's old path.
  var filter = document.getElementById("filter");
  function matches(element, words) {
    var path = (element.getAttribute("data-path") || "").toLowerCase();
    return words.every(function (word) {
      return path.indexOf(word) >= 0;
    });
  }
  if (filter)
    filter.addEventListener("input", function () {
      var words = filter.value.toLowerCase().split(/\s+/).filter(Boolean);
      files.forEach(function (file) {
        file.classList.toggle("hidden", !matches(file, words));
      });
      links.forEach(function (link) {
        link.parentNode.classList.toggle("hidden", !matches(link, words));
      });
      // A folder goes with the last of its files.
      Array.prototype.forEach.call(document.querySelectorAll("nav.tree li"), function (item) {
        if (!item.querySelector(":scope > .folder")) return;
        var kept = Array.prototype.some.call(item.querySelectorAll("a"), function (link) {
          return !link.parentNode.classList.contains("hidden");
        });
        item.classList.toggle("hidden", !kept);
      });
    });

  // Keys: j and k move between the files shown, v marks the current one viewed.
  function shown() {
    return files.filter(function (file) {
      return !file.classList.contains("hidden");
    });
  }
  function select(file) {
    current = files.indexOf(file);
    links.forEach(function (link) {
      link.classList.toggle("current", link.getAttribute("href") === "#" + file.id);
    });
  }
  window.addEventListener(
    "scroll",
    function () {
      if (Date.now() - jumpedAt < 300) return;
      var visible = shown();
      for (var index = visible.length - 1; index >= 0; index--) {
        if (visible[index].getBoundingClientRect().top <= 1) {
          select(visible[index]);
          return;
        }
      }
      if (visible.length) select(visible[0]);
    },
    { passive: true }
  );
  document.addEventListener("keydown", function (event) {
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    var target = event.target;
    if (target && target.closest && target.closest("input:not([type=checkbox]), textarea, select")) return;
    var visible = shown();
    if (!visible.length) return;
    var at = Math.max(0, visible.indexOf(files[current]));
    if (event.key === "j" || event.key === "k") {
      var next = visible[at + (event.key === "j" ? 1 : -1)];
      if (!next) return;
      event.preventDefault();
      jumpedAt = Date.now();
      next.scrollIntoView({ block: "start" });
      select(next);
    } else if (event.key === "v") {
      event.preventDefault();
      var file = visible[at];
      markViewed(file, !file.querySelector("[data-viewed]").checked);
    }
  });
  if (files.length) select(files[0]);
})();
