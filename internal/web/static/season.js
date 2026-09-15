// The season screen.
//
// Every action here changes a published number, so each one says what it did
// and the page reloads afterwards rather than trying to patch itself. The
// standings are a whole-table calculation — one correction reseeds everything
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

  // --- racer names ----------------------------------------------------------

  const renameRacer = document.getElementById("rename-racer");
  if (renameRacer) {
    // Start from the name as it is, since most fixes are one letter.
    renameRacer.addEventListener("change", function () {
      const opt = renameRacer.selectedOptions[0];
      document.getElementById("rename-first").value = (opt && opt.dataset.first) || "";
      document.getElementById("rename-last").value = (opt && opt.dataset.last) || "";
    });
    document.getElementById("rename-save").addEventListener("click", async function () {
      const status = document.getElementById("rename-status");
      try {
        await post("/api/racer/rename", {
          racer_id: renameRacer.value,
          first: document.getElementById("rename-first").value,
          last: document.getElementById("rename-last").value,
        });
        location.reload();
      } catch (err) {
        say(status, err.message, true);
      }
    });
  }

  const mergeSave = document.getElementById("merge-save");
  if (mergeSave) {
    mergeSave.addEventListener("click", async function () {
      const status = document.getElementById("merge-status");
      const drop = document.getElementById("merge-drop");
      const keep = document.getElementById("merge-keep");
      if (!drop.value || !keep.value) {
        say(status, "Choose both racers.", true);
        return;
      }
      if (!confirm("Merge " + drop.selectedOptions[0].text + " into " +
                   keep.selectedOptions[0].text + "?\n\nEvery car and result moves across " +
                   "and the duplicate is removed. A backup is taken first.")) return;
      try {
        const data = await post("/api/racer/merge", { keep_id: keep.value, drop_id: drop.value });
        if (data.shared_races > 0) {
          // Each half earned points for its own best car in those races, and
          // the rule is one best car per racer — so the frozen points are now
          // too generous until somebody recomputes.
          say(status, "Merged. They had cars in the same race " + data.shared_races +
              " time" + (data.shared_races === 1 ? "" : "s") +
              ", so press Recompute every race to put the points right.");
          setTimeout(() => location.reload(), 5000);
        } else {
          location.reload();
        }
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
})();
