// Shared client behaviour: the live connection indicator, the one event stream
// every page on this tab shares, and the small actions on the status page.
//
// Every page holds one EventSource — one, not one each. The browser reconnects
// on its own, so an unplugged HDMI stick or a wifi blip recovers without
// anyone touching it.
//
// Sharing it is not tidiness. A browser allows six connections per host over
// HTTP/1.1 and an SSE stream never returns one, so when this script and the
// page's own script each opened a stream, three open tabs used the entire
// budget: the next page load, every photo and every poll queued behind them
// and Chrome sat there spinning with the server perfectly healthy. On the
// HTTPS address the limit does not apply — that connection is HTTP/2, which
// multiplexes — which is part of why this only ever bit on one browser.

(function () {
  "use strict";

  const conn = document.getElementById("conn");

  function setConn(state, text) {
    if (!conn) return;
    conn.className = "conn " + state;
    conn.textContent = "• " + text;
  }

  let source;
  // Listeners registered by a page script before the stream exists.
  const waiting = [];

  // raceEvents is what the page scripts attach to instead of opening their own
  // stream. It takes the same addEventListener call an EventSource does, so a
  // page reads the same either way — and a page served without this script,
  // like a display or the ballot, still opens one of its own.
  window.raceEvents = {
    addEventListener: function (topic, fn) {
      if (source) {
        source.addEventListener(topic, fn);
        return;
      }
      waiting.push([topic, fn]);
    },
  };

  function connect() {
    source = new EventSource("/events");
    while (waiting.length) {
      const pair = waiting.shift();
      source.addEventListener(pair[0], pair[1]);
    }

    source.onopen = function () {
      setConn("live", "live");
    };

    source.onerror = function () {
      // EventSource retries by itself using the server's `retry:` hint.
      setConn("lost", "reconnecting");
    };

    // System events are the only ones this shared script cares about; pages
    // that need race, timer or vote streams add their own listeners.
    source.addEventListener("system", function (e) {
      let ev;
      try {
        ev = JSON.parse(e.data);
      } catch (err) {
        return;
      }
      if (ev.kind === "backup") {
        note("Snapshot saved.");
      }
      if (ev.kind === "shutdown") {
        setConn("lost", "server stopped");
        source.close();
      }
    });
  }

  function note(text) {
    const el = document.getElementById("backup-status");
    if (!el) return;
    el.textContent = text;
    setTimeout(function () {
      if (el.textContent === text) el.textContent = "";
    }, 4000);
  }

  // "Back up now" on the status page.
  const backupBtn = document.getElementById("backup-now");
  if (backupBtn) {
    backupBtn.addEventListener("click", async function () {
      backupBtn.disabled = true;
      note("Working…");
      try {
        const res = await fetch("/api/backup", { method: "POST" });
        const body = await res.json();
        if (!res.ok) throw new Error(body.error || res.statusText);
        note("Snapshot saved. Reloading…");
        setTimeout(function () { location.reload(); }, 700);
      } catch (err) {
        note("Backup failed: " + err.message);
        backupBtn.disabled = false;
      }
    });
  }

  // Going back to a backup. It stops the app, so it says so plainly first.
  document.querySelectorAll("button.restore").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      if (!confirm("Go back to the backup from " + btn.dataset.when + "?\n\n" +
                   "Everything since then is replaced. The database as it is now is " +
                   "saved as a backup first. The app stops — open it again to carry on.")) {
        return;
      }
      btn.disabled = true;
      note("Saving the current database and staging the restore…");
      try {
        const res = await fetch("/api/backup/restore", {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: "name=" + encodeURIComponent(btn.dataset.name),
        });
        const body = await res.json().catch(function () { return {}; });
        if (!res.ok) throw new Error(body.error || res.statusText);
        setConn("lost", "server stopped");
        note("Stopped. Open Derby and Ales again and it starts from that backup.");
      } catch (err) {
        note("Could not restore: " + err.message);
        btn.disabled = false;
      }
    });
  });

  const cancelRestore = document.getElementById("restore-cancel");
  if (cancelRestore) {
    cancelRestore.addEventListener("click", async function () {
      await fetch("/api/backup/restore/cancel", { method: "POST" });
      location.reload();
    });
  }

  // "Quit" on the status page. Launched from the .app there is no Dock icon,
  // so this is the normal way to stop the server.
  const quitBtn = document.getElementById("quit");
  const quitStatus = document.getElementById("quit-status");
  if (quitBtn) {
    quitBtn.addEventListener("click", async function () {
      if (!confirm("Stop the race server?\n\nA final snapshot is taken first. " +
                   "Displays and tablets will lose their connection.")) {
        return;
      }
      quitBtn.disabled = true;
      quitStatus.textContent = "Saving and stopping…";
      try {
        await fetch("/api/quit", { method: "POST" });
      } catch (err) {
        // The server may drop the connection before replying, which is fine.
      }
      setConn("lost", "server stopped");
      quitStatus.textContent = "Stopped. You can close this tab.";
    });
  }

  connect();
})();

// Displays manager: assign scenes, rename screens, choose the race.
(function () {
  "use strict";

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

  function say(id, text) {
    const el = document.getElementById(id);
    if (el) el.textContent = text;
  }

  document.querySelectorAll(".scene-picker").forEach(function (sel) {
    sel.addEventListener("change", async function () {
      try {
        await post("/api/displays/scene", { id: sel.dataset.id, scene: sel.value });
        say("display-status", "Scene changed.");
      } catch (err) {
        say("display-status", err.message);
      }
    });
  });

  document.querySelectorAll(".display-rename").forEach(function (input) {
    let timer;
    input.addEventListener("input", function () {
      clearTimeout(timer);
      timer = setTimeout(async function () {
        try {
          await post("/api/displays/rename", { id: input.dataset.id, name: input.value });
          say("display-status", "Renamed.");
        } catch (err) {
          say("display-status", err.message);
        }
      }, 600);
    });
  });

  document.querySelectorAll(".forget-display").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      if (!confirm("Forget this display? It will reappear if the screen is still open.")) return;
      try {
        await post("/api/displays/forget", { id: btn.dataset.id });
        location.reload();
      } catch (err) {
        say("display-status", err.message);
      }
    });
  });

  const loadBtn = document.getElementById("load-race");
  if (loadBtn) {
    loadBtn.addEventListener("click", async function () {
      const sel = document.getElementById("race-picker");
      try {
        await post("/api/race/load", { race_id: sel.value });
        say("race-status", "Loaded. The screens will follow it.");
      } catch (err) {
        say("race-status", err.message);
      }
    });
  }
})();
