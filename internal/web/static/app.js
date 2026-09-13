// Shared client behaviour: the live connection indicator, and the small
// actions on the status page.
//
// Every page holds one EventSource. The browser reconnects on its own, so an
// unplugged HDMI stick or a wifi blip recovers without anyone touching it.

(function () {
  "use strict";

  const conn = document.getElementById("conn");

  function setConn(state, text) {
    if (!conn) return;
    conn.className = "conn " + state;
    conn.textContent = "• " + text;
  }

  let source;

  function connect() {
    source = new EventSource("/events");

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
