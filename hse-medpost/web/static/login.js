const form = document.getElementById("login-form");
const errEl = document.getElementById("error");
const staffSelect = document.getElementById("staff-select");
const freeInput = document.getElementById("username");
const typeWrap = document.getElementById("type-wrap");
const pickWrap = document.getElementById("pick-wrap");
const modeRadios = document.querySelectorAll('input[name="mode"]');

function entryMode() {
  return document.querySelector('input[name="mode"]:checked')?.value || "pick";
}

function syncEntryUI() {
  const pick = entryMode() === "pick";
  pickWrap.hidden = !pick;
  typeWrap.hidden = pick;
  staffSelect.required = pick;
  freeInput.required = !pick;
}

modeRadios.forEach((r) => r.addEventListener("change", syncEntryUI));

async function loadStaff() {
  const res = await fetch("/api/login/staff-usernames");
  const list = await res.json().catch(() => []);
  staffSelect.innerHTML = `<option value="">Select staff…</option>`;
  for (const row of list) {
    const opt = document.createElement("option");
    opt.value = row.username;
    const role = row.role ? ` · ${row.role}` : "";
    opt.textContent = `${row.staffName} (${row.username})${role}`;
    opt.dataset.staffName = row.staffName || row.username;
    staffSelect.appendChild(opt);
  }
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  errEl.style.display = "none";
  const fd = new FormData(form);
  const password = String(fd.get("password") || "").trim();
  const pick = entryMode() === "pick";
  const username = pick
    ? String(staffSelect.value || "").trim()
    : String(freeInput.value || "").trim();

  if (!username) {
    errEl.textContent = pick ? "Select a username." : "Enter username.";
    errEl.style.display = "block";
    return;
  }
  if (!/^[0-9]{6}$/.test(password)) {
    errEl.textContent = "Password must be 6 digits.";
    errEl.style.display = "block";
    return;
  }

  const res = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ username, password }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    errEl.textContent = data.error || "Login failed";
    errEl.style.display = "block";
    return;
  }
  location.href = "/start";
});

loadStaff();
syncEntryUI();
