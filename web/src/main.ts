import { createApp } from 'vue'
import { createPinia } from 'pinia'
import Vant from 'vant'
import 'vant/lib/index.css'
import App from './App.vue'
import router from './router'
import { useUserStore } from './stores/user'
import './styles/index.scss'

const app = createApp(App)
const pinia = createPinia()

app.use(pinia)
app.use(Vant)

// 必须先拿到登录态再装载路由：否则首次导航时守卫会误判为未登录，
// 把已登录用户从 /new 重定向到登录页。
useUserStore(pinia)
  .init()
  .catch(() => undefined)
  .finally(() => {
    app.use(router)
    app.mount('#app')
  })
