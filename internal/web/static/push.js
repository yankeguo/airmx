// airmx push subscription toggle, loaded on the list page when Web Push is
// enabled. Expects a #push-toggle button carrying data-key="<vapid public>".
(function () {
  var btn = document.getElementById("push-toggle");
  if (!btn) return;
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
    btn.hidden = true;
    return;
  }

  function b64ToUint8(b64) {
    var pad = "=".repeat((4 - (b64.length % 4)) % 4);
    var raw = atob((b64 + pad).replace(/-/g, "+").replace(/_/g, "/"));
    return Uint8Array.from(raw, function (c) {
      return c.charCodeAt(0);
    });
  }

  function render(on) {
    btn.dataset.on = on ? "1" : "";
    btn.innerHTML =
      '<i class="bi ' + (on ? "bi-bell-fill" : "bi-bell") + ' me-1"></i>' +
      (on ? btn.dataset.labelOff : btn.dataset.labelOn);
  }

  function postJSON(url, body) {
    return fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then(function (resp) {
      if (!resp.ok) throw new Error("HTTP " + resp.status);
    });
  }

  var regPromise = navigator.serviceWorker.register("/sw.js");

  regPromise
    .then(function (reg) {
      return reg.pushManager.getSubscription();
    })
    .then(function (sub) {
      render(!!sub);
    })
    .catch(function () {
      btn.hidden = true;
    });

  btn.addEventListener("click", function () {
    btn.disabled = true;
    var p = regPromise.then(function (reg) {
      if (btn.dataset.on) {
        return reg.pushManager.getSubscription().then(function (sub) {
          if (!sub) return;
          return postJSON("/api/push/unsubscribe", { endpoint: sub.endpoint }).then(function () {
            return sub.unsubscribe();
          });
        });
      }
      return Notification.requestPermission().then(function (perm) {
        if (perm !== "granted") throw new Error(btn.dataset.errDenied);
        return reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: b64ToUint8(btn.dataset.key),
        });
      }).then(function (sub) {
        return postJSON("/api/push/subscribe", sub);
      });
    });
    p.then(function () {
      return regPromise.then(function (reg) {
        return reg.pushManager.getSubscription();
      });
    })
      .then(function (sub) {
        render(!!sub);
      })
      .catch(function (err) {
        alert(btn.dataset.errFailed + ": " + (err && err.message ? err.message : err));
      })
      .finally(function () {
        btn.disabled = false;
      });
  });
})();
