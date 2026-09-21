// The voting tablet.
//
// One shared device, staffed, used during the intermission. There is no dedupe
// by design: a per-device limit would block every voter after the first.
//
// The flow is: question 1, question 2, thank you, back to the start. Each tap
// is final and moves on by itself — asking a person holding a drink to confirm
// a vote is a step too many. The handover at the end is the exception: the
// tablet waits for somebody to say they are done with it.

(function () {
  "use strict";

  const root = document.getElementById("ballot");

  let categories = [];
  let cars = [];
  let theme = "";
  let open = false;
  let step = 0;
  let busy = false;

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
      theme = data.theme || "";

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

  // The handover waits for a person rather than a clock.
  //
  // It used to reset itself after four seconds, which is either too long for
  // somebody standing there holding the tablet out, or — far worse — too short
  // for one being set back down on the table, in which case the next voter
  // walks up to a live ballot and their first tap is somebody else's vote.
  function renderThankYou() {
    const wrap = el("div", "ballot-done");
    wrap.appendChild(el("div", "mark", "✓"));
    wrap.appendChild(el("h1", null, "Thank you"));
    wrap.appendChild(el("p", null, "Both votes recorded."));

    const next = el("button", "ballot-next", "Pass to the next voter");
    next.type = "button";
    next.addEventListener("click", function () {
      step = 0;
      render();
    });
    wrap.appendChild(next);
    root.replaceChildren(wrap);

    // The button is what the tablet is handed over on, so nothing else on the
    // screen should be able to take the tap.
    guardTaps(next);
  }

  function renderQuestion(category) {
    const head = el("div", "ballot-head");
    head.appendChild(
      el("p", "ballot-step", "Step " + (step + 1) + " of " + categories.length)
    );
    head.appendChild(el("h1", "ballot-question", category.label));
    // The theme question means nothing without the theme. Asking for the "best
    // themed car" and leaving the voter to remember what the theme was is a lot
    // to ask of somebody on their third pint.
    if (category.key === "theme" && theme) {
      head.appendChild(el("p", "ballot-theme", theme));
    }
    head.appendChild(el("p", "ballot-hint", "Tap the car you like best."));

    const grid = el("div", "ballot-grid");
    cars.forEach(function (car) {
      grid.appendChild(carTile(car, category));
    });

    const pips = el("div", "ballot-progress");
    categories.forEach(function (_, i) {
      pips.appendChild(el("span", "pip" + (i <= step ? " on" : "")));
    });

    const more = el("div", "ballot-more");
    root.replaceChildren(head, grid, more, pips);

    // A fast second tap would otherwise land on the next question and vote
    // again. The grid ignores taps for a moment after it changes.
    guardTaps(grid);
    watchOverflow(grid, more, cars.length);
  }

  // watchOverflow tells people there are more cars below.
  //
  // Without this, anyone who does not think to scroll votes only among the cars
  // they can see — which would quietly favour whichever cars sort first.
  function watchOverflow(grid, more, total) {
    function update() {
      const overflowing = grid.scrollHeight > grid.clientHeight + 4;
      grid.classList.toggle("overflowing", overflowing);
      if (!overflowing) {
        more.classList.remove("show");
        return;
      }

      const atEnd = grid.scrollTop + grid.clientHeight >= grid.scrollHeight - 8;
      grid.classList.toggle("at-end", atEnd);
      more.classList.toggle("show", !atEnd);

      if (!atEnd) {
        // Count the cars still out of sight, so the prompt is concrete rather
        // than a vague "scroll for more".
        const tiles = grid.querySelectorAll(".car-tile");
        const bottom = grid.scrollTop + grid.clientHeight;
        let hidden = 0;
        tiles.forEach(function (t) {
          if (t.offsetTop + t.offsetHeight * 0.5 > bottom) hidden++;
        });
        more.textContent =
          hidden > 0
            ? hidden + " more car" + (hidden === 1 ? "" : "s") + " below \u2193"
            : "more below \u2193";
      }
    }

    grid.addEventListener("scroll", update, { passive: true });
    window.addEventListener("resize", update);
    // Images load after the first paint and change the height.
    grid.querySelectorAll("img").forEach(function (img) {
      img.addEventListener("load", update);
    });
    update();
    setTimeout(update, 400);
  }

  // guardTaps blocks input briefly after the screen is replaced, so a tap meant
  // for the previous question cannot carry through to what took its place.
  function guardTaps(node) {
    node.style.pointerEvents = "none";
    setTimeout(function () {
      node.style.pointerEvents = "";
    }, 450);
  }

  // A tile shows the photo and the car number together. The number is how
  // someone matches a tile to the car sitting on the table in front of them;
  // the photo confirms it. Neither alone is enough.
  function carTile(car, category) {
    const tile = el("button", "car-tile");
    tile.type = "button";

    const photo = el("div", "car-photo");
    if (car.photo_id) {
      const img = document.createElement("img");
      img.src = "/photo/" + car.photo_id + "?size=card";
      img.alt = "";
      img.loading = "lazy";
      // A broken image would otherwise leave an empty box with no number.
      img.addEventListener("error", function () {
        photo.classList.add("empty");
        img.remove();
      });
      photo.appendChild(img);
    } else {
      // No photo is common early in a season; the number fills the space so the
      // tile is still matchable.
      photo.classList.add("empty");
      photo.appendChild(el("div", "no-photo-note", "no photo"));
    }
    photo.appendChild(el("div", "car-number", car.car_number));
    tile.appendChild(photo);

    // The car, and only the car. These two trophies are for how a car looks and
    // how well it carries its theme, and a driver's name on the tile invites a
    // vote for the person instead — which is the one thing the design and theme
    // votes must not be.
    const label = el("div", "car-label");
    label.appendChild(el("div", "car-name", car.car_name || "Car " + car.car_number));
    tile.appendChild(label);

    // Spoken aloud by a screen reader, and what the tile means in one phrase.
    tile.setAttribute(
      "aria-label",
      "Car " + car.car_number + (car.car_name ? ", " + car.car_name : "")
    );

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
