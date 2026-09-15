// The championship screen.
//
// Building the bracket fixes a whole season's results into a running order, and
// once it has started it cannot be rebuilt — so that one button confirms, and
// everything else here is a normal race-night control.

(function () {
  "use strict";

  function seasonOf(el) {
    return (el && el.dataset.season) || "";
  }

  async function post(url, params) {
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(params || {}).toString(),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  function say(id, text, bad) {
    const el = document.getElementById(id);
    if (!el) return;
    el.textContent = text;
    el.classList.toggle("err-text", !!bad);
  }

  // --- building the bracket ---------------------------------------------------

  const generate = document.getElementById("generate-bracket");
  if (generate) {
    generate.addEventListener("click", async function () {
      if (!confirm(
        "Build the championship bracket from this seeding?\n\n" +
        "Anyone who has not checked in is left out and the seeds are " +
        "renumbered. Once the first matchup has been raced the bracket " +
        "cannot be rebuilt."
      )) return;

      generate.disabled = true;
      say("generate-status", "Building…");
      try {
        const data = await post("/api/bracket/generate", { season_id: seasonOf(generate) });
        say("generate-status",
          data.entrants + " cars, " + data.byes + " byes, " + data.rounds + " rounds.");
        setTimeout(() => location.reload(), 500);
      } catch (err) {
        say("generate-status", err.message, true);
        generate.disabled = false;
      }
    });
  }

  // --- normal race or bracket --------------------------------------------------

  for (const [id, format] of [["run-as-bracket", "bracket"], ["run-as-standard", "standard"]]) {
    const btn = document.getElementById(id);
    if (!btn) continue;
    btn.addEventListener("click", async function () {
      btn.disabled = true;
      try {
        await post("/api/race/format", { race_id: btn.dataset.race, format: format });
        location.reload();
      } catch (err) {
        say(format === "bracket" ? "format-status" : "generate-status", err.message, true);
        btn.disabled = false;
      }
    });
  }

  // --- running it --------------------------------------------------------------

  const armNext = document.getElementById("arm-next");
  if (armNext) {
    armNext.addEventListener("click", async function () {
      try {
        await post("/api/bracket/arm", { season_id: seasonOf(armNext) });
        say("bracket-status", "Armed. Watch the race screen.");
      } catch (err) {
        say("bracket-status", err.message, true);
      }
    });
  }

  document.querySelectorAll(".arm-matchup").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/bracket/arm", {
          season_id: seasonOf(btn), matchup_id: btn.dataset.matchup,
        });
        say("bracket-status", "Armed.");
      } catch (err) {
        say("bracket-status", err.message, true);
      }
    });
  });

  document.querySelectorAll(".swap-lanes").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/bracket/swap", { matchup_id: btn.dataset.matchup });
        location.reload();
      } catch (err) {
        say("bracket-status", err.message, true);
      }
    });
  });

  document.querySelectorAll(".record-result").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/bracket/result", {
          season_id: seasonOf(btn), matchup_id: btn.dataset.matchup,
        });
        location.reload();
      } catch (err) {
        // The usual cause is a dead heat, which the software will not settle.
        say("bracket-status", err.message, true);
      }
    });
  });

  // --- live ---------------------------------------------------------------------

  const source = new EventSource("/events?topics=bracket");
  source.addEventListener("bracket", function (e) {
    let ev;
    try {
      ev = JSON.parse(e.data);
    } catch (err) {
      return;
    }
    if (ev.kind === "result" || ev.kind === "generated" || ev.kind === "champion") {
      // The bracket is a whole-page calculation: one result moves a car into
      // the round above and changes which matchup is next. Patching parts of
      // it would leave the page disagreeing with itself.
      location.reload();
    }
  });
})();
