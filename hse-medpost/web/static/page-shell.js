async function logout() {
  await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
  location.href = "/";
}
document.getElementById("logout")?.addEventListener("click", logout);
