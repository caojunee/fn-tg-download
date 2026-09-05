/** 账号信息 */
export interface UserInfo {
  id: number
  /** 完整手机号（含区号，如 +8613800138000） */
  phone: string
  firstName: string
  lastName?: string
  username?: string
  /** 无头像时用首字符生成的占位 */
  initial: string
}

/** 登录流程所处阶段 */
export type LoginStep = 'phone' | 'code' | 'password'

export interface CountryCode {
  label: string
  value: string
  code: string
}

/** 下载任务状态 */
export type TaskStatus = 'pending' | 'downloading' | 'paused' | 'completed' | 'failed' | 'canceled'

/** 消息类型：单条 / 相册 / 组合消息 / 整频道 / 自选条目 */
export type MediaKind = 'single' | 'album' | 'group' | 'channel' | 'selection'

/** 一条可选媒体资源（资源选择器用） */
export interface Resource {
  /** 唯一键「<会话ID>_<消息ID>」，同时用于取缩略图 */
  key: string
  msgId: number
  name: string
  size: number
  mime: string
  /** photo | video | audio | document */
  kind: string
  /** 毫秒时间戳 */
  date: number
  hasThumb: boolean
  thumbUrl?: string
}

/** 一条链接解析出的资源清单 */
export interface ResourceSet {
  url: string
  chatTitle: string
  /** tdl 语义的会话 ID，创建任务时原样回传 */
  peerId: number
  /** channel | album | single | range */
  kind: string
  total: number
  resources: Resource[]
  error?: string
}

export interface DownloadFile {
  id: string
  name: string
  size: number
  downloaded: number
  progress: number
  status: TaskStatus
  /** 缩略图地址（可选） */
  thumb?: string
}

export interface DownloadTask {
  id: string
  /** 任务显示名 */
  name: string
  /** 来源链接，如 https://t.me/siwass/6729 */
  url: string
  chatTitle: string
  messageId: number
  kind: MediaKind
  /** 是否开启相册 / 组合消息探测（tdl --group） */
  group: boolean
  totalSize: number
  downloadedSize: number
  /** 字节 / 秒 */
  speed: number
  progress: number
  status: TaskStatus
  savePath: string
  createdAt: number
  finishedAt?: number
  error?: string
  files: DownloadFile[]
}

/** 新建下载表单 */
export interface CreateDownloadPayload {
  urls: string[]
  /** 相册 / 组合消息探测，默认开启 */
  group: boolean
  savePath?: string
  /** 并发数 */
  concurrency?: number
  /** 包含 / 排除的文件扩展名，如 mp4,mkv */
  include?: string
  exclude?: string
  /** 文件名模板 */
  template?: string
  /** 选择性下载：链接 → 勾选范围（未列出的链接走全量下载） */
  selected?: Record<string, DownloadSelection>
}

/** 一条链接上用户勾选的下载范围 */
export interface DownloadSelection {
  /** 资源清单返回的会话 ID */
  peerId: number
  msgIds: number[]
}

/** 系统设置 */
export interface SystemSettings {
  /** 默认下载路径 */
  downloadPath: string
  /** 代理地址，如 http://192.168.31.180:20172 */
  proxy: string
  /** 默认并发数 */
  concurrency: number
  /** 默认开启相册 / 组合消息探测 */
  autoGroup: boolean
  /** 单文件大小上限（MB），0 表示不限制 */
  maxSizeMB: number
  /** 限速（KB/s），0 表示不限速 */
  speedLimitKB: number
  /** tdl 可执行文件位置 */
  tdlPath: string
  /** tdl 数据目录（登录态 session 所在，官方布局 <dir>/<namespace>） */
  tdlDataDir?: string
  /** 自定义 Telegram 应用 ID，留空用内置身份 */
  appId?: number
  /** 自定义 Telegram 应用 Hash，留空用内置身份 */
  appHash?: string
  /** 后端版本号（构建注入） */
  version?: string
}

/** 后端统一响应结构 */
export interface ApiResult<T> {
  code: number
  message: string
  data: T
}
