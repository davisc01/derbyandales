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
  // The awards scene is paced the same way as the reveal: one trophy, a pause
  // while it is handed over and photographed, then the next.
  let awardIndex = 0;
  let awardRows = [];
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
    awardIndex = 0;
    awardRows = [];
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
        case "voting-qr":
          return await renderVoting();
        case "roster":
          return await renderRoster();
        case "results-reveal":
          return await renderReveal();
        case "final-standings":
          return await renderFinal();
        case "awards":
          return await renderAwards();
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

    // During the intermission the screen says so rather than sitting on a
    // finished heat for twenty minutes while people are at the bar.
    if (state.intermission) {
      return renderVoting();
    }

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
  // The two voted trophies, one at a time, with the car big on the screen.
  //
  // These are given for how a car looks, so the photo is the point — a list of
  // names would be the wrong shape entirely. The speed trophies are not here:
  // they are handed out during the results reveal, as each of the top three
  // comes up, which is what the club asked for.
  async function renderAwards() {
    if (!awardRows.length) {
      const data = await getJSON("/api/race/awards");
      awardRows = (data.awards || []).filter(function (a) { return a.voted; });
      revealTitle = data.race || "Trophies";
    }

    if (!awardRows.length) {
      const { wrap, body } = sceneShell("Trophies");
      body.appendChild(el("p", "display-hint",
        "The design and theme trophies have not been declared yet."));
      return swap(wrap);
    }

    const shown = Math.min(awardIndex, awardRows.length - 1);
    const a = awardRows[shown];
    const { wrap, body } = sceneShell(a.name,
      (shown + 1) + " of " + awardRows.length);

    const card = el("div", "award-card");
    const pic = el("div", "award-pic");
    if (a.photo_id) {
      const img = document.createElement("img");
      img.src = "/photo/" + a.photo_id + "?size=large";
      img.alt = "";
      img.addEventListener("error", function () {
        pic.classList.add("empty");
        img.remove();
        pic.appendChild(el("div", "award-number", "#" + a.car_number));
      });
      pic.appendChild(img);
    } else {
      pic.classList.add("empty");
      pic.appendChild(el("div", "award-number", "#" + a.car_number));
    }
    card.appendChild(pic);

    const who = el("div", "award-who");
    who.appendChild(el("div", "award-car", a.car_name || ("Car " + a.car_number)));
    who.appendChild(el("div", "award-driver", a.driver));
    who.appendChild(el("div", "award-number-small", "#" + a.car_number));
    card.appendChild(who);
    body.appendChild(card);

    if (awardIndex < awardRows.length - 1) {
      wrap.appendChild(el("div", "reveal-prompt", "Press space for the next trophy"));
    }
    swap(wrap);
  }

  // The whole table at once, for the wrap-up — after the reveal has walked up
  // the order and after any tie for a trophy has been run off. This is the
  // picture people photograph, so it shows the settled result rather than the
  // one the reveal ended on.
  async function renderFinal() {
    const data = await getJSON("/api/race/standings");
    const rows = (data.standings || []).filter(function (s) { return s.place > 0; });

    const { wrap, body } = sceneShell(data.race || "Final standings", "Final standings");
    if (!rows.length) {
      body.appendChild(el("p", "display-hint", "No results yet."));
      return swap(wrap);
    }

    const list = el("div", "final");
    // Row height follows the field size, so twenty-four cars and eight both
    // fill the screen rather than one of them overflowing it.
    list.style.setProperty("--final-rows", String(rows.length));
    rows.forEach(function (s) {
      const row = el("div", "final-row");
      if (s.place <= 3) row.classList.add("podium");
      if (s.place === 1) row.classList.add("winner");
      if (s.is_control) row.classList.add("control");

      row.appendChild(el("div", "final-place", (s.tied ? "T" : "") + s.place));
      row.appendChild(el("div", "final-car", "#" + s.car_number));
      const who = el("div", "final-who");
      who.appendChild(el("div", "final-driver", s.driver));
      who.appendChild(el("div", "final-name", s.car_name || ""));
      row.appendChild(who);
      row.appendChild(el("div", "final-time", s.average ? s.average + "s" : "—"));
      row.appendChild(el("div", "final-mph", s.mph ? s.mph + " mph" : ""));
      list.appendChild(row);
    });
    body.appendChild(list);
    swap(wrap);
  }

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
        // The speed trophies are handed over here, as each of the top three is
        // revealed, so the screen says which one is due. The pace car is ranked
        // but takes nothing, and a place still tied has no trophy to give yet.
        if (s.place <= 3 && !s.tied && !s.is_control) {
          who.appendChild(el("div", "reveal-trophy", trophyFor(s.place)));
        }
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

  // The intermission screen: what to vote for and where the tablet is. There
  // is deliberately no countdown — the intermission ends when the coordinator
  // says it does, not when a clock runs out.
  async function renderVoting() {
    let data = { categories: [], voting_open: false };
    try {
      data = await getJSON("/api/vote/tally");
    } catch (err) {
      /* fall through to the static text */
    }

    const wrap = el("div", "scene scene-voting");
    const inner = el("div", "voting-panel");

    inner.appendChild(el("p", "voting-kicker", "Intermission"));
    inner.appendChild(el("h1", "voting-title", "Vote for your favourites"));
    inner.appendChild(
      el("p", "voting-where", "The tablet is at the check-in table. Refill first.")
    );

    const qs = el("div", "voting-questions");
    (data.categories || [])
      .filter(function (c) { return c.enabled; })
      .forEach(function (c) {
        const box = el("div", "voting-q");
        box.appendChild(el("div", "voting-q-label", c.label));
        box.appendChild(el("div", "voting-q-count", c.votes));
        box.appendChild(el("div", "voting-q-word", c.votes === 1 ? "vote" : "votes"));
        qs.appendChild(box);
      });
    if (qs.children.length) inner.appendChild(qs);

    if (!data.voting_open) {
      inner.appendChild(el("p", "voting-closed-note", "Voting is closed."));
    }

    wrap.appendChild(inner);
    swap(wrap);
  }

  function ordinal(n) {
    const suffix = ["th", "st", "nd", "rd"][n % 100 > 10 && n % 100 < 14 ? 0 : Math.min(n % 10, 4) % 4] || "th";
    return n + suffix;
  }

  // The trophy due at a given place, in the club's words.
  function trophyFor(place) {
    return ["1st", "2nd", "3rd"][place - 1] + " place trophy";
  }

  // The reveal is operator-paced: space or arrow advances, backspace steps back.
  document.addEventListener("keydown", function (e) {
    if (scene === "awards") {
      if (e.key === " " || e.key === "ArrowRight" || e.key === "Enter") {
        e.preventDefault();
        if (awardIndex < awardRows.length - 1) {
          awardIndex++;
          render();
        }
      } else if (e.key === "ArrowLeft" || e.key === "Backspace") {
        e.preventDefault();
        if (awardIndex > 0) {
          awardIndex--;
          render();
        }
      }
      return;
    }
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
    if (scene === "awards") {
      if (awardIndex < awardRows.length - 1) {
        awardIndex++;
        render();
      }
      return;
    }
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
      if (scene === "now-racing" || scene === "roster" || scene === "voting-qr" ||
          scene === "final-standings") render();
    });

    source.addEventListener("vote", function () {
      // The live count on the intermission screen shows the tablet is being
      // used; the coordinator watches it from across the room.
      if (scene === "voting-qr" || scene === "now-racing" || scene === "final-standings") render();
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
