// Shared confirm/alert dialog. Replaces window.confirm and window.alert.
// Forms opt in with data-confirm (body), data-confirm-title, data-confirm-ok,
// and data-confirm-danger. Alerts go through window.airmxAlert({title, body}).
(function () {
  var root = document.getElementById("amx-modal");
  if (!root) return;

  var titleEl = document.getElementById("amx-modal-title");
  var bodyEl = document.getElementById("amx-modal-body");
  var okBtn = document.getElementById("amx-modal-ok");
  var cancelBtn = document.getElementById("amx-modal-cancel");
  var mark = document.getElementById("amx-modal-mark");
  var pending = null;
  var lastFocus = null;

  function finish(value) {
    if (!pending) return;
    var resolve = pending;
    pending = null;
    root.hidden = true;
    document.body.classList.remove("amx-modal-open");
    if (lastFocus && typeof lastFocus.focus === "function") lastFocus.focus();
    resolve(value);
  }

  function open(opts) {
    if (pending) finish(false);
    lastFocus = document.activeElement;
    titleEl.textContent = opts.title || "";
    bodyEl.textContent = opts.body || "";
    bodyEl.hidden = !opts.body;
    okBtn.textContent = opts.ok || root.dataset.ok || "OK";
    okBtn.className = "btn " + (opts.danger ? "btn-danger" : "btn-primary");
    var showCancel = !!opts.cancel;
    cancelBtn.hidden = !showCancel;
    if (showCancel) cancelBtn.textContent = opts.cancel;
    var tone = opts.danger ? "is-danger" : "is-warn";
    var icon = opts.danger ? "bi-trash" : "bi-exclamation-circle";
    mark.className = "amx-modal-mark " + tone;
    mark.innerHTML = '<i class="bi ' + icon + '"></i>';
    root.hidden = false;
    document.body.classList.add("amx-modal-open");
    (showCancel && opts.danger ? cancelBtn : okBtn).focus();
    return new Promise(function (resolve) {
      pending = resolve;
    });
  }

  okBtn.addEventListener("click", function () { finish(true); });
  cancelBtn.addEventListener("click", function () { finish(false); });
  root.querySelector("[data-amx-dismiss]").addEventListener("click", function () { finish(false); });

  document.addEventListener("keydown", function (e) {
    if (root.hidden) return;
    if (e.key === "Escape") {
      e.preventDefault();
      finish(false);
      return;
    }
    if (e.key !== "Tab") return;
    var nodes = [cancelBtn, okBtn].filter(function (b) { return !b.hidden; });
    if (!nodes.length) return;
    var first = nodes[0];
    var last = nodes[nodes.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  });

  window.airmxAlert = function (opts) {
    return open({ title: opts.title, body: opts.body, ok: opts.ok });
  };

  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || !form.getAttribute || !form.hasAttribute("data-confirm")) return;
    if (form.dataset.confirmed === "1") return;
    e.preventDefault();
    open({
      title: form.dataset.confirmTitle || "",
      body: form.getAttribute("data-confirm") || "",
      ok: form.dataset.confirmOk || root.dataset.ok,
      cancel: root.dataset.cancel,
      danger: form.hasAttribute("data-confirm-danger"),
    }).then(function (yes) {
      if (!yes) return;
      form.dataset.confirmed = "1";
      if (typeof form.requestSubmit === "function") form.requestSubmit();
      else form.submit();
    });
  });
})();
