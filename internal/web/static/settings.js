// The settings page.
//
// Reading the club's published archive back in, so the previous-championship
// rule can be checked at the table instead of recalled — and the demo data,
// which is how a race night is rehearsed without cars.

(function () {
  "use strict";

  demoButtons();

  const btn = document.getElementById("import-history");
  if (!btn) return;

  btn.addEventListener("click", async function () {
    const status = document.getElementById("import-status");
    btn.disabled = true;
    status.textContent = "Reading…";
    status.classList.remove("err-text");

    try {
      const res = await fetch("/api/history/import", { method: "POST" });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || res.statusText);

      // Say which years could not be read rather than quietly importing the
      // rest: a missing year means cars that will not be flagged.
      const bad = (data.years || []).filter(function (y) { return y.problem; });
      const good = (data.years || []).length - bad.length;
      let msg = good + " championship" + (good === 1 ? "" : "s") +
        " read, " + data.cars + " cars.";
      if (bad.length) {
        msg += " Could not read: " +
          bad.map(function (y) { return y.year + " (" + y.problem + ")"; }).join(", ") + ".";
      }
      if (data.races_error) {
        msg += " Race nights: " + data.races_error + ".";
      } else {
        msg += " " + data.races + " race night" + (data.races === 1 ? "" : "s") +
          " read, " + data.runs + " runs.";
      }
      const badRaces = data.race_problems || [];
      if (badRaces.length) {
        msg += " Could not read: " +
          badRaces.map(function (r) { return r.race + " (" + r.problem + ")"; }).join(", ") + ".";
      }
      status.textContent = msg;
      setTimeout(() => location.reload(), 1500);
    } catch (err) {
      status.textContent = err.message;
      status.classList.add("err-text");
      btn.disabled = false;
    }
  });

  // --- demo data ------------------------------------------------------------

  function demoButtons() {
    const status = document.getElementById("demo-status");
    const make = document.getElementById("demo-race");
    const clear = document.getElementById("demo-clear");
    if (!status || !make || !clear) return;

    async function run(button, url, done) {
      const others = [make, clear];
      others.forEach(function (b) { b.disabled = true; });
      status.classList.remove("err-text");
      status.textContent = "Working…";
      try {
        const res = await fetch(url, { method: "POST" });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data.error || res.statusText);
        status.textContent = done(data);
        // The page carries the counts and which race is loaded, so it is read
        // again rather than patched in two places.
        setTimeout(() => location.reload(), 1200);
      } catch (err) {
        status.textContent = err.message;
        status.classList.add("err-text");
        others.forEach(function (b) { b.disabled = false; });
      }
    }

    make.addEventListener("click", function () {
      run(make, "/api/demo/race", function (d) {
        return d.race + " created with " + d.cars + " cars checked in.";
      });
    });

    clear.addEventListener("click", function () {
      // The one button on this page that destroys work. It only ever reaches
      // demo data, but somebody mid-rehearsal still loses the night.
      if (!confirm("Delete the demo season and every race in it?\n\n" +
        "A snapshot is taken first. The club's own seasons and the archive " +
        "are not touched.")) return;
      run(clear, "/api/demo/clear", function (d) {
        return d.detail + ".";
      });
    });
  }
})();
