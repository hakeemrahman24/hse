// Signed-in staff on other sections. The open page heartbeats its section;
// the card lists everyone else as "Name on Section; Name on Section logged in".

function renderPresence(banner, others) {
  const people = (others || []).filter((person) => person && person.name);
  banner.replaceChildren();
  if (people.length === 0) {
    banner.hidden = true;
    return;
  }
  people.forEach((person, index) => {
    if (index > 0) banner.append("; ");
    const name = document.createElement("span");
    name.className = "presence-name";
    name.textContent = person.name;
    banner.append(name);
    if (person.section) banner.append(` on ${person.section}`);
  });
  banner.append(" logged in");
  banner.hidden = false;
}

function startPresence(sectionOf) {
  const banner = document.getElementById("presence-banner");
  if (!banner || typeof sectionOf !== "function") return () => {};

  async function poll() {
    const section = String(sectionOf() || "").trim();
    if (!section) return;
    try {
      await fetch(`/api/presence/heartbeat?section=${encodeURIComponent(section)}`, {
        method: "POST",
        credentials: "same-origin",
      });
      const res = await fetch("/api/presence/who", { credentials: "same-origin" });
      if (!res.ok) return;
      const data = await res.json();
      renderPresence(banner, data.others);
    } catch {
      // A missed heartbeat should not disturb the rest of the page.
    }
  }

  poll();
  setInterval(poll, 5000);
  return poll;
}
