// The run-of-show screen.
//
// It reloads on anything that changes the state of the night rather than
// patching itself: the steps depend on each other, so a half-updated list would
// tell somebody the wrong thing to do next.

(function () {
  "use strict";

  const load = document.getElementById("load-race");
  if (load) {
    load.addEventListener("click", async function () {
      const picker = document.getElementById("race-picker");
      const status = document.getElementById("race-status");
      try {
        const res = await fetch("/api/race/load", {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({ race_id: picker.value }).toString(),
        });
        if (!res.ok) {
          const data = await res.json().catch(() => ({}));
          throw new Error(data.error || res.statusText);
        }
        location.reload();
      } catch (err) {
        status.textContent = err.message;
        status.classList.add("err-text");
      }
    });
  }

  let pending = null;
  function refresh() {
    clearTimeout(pending);
    // A short settle: a heat result arrives as several events in a row, and
    // reloading on each would make the page flicker through a whole race.
    pending = setTimeout(function () { location.reload(); }, 900);
  }

  const source = new EventSource("/events?topics=race,timer,vote,system");
  ["race", "timer", "vote", "system"].forEach(function (topic) {
    source.addEventListener(topic, function (e) {
      let ev;
      try { ev = JSON.parse(e.data); } catch (err) { return; }
      // Only the things that move the checklist on. Gate and heartbeat events
      // fire constantly and change nothing here.
      const moves = [
        "complete", "intermission", "resumed", "intermission-ended",
        "heat.recorded", "schedule", "started", "stopped",
        "bench", "connected", "disconnected", "published",
      ];
      if (moves.indexOf(ev.kind) >= 0) refresh();
    });
  });
})();
