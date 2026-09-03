import { createRouter, createWebHashHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import MainLayout from '@/layouts/MainLayout.vue'
import { useUserStore } from '@/stores/user'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: MainLayout,
    redirect: '/new',
    children: [
      {
        path: 'login',
        name: 'login',
        component: () => import('@/views/LoginView.vue'),
        meta: { title: '用户登录', icon: 'user' },
      },
      {
        path: 'new',
        name: 'new',
        component: () => import('@/views/NewDownloadView.vue'),
        meta: { title: '新建下载', icon: 'plus', requiresAuth: true },
      },
      {
        path: 'tasks',
        name: 'tasks',
        component: () => import('@/views/DownloadManagerView.vue'),
        meta: { title: '下载管理', icon: 'list' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/SystemSettingsView.vue'),
        meta: { title: '系统管理', icon: 'settings' },
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/new' },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

router.beforeEach((to) => {
  const user = useUserStore()
  if (to.meta.requiresAuth && !user.logged) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})

export default router
