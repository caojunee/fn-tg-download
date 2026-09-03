import { httpGet } from './request'

export interface FsDirEntry {
  name: string
  path: string
}

export interface FsDirList {
  path: string
  parent: string
  dirs: FsDirEntry[]
}

/** 列出远端目录下的子目录（目录选择器用） */
export function listDirs(path = '') {
  const q = path ? `?path=${encodeURIComponent(path)}` : ''
  return httpGet<FsDirList>(`/fs/dirs${q}`)
}
