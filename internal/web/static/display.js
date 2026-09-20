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
  let revealTrophies = 3;
  let revealChampionship = false;
  let revealTrophyNames = ["1st", "2nd", "3rd"];

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

  // A screen can be pinned to one scene with ?scene= in its address. The
  // impound screen does one job all night and should not be something anybody
  // has to remember to set — or can change by accident from the coordinator.
  const pinned = new URLSearchParams(location.search).get("scene");

  function setScene(next, rawParams) {
    if (pinned) {
      next = pinned;
    }
    scene = next || "blank";
    params = typeof rawParams === "string" ? safeParse(rawParams) : rawParams || {};
    revealIndex = 0;
    revealRows = [];
    awardIndex = 0;
    awardRows = [];
    stopSlides();
    resetRacing();
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
    // The club's mark, top left of every scene. A TV in a brewery is seen by
    // people who did not come for the racing.
    const badge = document.createElement("img");
    badge.className = "scene-logo";
    badge.src = "/static/logo.png";
    badge.alt = "";
    wrap.appendChild(badge);
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
        case "impound":
          return await renderImpound();
        case "bracket":
          return await renderBracket();
        case "slideshow":
          return await renderSlideshow();
        case "records":
          return await renderRecords();
        case "wrap-up":
          return await renderWrapUp();
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
    const mark = document.createElement("img");
    mark.className = "blank-mark";
    mark.src = "/static/logo.png";
    mark.alt = "MidSouth Derby and Ales";
    wrap.appendChild(mark);
    swap(wrap);
  }

  function renderPlaceholder() {
    const { wrap, body } = sceneShell("Derby and Ales");
    body.appendChild(el("p", "display-hint", "This screen is not showing anything yet."));
    swap(wrap);
  }

  // --- the now-racing screen, as choreography -------------------------------
  //
  // Rows come in from the left and leave to the right, because that is the
  // way the cars go. These timings are paired with the animations in
  // display.css — changing one without the other leaves the screen either
  // cutting a slide off or sitting on a gap.

  // How long a finished heat stays up once the last car has landed. The
  // coordinator arms the next heat as soon as the times appear, and the room
  // is still reading the one that just ran.
  const RESULT_HOLD_MS = 7000;
  // Results come in one car at a time, in finish order: the screen is
  // announcing who won the heat, not drawing a table.
  const RESULT_STEP_MS = 600;
  // Staging is not an announcement. The lanes come in together, offset just
  // enough to read as four cars rather than one slab.
  const STAGE_STEP_MS = 90;
  const ROW_IN_MS = 500;   // `arrive` in display.css
  const ROW_OUT_MS = 550;  // `depart`
  const ROW_OUT_STEP_MS = 60;

  // Somebody who has asked for less motion gets the same information without
  // the sliding: every stagger collapses and the screen keeps its pace.
  function stillScreen() {
    return !!(window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  }

  // Lane assignments before the heat, finish order and times after it.
  // What the now-racing screen was last showing, so a redraw can tell the
  // difference between "nothing changed" and "they have just been released".
  let racingPhase = "";
  let racingHeat = 0;
  // When the last result row will have finished arriving, which is when its
  // seven seconds start.
  let resultVisibleAt = 0;
  // A next heat armed while the result is still being read waits here.
  let racingHold = null;
  // Set while the rows are sliding out. A redraw in the middle would cut the
  // slide off, and the render that follows it reads the state fresh anyway.
  let racingSwapping = false;
  let racingSwapTimer = null;
  // What is on screen already. A finished heat is announced by the timer and
  // by the race controller within a millisecond of each other, and the gate
  // closing announces it again; each of those redraws the screen. Landing in
  // the middle of a result coming in, the second one replaced the rows with a
  // finished copy of themselves — the stagger vanished and the times simply
  // appeared. Nothing is redrawn unless it would differ.
  let racingSig = "";

  // The heat whose result has already had its seven seconds before the voting
  // screen took over. The intermission starts the instant that heat's times
  // land, so the state carrying the result carries the intermission with it —
  // and the screen used to jump straight to the vote without ever showing who
  // won the heat that had just run. It deliberately survives resetRacing: once
  // that result has been shown, coming back to this scene means the vote.
  let intermissionAfter = 0;

  function racingSignature(state, phase) {
    const lanes = (state.lanes || []).map(function (l) {
      return [l.lane, l.car_number, l.car_name, l.driver, l.time, l.mph, l.place,
        l.bye ? 1 : 0, l.record ? l.record.kind + l.record.label : ""].join("~");
    });
    // The gate and the timer's own state are deliberately not in here: they
    // are in `phase` where they matter, and the gate being closed after a heat
    // must not count as the result having changed.
    return [phase, state.heat_no, state.heat_total, state.race_name].concat(lanes).join("|");
  }

  // Whenever this screen stops showing the racing, so a timer cannot fire into
  // a scene that has been replaced.
  function resetRacing() {
    if (racingHold) clearTimeout(racingHold);
    if (racingSwapTimer) clearTimeout(racingSwapTimer);
    racingHold = null;
    racingSwapTimer = null;
    racingSwapping = false;
    racingPhase = "";
    racingHeat = 0;
    resultVisibleAt = 0;
    racingSig = "";
  }

  // The rows on screen leave to the right, one after another, and the render
  // that follows brings whatever is next in from the left.
  function exitRows() {
    const lanes = root.querySelector(".lanes");
    const rows = lanes ? Array.prototype.slice.call(lanes.children) : [];
    // Whatever comes back arrives from the left rather than appearing.
    racingPhase = "";
    racingHeat = 0;
    resultVisibleAt = 0;
    racingSig = "";
    if (!rows.length) return redrawRacing();
    racingSwapping = true;
    lanes.classList.remove("arriving");
    lanes.classList.add("leaving");
    const step = stillScreen() ? 0 : ROW_OUT_STEP_MS;
    rows.forEach(function (row, i) {
      row.style.setProperty("--depart", i * step + "ms");
    });
    const gone = stillScreen() ? 0 : ROW_OUT_MS + (rows.length - 1) * step;
    racingSwapTimer = setTimeout(function () {
      racingSwapTimer = null;
      racingSwapping = false;
      redrawRacing();
    }, gone);
  }

  // The last heat before the intermission gets its seven seconds like any
  // other, and then the rows leave to the right and the voting screen takes
  // over. Marking the heat first is what stops the render after the slide
  // showing the result all over again.
  function holdThenVote(heat) {
    if (racingHold) return;
    racingHold = setTimeout(function () {
      racingHold = null;
      intermissionAfter = heat;
      exitRows();
    }, Math.max(0, resultVisibleAt + RESULT_HOLD_MS - Date.now()));
  }

  function redrawRacing() {
    renderRacing({ afterExit: true }).catch(function () {
      // The server went quiet mid-swap. The offline bar says so, and the next
      // event redraws.
    });
  }

  // Who is in a lane: the car's name large, its number and driver beneath.
  // The car is what the room is watching go down the track, and a car name is
  // what people shout; the driver is the smaller print. Both phases of the heat
  // use this, so the names never change size between staging and the result.
  function laneWho(l) {
    const car = el("div", "lane-car");
    if (l.bye) {
      car.appendChild(el("div", "lane-title", "empty lane"));
      return car;
    }
    car.appendChild(el("div", "lane-title", l.car_name || l.driver));
    const sub = el("div", "lane-sub");
    sub.appendChild(el("span", "lane-carno", "#" + l.car_number));
    if (l.car_name) sub.appendChild(document.createTextNode(l.driver));
    car.appendChild(sub);
    return car;
  }

  async function renderRacing(opts) {
    // The rows are mid-slide. Whatever this event was, the render that follows
    // the slide reads the state again.
    if (racingSwapping) return;

    const afterExit = !!(opts && opts.afterExit);
    const state = await getJSON("/api/race/state");

    // Three phases, and the screen behaves differently in each.
    //
    //   staged   cars are on the track, gate shut, lane assignments showing
    //   running  the gate is open and they are gone — the screen clears with
    //            them, because there is nothing to report for two seconds and
    //            a frozen table is worse than an empty one
    //   result   they are back, in the order they finished
    const lanes0 = state.lanes || [];
    const finished = lanes0.some(function (l) { return l.time !== undefined; });
    let phase = "staged";
    if (finished) {
      phase = "result";
    } else if (state.gate === "open" && state.running) {
      phase = "running";
    }

    // During the intermission the screen says so rather than sitting on a
    // finished heat for twenty minutes while people are at the bar. The one
    // thing it waits for is the heat that has just run: the intermission
    // starts as that result lands, and the room is owed it. It is announced
    // and held like any other, and the vote follows it.
    if (state.intermission) {
      if (phase !== "result" || state.heat_no === intermissionAfter) {
        resetRacing();
        return renderVoting();
      }
      // Usually the result is already on screen: the intermission is a second
      // event about the same heat, and only the vote's place in the queue is
      // new. Nothing is redrawn in that case, or the rows would restart.
      if (racingPhase === "result" && racingHeat === state.heat_no) {
        holdThenVote(state.heat_no);
        return;
      }
    } else if (intermissionAfter) {
      intermissionAfter = 0;
    }

    // A result is being held on screen. Nothing replaces it until its seven
    // seconds are up — except the cars actually being released, because what
    // is happening on the track outranks what just happened on it.
    if (racingHold) {
      if (phase !== "running") return;
      clearTimeout(racingHold);
      racingHold = null;
      return exitRows();
    }

    // The same heat, in the same state, as is already on screen. Redrawing it
    // would restart whatever is playing.
    const sig = racingSignature(state, phase);
    if (sig === racingSig) return;

    // Between two heats the finished one slides out to the right before the
    // next comes in from the left, and a result waits out its hold first. A
    // re-run is the same heat number arriving again, and gets the same swap.
    if (!afterExit && phase === "staged" &&
        (racingPhase === "result" ||
         (racingPhase === "staged" && racingHeat !== state.heat_no))) {
      if (racingPhase === "result") {
        const wait = resultVisibleAt + RESULT_HOLD_MS - Date.now();
        if (wait > 0) {
          racingHold = setTimeout(function () {
            racingHold = null;
            exitRows();
          }, wait);
          return;
        }
      }
      return exitRows();
    }

    if (!lanes0.length) {
      racingPhase = "";
      racingHeat = 0;
      racingSig = sig;
      const { wrap, body } = sceneShell(state.race_name || "Derby and Ales");
      body.appendChild(el("p", "display-hint", "Waiting for the next heat…"));
      return swap(wrap);
    }

    const sub =
      state.heat_total > 0
        ? "Heat " + state.heat_no + " of " + state.heat_total
        : "Heat " + state.heat_no;
    const { wrap, body } = sceneShell(state.race_name || "Now racing", sub);

    if (phase === "running") {
      // Nothing to read while they are on the track. The cars leave to the
      // right, staggered, and the screen is empty until the times land. A
      // redraw once they have gone — or a swap that already sent them out —
      // must not send them out a second time from where they started.
      const gone = el("div", "lanes leaving");
      const left = afterExit || (racingPhase === "running" && racingHeat === state.heat_no);
      if (!left) {
        const step = stillScreen() ? 0 : ROW_OUT_STEP_MS;
        state.lanes.forEach(function (l, i) {
          if (l.bye) return;
          const row = el("div", "lane-row");
          row.style.setProperty("--depart", i * step + "ms");
          row.appendChild(el("div", "lane-no", l.lane));
          row.appendChild(laneWho(l));
          gone.appendChild(row);
        });
      }
      body.appendChild(gone);
      racingPhase = phase;
      racingHeat = state.heat_no;
      racingSig = sig;
      return swap(wrap);
    }

    const lanes = el("div", "lanes");
    // Coming back in, they are ordered by how they finished rather than by
    // lane. Lane order is how they left; finish order is the thing being
    // announced.
    const rows = state.lanes.slice();
    if (phase === "result") {
      rows.sort(function (a, b) {
        if (a.bye !== b.bye) return a.bye ? 1 : -1;
        const pa = a.place || 99, pb = b.place || 99;
        if (pa !== pb) return pa - pb;
        return a.lane - b.lane;
      });
    }
    // Only animate them in on a change, not on every redraw — a vote arriving
    // should not send the whole table skating across the screen.
    const arriving = phase !== racingPhase || racingHeat !== state.heat_no;
    if (arriving) lanes.classList.add("arriving");
    const step = stillScreen() ? 0 : (phase === "result" ? RESULT_STEP_MS : STAGE_STEP_MS);

    rows.forEach(function (l, i) {
      const row = el("div", "lane-row");
      row.style.setProperty("--arrive", i * step + "ms");
      row.dataset.lane = l.lane;
      if (l.bye) row.classList.add("bye");
      if (l.place === 1) row.classList.add("p1");
      else if (l.place === 2) row.classList.add("p2");

      row.appendChild(el("div", "lane-no", l.lane));

      row.appendChild(laneWho(l));

      const result = el("div", "lane-result");
      if (l.time !== undefined) {
        // A car that never finished must not read as a very fast time.
        const dnf = parseFloat(l.time) >= 9.0;
        if (dnf) {
          row.classList.add("dnf");
          // The recorded time is shown as well as the word, because 9.999 is
          // what goes into the results and somebody will ask about it.
          result.appendChild(el("div", "lane-time", "did not finish"));
          result.appendChild(el("div", "lane-dnf-time", l.time + "s"));
        } else {
          result.appendChild(el("div", "lane-time", l.time + "s"));
          if (l.record) {
            // A record takes the speed's place: it is the bigger news, and the
            // row must not grow and push the lanes below it off the screen.
            row.classList.add("record-" + l.record.kind);
            result.appendChild(el("div", "lane-record", l.record.label));
            result.appendChild(el("div", "lane-record-was", l.record.previous));
          } else {
            result.appendChild(el("div", "lane-mph", l.mph + " mph scale"));
          }
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
    // The hold starts when the last car is on screen, not when the times
    // landed: the point is seven seconds of everybody being able to read it.
    if (phase === "result") {
      resultVisibleAt = arriving
        ? Date.now() + (rows.length - 1) * step + (stillScreen() ? 0 : ROW_IN_MS)
        : resultVisibleAt || Date.now();
    }
    racingPhase = phase;
    racingHeat = state.heat_no;
    racingSig = sig;
    swap(wrap);

    // The vote is waiting behind this result.
    if (phase === "result" && state.intermission) holdThenVote(state.heat_no);
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
  // The impound screen: what is on the track, and what to load next.
  //
  // The official reading this is not watching the racing — they are putting the
  // next four cars into a tray while the current four run. So it is numbers,
  // lanes and pictures, and nothing else. A driver's name is no help when you
  // are looking along a shelf.
  async function renderImpound() {
    const data = await getJSON("/api/race/impound");
    const { wrap, body } = sceneShell(data.race || "Impound", "Loading order");

    if (!data.current) {
      body.appendChild(el("p", "display-hint", "Nothing to load yet."));
      return swap(wrap);
    }

    const rows = el("div", "impound");
    rows.appendChild(impoundRow("On the track", data.current, "now"));
    if (data.upcoming) {
      rows.appendChild(impoundRow("Load next", data.upcoming, "next"));
    } else {
      const done = el("div", "impound-row");
      done.appendChild(el("div", "impound-label", "Load next"));
      done.appendChild(el("p", "display-hint", "That is the last heat."));
      rows.appendChild(done);
    }
    body.appendChild(rows);
    swap(wrap);
  }

  function impoundRow(label, heat, kind) {
    const row = el("div", "impound-row " + kind);
    const head = el("div", "impound-label");
    head.appendChild(el("div", "impound-label-text", label));
    head.appendChild(el("div", "impound-heat", "Heat " + heat.heat));
    row.appendChild(head);

    const cars = el("div", "impound-cars");
    // Highest lane on the left. The lanes are numbered 1 to 4 left to right
    // from behind the gate, but the official loading trays stands with their
    // back to the track, and the trays are numbered the way they see them — so
    // the screen is mirrored to match the tray, not the track. Only here: every
    // other screen faces the room the way the track does.
    (heat.lanes || []).slice().reverse().forEach(function (l) {
      const cell = el("div", "impound-car");
      if (l.car_number === undefined) {
        cell.classList.add("empty");
        cell.appendChild(el("div", "impound-lane", "Lane " + l.lane));
        cell.appendChild(el("div", "impound-bye", "empty"));
        cars.appendChild(cell);
        return;
      }
      cell.appendChild(el("div", "impound-lane", "Lane " + l.lane));

      const pic = el("div", "impound-pic");
      if (l.photo_id) {
        const img = document.createElement("img");
        img.src = "/photo/" + l.photo_id + "?size=card";
        img.alt = "";
        img.addEventListener("error", function () {
          pic.classList.add("empty");
          img.remove();
        });
        pic.appendChild(img);
      } else {
        pic.classList.add("empty");
      }
      cell.appendChild(pic);
      cell.appendChild(el("div", "impound-number", "#" + l.car_number));
      cars.appendChild(cell);
    });
    row.appendChild(cars);
    return row;
  }

  // The championship bracket, as it stands.
  //
  // Read from across a bar, so it is rounds as columns and nothing else: seed,
  // number, car. The matchup on the track is lit, winners stay bright and the
  // beaten car fades, and an upset is marked because it is what the room
  // reacts to. Byes are left out of the first round — they were settled when
  // the bracket was built, and eight empty boxes would push the real races off
  // the screen.
  async function renderBracket() {
    const data = await getJSON("/api/race/bracket");
    if (!data.seeded || !(data.rounds || []).length) {
      const { wrap, body } = sceneShell(data.race || "Championship", "Bracket");
      body.appendChild(el("p", "display-hint", "The bracket has not been built yet."));
      return swap(wrap);
    }

    const sub = data.champion
      ? "Champion: #" + data.champion.number + " " + data.champion.car + " — " + data.champion.driver
      : data.remaining + " race" + (data.remaining === 1 ? "" : "s") + " to go";
    const { wrap, body } = sceneShell(data.race || "Championship", sub);
    if (data.champion) wrap.classList.add("has-champion");

    const grid = el("div", "bracket");
    grid.style.setProperty("--bracket-rounds", String(data.rounds.length));
    data.rounds.forEach(function (rd) {
      const col = el("div", "bracket-col");
      col.appendChild(el("div", "bracket-round", rd.name));
      const list = el("div", "bracket-list");
      rd.matchups
        .filter(function (m) { return !m.walkover; })
        .forEach(function (m) {
          const box = el("div", "bracket-match");
          if (data.on_track === m.id && !m.decided) box.classList.add("on-track");
          if (m.upset) box.classList.add("upset");
          [m.top, m.bottom].forEach(function (slot) {
            const line = el("div", "bracket-slot");
            if (!slot) {
              line.classList.add("pending");
              line.appendChild(el("span", "bracket-seed", ""));
              line.appendChild(el("span", "bracket-car", "—"));
            } else {
              if (m.decided) {
                line.classList.add(slot.entry_id === m.winner_id ? "won" : "lost");
              }
              line.appendChild(el("span", "bracket-seed", String(slot.seed || "")));
              line.appendChild(el("span", "bracket-number", "#" + slot.number));
              line.appendChild(el("span", "bracket-car", slot.car));
            }
            box.appendChild(line);
          });
          if (m.upset) box.appendChild(el("div", "bracket-upset", "upset"));
          list.appendChild(box);
        });
      col.appendChild(list);
      grid.appendChild(col);
    });
    body.appendChild(grid);
    swap(wrap);
  }

  // Car photos, one at a time, for the gaps in an evening: people arriving,
  // the intermission, the wait while results are checked.
  //
  // It runs itself on a timer — nobody should have to stand at a laptop pressing
  // next through twenty-four cars. The list is fetched once per pass, so a car
  // photographed at check-in joins the loop on the next time round rather than
  // jolting the one on screen.
  const SLIDE_MS = 6000;
  let slideTimer = null;
  let slides = [];
  let slideIndex = 0;

  function stopSlides() {
    if (slideTimer) clearTimeout(slideTimer);
    slideTimer = null;
    slides = [];
    slideIndex = 0;
  }

  async function renderSlideshow() {
    if (slideTimer) clearTimeout(slideTimer);
    if (slideIndex >= slides.length) {
      const data = await getJSON("/api/race/roster");
      slides = (data.entries || []).filter(function (e) { return e.photo_id && !e.excluded; });
      slideIndex = 0;
    }
    if (!slides.length) {
      const { wrap, body } = sceneShell("Tonight's cars");
      body.appendChild(el("p", "display-hint", "No cars have been photographed yet."));
      swap(wrap);
      // Keep looking: photos are taken at check-in while this is on screen.
      slideTimer = setTimeout(function () { if (scene === "slideshow") render(); }, SLIDE_MS);
      return;
    }

    const e = slides[slideIndex];
    const { wrap, body } = sceneShell("", "");
    wrap.classList.add("slide");
    const pic = el("div", "slide-pic");
    const img = document.createElement("img");
    img.src = "/photo/" + e.photo_id + "?size=full";
    img.alt = e.car_name || "";
    pic.appendChild(img);
    body.appendChild(pic);
    const cap = el("div", "slide-caption");
    cap.appendChild(el("div", "slide-number", "#" + e.car_number));
    const words = el("div", "slide-words");
    words.appendChild(el("div", "slide-car", e.car_name || ""));
    words.appendChild(el("div", "slide-driver", e.driver));
    cap.appendChild(words);
    body.appendChild(cap);
    swap(wrap);

    slideIndex++;
    slideTimer = setTimeout(function () { if (scene === "slideshow") render(); }, SLIDE_MS);
  }

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
      revealTrophies = data.trophies || 3;
      revealChampionship = !!data.championship;
      revealTrophyNames = data.trophy_names || ["1st", "2nd", "3rd"];
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
        if (s.place <= revealTrophies && !s.tied && !s.is_control) {
          who.appendChild(el("div", "reveal-trophy", trophyFor(s.place)));
        }
        // Said here, as the car comes up, rather than when the last heat
        // landed: announcing a record average then would give away the winner
        // before the reveal had begun.
        if (s.record) {
          const rec = el("div", "reveal-record", s.record.label);
          rec.appendChild(el("span", "reveal-record-was", s.record.previous));
          who.appendChild(rec);
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

  // The night in review. Last on the screens, after the ceremony, for the
  // people who look after the track as much as for the room: which lanes ran
  // fast, where the non-finishes were, and the moments worth remembering.
  async function renderWrapUp() {
    const data = await getJSON("/api/race/wrapup");
    const { wrap, body } = sceneShell(data.race || "Wrap-up", "The night in review");
    if (!data.heats) {
      body.appendChild(el("p", "display-hint", "Nothing has been raced yet."));
      return swap(wrap);
    }

    function who(c) {
      return (c.car_name || "#" + c.car_number) + " \u00b7 " + c.driver;
    }
    function panel(title) {
      const box = el("div", "wu-panel");
      box.appendChild(el("div", "wu-title", title));
      return box;
    }

    const grid = el("div", "wu-grid");

    // Lanes, fastest first. In a normal race every car runs every lane once,
    // so these averages compare like with like: a gap here is the track.
    const lanes = panel("Lanes, fastest first");
    const table = el("div", "wu-lanes");
    ["Lane", "Average", "Won", "Expected", "DNFs"].forEach(function (h) {
      table.appendChild(el("div", "wu-th", h));
    });
    const odd = [];
    (data.lanes || []).forEach(function (l) {
      const cls = l.unusual ? " wu-unusual" : "";
      table.appendChild(el("div", "wu-lane-no" + cls, l.lane));
      table.appendChild(el("div", "wu-num", l.average ? l.average + "s" : "\u2014"));
      table.appendChild(el("div", "wu-num" + cls, l.wins));
      table.appendChild(el("div", "wu-num wu-dim", l.expected));
      table.appendChild(el("div", "wu-num" + (l.dnfs ? " wu-dnf" : ""), l.dnfs));
      if (l.unusual) odd.push(l);
    });
    lanes.appendChild(table);
    // Wins against expected is noisy over twenty heats, so a lane is only
    // called out when luck is a poor explanation — with the odds, so nobody
    // re-shims a track over a 1 in 3.
    odd.forEach(function (l) {
      const more = l.wins > parseFloat(l.expected);
      lanes.appendChild(el("div", "wu-warn",
        "Lane " + l.lane + " won " + (more ? "more" : "fewer") + " than chance would give: 1 in " +
        l.one_in + ". Worth a look at the track."));
    });
    lanes.appendChild(el("div", "wu-foot",
      data.heats + " heats \u00b7 " + data.runs + " runs \u00b7 " + data.dnfs + " did not finish"));
    grid.appendChild(lanes);

    const fast = panel("Fastest heats");
    (data.fastest_heats || []).forEach(function (h, i) {
      const row = el("div", "wu-fast");
      row.appendChild(el("div", "wu-rank", i + 1));
      const mid = el("div", "wu-mid");
      // The car leads, as it does on the racing screen, with the driver and
      // where it happened on lines of their own so neither is cut short.
      mid.appendChild(el("div", "wu-who", h.car_name || "#" + h.car_number));
      mid.appendChild(el("div", "wu-sub", h.driver));
      mid.appendChild(el("div", "wu-sub", "Heat " + h.heat + " \u00b7 lane " + h.lane));
      row.appendChild(mid);
      row.appendChild(el("div", "wu-time", h.time + "s"));
      fast.appendChild(row);
    });
    grid.appendChild(fast);

    const moments = panel("Moments");
    function moment(label, text, sub) {
      const row = el("div", "wu-moment");
      row.appendChild(el("div", "wu-label", label));
      row.appendChild(el("div", "wu-who", text));
      if (sub) row.appendChild(el("div", "wu-sub wu-wrap", sub));
      moments.appendChild(row);
    }
    if (data.margin) {
      moment("Winning margin", data.margin.gap + "s",
        data.margin.winner + " over " + data.margin.runner_up);
    }
    if (data.closest) {
      moment("Closest finish", data.closest.gap + "s in heat " + data.closest.heat,
        who(data.closest.winner) + " over " + (data.closest.runner_up.car_name || data.closest.runner_up.driver));
    }
    if (data.steadiest) {
      moment("Most consistent", who(data.steadiest),
        "every run within " + data.steadiest.gap + "s (" + data.steadiest.best + "\u2013" + data.steadiest.worst + ")");
    }
    if (moments.children.length > 1) grid.appendChild(moments);

    const recs = panel("Records tonight");
    (data.records || []).forEach(function (r) {
      const row = el("div", "wu-moment");
      row.appendChild(el("div", "wu-label wu-record wu-record-" + r.kind, r.label));
      row.appendChild(el("div", "wu-who", (r.time ? r.time + "s \u00b7 " : "") + who(r.who)));
      row.appendChild(el("div", "wu-sub wu-wrap", r.previous));
      recs.appendChild(row);
    });
    const pbs = data.personal_bests || [];
    if (pbs.length) {
      const row = el("div", "wu-moment");
      row.appendChild(el("div", "wu-label wu-record wu-record-pb",
        pbs.length + (pbs.length === 1 ? " personal best" : " personal bests")));
      row.appendChild(el("div", "wu-sub wu-wrap", pbs.join(", ")));
      recs.appendChild(row);
    }
    if (recs.children.length === 1) {
      recs.appendChild(el("div", "wu-sub", "None broken tonight."));
    }
    grid.appendChild(recs);

    // Timer health: everything that went wrong, heat by heat, or a plain
    // statement that nothing did.
    // Timer health sits under the lanes: both are about the track rather than
    // the racing, and the lanes table leaves room beneath it.
    const tm = data.timer || {};
    const health = el("div", "wu-timer");
    health.appendChild(el("div", "wu-title wu-subtitle", "Timer"));
    function trouble(label, item, extra) {
      if (!item || !item.count) return;
      const row = el("div", "wu-moment");
      row.appendChild(el("div", "wu-label", label));
      let text = item.count + (item.count === 1 ? " heat" : " heats") + ": " + item.heats.join(", ");
      if (extra) text += extra;
      row.appendChild(el("div", "wu-sub", text));
      health.appendChild(row);
    }
    function laneList(item) {
      if (!item || !item.lanes) return "";
      const parts = Object.keys(item.lanes).map(function (l) {
        return "lane " + l + (item.lanes[l] > 1 ? " \u00d7" + item.lanes[l] : "");
      });
      return " \u00b7 " + parts.join(", ");
    }
    trouble("Bad reads", tm.bad_reads, laneList(tm.bad_reads));
    trouble("False triggers", tm.false_triggers);
    trouble("Re-run", tm.reruns);
    trouble("Typed in by hand", tm.typed);
    if ((tm.flagged || []).length) {
      trouble("Still flagged as a fault", { count: tm.flagged.length, heats: tm.flagged });
    }
    if (health.children.length === 1) {
      health.appendChild(el("div", "wu-sub wu-ok", "No trouble \u2014 every heat read cleanly, first time."));
    }
    lanes.appendChild(health);

    // The club's fastest nights, and where tonight landed. Tonight is marked
    // in the list when it made it, and given its place below when it did not.
    if (data.top_races) {
      const tr = data.top_races;
      const box = panel("Top MDnA races ever");
      (tr.top || []).forEach(function (r) {
        const row = el("div", "wu-race" + (r.tonight ? " wu-race-tonight" : ""));
        row.appendChild(el("div", "wu-rank", r.rank));
        row.appendChild(el("div", "wu-who", r.race + (r.tonight ? " \u2014 tonight" : "")));
        row.appendChild(el("div", "wu-race-avg", r.average + "s"));
        box.appendChild(row);
      });
      const tonight = tr.this_race;
      if (tonight && tonight.championship) {
        box.appendChild(el("div", "wu-sub wu-this", "Championships are not ranked: their field is the season's fastest cars."));
      } else if (tonight && !(tr.top || []).some(function (r) { return r.tonight; })) {
        const row = el("div", "wu-this");
        row.appendChild(el("div", "wu-label", "This race"));
        row.appendChild(el("div", "wu-who", ordinal(tonight.rank) + " place"));
        row.appendChild(el("div", "wu-sub", tonight.average + "s average \u00b7 of " + tr.of + " race nights"));
        box.appendChild(row);
      }
      grid.appendChild(box);
    }

    // The CONTROL car: the same car every night, so the one measure of the
    // track itself rather than of anybody's car.
    if (data.control) {
      const c = data.control;
      const ctl = panel("CONTROL car");
      ctl.appendChild(el("div", "wu-time", c.average + "s"));
      if (c.versus) {
        ctl.appendChild(el("div", "wu-who", c.versus));
        ctl.appendChild(el("div", "wu-sub", "usual " + c.usual + "s, the median of its last " + c.nights +
          (c.nights === 1 ? " night" : " nights")));
      }
      if (c.spread) {
        ctl.appendChild(el("div", "wu-sub", "tonight's runs within " + c.spread + "s (" + c.best +
          "\u2013" + c.worst + ")" + (c.dnfs ? " \u00b7 " + c.dnfs + " did not finish" : "")));
      }
      if ((c.recent || []).length) {
        const hist = el("div", "wu-history");
        c.recent.forEach(function (n) {
          const cell = el("div", "wu-hist");
          cell.appendChild(el("div", "wu-hist-time", n.average));
          cell.appendChild(el("div", "wu-hist-race", n.race.replace(/^20(\d\d) /, "\u2019$1 ")));
          hist.appendChild(cell);
        });
        ctl.appendChild(hist);
      }
      grid.appendChild(ctl);
    }

    body.appendChild(grid);
    swap(wrap);
  }

  // The club records, for the room: something to look at between segments
  // that gives people a number to beat.
  async function renderRecords() {
    const data = await getJSON("/api/records");
    const { wrap, body } = sceneShell("Club records", data.demo ? "Including the demo season" : "");

    if (!data.fastest_run) {
      body.appendChild(el("p", "display-hint", "No records yet."));
      return swap(wrap);
    }

    function card(what, r) {
      const box = el("div", "rec-card");
      box.appendChild(el("div", "rec-what", what));
      box.appendChild(el("div", "rec-time", r.time + "s"));
      box.appendChild(el("div", "rec-who", r.driver));
      box.appendChild(el("div", "rec-car", (r.car_name || "#" + r.car_number) + " \u00b7 " + r.race));
      return box;
    }

    const top = el("div", "rec-top");
    top.appendChild(card("Fastest run", data.fastest_run));
    if (data.fastest_average) top.appendChild(card("Fastest average", data.fastest_average));
    body.appendChild(top);

    const lanes = el("div", "rec-lanes");
    (data.lanes || []).forEach(function (l) {
      const box = el("div", "rec-lane");
      box.appendChild(el("div", "rec-what", "Lane " + l.lane));
      box.appendChild(el("div", "rec-lane-time", l.time + "s"));
      box.appendChild(el("div", "rec-car", l.driver));
      lanes.appendChild(box);
    });
    body.appendChild(lanes);

    if ((data.career || []).length) {
      const list = el("div", "rec-career");
      list.appendChild(el("div", "rec-what", "Most race wins"));
      data.career.forEach(function (c) {
        const row = el("div", "rec-career-row");
        row.appendChild(el("span", "rec-career-name", c.driver));
        let tally = c.wins + (c.wins === 1 ? " win" : " wins");
        if (c.cups) tally += " \u00b7 " + c.cups + (c.cups === 1 ? " D'Ale Cup" : " D'Ale Cups");
        row.appendChild(el("span", "rec-career-tally", tally));
        list.appendChild(row);
      });
      body.appendChild(list);
    }
    swap(wrap);
  }

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
    // The names come from the server. The championship hands out one trophy,
    // The D'Ale Cup, and it is not a "1st place" one.
    const name = revealTrophyNames[place - 1] || "";
    return revealChampionship ? name : name + " place trophy";
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
    const source = new EventSource("/events?topics=race,timer,display,system,bracket");

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
          scene === "final-standings" || scene === "impound" || scene === "bracket" ||
          scene === "records" || scene === "wrap-up") render();
    });

    source.addEventListener("bracket", function () {
      // A matchup decided, or the bracket built or rebuilt.
      if (scene === "bracket" || scene === "impound") render();
    });

    source.addEventListener("timer", function () {
      // The gate opening is what starts the cars, and it is a timer event
      // rather than a race one. The now-racing screen clears on it.
      if (scene === "now-racing" || scene === "impound") render();
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
