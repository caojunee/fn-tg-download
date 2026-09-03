import { ref } from 'vue'

const mql = window.matchMedia('(max-width: 768px)')
const isMobile = ref(mql.matches)

mql.addEventListener('change', (e) => {
  isMobile.value = e.matches
})

/** 是否窄屏（手机端飞牛 iframe 内） */
export function useIsMobile() {
  return isMobile
}
