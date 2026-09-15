// Race control.
//
// The screen updates itself from the event stream; the buttons here are for
// exceptions — closing check-in, a re-run, stopping — not for running a heat.

(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);

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

  function say(id, text, isError) {
    const el = $(id);
    if (!el) return;
    el.textContent = text;
    el.classList.toggle("err-text", !!isError);
  }

  function applyState(s) {
    if (!s) return;
    if ($("heat-no")) $("heat-no").textContent = s.heat_no || 0;
    if ($("heat-total")) $("heat-total").textContent = s.heat_total || 0;
    if ($("timer-state")) $("timer-state").textContent = s.timer_state || s.timer || "";
    if ($("gate-state")) $("gate-state").textContent = s.gate || "";
    const badge = $("run-badge");
    if (badge) {
      badge.textContent = s.running ? "Racing" : "Stopped";
      badge.className = "badge " + (s.running ? "ok" : "off");
    }
  }

  // Render the armed heat, and the times as they land.
  async function refreshHeat() {
    const box = $("current-heat");
    if (!box) return;
    try {
      const state = await fetch("/api/race/state").then((r) => r.json());
      applyState(state);

      if (!state.lanes || !state.lanes.length) {
        box.innerHTML = '<p class="empty">Nothing armed.</p>';
        return;
      }
      box.innerHTML = "";
      const list = document.createElement("div");
      list.className = "current-lanes";
      state.lanes.forEach(function (l) {
        const row = document.createElement("div");
        row.className = "current-lane" + (l.bye ? " bye" : "");
        row.innerHTML =
          '<span class="cl-lane">' + l.lane + "</span>" +
          '<span class="cl-who">' +
          (l.bye ? "<em>empty lane</em>" : escapeHTML(l.driver) + ' <span class="note">#' +
            l.car_number + " " + escapeHTML(l.car_name || "") + "</span>") +
          "</span>" +
          '<span class="cl-time">' +
          (l.time !== undefined
            ? (parseFloat(l.time) >= 9 ? "no finish" : l.time + "s · " + l.mph + " mph")
            : (l.bye ? "" : "ready")) +
          "</span>" +
          '<span class="cl-place">' + (l.place ? l.place : "") + "</span>";
        list.appendChild(row);
      });
      box.appendChild(list);

      if (state.advance_in > 0) {
        const p = document.createElement("p");
        p.className = "note";
        p.textContent = "Next heat in " + Math.ceil(state.advance_in) + "s";
        box.appendChild(p);
      }
    } catch (err) {
      /* the connection indicator already reports this */
    }
  }

  function escapeHTML(s) {
    const d = document.createElement("div");
    d.textContent = s == null ? "" : s;
    return d.innerHTML;
  }

  // --- buttons --------------------------------------------------------------

  function wire(id, fn) {
    const btn = $(id);
    if (btn) btn.addEventListener("click", fn);
  }

  wire("load-race", async function () {
    try {
      await post("/api/race/load", { race_id: $("race-picker").value });
      location.reload();
    } catch (err) {
      say("race-status", err.message, true);
    }
  });

  wire("demo-tie", async function () {
    try {
      await post("/api/race/demo-tie", {});
      location.reload();
    } catch (err) {
      say("demo-tie-status", err.message, true);
    }
  });

  wire("close-checkin", async function () {
    if (!confirm("Close check-in and build the heat schedule?\n\nNo more cars can be added after this.")) return;
    say("schedule-status", "Building…");
    try {
      const r = await post("/api/race/schedule", {});
      let msg = r.heats + " heats for " + r.cars + " cars, " + r.runs_per_car + " runs each.";
      if (r.perfect) msg += " No pair of cars meets twice.";
      if (r.warning) msg += " " + r.warning;
      else if (r.note) msg += " " + r.note;
      say("schedule-status", msg);
      setTimeout(() => location.reload(), 2500);
    } catch (err) {
      say("schedule-status", err.message, true);
    }
  });

  wire("start-race", async function () {
    try {
      applyState(await post("/api/race/start", {}));
      say("run-status", "Racing.");
      refreshHeat();
    } catch (err) {
      say("run-status", err.message, true);
    }
  });

  wire("stop-race", async function () {
    applyState(await post("/api/race/stop", {}));
    say("run-status", "Stopped.");
  });

  wire("next-heat", async function () {
    try {
      applyState(await post("/api/race/next", {}));
      say("run-status", "Armed.");
      refreshHeat();
    } catch (err) {
      say("run-status", err.message, true);
    }
  });

  const auto = $("auto-advance");
  if (auto) {
    auto.addEventListener("change", async function () {
      await post("/api/race/auto", { on: auto.checked ? "true" : "false" });
      say("run-status", auto.checked ? "Advancing automatically." : "Manual advance.");
    });
  }

  document.querySelectorAll(".rerun").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      if (!confirm("Re-run this heat?\n\nIts recorded times will be cleared.")) return;
      try {
        await post("/api/race/rerun", { heat_id: btn.dataset.heat });
        location.reload();
      } catch (err) {
        say("run-status", err.message, true);
      }
    });
  });

  document.querySelectorAll(".run-off").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/race/runoff", { place: btn.dataset.place });
        location.reload();
      } catch (err) {
        say("run-status", err.message, true);
      }
    });
  });

  // --- entering times by hand -------------------------------------------------
  //
  // A correction, or a whole night with no timer at all. Both are the same
  // thing, and both were constant in the old system.

  const timesDialog = document.getElementById("times-dialog");
  let timesHeat = null;

  document.querySelectorAll(".enter-times").forEach(function (btn) {
    btn.addEventListener("click", function () {
      timesHeat = btn.dataset.heat;
      const box = document.getElementById("times-lanes");
      const err = document.getElementById("times-error");
      err.textContent = "";
      box.replaceChildren();

      document.getElementById("times-title").textContent =
        "Heat " + btn.dataset.number;

      // Whatever is already recorded, so an edit starts from the real numbers
      // rather than from nothing.
      const existing = {};
      (btn.dataset.times || "").split(";").forEach(function (pair) {
        if (!pair) return;
        const [lane, t] = pair.split("=");
        if (lane) existing[lane] = t || "";
      });

      (btn.dataset.lanes || "").split(",").forEach(function (entry) {
        if (!entry) return;
        const parts = entry.split(":");
        const lane = parts[0];
        const row = document.createElement("label");
        row.className = "times-row";

        const label = document.createElement("span");
        label.className = "lbl";
        label.textContent = "Lane " + lane + " — #" + parts[1] + " " + (parts.slice(2).join(":") || "");
        row.appendChild(label);

        const input = document.createElement("input");
        input.type = "text";
        input.name = "lane_" + lane;
        input.inputMode = "decimal";
        input.autocomplete = "off";
        input.placeholder = "2.431 or DNF";
        input.value = existing[lane] || "";
        row.appendChild(input);

        box.appendChild(row);
      });

      timesDialog.showModal();
      const first = box.querySelector("input");
      if (first) first.focus();
    });
  });

  if (timesDialog) {
    timesDialog.addEventListener("close", async function () {
      if (timesDialog.returnValue !== "save" || !timesHeat) return;
      const params = { heat_id: timesHeat };
      document.querySelectorAll("#times-lanes input").forEach(function (i) {
        params[i.name] = i.value;
      });
      try {
        await post("/api/race/times", params);
        location.reload();
      } catch (err) {
        say("run-status", err.message, true);
      }
    });
  }

  // --- live -----------------------------------------------------------------

  const source = new EventSource("/events?topics=race,timer");
  source.addEventListener("race", function (e) {
    let ev;
    try { ev = JSON.parse(e.data); } catch (err) { return; }
    if (ev.kind === "suspect-result") {
      const d = ev.data || {};
      say("run-status", d.reason || "Suspect result; racing paused.", true);
    }
    refreshHeat();
  });
  source.addEventListener("timer", refreshHeat);

  refreshHeat();
  setInterval(refreshHeat, 1000);
})();
