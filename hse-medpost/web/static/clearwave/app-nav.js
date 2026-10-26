/* Minimal Clearwave nav helpers for app pages */

// Date fields stay typeable. A click on the calendar icon opens the picker.
document.addEventListener("click", (event) => {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || input.type !== "date") return;
  if (typeof input.showPicker !== "function") return;
  const rect = input.getBoundingClientRect();
  if (event.clientX < rect.right - 32) return;
  try {
    input.showPicker();
  } catch {
    // The calendar is already open.
  }
});
const hamburger = document.getElementById("hamburger");
const mobileMenu = document.getElementById("mobileMenu");

function openMobileMenu() {
  hamburger?.classList.add("open");
  mobileMenu?.classList.add("open");
  hamburger?.setAttribute("aria-expanded", "true");
  document.body.style.overflow = "hidden";
}
function closeMobileMenu() {
  hamburger?.classList.remove("open");
  mobileMenu?.classList.remove("open");
  hamburger?.setAttribute("aria-expanded", "false");
  document.body.style.overflow = "";
}
hamburger?.addEventListener("click", () => {
  mobileMenu?.classList.contains("open") ? closeMobileMenu() : openMobileMenu();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeMobileMenu();
});
mobileMenu?.querySelectorAll("a").forEach((link) => {
  link.addEventListener("click", () => closeMobileMenu());
});

async function changePassword() {
  if (typeof openSettingsEdit === "function") {
    openSettingsEdit({
      heading: "Change password",
      submitLabel: "Update password",
      fields: [
        { label: "Current password", name: "currentPassword", maxLength: 6, inputMode: "numeric" },
        { label: "New password", name: "newPassword", maxLength: 6, inputMode: "numeric" },
      ],
      onSubmit: async (data) => {
        if (!/^[0-9]{6}$/.test(data.newPassword || "")) {
          throw new Error("New password must be 6 digits");
        }
        const res = await fetch("/api/me/password", {
          method: "PUT",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            currentPassword: data.currentPassword || "",
            newPassword: data.newPassword || "",
          }),
        });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.error || "Failed to change password");
      },
    });
    return;
  }
  const currentPassword = prompt("Current password (6 digits):");
  if (!currentPassword) return;
  const newPassword = prompt("New password (6 digits):");
  if (!newPassword) return;
  if (!/^[0-9]{6}$/.test(newPassword)) {
    alert("New password must be 6 digits");
    return;
  }
  const res = await fetch("/api/me/password", {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ currentPassword, newPassword }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed to change password");
    return;
  }
  alert("Password updated");
}
document.getElementById("change-password")?.addEventListener("click", changePassword);
document.getElementById("mobile-change-password")?.addEventListener("click", (e) => {
  e.preventDefault();
  changePassword();
});

const backToTopBtn = document.getElementById("back-to-top");
if (backToTopBtn) {
  let activeScroller = null;

  const scrollerTop = (el) => {
    if (!el || el === document.scrollingElement) return window.scrollY || 0;
    return el.scrollTop || 0;
  };

  const syncBackToTop = (event) => {
    const target = !event || event.target === document ? document.scrollingElement : event.target;
    if (!target) return;
    activeScroller = target;
    backToTopBtn.classList.toggle("visible", scrollerTop(target) > 80);
  };

  document.addEventListener("scroll", syncBackToTop, { capture: true, passive: true });
  backToTopBtn.addEventListener("click", () => {
    const el = activeScroller;
    if (el && el !== document.scrollingElement && typeof el.scrollTo === "function") {
      el.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }
    window.scrollTo({ top: 0, behavior: "smooth" });
  });
}
