// Once the session is gone the page on screen is a stale copy: send the user to
// login instead of leaving a lookalike they can still type into. Any API 401
// triggers it, and so does a page restored from the back/forward cache.
(function () {
  const nativeFetch = window.fetch.bind(window);
  function leaveSignedOutPage() {
    location.replace("/");
  }
  window.fetch = async (...args) => {
    const res = await nativeFetch(...args);
    if (res.status === 401) leaveSignedOutPage();
    return res;
  };
  window.addEventListener("pageshow", (e) => {
    if (!e.persisted) return;
    nativeFetch("/api/me", { credentials: "same-origin" })
      .then((r) => r.status === 401 && leaveSignedOutPage())
      .catch(() => {});
  });
})();
