// Timer Test Bench.
//
// The interactive checks take as long as someone needs to walk to the track, so
// every button reports progress rather than appearing to hang.

(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);

  async function post(url, params) {
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: params ? new URLSearchParams(params).toString() : undefined,
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || res.statusText);
    return body;
  }

  // Rehearsing a bad read: the timer reports nothing, and the gate ends the
  // heat instead.
  document.querySelectorAll("#sim-drop-all, #sim-drop-lane").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      const params = btn.id === "sim-drop-lane" ? { lane: "2" } : {};
      try {
        const data = await post("/api/timer/sim/drop", params);
        say("sim-status", data.note || "Ready.");
      } catch (err) {
        say("sim-status", err.message, true);
      }
    });
  });

  // --- live status ----------------------------------------------------------

  // app.js holds this tab's one event stream; see the note there about why
  // a second one per page cost Chrome its whole connection budget.
  const source = window.raceEvents || new EventSource("/events?topics=timer");

  source.addEventListener("timer", function (e) {
    let ev;
    try {
      ev = JSON.parse(e.data);
    } catch (err) {
      return;
    }
    const d = ev.data || {};
    if (d.state) $("race-state").textContent = d.state;

    switch (ev.kind) {
      case "gate_closed":
        $("gate-state").textContent = "closed";
        break;
      case "gate_open":
        $("gate-state").textContent = "open";
        break;
      case "gate_not_supported":
        $("gate-state").textContent = "not reported by this timer";
        break;
      case "bench":
        renderChecks(d.Checks || d.checks || []);
        break;
      case "lost_connection":
        note("trace-status", "Timer disconnected.");
        break;
    }
  });

  function note(id, text, isError) {
    const el = $(id);
    if (!el) return;
    el.textContent = text;
    el.classList.toggle("err-text", !!isError);
  }

  // --- rendering ------------------------------------------------------------

  function renderChecks(checks) {
    const list = $("bench-checks");
    if (!list || !checks.length) return;
    list.innerHTML = "";
    checks.forEach(function (c) {
      const li = document.createElement("li");
      li.className = "check " + c.verdict;
      li.dataset.id = c.id;

      const dot = document.createElement("span");
      dot.className = "dot";
      const name = document.createElement("span");
      name.className = "cname";
      name.textContent = c.name;
      const detail = document.createElement("span");
      detail.className = "cdetail";
      detail.textContent = c.detail;
      li.append(dot, name, detail);

      if (c.evidence) {
        const btn = document.createElement("button");
        btn.className = "btn small evidence-toggle";
        btn.textContent = "Details";
        const pre = document.createElement("pre");
        pre.className = "evidence";
        pre.hidden = true;
        pre.textContent = c.evidence;
        li.append(btn, pre);
      }
      list.appendChild(li);
    });
  }

  function updateOneCheck(check) {
    const existing = document.querySelector('.check[data-id="' + check.id + '"]');
    if (!existing) return;
    existing.className = "check " + check.verdict;
    const detail = existing.querySelector(".cdetail");
    if (detail) detail.textContent = check.detail;
  }

  // Evidence panels, including ones rendered server-side.
  document.addEventListener("click", function (e) {
    if (!e.target.classList.contains("evidence-toggle")) return;
    const pre = e.target.parentElement.querySelector(".evidence");
    if (pre) pre.hidden = !pre.hidden;
  });

  // --- connect --------------------------------------------------------------

  const connectForm = $("connect-form");
  if (connectForm) {
    connectForm.addEventListener("submit", async function (e) {
      e.preventDefault();
      const btn = $("connect-btn");
      btn.disabled = true;
      btn.textContent = "Connecting…";
      try {
        await post("/api/timer/connect", {
          port: $("port-select").value,
          profile: $("profile-select").value,
        });
        location.reload();
      } catch (err) {
        btn.disabled = false;
        btn.textContent = "Connect";
        note("bench-status", "Could not connect: " + err.message, true);
      }
    });
  }

  const disconnectBtn = $("disconnect-btn");
  if (disconnectBtn) {
    disconnectBtn.addEventListener("click", async function () {
      await post("/api/timer/disconnect");
      location.reload();
    });
  }

  // --- checks ---------------------------------------------------------------

  const runBtn = $("run-bench");
  if (runBtn) {
    runBtn.addEventListener("click", async function () {
      runBtn.disabled = true;
      note("bench-status", "Talking to the timer…");
      try {
        const result = await post("/api/timer/bench");
        renderChecks(result.checks || []);
        // A warning is not a pass. Saying "all checks passed" over the top of
        // one is the sort of green tick that gets believed on a race night and
        // then turns out to have meant nothing.
        const checks = result.checks || [];
        const failed = checks.filter((c) => c.verdict === "fail").length;
        const warned = checks.filter((c) => c.verdict === "warn").length;
        const skipped = checks.filter((c) => c.verdict === "skipped").length;
        let summary;
        if (failed) {
          summary = failed + (failed === 1 ? " check failed." : " checks failed.");
          if (warned) summary += " " + warned + " to read.";
        } else if (warned) {
          summary = warned === 1
            ? "Passed, with one thing worth reading."
            : "Passed, with " + warned + " things worth reading.";
        } else if (skipped) {
          summary = "All automatic checks passed; " + skipped + " did not apply.";
        } else {
          summary = "All automatic checks passed.";
        }
        note("bench-status", summary, failed > 0);
      } catch (err) {
        note("bench-status", err.message, true);
      } finally {
        runBtn.disabled = false;
      }
    });
  }

  // Each interactive check runs one at a time, with a live hint about what the
  // person at the track should be doing.
  function interactive(btnId, resultId, url, params, waitingText) {
    const btn = $(btnId);
    if (!btn) return;
    btn.addEventListener("click", async function () {
      btn.disabled = true;
      note(resultId, waitingText);
      try {
        const check = await post(url, typeof params === "function" ? params() : params);
        updateOneCheck(check);
        note(resultId, check.detail, check.verdict === "fail");
      } catch (err) {
        note(resultId, err.message, true);
      } finally {
        btn.disabled = false;
      }
    });
  }

  interactive("check-gate", "gate-result", "/api/timer/bench/gate", null,
    "Watching… open the gate, then close it.");

  interactive("check-lane", "lane-result", "/api/timer/bench/lane",
    () => ({ lane: $("lane-select").value }),
    "Waiting… roll a car down that lane now.");

  interactive("check-heat", "heat-result", "/api/timer/bench/heat", null,
    "Waiting… stage the cars and release them.");

  // --- override -------------------------------------------------------------

  const overrideForm = $("override-form");
  if (overrideForm) {
    overrideForm.addEventListener("submit", async function (e) {
      e.preventDefault();
      const reason = $("override-reason").value.trim();
      if (!reason) {
        note("override-status", "A reason is required.", true);
        return;
      }
      try {
        await post("/api/timer/bench/override", { reason: reason });
        note("override-status", "Recorded. Racing may continue.");
      } catch (err) {
        note("override-status", err.message, true);
      }
    });
  }

  // --- console --------------------------------------------------------------

  async function refreshTrace() {
    try {
      const res = await fetch("/api/timer/trace");
      const el = $("trace");
      el.textContent = await res.text();
      el.scrollTop = el.scrollHeight;
    } catch (err) {
      note("trace-status", err.message, true);
    }
  }

  const refreshBtn = $("refresh-trace");
  if (refreshBtn) refreshBtn.addEventListener("click", refreshTrace);

  const sendBtn = $("send-raw");
  if (sendBtn) {
    sendBtn.addEventListener("click", async function () {
      const cmd = $("raw-command").value.trim().toUpperCase();
      if (!cmd) return;
      try {
        await post("/api/timer/send", { command: cmd });
        note("trace-status", "Sent " + cmd);
        setTimeout(refreshTrace, 300);
      } catch (err) {
        note("trace-status", err.message, true);
      }
    });
  }

  const saveBtn = $("save-trace");
  if (saveBtn) {
    saveBtn.addEventListener("click", async function () {
      try {
        const body = await post("/api/timer/trace/save");
        note("trace-status", "Saved to " + body.path);
      } catch (err) {
        note("trace-status", err.message, true);
      }
    });
  }

  if ($("trace")) {
    refreshTrace();
    setInterval(refreshTrace, 5000);
  }
})();

// Simulator stand-ins. Present only when the simulated timer is connected;
// they play the part of the person at the track so the bench can be rehearsed.
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

  function status(text) {
    const el = document.getElementById("sim-status");
    if (el) el.textContent = text;
  }

  document.querySelectorAll("[data-sim-gate]").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      const closed = btn.dataset.simGate;
      try {
        await post("/api/timer/sim/gate", { closed: closed });
        status(closed === "true" ? "Gate closed." : "Gate open — cars away.");
      } catch (err) {
        status(err.message);
      }
    });
  });

  document.querySelectorAll("[data-sim-car]").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/timer/sim/car", { lane: btn.dataset.simCar });
        status("Car sent down lane " + btn.dataset.simCar + ".");
      } catch (err) {
        status(err.message);
      }
    });
  });
})();
