// 手机主屏应用的安装条件需要一个 service worker。
// 故意不注册 fetch 处理、不缓存任何请求：新版本部署后，主屏应用打开就是新版本。
self.addEventListener('install', (event) => {
  event.waitUntil(self.skipWaiting())
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})
