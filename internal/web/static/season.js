// The season screen.
//
// Every action here changes a published number, so each one says what it did
// and the page reloads afterwards rather than trying to patch itself. The
// standings are a whole-table calculation — one substitution reseeds everything
// below it — so a partial update would be a lie.

(function () {
  "use strict";

  const seasonID = document.querySelector("[data-season]")?.dataset.season || "";

  async function post(url, params) {
    const body = new URLSearchParams(params || {});
    if (seasonID) body.set("season_id", seasonID);
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  function say(el, text, bad) {
    if (!el) return;
    el.textContent = text;
    el.classList.toggle("err-text", !!bad);
  }

  // --- recompute ------------------------------------------------------------

  const recompute = document.getElementById("recompute");
  if (recompute) {
    recompute.addEventListener("click", async function () {
      // This rewrites every points row in the season, so it asks first.
      if (!confirm(
        "Recompute every finished race in this season?\n\n" +
        "This rewrites the points using the current settings. A backup is " +
        "taken first, and the change is recorded in the audit log."
      )) return;

      const status = document.getElementById("recompute-status");
      recompute.disabled = true;
      say(status, "Recomputing…");
      try {
        const data = await post("/api/season/recompute");
        say(status, data.races + " race" + (data.races === 1 ? "" : "s") + " recomputed.");
        setTimeout(() => location.reload(), 600);
      } catch (err) {
        say(status, err.message, true);
        recompute.disabled = false;
      }
    });
  }

  // --- season settings -----------------------------------------------------

  const settings = document.getElementById("season-settings");
  if (settings) {
    settings.addEventListener("submit", async function (e) {
      e.preventDefault();
      const status = document.getElementById("settings-status");
      const params = {};
      new FormData(settings).forEach(function (v, k) { params[k] = v; });
      // An unticked checkbox is simply absent from the form, which would read
      // as "no change" rather than "turn it off".
      params.points_count_control = settings.elements.points_count_control.checked ? "true" : "false";
      try {
        const data = await post("/api/season/settings", params);
        if (!data.changed || !data.changed.length) {
          say(status, "Nothing changed.");
          return;
        }
        let msg = "Saved: " + data.changed.join("; ") + ".";
        if (data.needs_recompute) msg += " Finished races keep their old points until you recompute.";
        if (data.bracket_built) msg += " The championship bracket is already built and was not rebuilt.";
        say(status, msg);
        // Leave the message up long enough to read before the page redraws.
        setTimeout(() => location.reload(), data.needs_recompute || data.bracket_built ? 4000 : 1200);
      } catch (err) {
        say(status, err.message, true);
      }
    });
  }

  // --- adjustments ----------------------------------------------------------

  const save = document.getElementById("adjust-save");
  if (save) {
    save.addEventListener("click", async function () {
      const status = document.getElementById("adjust-status");
      const racer = document.getElementById("adjust-racer");
      const points = document.getElementById("adjust-points");
      const reason = document.getElementById("adjust-reason");

      try {
        await post("/api/season/adjust", {
          racer_id: racer.value,
          points: points.value,
          reason: reason.value.trim(),
        });
        location.reload();
      } catch (err) {
        say(status, err.message, true);
      }
    });
  }

  document.querySelectorAll(".remove-adjustment").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/season/adjust/remove", { id: btn.dataset.id });
        location.reload();
      } catch (err) {
        say(document.getElementById("adjust-status"), err.message, true);
      }
    });
  });

  // --- substitutions --------------------------------------------------------

  const dialog = document.getElementById("substitute-dialog");
  const pick = document.getElementById("substitute-pick");
  const title = document.getElementById("substitute-title");
  const hint = document.getElementById("substitute-hint");
  const error = document.getElementById("substitute-error");
  let replacing = null;

  document.querySelectorAll(".substitute").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      replacing = btn.dataset.entry;
      say(error, "");
      pick.replaceChildren();

      let data;
      try {
        const res = await fetch("/api/season/substitutes?entry_id=" + encodeURIComponent(replacing) +
          (seasonID ? "&season_id=" + seasonID : ""));
        data = await res.json();
        if (!res.ok) throw new Error(data.error || res.statusText);
      } catch (err) {
        alert(err.message);
        return;
      }

      title.textContent = "Give up " + data.driver + "'s slot";
      if (!data.candidates.length) {
        hint.textContent = "Nobody else from race " + data.race +
          " is eligible. Every other finisher already qualified or is standing in elsewhere.";
      } else {
        // Naming the race is the point: the replacement comes from the same
        // night, so that night still sends the same number of cars.
        hint.textContent = data.car_name + " finished in race " + data.race +
          ". The replacement must come from that race, so it keeps its place in the championship.";
      }

      data.candidates.forEach(function (c) {
        const opt = document.createElement("option");
        opt.value = c.entry_id;
        opt.textContent = c.place + ". " + c.driver + " — " + c.car_name +
          " (" + c.average.toFixed(3) + ")" +
          // Handing the slot to someone already at the cap immediately recreates
          // the problem this dialog is here to fix. It stays allowed, because on
          // a thin night they may be the only person left, but it is not going
          // to happen by accident.
          (c.at_cap ? "  ⚠ already holds " + c.slots : "");
        pick.appendChild(opt);
      });
      document.getElementById("substitute-confirm").disabled = !data.candidates.length;
      dialog.showModal();
    });
  });

  if (dialog) {
    dialog.addEventListener("close", async function () {
      if (dialog.returnValue !== "confirm" || !pick.value) return;
      try {
        await post("/api/season/substitute", {
          original_entry_id: replacing,
          substitute_entry_id: pick.value,
        });
        location.reload();
      } catch (err) {
        alert(err.message);
      }
    });
  }

  document.querySelectorAll(".undo-substitute").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/season/substitute/undo", { original_entry_id: btn.dataset.original });
        location.reload();
      } catch (err) {
        alert(err.message);
      }
    });
  });
})();
