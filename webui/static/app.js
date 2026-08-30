// WhatsMap research governance console — front-end controller.
// Talks only to the same-origin JSON API. No external dependencies.
"use strict";

const SVGNS = "http://www.w3.org/2000/svg";
const $ = (id) => document.getElementById(id);

const state = {
  connection: "idle",
  participants: [],
  selectedParticipant: "",
  testStates: [],
  running: false,
  telemetryTimer: null,
  lastValidate: null,
};

// ---- Fetch helpers ---------------------------------------------------------

async function getJSON(url) {
  const r = await fetch(url, { headers: { Accept: "application/json" } });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}
async function postJSON(url, body) {
  const r = await fetch(url, {
    method: "POST",
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await r.json().catch(() => ({}));
  return { ok: r.ok, status: r.status, data };
}

// ---- Connection card -------------------------------------------------------

function connClass(s) {
  if (s === "connected") return "ok";
  if (s === "pending") return "warn";
  if (s === "error" || s === "expired" || s === "disconnected") return "danger";
  return "";
}

function renderConnectionPill() {
  const pill = $("connPill");
  const running = state.running;
  pill.className = "pill " + (running ? "live" : connClass(state.connection));
  const labels = {
    idle: "Not connected", pending: "Awaiting scan", connected: "Connected",
    expired: "Code expired", disconnected: "Disconnected", error: "Pairing error",
  };
  $("connPillText").textContent = running
    ? "Experiment running"
    : (labels[state.connection] || state.connection);
}

function drawQR(matrix) {
  const svg = document.createElementNS(SVGNS, "svg");
  svg.setAttribute("class", "qr");
  const n = matrix.length;
  svg.setAttribute("viewBox", `0 0 ${n} ${n}`);
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "Placeholder pairing code (mock, not a real QR)");
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if (!matrix[i][j]) continue;
      const rect = document.createElementNS(SVGNS, "rect");
      rect.setAttribute("x", j); rect.setAttribute("y", i);
      rect.setAttribute("width", 1); rect.setAttribute("height", 1);
      rect.setAttribute("fill", "#111");
      svg.appendChild(rect);
    }
  }
  return svg;
}

function renderConnection(s) {
  state.connection = s.state;
  const body = $("connBody");
  body.textContent = "";

  const pill = document.createElement("span");
  pill.className = "pill " + connClass(s.state);
  pill.appendChild(el("span", "dot"));
  pill.appendChild(document.createTextNode(" " + (s.state || "unknown")));
  body.appendChild(pill);

  if (s.message) {
    const p = el("p", "card-hint");
    p.style.marginTop = "10px";
    p.textContent = s.message;
    body.appendChild(p);
  }

  if (s.state === "pending" && s.qr_matrix) {
    const box = el("div", "qr-box");
    box.appendChild(drawQR(s.qr_matrix));
    const cap = el("p", "qr-caption");
    const expiry = s.expires_in_sec ? ` — refreshes in ~${s.expires_in_sec}s` : "";
    cap.textContent = s.mock
      ? `Mock code${expiry}. This encodes nothing real.`
      : `Scan in WhatsApp → Linked devices${expiry}.`;
    box.appendChild(cap);
    body.appendChild(box);
  }
  if (s.state === "connected" && s.account) {
    const p = el("p", "card-hint");
    p.style.marginTop = "6px";
    p.textContent = "Linked account: " + s.account;
    body.appendChild(p);
  }

  const row = el("div", "btn-row");
  if (s.state === "connected") {
    row.appendChild(button("Disconnect", "", () => act("/api/session/disconnect")));
  } else {
    const label = (s.state === "pending") ? "Regenerate code" : "Connect research account";
    row.appendChild(button(label, "primary", () => act("/api/session/connect")));
    // Demonstration helpers for the non-happy terminal states.
    row.appendChild(button("Simulate expiry", "link", () => act("/api/session/connect?simulate=expired")));
    row.appendChild(button("Simulate error", "link", () => act("/api/session/connect?simulate=error")));
  }
  body.appendChild(row);

  renderConnectionPill();
  updateStartAvailability();
}

async function act(url) {
  try {
    const { data } = await postJSON(url);
    renderConnection(data);
  } catch (e) {
    showConnError();
  }
}

function showConnError() {
  const body = $("connBody");
  body.textContent = "";
  const m = el("div", "msg error");
  m.textContent = "Could not reach the server. Retry in a moment.";
  body.appendChild(m);
}

// ---- Participants card -----------------------------------------------------

function renderParticipants(list) {
  state.participants = list;
  const body = $("partBody");
  body.textContent = "";

  if (!list || list.length === 0) {
    const b = el("p", "state-block");
    b.textContent = "No participants are enrolled yet. Enroll and verify consent to begin.";
    body.appendChild(b);
    updateStartAvailability();
    return;
  }

  const ul = el("ul", "participant-list");
  ul.setAttribute("role", "radiogroup");
  ul.setAttribute("aria-label", "Authorized research participants");

  list.forEach((p) => {
    const li = el("li", "participant" + (p.eligible ? " selectable" : " ineligible"));
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "participant";
    radio.value = p.id;
    radio.id = "part-" + p.id;
    radio.disabled = !p.eligible;
    radio.checked = state.selectedParticipant === p.id;
    radio.addEventListener("change", () => {
      state.selectedParticipant = p.id;
      markChosen(ul);
      revalidate();
    });
    li.appendChild(radio);

    const bodyDiv = el("div", "p-body");
    const lab = document.createElement("label");
    lab.setAttribute("for", radio.id);
    lab.className = "p-label";
    lab.textContent = p.label;
    bodyDiv.appendChild(lab);

    const meta = el("div", "p-meta");
    meta.textContent = `${p.masked_contact} · consent ref: ${p.consent_ref || "none"}` +
      (p.consent_expiry ? ` · expires ${p.consent_expiry}` : "") +
      (p.ownership_verified ? " · ownership verified" : "");
    bodyDiv.appendChild(meta);

    if (!p.eligible) {
      const why = el("div", "p-meta");
      why.textContent = "Not eligible for experiments (see consent status).";
      bodyDiv.appendChild(why);
    }
    li.appendChild(bodyDiv);

    const badge = el("span", "badge " + p.consent_status);
    badge.textContent = p.consent_status;
    li.appendChild(badge);

    if (p.eligible) {
      li.addEventListener("click", (ev) => {
        if (ev.target.tagName !== "INPUT") radio.click();
      });
    }
    ul.appendChild(li);
  });

  body.appendChild(ul);
  markChosen(ul);
  updateStartAvailability();
}

function markChosen(ul) {
  ul.querySelectorAll(".participant").forEach((li) => {
    const input = li.querySelector("input");
    li.classList.toggle("chosen", input && input.checked);
  });
}

async function reloadParticipants() {
  try {
    renderParticipants(await getJSON("/api/participants"));
  } catch (e) {
    /* leave existing list in place */
  }
}

async function enrollParticipant(ev) {
  ev.preventDefault();
  const body = {
    contact: $("enrollContact").value.trim(),
    label: $("enrollLabel").value.trim(),
    basis: (document.querySelector('input[name="basis"]:checked') || {}).value || "",
    reference: $("enrollRef").value.trim(),
    attestation: $("enrollAttest").checked,
  };
  const msg = $("enrollMsg");
  msg.innerHTML = "";
  const { ok, data } = await postJSON("/api/participants", body);
  if (!ok) {
    const m = el("div", "msg error");
    m.textContent = (data.errors && data.errors.join(" ")) || data.error || "Could not enroll participant.";
    msg.appendChild(m);
    return;
  }
  const okMsg = el("div", "msg ok");
  okMsg.textContent = `Enrolled ${data.label} (${data.masked_contact}). It can now be selected below.`;
  msg.appendChild(okMsg);
  $("enrollForm").reset();
  state.selectedParticipant = data.id;
  await reloadParticipants();
  revalidate();
}

// ---- Experiment form -------------------------------------------------------

function renderTestStates(list) {
  state.testStates = list || [];
  const sel = $("testState");
  (list || []).forEach((ts) => {
    const opt = document.createElement("option");
    opt.value = ts.key;
    opt.textContent = ts.label;
    opt.title = ts.description;
    sel.appendChild(opt);
  });
}

function readConfig() {
  return {
    participant_id: state.selectedParticipant,
    test_state: $("testState").value,
    interval_ms: intVal("intervalMs"),
    duration_sec: intVal("durationSec"),
    max_probes: intVal("maxProbes"),
    timeout_ms: intVal("timeoutMs"),
  };
}
function intVal(id) {
  const v = parseInt($(id).value, 10);
  return Number.isFinite(v) ? v : 0;
}

let validateHandle = null;
function revalidate() {
  clearTimeout(validateHandle);
  validateHandle = setTimeout(runValidate, 150);
}

async function runValidate() {
  const cfg = readConfig();
  let res;
  try {
    res = await postJSON("/api/experiment/validate", cfg);
    res = res.data;
  } catch (e) {
    $("validationMsg").innerHTML = "";
    const m = el("div", "msg error");
    m.textContent = "Validation service unavailable.";
    $("validationMsg").appendChild(m);
    return;
  }
  state.lastValidate = res;

  // Estimate line.
  const est = $("estimate");
  est.className = "msg " + (res.valid ? "ok" : "warn");
  est.textContent = `Estimated ${res.estimated_probes} probe(s) over ~${res.estimated_duration_sec}s ` +
    `at ${res.effective_rate_per_sec.toFixed(2)} probe/s.`;

  // Errors / warnings.
  const vm = $("validationMsg");
  vm.innerHTML = "";
  if (res.errors && res.errors.length) {
    vm.appendChild(msgList("error", "Fix before starting:", res.errors));
  }
  if (res.warnings && res.warnings.length) {
    vm.appendChild(msgList("warn", "Warnings:", res.warnings));
  }

  markInvalidFields(res.errors || []);
  updateStartAvailability();
}

function markInvalidFields(errors) {
  const text = errors.join(" ").toLowerCase();
  setInvalid("intervalMs", text.includes("interval"));
  setInvalid("durationSec", text.includes("duration"));
  setInvalid("timeoutMs", text.includes("timeout"));
  setInvalid("maxProbes", text.includes("iteration"));
  setInvalid("testState", text.includes("test state"));
}
function setInvalid(id, bad) {
  $(id).setAttribute("aria-invalid", bad ? "true" : "false");
}

function updateStartAvailability() {
  const ready = state.connection === "connected" &&
    state.lastValidate && state.lastValidate.valid && !state.running;
  $("startBtn").disabled = !ready;
}

async function startExperiment(ev) {
  ev.preventDefault();
  const { ok, data } = await postJSON("/api/experiment/start", readConfig());
  if (!ok) {
    const vm = $("validationMsg");
    vm.innerHTML = "";
    const m = el("div", "msg error");
    m.textContent = data.error || (data.errors ? data.errors.join(" ") : "Could not start experiment.");
    vm.appendChild(m);
    return;
  }
  onExperimentState(data);
  startTelemetryPolling();
}

async function stopExperiment() {
  const { data } = await postJSON("/api/experiment/stop");
  onExperimentState(data);
}

function onExperimentState(st) {
  state.running = st.status === "running";
  $("stopBtn").hidden = !state.running;
  $("emergencyStop").hidden = !state.running;
  renderConnectionPill();
  updateStartAvailability();
  if (!state.running) stopTelemetryPolling();
}

// ---- Telemetry / live chart ------------------------------------------------

function startTelemetryPolling() {
  stopTelemetryPolling();
  pollTelemetry();
  state.telemetryTimer = setInterval(pollTelemetry, 1000);
}
function stopTelemetryPolling() {
  if (state.telemetryTimer) clearInterval(state.telemetryTimer);
  state.telemetryTimer = null;
}

async function pollTelemetry() {
  let t;
  try {
    t = await getJSON("/api/telemetry");
  } catch (e) {
    return;
  }
  renderTelemetry(t);
  // Also refresh experiment status so completion/limits reflect in the UI.
  try {
    const exp = await getJSON("/api/experiment");
    if (exp.status !== "running") onExperimentState(exp);
  } catch (e) { /* ignore */ }
}

function fmtMs(v) {
  return v == null ? "—" : `${Math.round(v)}<small> ms</small>`;
}

function renderTelemetry(t) {
  $("statCurrent").innerHTML = fmtMs(t.current_rtt_ms);
  $("statMedian").innerHTML = fmtMs(t.median_rtt_ms);
  $("statP95").innerHTML = fmtMs(t.p95_rtt_ms);
  $("statCount").textContent = t.count;

  drawChart(t);
  drawLegend(t.bands);
  drawDistribution(t);
  drawConfidence(t);
  drawConfounders(t.confounders);

  const gt = $("gtNote");
  if (t.verified_ground_truth == null) {
    gt.textContent = "Verified ground-truth state: none. RTT is an indirect signal; " +
      "treat all band labels as hypotheses, not confirmed device states.";
  } else {
    gt.textContent = "Verified ground-truth state: " + t.verified_ground_truth;
  }
}

const CHART_W = 640, CHART_H = 260, PAD_L = 44, PAD_B = 22, PAD_T = 8, PAD_R = 8;
const RTT_MAX = 4000; // Chart ceiling (ms); values above are clamped to the top band.

function yFor(rtt) {
  const clamped = Math.min(rtt, RTT_MAX);
  const h = CHART_H - PAD_T - PAD_B;
  return PAD_T + h * (1 - clamped / RTT_MAX);
}

function drawChart(t) {
  const svg = $("chart");
  svg.textContent = "";

  // Hypothesis band backgrounds.
  t.bands.forEach((b) => {
    const top = yFor(b.max_ms === 0 ? RTT_MAX : b.max_ms);
    const bottom = yFor(b.min_ms);
    const rect = document.createElementNS(SVGNS, "rect");
    rect.setAttribute("x", PAD_L);
    rect.setAttribute("y", top);
    rect.setAttribute("width", CHART_W - PAD_L - PAD_R);
    rect.setAttribute("height", Math.max(0, bottom - top));
    rect.setAttribute("fill", b.color);
    rect.setAttribute("opacity", "0.14");
    svg.appendChild(rect);
  });

  // Y grid + labels.
  [0, 300, 1000, 3000].forEach((v) => {
    const y = yFor(v);
    const line = document.createElementNS(SVGNS, "line");
    line.setAttribute("x1", PAD_L); line.setAttribute("x2", CHART_W - PAD_R);
    line.setAttribute("y1", y); line.setAttribute("y2", y);
    line.setAttribute("class", "grid-line");
    svg.appendChild(line);
    const txt = document.createElementNS(SVGNS, "text");
    txt.setAttribute("x", 4); txt.setAttribute("y", y + 3);
    txt.setAttribute("class", "axis-label");
    txt.textContent = v;
    svg.appendChild(txt);
  });

  const samples = (t.samples || []).filter((s) => s.success);
  if (samples.length === 0) {
    const txt = document.createElementNS(SVGNS, "text");
    txt.setAttribute("x", CHART_W / 2); txt.setAttribute("y", CHART_H / 2);
    txt.setAttribute("text-anchor", "middle");
    txt.setAttribute("class", "axis-label");
    txt.textContent = state.running ? "Waiting for first samples…" : "No data yet — start an experiment.";
    svg.appendChild(txt);
    return;
  }

  const plotW = CHART_W - PAD_L - PAD_R;
  const n = samples.length;
  const xFor = (i) => PAD_L + (n === 1 ? plotW / 2 : plotW * (i / (n - 1)));

  let d = "";
  samples.forEach((s, i) => {
    d += (i === 0 ? "M" : "L") + xFor(i).toFixed(1) + "," + yFor(s.rtt_ms).toFixed(1) + " ";
  });
  const path = document.createElementNS(SVGNS, "path");
  path.setAttribute("d", d.trim());
  path.setAttribute("class", "rtt-line");
  svg.appendChild(path);

  // Point markers coloured by band.
  samples.forEach((s, i) => {
    const c = document.createElementNS(SVGNS, "circle");
    c.setAttribute("cx", xFor(i)); c.setAttribute("cy", yFor(s.rtt_ms));
    c.setAttribute("r", n > 120 ? 1.3 : 2.4);
    c.setAttribute("fill", bandColor(t.bands, s.band_key));
    svg.appendChild(c);
  });
}

function bandColor(bands, key) {
  const b = bands.find((x) => x.key === key);
  return b ? b.color : "#888";
}

function drawLegend(bands) {
  const box = $("bandLegend");
  box.textContent = "";
  bands.forEach((b) => {
    const item = el("span", "item");
    const sw = el("span", "sw");
    sw.style.background = b.color;
    item.appendChild(sw);
    const span = document.createElement("span");
    const range = b.max_ms === 0 ? `>${b.min_ms} ms` : `${b.min_ms}–${b.max_ms} ms`;
    span.textContent = `${range}: ${b.hypothesis}`;
    item.appendChild(span);
    box.appendChild(item);
  });
}

function drawDistribution(t) {
  const box = $("dist");
  box.textContent = "";
  const total = t.count || 0;
  t.bands.forEach((b) => {
    const c = (t.distribution && t.distribution[b.key]) || 0;
    const pct = total ? (c / total) * 100 : 0;
    const row = el("div", "row");
    const label = document.createElement("span");
    label.textContent = b.hypothesis;
    row.appendChild(label);
    const track = el("div", "track");
    const fill = el("div", "fill");
    fill.style.width = pct.toFixed(1) + "%";
    fill.style.background = b.color;
    track.appendChild(fill);
    row.appendChild(track);
    const count = el("span", "count");
    count.textContent = `${c}`;
    row.appendChild(count);
    box.appendChild(row);
  });
  $("overlapNote").textContent =
    `Distribution overlap: ${(t.overlap * 100).toFixed(0)}% of samples fall outside the single most common band — ` +
    `higher overlap means the hypotheses are less separable in this run.`;
}

function drawConfidence(t) {
  const pct = Math.round((t.confidence || 0) * 100);
  $("confFill").style.width = pct + "%";
  const meter = $("confMeter");
  meter.setAttribute("aria-valuenow", pct);
  meter.setAttribute("aria-valuetext", pct + "% (capped below certainty)");
  $("confNote").textContent = `${pct}% — ${t.uncertainty_note}`;
}

function drawConfounders(list) {
  const box = $("confounders");
  box.textContent = "";
  (list || []).forEach((c) => {
    const li = document.createElement("li");
    const imp = el("span", "impact " + c.impact);
    imp.textContent = c.impact;
    li.appendChild(imp);
    li.appendChild(document.createTextNode(`${c.name} — ${c.note}`));
    box.appendChild(li);
  });
}

// ---- Small DOM helpers -----------------------------------------------------

function el(tag, className) {
  const e = document.createElement(tag);
  if (className) e.className = className;
  return e;
}
function button(label, className, onClick) {
  const b = document.createElement("button");
  b.type = "button";
  if (className) b.className = className;
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}
function msgList(kind, title, items) {
  const wrap = el("div", "msg " + kind);
  const strong = document.createElement("strong");
  strong.textContent = title;
  wrap.appendChild(strong);
  const ul = document.createElement("ul");
  items.forEach((i) => {
    const li = document.createElement("li");
    li.textContent = i;
    ul.appendChild(li);
  });
  wrap.appendChild(ul);
  return wrap;
}

// ---- Boot ------------------------------------------------------------------

async function boot() {
  $("expForm").addEventListener("submit", startExperiment);
  $("stopBtn").addEventListener("click", stopExperiment);
  $("emergencyStop").addEventListener("click", stopExperiment);
  $("enrollForm").addEventListener("submit", enrollParticipant);
  document.querySelectorAll('input[name="basis"]').forEach((r) => {
    r.addEventListener("change", () => {
      const consent = document.querySelector('input[name="basis"]:checked').value === "consent";
      $("refUnit").textContent = consent ? "(required for consent)" : "(optional for owned device)";
    });
  });
  ["intervalMs", "durationSec", "maxProbes", "timeoutMs", "testState"].forEach((id) => {
    $(id).addEventListener("input", revalidate);
  });

  try {
    const [session, participants, testStates, exp] = await Promise.all([
      getJSON("/api/session"),
      getJSON("/api/participants"),
      getJSON("/api/test-states"),
      getJSON("/api/experiment"),
    ]);
    renderConnection(session);
    renderParticipants(participants);
    renderTestStates(testStates);
    onExperimentState(exp);
    if (exp.status === "running") startTelemetryPolling();
  } catch (e) {
    showConnError();
    $("partBody").textContent = "";
    const m = el("div", "msg error");
    m.textContent = "Failed to load. Check that the server is running and reload.";
    $("partBody").appendChild(m);
  }

  runValidate();
  // Refresh the connection card periodically so the mock QR state machine
  // (pending → connected/expired) is reflected without a manual reload.
  setInterval(async () => {
    if (state.running) return;
    try { renderConnection(await getJSON("/api/session")); } catch (e) { /* ignore */ }
  }, 2500);
}

document.addEventListener("DOMContentLoaded", boot);
