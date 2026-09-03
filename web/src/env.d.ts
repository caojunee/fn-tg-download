/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 是否启用前端 mock 数据（无后端时用于 UI 预览） */
  readonly VITE_USE_MOCK?: string
  readonly VITE_API_BASE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
