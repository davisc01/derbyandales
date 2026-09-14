// A display screen.
//
// The screen registers itself, remembers who it is, and then does what the
// coordinator tells it. Scenes are swapped in place rather than by reloading,
// so a TV never shows a white flash in the middle of a race.

(function () {
  "use strict";

  const root = document.getElementById("display-root");
  const nameTag = document.getElementById("display-name");

  const TOKEN_KEY = "derbyandales.display.token";
  let token = null;
  let scene = "blank";
  let params = {};
  let revealIndex = 0;
  let revealRows = [];

  try {
    token = localStorage.getItem(TOKEN_KEY);
  } catch (err) {
    // A TV in a private window still works; it just gets a new name each time.
  }

  // --- helpers --------------------------------------------------------------

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = String(text);
    return node;
  }

  async function post(url, body) {
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(body || {}).toString(),
    });
    if (!res.ok) throw new Error(res.statusText);
    return res.json();
  }

  async function getJSON(url) {
    const res = await fetch(url);
    if (!res.ok) throw new Error(res.statusText);
    return res.json();
  }

  function showOffline(on) {
    let bar = document.getElementById("offline-bar");
    if (on && !bar) {
      bar = el("div", "display-offline", "Lost contact with the race server — reconnecting");
      bar.id = "offline-bar";
      document.body.appendChild(bar);
    } else if (!on && bar) {
      bar.remove();
    }
  }

  function flashName(name) {
    if (!nameTag) return;
    nameTag.textContent = name;
    nameTag.classList.add("show");
    setTimeout(() => nameTag.classList.remove("show"), 6000);
  }

  // --- registration ---------------------------------------------------------

  async function register() {
    const d = await post("/api/display/register", { token: token || "" });
    token = d.Token || d.token;
    try {
      localStorage.setItem(TOKEN_KEY, token);
    } catch (err) {
      /* not fatal */
    }
    flashName(d.Name || d.name);
    setScene(d.Page || d.page || "blank", d.Params || d.params);
  }

  function heartbeat() {
    if (!token) return;
    post("/api/display/heartbeat", { token: token }).catch(() => {});
  }

  // --- scenes ---------------------------------------------------------------

  function setScene(next, rawParams) {
    scene = next || "blank";
    params = typeof rawParams === "string" ? safeParse(rawParams) : rawParams || {};
    revealIndex = 0;
    revealRows = [];
    render();
  }

  function safeParse(raw) {
    try {
      return JSON.parse(raw);
    } catch (err) {
      return {};
    }
  }

  function swap(node) {
    root.replaceChildren(node);
  }

  function sceneShell(title, sub) {
    const wrap = el("div", "scene scene-" + scene);
    if (title) {
      const head = el("div", "scene-head");
      head.appendChild(el("h1", "scene-title", title));
      if (sub) head.appendChild(el("p", "scene-sub", sub));
      wrap.appendChild(head);
    }
    const body = el("div", "scene-body");
    wrap.appendChild(body);
    return { wrap, body };
  }

  async function render() {
    try {
      switch (scene) {
        case "now-racing":
          return await renderRacing();
        case "roster":
          return await renderRoster();
        case "results-reveal":
          return await renderReveal();
        case "blank":
          return renderBlank();
        default:
          return renderPlaceholder();
      }
    } catch (err) {
      const { wrap, body } = sceneShell("");
      body.appendChild(el("p", "display-hint", "Waiting for the race server…"));
      swap(wrap);
    }
  }

  function renderBlank() {
    const wrap = el("div", "scene scene-blank");
    wrap.appendChild(el("div", "blank-mark", "D&A"));
    swap(wrap);
  }

  function renderPlaceholder() {
    const { wrap, body } = sceneShell("Derby and Ales");
    body.appendChild(el("p", "display-hint", "This screen is not showing anything yet."));
    swap(wrap);
  }

  // Lane assignments before the heat, finish order and times after it.
  async function renderRacing() {
    const state = await getJSON("/api/race/state");

    if (!state.lanes || !state.lanes.length) {
      const { wrap, body } = sceneShell(state.race_name || "Derby and Ales");
      body.appendChild(el("p", "display-hint", "Waiting for the next heat…"));
      return swap(wrap);
    }

    const sub =
      state.heat_total > 0
        ? "Heat " + state.heat_no + " of " + state.heat_total
        : "Heat " + state.heat_no;
    const { wrap, body } = sceneShell(state.race_name || "Now racing", sub);

    const lanes = el("div", "lanes");
    state.lanes.forEach(function (l) {
      const row = el("div", "lane-row");
      row.dataset.lane = l.lane;
      if (l.bye) row.classList.add("bye");
      if (l.place === 1) row.classList.add("p1");
      else if (l.place === 2) row.classList.add("p2");

      row.appendChild(el("div", "lane-no", l.lane));

      const car = el("div", "lane-car");
      if (l.bye) {
        car.appendChild(el("div", "lane-driver", "empty lane"));
      } else {
        car.appendChild(el("div", "lane-driver", l.driver));
        const name = el("div", "lane-carname");
        name.appendChild(el("span", "lane-carno", "#" + l.car_number));
        name.appendChild(document.createTextNode(l.car_name || ""));
        car.appendChild(name);
      }
      row.appendChild(car);

      const result = el("div", "lane-result");
      if (l.time !== undefined) {
        // A car that never finished must not read as a very fast time.
        const dnf = parseFloat(l.time) >= 9.0;
        if (dnf) {
          row.classList.add("dnf");
          result.appendChild(el("div", "lane-time", "did not finish"));
        } else {
          result.appendChild(el("div", "lane-time", l.time + "s"));
          result.appendChild(el("div", "lane-mph", l.mph + " mph scale"));
        }
      } else if (!l.bye) {
        result.appendChild(el("div", "lane-mph", "ready"));
      }
      row.appendChild(result);

      if (l.place) {
        row.appendChild(el("div", "lane-place", ordinal(l.place)));
      }
      lanes.appendChild(row);
    });

    body.appendChild(lanes);
    swap(wrap);
  }

  async function renderRoster() {
    const data = await getJSON("/api/race/roster");
    const entries = data.entries || [];

    const sub =
      entries.length > 0
        ? data.racers + (data.racers === 1 ? " racer" : " racers") + " · " + data.cars + " cars"
        : "";
    const { wrap, body } = sceneShell(data.race ? data.race + " — tonight's racers" : "Tonight's racers", sub);

    if (!entries.length) {
      body.appendChild(el("p", "display-hint", "Nobody has checked in yet."));
      return swap(wrap);
    }

    const grid = el("div", "roster");
    // Two columns once the field is big enough that one would overflow a TV.
    const columns = entries.length > 14 ? 2 : 1;
    grid.style.gridTemplateColumns = "repeat(" + columns + ", 1fr)";
    // Scale the type so the whole field fits without scrolling.
    const rows = Math.ceil(entries.length / columns);
    const size = Math.max(1.6, Math.min(3.4, 78 / rows));
    grid.style.fontSize = size + "vh";

    const withPhotos = entries.some(function (e) { return e.photo_id; });
    if (withPhotos) grid.classList.add("with-photos");

    entries.forEach(function (e) {
      const row = el("div", "roster-row");
      if (e.excluded) row.classList.add("excluded");
      if (withPhotos) {
        const cell = el("div", "roster-pic");
        if (e.photo_id) {
          const img = document.createElement("img");
          img.src = "/photo/" + e.photo_id + "?size=thumb";
          img.alt = "";
          img.loading = "lazy";
          cell.appendChild(img);
        }
        row.appendChild(cell);
      }
      row.appendChild(el("div", "roster-no", "#" + e.car_number));
      const who = el("div");
      who.appendChild(el("span", "roster-driver", e.driver));
      if (e.car_name) {
        who.appendChild(document.createTextNode("  "));
        who.appendChild(el("span", "roster-car", e.car_name));
      }
      row.appendChild(who);
      grid.appendChild(row);
    });

    body.appendChild(grid);
    swap(wrap);
  }

  // Slowest first, one at a time, building up to the winner.
  async function renderReveal() {
    if (!revealRows.length) {
      const data = await getJSON("/api/race/standings");
      // Slowest first so each press moves up the order.
      revealRows = (data.standings || [])
        .filter(function (s) { return s.place > 0; })
        .sort(function (a, b) { return b.place - a.place; });
      revealTitle = data.race || "Results";
    }

    const shown = revealRows.slice(0, revealIndex);
    const { wrap, body } = sceneShell(revealTitle, revealIndex + " of " + revealRows.length);

    if (!revealRows.length) {
      body.appendChild(el("p", "display-hint", "No results yet."));
      return swap(wrap);
    }

    const list = el("div", "reveal");
    // Newest at the top: each reveal pushes the previous ones down.
    shown
      .slice()
      .reverse()
      .forEach(function (s) {
        const row = el("div", "reveal-row");
        if (s.place <= 3) row.classList.add("podium");
        if (s.place === 1) row.classList.add("winner");

        row.appendChild(el("div", "reveal-place", (s.tied ? "T" : "") + s.place));

        const who = el("div");
        who.appendChild(el("div", "reveal-driver", s.driver));
        who.appendChild(el("div", "reveal-car", "#" + s.car_number + "  " + (s.car_name || "")));
        row.appendChild(who);

        const time = el("div");
        time.appendChild(el("div", "reveal-time", s.average ? s.average + "s" : "—"));
        if (s.mph) time.appendChild(el("div", "reveal-mph", s.mph + " mph scale"));
        row.appendChild(time);

        list.appendChild(row);
      });

    body.appendChild(list);

    if (revealIndex < revealRows.length) {
      const prompt = el(
        "div",
        "reveal-prompt",
        revealIndex === 0 ? "Press space to begin" : "Press space for the next car"
      );
      wrap.appendChild(prompt);
    }
    swap(wrap);
  }

  let revealTitle = "Results";

  function ordinal(n) {
    const suffix = ["th", "st", "nd", "rd"][n % 100 > 10 && n % 100 < 14 ? 0 : Math.min(n % 10, 4) % 4] || "th";
    return n + suffix;
  }

  // The reveal is operator-paced: space or arrow advances, backspace steps back.
  document.addEventListener("keydown", function (e) {
    if (scene !== "results-reveal") return;
    if (e.key === " " || e.key === "ArrowRight" || e.key === "Enter") {
      e.preventDefault();
      if (revealIndex < revealRows.length) {
        revealIndex++;
        render();
      }
    } else if (e.key === "ArrowLeft" || e.key === "Backspace") {
      e.preventDefault();
      if (revealIndex > 0) {
        revealIndex--;
        render();
      }
    }
  });

  // Touch works too, for a screen driven from a tablet.
  document.addEventListener("click", function () {
    if (scene !== "results-reveal") return;
    if (revealIndex < revealRows.length) {
      revealIndex++;
      render();
    }
  });

  // --- live updates ---------------------------------------------------------

  function connect() {
    const source = new EventSource("/events?topics=race,display,system");

    source.onopen = function () {
      showOffline(false);
    };
    source.onerror = function () {
      showOffline(true);
    };

    source.addEventListener("display", function (e) {
      const ev = safeParseEvent(e.data);
      if (!ev) return;
      const d = ev.data || {};
      // Scene changes are addressed to one display.
      if (ev.kind === "scene" && String(d.id) === String(currentID)) {
        setScene(d.scene, d.params);
      }
    });

    source.addEventListener("race", function () {
      // Any race change redraws whatever this screen is showing. The reveal is
      // operator-paced, so it is left alone.
      if (scene === "now-racing" || scene === "roster") render();
    });

    source.addEventListener("system", function (e) {
      const ev = safeParseEvent(e.data);
      if (ev && ev.kind === "shutdown") showOffline(true);
    });
  }

  function safeParseEvent(raw) {
    try {
      return JSON.parse(raw);
    } catch (err) {
      return null;
    }
  }

  let currentID = null;

  // --- start ----------------------------------------------------------------

  (async function start() {
    try {
      const d = await post("/api/display/register", { token: token || "" });
      currentID = d.ID || d.id;
      token = d.Token || d.token;
      try {
        localStorage.setItem(TOKEN_KEY, token);
      } catch (err) {
        /* not fatal */
      }
      flashName(d.Name || d.name);
      setScene(d.Page || d.page, d.Params || d.params);
    } catch (err) {
      showOffline(true);
      setTimeout(start, 3000);
      return;
    }

    connect();
    setInterval(heartbeat, 15000);

    // A slow safety net: if an event is ever missed, the screen still catches
    // up within half a minute rather than showing a stale heat all night.
    setInterval(async function () {
      try {
        const info = await getJSON("/api/display/scene?token=" + encodeURIComponent(token));
        if (info.scene !== scene) setScene(info.scene, info.params);
      } catch (err) {
        /* the SSE error handler already reports this */
      }
    }, 30000);
  })();
})();
