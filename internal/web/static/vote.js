// The voting tablet.
//
// One shared device, staffed, used during the intermission. There is no dedupe
// by design: a per-device limit would block every voter after the first.
//
// The flow is: question 1, question 2, thank you, back to the start. Each tap
// is final and moves on by itself — asking a person holding a drink to confirm
// a vote is a step too many.

(function () {
  "use strict";

  const root = document.getElementById("ballot");

  // How long the thank-you stays up before resetting for the next voter.
  const THANK_YOU_MS = 4000;

  let categories = [];
  let cars = [];
  let open = false;
  let step = 0;
  let busy = false;
  let resetTimer = null;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = String(text);
    return node;
  }

  async function getJSON(url) {
    const res = await fetch(url);
    if (!res.ok) throw new Error(res.statusText);
    return res.json();
  }

  async function post(url, params) {
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(params || {}).toString(),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || res.statusText);
    return body;
  }

  // --- loading --------------------------------------------------------------

  async function load() {
    try {
      const data = await getJSON("/api/vote/ballot");
      const wasOpen = open;
      open = !!data.open;
      categories = data.categories || [];
      cars = data.cars || [];

      // Voting closing mid-ballot sends the tablet back to the closed screen
      // rather than leaving a live-looking grid that swallows taps.
      if (wasOpen && !open) {
        step = 0;
        render();
        return;
      }
      if (!wasOpen && open) {
        step = 0;
      }
      render();
    } catch (err) {
      renderClosed("Cannot reach the race server", "It should come back on its own.");
    }
  }

  // --- rendering ------------------------------------------------------------

  function render() {
    if (!open) {
      return renderClosed(
        "Voting is closed",
        "Voting opens during the intermission, about halfway through the racing."
      );
    }
    if (!categories.length) {
      return renderClosed("Nothing to vote on yet", "The ballot has not been set up.");
    }
    if (!cars.length) {
      return renderClosed("No cars yet", "Nobody has checked in.");
    }
    if (step >= categories.length) {
      return renderThankYou();
    }
    renderQuestion(categories[step]);
  }

  function renderClosed(title, detail) {
    const wrap = el("div", "ballot-closed");
    wrap.appendChild(el("div", "mark", "⏸"));
    wrap.appendChild(el("h1", null, title));
    wrap.appendChild(el("p", null, detail));
    root.replaceChildren(wrap);
  }

  function renderThankYou() {
    const wrap = el("div", "ballot-done");
    wrap.appendChild(el("div", "mark", "✓"));
    wrap.appendChild(el("h1", null, "Thank you"));
    wrap.appendChild(el("p", null, "Both votes recorded. Passing to the next voter…"));
    root.replaceChildren(wrap);

    clearTimeout(resetTimer);
    resetTimer = setTimeout(function () {
      step = 0;
      render();
    }, THANK_YOU_MS);
  }

  function renderQuestion(category) {
    const wrap = el("div", "ballot");

    const head = el("div", "ballot-head");
    head.appendChild(
      el("p", "ballot-step", "Step " + (step + 1) + " of " + categories.length)
    );
    head.appendChild(el("h1", "ballot-question", category.label));
    head.appendChild(el("p", "ballot-hint", "Tap the car you like best."));
    wrap.appendChild(head);

    const grid = el("div", "ballot-grid");
    cars.forEach(function (car) {
      grid.appendChild(carTile(car, category));
    });
    wrap.appendChild(grid);

    const pips = el("div", "ballot-progress");
    categories.forEach(function (_, i) {
      pips.appendChild(el("span", "pip" + (i <= step ? " on" : "")));
    });
    wrap.appendChild(pips);

    root.replaceChildren(wrap);
  }

  function carTile(car, category) {
    const tile = el("button", "car-tile");
    tile.type = "button";

    const photo = el("div", "car-photo");
    if (car.photo_id) {
      const img = document.createElement("img");
      img.src = "/photo/" + car.photo_id + "?size=card";
      img.alt = "";
      img.loading = "lazy";
      photo.appendChild(img);
    } else {
      // No photo is common early in a season; the car number is what people
      // match against the car in front of them anyway.
      photo.appendChild(el("div", "no-photo", "#" + car.car_number));
    }
    tile.appendChild(photo);

    const label = el("div", "car-label");
    label.appendChild(el("div", "car-name", car.car_name || "Car " + car.car_number));
    label.appendChild(el("div", "car-meta", "#" + car.car_number + "  " + car.driver));
    tile.appendChild(label);

    tile.addEventListener("click", function () {
      vote(category, car, tile);
    });
    return tile;
  }

  // --- voting ---------------------------------------------------------------

  async function vote(category, car, tile) {
    if (busy) return;
    busy = true;

    // Mark the choice immediately. The tap must feel answered even if the
    // request takes a moment.
    tile.classList.add("chosen");

    try {
      await post("/api/vote/cast", { category_id: category.id, entry_id: car.id });
      // A short beat so the confirmation is visible, then move on.
      setTimeout(function () {
        busy = false;
        step++;
        render();
      }, 320);
    } catch (err) {
      busy = false;
      tile.classList.remove("chosen");
      // The usual cause is the coordinator resuming racing mid-vote.
      load();
    }
  }

  // --- live -----------------------------------------------------------------

  const source = new EventSource("/events?topics=vote,race");

  source.addEventListener("vote", function (e) {
    let ev;
    try {
      ev = JSON.parse(e.data);
    } catch (err) {
      return;
    }
    // Opening and closing are the only things the tablet reacts to; it must
    // not redraw under someone's finger every time a vote is cast.
    if (ev.kind === "opened" || ev.kind === "closed" || ev.kind === "categories") {
      load();
    }
  });

  source.addEventListener("race", function (e) {
    let ev;
    try {
      ev = JSON.parse(e.data);
    } catch (err) {
      return;
    }
    if (ev.kind === "intermission" || ev.kind === "resumed" || ev.kind === "intermission-ended") {
      load();
    }
  });

  load();
  // A slow safety net in case an event is ever missed; the tablet is unattended
  // for long stretches.
  setInterval(load, 20000);
})();
