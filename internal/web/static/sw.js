// airmx push service worker: shows a notification for each push and
// focuses / opens the UI when it is clicked.

self.addEventListener("push", function (e) {
  var data = {};
  try {
    data = e.data ? e.data.json() : {};
  } catch (err) {}
  // The payload carries both title variants; pick by the browser's locale.
  var zh = (self.navigator.language || "").toLowerCase().indexOf("zh") === 0;
  var title = (zh ? data.title_zh : data.title_en) || data.title_zh || data.title_en || "AirMX";
  e.waitUntil(
    self.registration.showNotification(title, {
      body: data.body || "",
      data: { url: data.url || "/" },
    })
  );
});

self.addEventListener("notificationclick", function (e) {
  e.notification.close();
  var url = (e.notification.data && e.notification.data.url) || "/";
  e.waitUntil(
    clients
      .matchAll({ type: "window", includeUncontrolled: true })
      .then(function (list) {
        for (var i = 0; i < list.length; i++) {
          if ("focus" in list[i]) return list[i].focus();
        }
        return clients.openWindow(url);
      })
  );
});
