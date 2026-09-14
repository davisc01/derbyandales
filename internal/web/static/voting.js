// The coordinator's side of voting.
//
// Tallies live here rather than on a separate screen, because resuming racing
// is what closes voting — you should not have to go looking for the numbers
// first.

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

  function say(el, text, isError) {
    if (!el) return;
    el.textContent = text || "";
    el.classList.toggle("err-text", !!isError);
  }

  const status = document.getElementById("intermission-status");

  function statusFor(btn) {
    const card = btn.closest("[data-category]");
    return card ? card.querySelector(".category-status") : null;
  }

  // --- intermission ---------------------------------------------------------

  const resume = document.getElementById("resume-racing");
  if (resume) {
    resume.addEventListener("click", async function () {
      const undeclared = [...document.querySelectorAll("[data-category]")].filter(
        (c) => !c.querySelector(".winner-line")
      ).length;

      let message = "Resume racing?\n\nThis closes voting.";
      if (undeclared > 0) {
        message +=
          "\n\n" + undeclared + " question(s) have no winner declared yet. " +
          "You can still declare them afterwards, and reopen voting if someone " +
          "has not voted.";
      }
      if (!confirm(message)) return;

      resume.disabled = true;
      try {
        await post("/api/race/resume", {});
        say(status, "Racing resumed. Voting is closed.");
        setTimeout(() => location.reload(), 600);
      } catch (err) {
        resume.disabled = false;
        say(status, err.message, true);
      }
    });
  }

  const closeVoting = document.getElementById("close-voting");
  if (closeVoting) {
    closeVoting.addEventListener("click", async function () {
      try {
        await post("/api/vote/close", {});
        setTimeout(() => location.reload(), 400);
      } catch (err) {
        say(status, err.message, true);
      }
    });
  }

  const reopen = document.getElementById("reopen-voting");
  if (reopen) {
    reopen.addEventListener("click", async function () {
      try {
        await post("/api/vote/reopen", {});
        setTimeout(() => location.reload(), 400);
      } catch (err) {
        say(status, err.message, true);
      }
    });
  }

  // --- per-question actions -------------------------------------------------

  document.querySelectorAll(".undo-vote").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        const body = await post("/api/vote/undo", { category_id: btn.dataset.category });
        say(statusFor(btn), body.undone ? "Undid the vote for " + body.undone + "." : "Undone.");
        refresh();
      } catch (err) {
        say(statusFor(btn), err.message, true);
      }
    });
  });

  document.querySelectorAll(".reset-votes").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      if (!confirm("Clear every vote for this question?\n\nThe votes are kept but no longer counted.")) return;
      try {
        await post("/api/vote/reset", { category_id: btn.dataset.category });
        setTimeout(() => location.reload(), 400);
      } catch (err) {
        say(statusFor(btn), err.message, true);
      }
    });
  });

  document.querySelectorAll(".declare").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/vote/declare", {
          category_id: btn.dataset.category,
          entry_id: btn.dataset.entry,
        });
        setTimeout(() => location.reload(), 400);
      } catch (err) {
        say(statusFor(btn), err.message, true);
      }
    });
  });

  document.querySelectorAll(".clear-winner").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      try {
        await post("/api/vote/clear", { category_id: btn.dataset.category });
        setTimeout(() => location.reload(), 400);
      } catch (err) {
        say(statusFor(btn), err.message, true);
      }
    });
  });

  document.querySelectorAll(".toggle-category").forEach(function (box) {
    box.addEventListener("change", async function () {
      try {
        await post("/api/vote/enable", {
          category_id: box.dataset.category,
          enabled: box.checked ? "true" : "false",
        });
        say(statusFor(box), box.checked ? "On the ballot." : "Off the ballot.");
      } catch (err) {
        say(statusFor(box), err.message, true);
      }
    });
  });

  // --- live tallies ---------------------------------------------------------

  // Counts update in place. The page is not reloaded on every vote: the
  // coordinator may be mid-click on a declare button.
  async function refresh() {
    try {
      const data = await fetch("/api/vote/tally").then((r) => r.json());
      (data.categories || []).forEach(function (c) {
        const card = document.querySelector('[data-category="' + c.id + '"]');
        if (!card) return;
        const count = card.querySelector(".vote-count");
        if (count) count.textContent = c.votes;

        const rows = card.querySelectorAll(".tally tr");
        const byEntry = {};
        (c.tally || []).forEach((t) => (byEntry[t.entry_id] = t));
        rows.forEach(function (row) {
          const btn = row.querySelector(".declare");
          if (!btn) return;
          const t = byEntry[btn.dataset.entry];
          if (!t) return;
          const votes = row.querySelector(".tally-votes");
          if (votes) votes.textContent = t.votes;
          row.classList.toggle("leading", !!t.leading);
        });
      });
    } catch (err) {
      /* the connection indicator already reports this */
    }
  }

  const source = new EventSource("/events?topics=vote,race");
  source.addEventListener("vote", function (e) {
    let ev;
    try { ev = JSON.parse(e.data); } catch (err) { return; }
    // Opening, closing and declaring change the page's shape, so reload;
    // a cast vote only changes numbers.
    if (ev.kind === "opened" || ev.kind === "closed" || ev.kind === "declared") {
      location.reload();
      return;
    }
    refresh();
  });
  source.addEventListener("race", function (e) {
    let ev;
    try { ev = JSON.parse(e.data); } catch (err) { return; }
    if (ev.kind === "intermission" || ev.kind === "resumed") location.reload();
  });

  refresh();
  setInterval(refresh, 3000);
})();
