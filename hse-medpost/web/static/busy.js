// Shows a spinning busy cursor for the duration of any in-flight fetch, so
// the UI doesn't look idle while waiting on the server. Wraps window.fetch
// once, globally. Background presence polls are left alone so the cursor
// does not spin on every heartbeat. Non-fetch work (photo compression) can
// call beginBusy() and invoke the returned end function when it finishes.
(function () {
  let pending = 0;
  let x = 0;
  let y = 0;
  let seenPointer = false;

  const spinner = document.createElement("div");
  spinner.className = "busy-spinner";
  spinner.setAttribute("aria-hidden", "true");

  function mount() {
    if (!spinner.isConnected && document.body) document.body.appendChild(spinner);
  }

  function place() {
    spinner.style.left = x + "px";
    spinner.style.top = y + "px";
  }

  function sync() {
    const busy = pending > 0;
    document.documentElement.classList.toggle("busy", busy);
    document.documentElement.classList.toggle("busy-follow", busy && seenPointer);
    if (busy && seenPointer) {
      mount();
      place();
    }
  }

  document.addEventListener("mousemove", (event) => {
    x = event.clientX;
    y = event.clientY;
    seenPointer = true;
    if (pending > 0) sync();
  }, { passive: true });

  function requestUrl(input) {
    if (typeof input === "string") return input;
    if (input instanceof URL) return input.href;
    if (input && typeof input.url === "string") return input.url;
    return "";
  }

  function isBackground(input) {
    return requestUrl(input).includes("/api/presence/");
  }

  function beginBusy() {
    pending += 1;
    sync();
    let ended = false;
    return function endBusy() {
      if (ended) return;
      ended = true;
      pending = Math.max(0, pending - 1);
      sync();
    };
  }

  window.beginBusy = beginBusy;

  const nativeFetch = window.fetch;
  window.fetch = function (...args) {
    if (isBackground(args[0])) return nativeFetch.apply(this, args);
    const endBusy = beginBusy();
    let promise;
    try {
      promise = nativeFetch.apply(this, args);
    } catch (err) {
      endBusy();
      throw err;
    }
    return promise.finally(endBusy);
  };
})();
