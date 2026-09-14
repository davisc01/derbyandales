// The settings page.
//
// The one action here reads the club's published archive back in, so the
// previous-championship rule can be checked at the table instead of recalled.

(function () {
  "use strict";

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
          bad.map(function (y) { return y.year + " (" + y.problem + ")"; }).join(", ");
      }
      status.textContent = msg;
      setTimeout(() => location.reload(), 1500);
    } catch (err) {
      status.textContent = err.message;
      status.classList.add("err-text");
      btn.disabled = false;
    }
  });
})();
