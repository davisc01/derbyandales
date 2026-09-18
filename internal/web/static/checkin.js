// Check-in.
//
// The camera is the fiddly part. Browsers only expose it on a secure address,
// so the page is served over HTTPS or on localhost; when it is not, the capture
// controls are hidden and a file picker stands in.

(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);

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

  function say(id, text, isError) {
    const el = $(id);
    if (!el) return;
    el.textContent = text || "";
    el.classList.toggle("err-text", !!isError);
  }

  // --- camera ---------------------------------------------------------------

  let stream = null;
  let photoID = null;

  const video = $("camera");
  const shot = $("shot");
  const canvas = $("canvas");
  const picker = $("camera-picker");

  async function listCameras() {
    if (!picker || !navigator.mediaDevices?.enumerateDevices) return;
    try {
      const devices = await navigator.mediaDevices.enumerateDevices();
      const cams = devices.filter((d) => d.kind === "videoinput");
      picker.innerHTML = "";
      cams.forEach(function (c, i) {
        const opt = document.createElement("option");
        opt.value = c.deviceId;
        // Labels are empty until permission is granted, so fall back to a number.
        opt.textContent = c.label || "Camera " + (i + 1);
        picker.appendChild(opt);
      });
      picker.hidden = cams.length < 2;
    } catch (err) {
      /* not fatal — the default camera still works */
    }
  }

  async function startCamera() {
    if (!navigator.mediaDevices?.getUserMedia) {
      say("capture-status", "This browser will not give the page a camera.", true);
      return;
    }
    stopCamera();
    const constraints = {
      video: picker && picker.value
        ? { deviceId: { exact: picker.value }, width: { ideal: 1920 } }
        : { facingMode: "environment", width: { ideal: 1920 } },
    };
    try {
      stream = await navigator.mediaDevices.getUserMedia(constraints);
      video.srcObject = stream;
      video.hidden = false;
      shot.hidden = true;
      $("start-camera").hidden = true;
      $("take-photo").hidden = false;
      $("retake").hidden = true;
      say("capture-status", "");
      // Labels only appear once permission is granted.
      listCameras();
    } catch (err) {
      say("capture-status", cameraError(err), true);
    }
  }

  function cameraError(err) {
    switch (err && err.name) {
      case "NotAllowedError":
        return "Camera access was refused. Allow it for this address and try again.";
      case "NotFoundError":
        return "No camera was found. Check it is plugged in.";
      case "NotReadableError":
        return "The camera is in use by another app.";
      default:
        return "Could not start the camera: " + (err && err.message ? err.message : err);
    }
  }

  function stopCamera() {
    if (stream) {
      stream.getTracks().forEach((t) => t.stop());
      stream = null;
    }
  }

  async function takePhoto() {
    if (!stream) return;
    const w = video.videoWidth;
    const h = video.videoHeight;
    if (!w || !h) {
      say("capture-status", "The camera is not ready yet.", true);
      return;
    }
    canvas.width = w;
    canvas.height = h;
    canvas.getContext("2d").drawImage(video, 0, 0, w, h);

    say("capture-status", "Saving…");
    const blob = await new Promise((resolve) =>
      canvas.toBlob(resolve, "image/jpeg", 0.92)
    );
    if (!blob) {
      say("capture-status", "Could not read the photo.", true);
      return;
    }

    try {
      const res = await fetch("/api/photo", {
        method: "POST",
        headers: { "Content-Type": "image/jpeg" },
        body: blob,
      });
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || res.statusText);

      photoID = body.id;
      shot.src = URL.createObjectURL(blob);
      shot.hidden = false;
      video.hidden = true;
      $("take-photo").hidden = true;
      $("retake").hidden = false;
      say("capture-status", "Photo saved.");
      stopCamera();
    } catch (err) {
      say("capture-status", err.message, true);
    }
  }

  function retake() {
    photoID = null;
    shot.hidden = true;
    shot.removeAttribute("src");
    startCamera();
  }

  if ($("start-camera")) $("start-camera").addEventListener("click", startCamera);
  if ($("take-photo")) $("take-photo").addEventListener("click", takePhoto);
  if ($("retake")) $("retake").addEventListener("click", retake);
  if (picker) picker.addEventListener("change", () => stream && startCamera());
  listCameras();

  // The fallback when the camera is unavailable.
  const fileInput = $("photo-file");
  if (fileInput) {
    fileInput.addEventListener("change", async function () {
      const file = fileInput.files && fileInput.files[0];
      if (!file) return;
      const form = new FormData();
      form.append("photo", file);
      try {
        const res = await fetch("/api/photo", { method: "POST", body: form });
        const body = await res.json();
        if (!res.ok) throw new Error(body.error || res.statusText);
        photoID = body.id;
        say("checkin-status", "Photo attached.");
      } catch (err) {
        say("checkin-status", err.message, true);
      }
    });
  }

  // --- car numbers ----------------------------------------------------------

  // Warn about a clash while typing, rather than on submit.
  const carNumber = $("car-number");
  if (carNumber) {
    carNumber.addEventListener("input", async function () {
      const n = parseInt(carNumber.value, 10);
      if (!n) return say("car-number-hint", "");
      try {
        const data = await fetch("/api/entries").then((r) => r.json());
        const clash = (data.entries || []).some((e) => e.car_number === n);
        say("car-number-hint", clash ? "Car " + n + " is already checked in." : "", clash);
      } catch (err) {
        /* the submit will catch it */
      }
    });
  }

  // Offer the rest of a known racer's name once the first name matches.
  const first = $("first-name");
  const last = $("last-name");
  if (first && last) {
    first.addEventListener("change", async function () {
      if (last.value.trim()) return;
      try {
        const data = await fetch("/api/racers").then((r) => r.json());
        const matches = (data.racers || []).filter(
          (r) => r.first_name.toLowerCase() === first.value.trim().toLowerCase()
        );
        if (matches.length === 1) last.value = matches[0].last_name;
      } catch (err) {
        /* typing the surname is not a hardship */
      }
    });
  }

  // An exclusion needs a reason, so the field appears with the checkbox.
  const excluded = $("excluded");
  if (excluded) {
    excluded.addEventListener("change", function () {
      $("reason-field").hidden = !excluded.checked;
      if (excluded.checked) $("reason").focus();
    });
  }

  // --- check a car in -------------------------------------------------------

  const form = $("checkin-form");
  if (form) {
    form.addEventListener("submit", async function (e) {
      e.preventDefault();
      const btn = $("add-entry");
      btn.disabled = true;
      say("checkin-status", "Saving…");

      const params = {
        first_name: first.value.trim(),
        last_name: last.value.trim(),
        car_number: carNumber.value,
        car_name: $("car-name").value.trim(),
        is_control: $("is-control").checked ? "true" : "false",
        excluded: excluded.checked ? "true" : "false",
        reason: $("reason").value.trim(),
      };
      if (photoID) params.photo_id = photoID;

      try {
        const result = await post("/api/entry", params);

        // A car gets one championship. The server checks the club's archive
        // and says so here; it does not exclude anything, because excluding a
        // car is a decision and it needs a reason. The page stops rather than
        // reloading, so the warning is actually read.
        if (result.warning) {
          say("checkin-status", result.warning, true);
          const status = $("checkin-status");
          const act = document.createElement("button");
          act.className = "btn small";
          act.textContent = "Mark ineligible";
          act.style.marginLeft = "10px";
          act.addEventListener("click", async function () {
            try {
              await post("/api/entry/update", {
                id: result.id,
                excluded: "true",
                reason: result.exclusion_reason || "raced in a previous championship",
              });
              location.reload();
            } catch (err) {
              say("checkin-status", err.message, true);
            }
          });
          const ok = document.createElement("button");
          ok.className = "btn small";
          ok.textContent = "Different car, carry on";
          ok.style.marginLeft = "6px";
          ok.addEventListener("click", function () { location.reload(); });
          status.appendChild(act);
          status.appendChild(ok);
          btn.disabled = false;
          return;
        }

        say("checkin-status", "Checked in.");
        // Keep the driver's name: the commonest next action is a second car
        // for the same person.
        carNumber.value = "";
        $("car-name").value = "";
        $("is-control").checked = false;
        excluded.checked = false;
        $("reason").value = "";
        $("reason-field").hidden = true;
        say("car-number-hint", "");
        photoID = null;
        setTimeout(() => location.reload(), 500);
      } catch (err) {
        say("checkin-status", err.message, true);
        btn.disabled = false;
      }
    });
  }

  // --- editing and removing a checked-in car --------------------------------

  // The edit row lives under its car in the table, already filled in by the
  // server. Opening it is a matter of unhiding it, so nothing here has to
  // re-render a row or escape a car name.
  function editRow(id) {
    return document.querySelector('.entry-edit[data-edit="' + id + '"]');
  }

  function closeEdit(row) {
    if (!row) return;
    row.hidden = true;
    const form = row.querySelector(".edit-form");
    if (form) {
      form.reset();
      const reason = form.querySelector(".edit-reason");
      if (reason) reason.hidden = !form.elements.excluded.checked;
      const status = form.querySelector(".edit-status");
      if (status) { status.textContent = ""; status.classList.remove("err-text"); }
    }
  }

  document.querySelectorAll(".edit-entry").forEach(function (btn) {
    btn.addEventListener("click", function () {
      const row = editRow(btn.dataset.id);
      if (!row) return;
      const opening = row.hidden;
      // One open editor at a time: two half-filled forms on one table is how a
      // correction gets saved onto the wrong car.
      document.querySelectorAll(".entry-edit").forEach(closeEdit);
      if (!opening) return;
      row.hidden = false;
      const first = row.querySelector('input[name="first_name"]');
      const number = row.querySelector('input[name="car_number"]');
      const focusOn = first && !first.disabled ? first : number;
      if (focusOn) focusOn.focus();
    });
  });

  document.querySelectorAll(".cancel-edit").forEach(function (btn) {
    btn.addEventListener("click", function () {
      closeEdit(btn.closest(".entry-edit"));
    });
  });

  // An exclusion needs a reason here too, for the same reason it does on the
  // add form: "why was my car not in the standings" is asked weeks later.
  document.querySelectorAll(".edit-form").forEach(function (form) {
    const excluded = form.elements.excluded;
    const reason = form.querySelector(".edit-reason");
    if (excluded && reason) {
      excluded.addEventListener("change", function () {
        reason.hidden = !excluded.checked;
        if (excluded.checked) reason.querySelector("input").focus();
      });
    }

    form.addEventListener("submit", async function (e) {
      e.preventDefault();
      const status = form.querySelector(".edit-status");
      const save = form.querySelector('button[type="submit"]');
      const setStatus = function (text, isError) {
        if (!status) return;
        status.textContent = text || "";
        status.classList.toggle("err-text", !!isError);
      };

      save.disabled = true;
      setStatus("Saving…");

      const params = {
        id: form.dataset.id,
        car_number: form.elements.car_number.value,
        car_name: form.elements.car_name.value.trim(),
        note: form.elements.note.value.trim(),
        is_control: form.elements.is_control.checked ? "true" : "false",
        excluded: form.elements.excluded.checked ? "true" : "false",
        reason: form.elements.reason.value.trim(),
      };
      // Disabled when the race is recorded, and then the driver is not ours to
      // change — sending nothing is what tells the server to leave it alone.
      if (!form.elements.first_name.disabled) {
        params.first_name = form.elements.first_name.value.trim();
        params.last_name = form.elements.last_name.value.trim();
      }

      try {
        const file = form.elements.photo.files && form.elements.photo.files[0];
        if (file) {
          setStatus("Uploading the photo…");
          const body = new FormData();
          body.append("photo", file);
          const res = await fetch("/api/photo", { method: "POST", body: body });
          const uploaded = await res.json();
          if (!res.ok) throw new Error(uploaded.error || res.statusText);
          params.photo_id = uploaded.id;
        } else if (form.elements.remove_photo && form.elements.remove_photo.checked) {
          params.photo_id = "";
        }

        setStatus("Saving…");
        await post("/api/entry/update", params);
        location.reload();
      } catch (err) {
        setStatus(err.message, true);
        save.disabled = false;
      }
    });
  });

  document.querySelectorAll(".remove-entry").forEach(function (btn) {
    btn.addEventListener("click", async function () {
      // Naming the car matters: the button is one of thirty identical ones.
      const what = btn.dataset.label || "this car";
      if (!confirm("Remove " + what + " from check-in?")) return;
      try {
        await post("/api/entry/delete", { id: btn.dataset.id });
        location.reload();
      } catch (err) {
        say("checkin-status", err.message, true);
      }
    });
  });

  // --- finding a car in the roster ------------------------------------------

  // The roster is thirty-odd rows on a laptop at a noisy table, and the
  // question is always about one car. Filtering and sorting happen here rather
  // than on the server so the list answers while someone is still typing, and
  // so a reload is never needed to get back to car-number order.
  const rosterRows = $("entry-rows");
  if (rosterRows) {
    const search = $("roster-search");
    const filter = $("roster-filter");
    const sorter = $("roster-sort");
    const count = $("roster-count");
    const empty = $("roster-empty");
    const rows = Array.prototype.slice.call(
      rosterRows.querySelectorAll(".entry-row")
    );

    const matchesFilter = function (row) {
      switch (filter.value) {
        case "excluded": return row.dataset.excluded === "true";
        case "control":  return row.dataset.control === "true";
        case "nophoto":  return row.dataset.photo === "0";
        // The pace car races and is ranked but is not a competitor, and an
        // ineligible car is not in the standings at all. Neither belongs in
        // the list of cars this filter is asked for.
        case "racing":
          return row.dataset.excluded !== "true" && row.dataset.control !== "true";
        default: return true;
      }
    };

    const matchesSearch = function (row) {
      const q = search.value.trim().toLowerCase();
      if (!q) return true;
      const hay = [
        row.dataset.number,
        "#" + row.dataset.number,
        row.dataset.car,
        row.dataset.driver,
      ].join(" ").toLowerCase();
      return hay.indexOf(q) !== -1;
    };

    const compare = function (a, b) {
      switch (sorter.value) {
        case "car":
          // A car with no name sorts last rather than to the top, where an
          // empty string would put it.
          return (a.dataset.car || "\uffff").toLowerCase()
            .localeCompare((b.dataset.car || "\uffff").toLowerCase());
        case "driver":
          return (a.dataset.last + " " + a.dataset.driver).toLowerCase()
            .localeCompare((b.dataset.last + " " + b.dataset.driver).toLowerCase());
        case "checked":
          return (+a.dataset.checked) - (+b.dataset.checked) ||
                 (+a.dataset.number) - (+b.dataset.number);
        default:
          return (+a.dataset.number) - (+b.dataset.number);
      }
    };

    const apply = function () {
      let shown = 0;
      rows.forEach(function (row) {
        const visible = matchesFilter(row) && matchesSearch(row);
        row.hidden = !visible;
        if (visible) shown++;
        // A hidden car must not leave its editor open above the next one.
        if (!visible) closeEdit(editRow(row.dataset.entry));
      });

      rows.slice().sort(compare).forEach(function (row) {
        rosterRows.appendChild(row);
        const edit = editRow(row.dataset.entry);
        if (edit) rosterRows.appendChild(edit);
      });

      if (empty) empty.hidden = shown > 0;
      if (count) {
        count.textContent = shown === rows.length
          ? shown + (shown === 1 ? " car" : " cars")
          : shown + " of " + rows.length;
      }
    };

    search.addEventListener("input", apply);
    filter.addEventListener("change", apply);
    sorter.addEventListener("change", apply);
    apply();
  }

  // --- seasons and races ----------------------------------------------------

  const loadBtn = $("load-race");
  if (loadBtn) {
    loadBtn.addEventListener("click", async function () {
      try {
        await post("/api/race/load", { race_id: $("race-picker").value });
        location.reload();
      } catch (err) {
        say("race-status", err.message, true);
      }
    });
  }

  const seasonForm = $("season-form");
  if (seasonForm) {
    seasonForm.addEventListener("submit", async function (e) {
      e.preventDefault();
      try {
        await post("/api/season", {
          year: $("season-year").value,
          name: $("season-name").value.trim(),
          lane_count: $("season-lanes").value,
          track_length_ft: $("season-track").value,
          race_count: $("season-races").value,
        });
        say("season-status", "Created.");
        setTimeout(() => location.reload(), 500);
      } catch (err) {
        say("season-status", err.message, true);
      }
    });
  }

  const raceForm = $("race-form");
  if (raceForm) {
    // "Run as" only means something for the championship. Showing it for a
    // season race would offer a choice the server refuses.
    const kind = $("race-kind");
    const showFormat = function () {
      const championship = kind.value === "championship";
      $("race-format-field").hidden = !championship;
      $("race-format-hint").hidden = !championship;
    };
    kind.addEventListener("change", showFormat);
    showFormat();

    raceForm.addEventListener("submit", async function (e) {
      e.preventDefault();
      try {
        await post("/api/race", {
          season_id: $("race-season").value,
          number: $("race-number").value,
          name: $("race-name").value.trim(),
          venue: $("race-venue").value.trim(),
          date: $("race-date").value,
          kind: $("race-kind").value,
          format: $("race-format").value,
        });
        say("race-create-status", "Created and opened for check-in.");
        setTimeout(() => location.reload(), 600);
      } catch (err) {
        say("race-create-status", err.message, true);
      }
    });
  }

  window.addEventListener("pagehide", stopCamera);
})();
