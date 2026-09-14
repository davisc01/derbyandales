// The publish screen.
//
// The only thing here that touches anything is one button, and it does not run
// after a preview has gone stale: picking a different thing to publish reloads
// the page so what is on screen is what would be written.

(function () {
  "use strict";

  function say(text, bad) {
    const el = document.getElementById("publish-status");
    if (!el) return;
    el.textContent = text;
    el.classList.toggle("err-text", !!bad);
  }

  const picker = document.getElementById("publish-what");
  const preview = document.getElementById("preview");
  if (preview && picker) {
    const show = function () {
      const v = picker.value;
      if (v === "season") {
        location.search = "?what=season";
      } else {
        location.search = "?what=race&race_id=" + encodeURIComponent(v.slice(5));
      }
    };
    preview.addEventListener("click", show);
    picker.addEventListener("change", show);
  }

  const go = document.getElementById("do-publish");
  if (go) {
    go.addEventListener("click", async function () {
      go.disabled = true;
      say("Writing…");
      try {
        const body = new URLSearchParams({ what: go.dataset.what });
        if (go.dataset.race && go.dataset.race !== "0") body.set("race_id", go.dataset.race);
        const res = await fetch("/api/publish", {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: body.toString(),
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data.error || res.statusText);
        say(data.count + " file" + (data.count === 1 ? "" : "s") +
          " written. Review and commit them in the website folder.");
        setTimeout(() => location.reload(), 1200);
      } catch (err) {
        say(err.message, true);
        go.disabled = false;
      }
    });
  }
})();
