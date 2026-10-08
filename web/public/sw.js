// HUI-2626 fix2（gate-r2 #8 offline 态）：断网时导航请求回应用内离线页，
// 不再落到浏览器错误页。只兜 navigate 请求，API/静态资源不做拦截。
const CACHE = "leads-offline-v1";
const OFFLINE_URL = "/offline.html";

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll([OFFLINE_URL])));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("fetch", (event) => {
  if (event.request.mode !== "navigate") return;
  event.respondWith(
    fetch(event.request).catch(() =>
      caches.match(OFFLINE_URL).then((hit) => hit || Response.error()),
    ),
  );
});
