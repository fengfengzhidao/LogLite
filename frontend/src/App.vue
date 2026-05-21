<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Dialogs, Events } from '@wailsio/runtime'
import { LogService } from '../bindings/example.com/wails-log-viewer'

type LogFile = {
  name: string
  path: string
  size: number
  modTime: string
}

type LogLine = {
  number: number
  text: string
  level: 'error' | 'warn' | 'info' | 'debug' | 'normal'
}

type LogDirectoryResult = {
  root: string
  files: LogFile[]
  warnings: string[]
}

type LogContentResult = {
  path: string
  lines: LogLine[]
  totalRead: number
  truncated: boolean
  warning: string
  size: number
}

type SearchHit = {
  lineNumber: number
  matchLines: number[]
  lines: LogLine[]
}

type SearchResult = {
  path: string
  keyword: string
  hits: SearchHit[]
  hitCount: number
  limited: boolean
  scanned: number
  warning: string
}

type SearchOptions = {
  keyword: string
  contextLines: number
  limit: number
  caseSensitive: boolean
  useRegex: boolean
  encoding: string
  startTime: string
  endTime: string
}

type FileSearchResult = {
  file: LogFile
  keyword: string
  hits: SearchHit[]
  hitCount: number
  limited: boolean
  scanned: number
  warning: string
}

type MultiSearchResult = {
  keyword: string
  files: FileSearchResult[]
  filesScanned: number
  hitCount: number
  limited: boolean
  warnings: string[]
}

type TailUpdateResult = {
  path: string
  lines: LogLine[]
  size: number
  truncated: boolean
  rotated: boolean
  warning: string
}

type TextPart = {
  text: string
  matched: boolean
}

const directory = ref('')
const files = ref<LogFile[]>([])
const warnings = ref<string[]>([])
const activeFile = ref<LogFile | null>(null)
const content = ref<LogContentResult | null>(null)
const search = ref('')
const searchResult = ref<SearchResult | null>(null)
const multiSearchResult = ref<MultiSearchResult | null>(null)
const searchScope = ref<'current' | 'all'>('current')
const encoding = ref('auto')
const caseSensitive = ref(false)
const useRegex = ref(false)
const startTime = ref('')
const endTime = ref('')
const lastSearchOptions = ref<SearchOptions | null>(null)
const activeLevel = ref<'all' | LogLine['level']>('all')
const loadingFiles = ref(false)
const loadingContent = ref(false)
const searching = ref(false)
const error = ref('')
const isDarkMode = ref(false)
const tailing = ref(false)
const tailPaused = ref(false)
const tailOffset = ref(0)
let removeDropListener: (() => void) | null = null
let tailTimer: number | null = null

const selectedLines = computed(() => {
  const lines = content.value?.lines ?? []
  if (activeLevel.value === 'all') return lines
  return lines.filter((line) => line.level === activeLevel.value)
})

const visibleHits = computed(() => {
  const hits = searchResult.value?.hits ?? []
  if (activeLevel.value === 'all') return hits
  return hits
    .map((hit) => ({
      ...hit,
      lines: hit.lines.filter((line) => line.level === activeLevel.value || line.number === hit.lineNumber),
    }))
    .filter((hit) => hit.lines.length > 0)
})

const visibleMultiFiles = computed(() => {
  const items = multiSearchResult.value?.files ?? []
  if (activeLevel.value === 'all') return items
  return items
    .map((item) => ({
      ...item,
      hits: item.hits
        .map((hit) => ({
          ...hit,
          lines: hit.lines.filter((line) => line.level === activeLevel.value || line.number === hit.lineNumber),
        }))
        .filter((hit) => hit.lines.length > 0),
    }))
    .filter((item) => item.hits.length > 0)
})

const statusText = computed(() => {
  if (!activeFile.value) return '选择目录后点击日志文件'
  if (multiSearchResult.value) {
    const limited = multiSearchResult.value.limited ? '，结果已限制' : ''
    return `搜索 ${formatNumber(multiSearchResult.value.filesScanned)} 个文件，命中 ${formatNumber(multiSearchResult.value.hitCount)} 处${limited}`
  }
  if (searchResult.value) {
    const limited = searchResult.value.limited ? '，已限制展示前 200 条' : ''
    return `扫描 ${formatNumber(searchResult.value.scanned)} 行，命中 ${formatNumber(searchResult.value.hitCount)} 处${limited}`
  }
  if (content.value?.truncated) return `显示尾部 ${formatNumber(content.value.totalRead)} 行`
  return `显示 ${formatNumber(content.value?.totalRead ?? 0)} 行`
})

function formatNumber(value: number) {
  return new Intl.NumberFormat('zh-CN').format(value)
}

function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function parentDir(path: string) {
  return path.replace(/[\\/][^\\/]+$/, '')
}

function setError(err: unknown, fallback: string) {
  error.value = err instanceof Error ? err.message : String(err || fallback)
}

function applyTheme() {
  document.documentElement.dataset.theme = isDarkMode.value ? 'dark' : 'light'
  localStorage.setItem('wails-log-viewer-theme', isDarkMode.value ? 'dark' : 'light')
}

function toggleTheme() {
  isDarkMode.value = !isDarkMode.value
  applyTheme()
}

function buildSearchOptions(): SearchOptions {
  return {
    keyword: search.value.trim(),
    contextLines: 2,
    limit: 200,
    caseSensitive: caseSensitive.value,
    useRegex: useRegex.value,
    encoding: encoding.value,
    startTime: startTime.value.trim(),
    endTime: endTime.value.trim(),
  }
}

async function chooseDirectory() {
  error.value = ''
  const selected = await Dialogs.OpenFile({
    CanChooseDirectories: true,
    CanChooseFiles: false,
    AllowsMultipleSelection: false,
    Title: '选择日志目录',
    ButtonText: '选择目录',
  })

  if (typeof selected === 'string' && selected) {
    directory.value = selected
    await scanDirectory()
  }
}

async function scanDirectory() {
  if (!directory.value || loadingFiles.value) return

  loadingFiles.value = true
  error.value = ''
  activeFile.value = null
  content.value = null
  searchResult.value = null
  multiSearchResult.value = null
  stopTail()

  try {
    const result = (await LogService.ListLogFiles(directory.value)) as LogDirectoryResult
    directory.value = result.root
    files.value = result.files
    warnings.value = result.warnings ?? []
  } catch (err) {
    files.value = []
    warnings.value = []
    setError(err, '扫描日志目录失败')
  } finally {
    loadingFiles.value = false
  }
}

async function openFile(file: LogFile) {
  activeFile.value = file
  directory.value = parentDir(file.path)
  content.value = null
  searchResult.value = null
  multiSearchResult.value = null
  error.value = ''
  loadingContent.value = true

  try {
    content.value = (await LogService.ReadTail(file.path, 2000, encoding.value)) as LogContentResult
    tailOffset.value = content.value.size
  } catch (err) {
    setError(err, '读取日志失败')
  } finally {
    loadingContent.value = false
  }
}

async function openDroppedFiles(paths: string[]) {
  const logPaths = paths.filter((path) => /\.(log|txt)$/i.test(path))
  if (!logPaths.length) {
    error.value = '只支持拖入 .log / .txt 日志文件'
    return
  }

  error.value = ''
  const added: LogFile[] = []

  for (const path of logPaths) {
    try {
      const file = (await LogService.GetLogFile(path)) as LogFile
      added.push(file)
    } catch (err) {
      setError(err, '读取拖入文件失败')
    }
  }

  if (!added.length) return

  const nextFiles = [...files.value]
  for (const file of added) {
    const index = nextFiles.findIndex((item) => item.path === file.path)
    if (index >= 0) {
      nextFiles.splice(index, 1, file)
    } else {
      nextFiles.unshift(file)
    }
  }

  files.value = nextFiles
  directory.value = parentDir(added[0].path)
  warnings.value = []
  await openFile(added[0])
}

async function runSearch() {
  if (searching.value) return
  const hasSearchCondition = Boolean(search.value.trim() || startTime.value.trim() || endTime.value.trim())
  if (!hasSearchCondition) {
    searchResult.value = null
    multiSearchResult.value = null
    lastSearchOptions.value = null
    return
  }
  if (searchScope.value === 'current' && !activeFile.value) return
  if (searchScope.value === 'all' && !files.value.length) return

  searching.value = true
  error.value = ''
  const options = buildSearchOptions()

  try {
    lastSearchOptions.value = options
    if (searchScope.value === 'all') {
      multiSearchResult.value = (await LogService.SearchInFiles(files.value, options)) as MultiSearchResult
      searchResult.value = null
      if (multiSearchResult.value.warnings?.length) {
        warnings.value = multiSearchResult.value.warnings
      }
    } else if (activeFile.value) {
      searchResult.value = (await LogService.SearchInFile(activeFile.value.path, options)) as SearchResult
      multiSearchResult.value = null
    }
  } catch (err) {
    searchResult.value = null
    multiSearchResult.value = null
    setError(err, '搜索失败')
  } finally {
    searching.value = false
  }
}

function clearSearch() {
  search.value = ''
  searchResult.value = null
  multiSearchResult.value = null
  lastSearchOptions.value = null
}

function splitMatchedText(text: string): TextPart[] {
  const options = lastSearchOptions.value
  const keyword = options?.keyword.trim()
  if (!keyword) return [{ text, matched: false }]

  if (options?.useRegex) {
    try {
      const flags = options.caseSensitive ? 'g' : 'gi'
      const re = new RegExp(keyword, flags)
      const parts: TextPart[] = []
      let start = 0
      let match = re.exec(text)
      while (match) {
        if (match.index > start) {
          parts.push({ text: text.slice(start, match.index), matched: false })
        }
        const matchedText = match[0]
        if (!matchedText) break
        parts.push({ text: matchedText, matched: true })
        start = match.index + matchedText.length
        match = re.exec(text)
      }
      if (start < text.length) {
        parts.push({ text: text.slice(start), matched: false })
      }
      return parts.length ? parts : [{ text, matched: false }]
    } catch {
      return [{ text, matched: false }]
    }
  }

  const lowerText = options?.caseSensitive ? text : text.toLowerCase()
  const lowerKeyword = options?.caseSensitive ? keyword : keyword.toLowerCase()
  const parts: TextPart[] = []
  let start = 0
  let index = lowerText.indexOf(lowerKeyword)

  while (index >= 0) {
    if (index > start) {
      parts.push({ text: text.slice(start, index), matched: false })
    }
    parts.push({ text: text.slice(index, index + keyword.length), matched: true })
    start = index + keyword.length
    index = lowerText.indexOf(lowerKeyword, start)
  }

  if (start < text.length) {
    parts.push({ text: text.slice(start), matched: false })
  }

  return parts.length ? parts : [{ text, matched: false }]
}

function lineClass(line: LogLine) {
  const options = lastSearchOptions.value
  if (!options?.keyword) return ['log-line', `level-${line.level}`]

  let matched = false
  if (options.useRegex) {
    try {
      matched = new RegExp(options.keyword, options.caseSensitive ? '' : 'i').test(line.text)
    } catch {
      matched = false
    }
  } else {
    matched = options.caseSensitive
      ? line.text.includes(options.keyword)
      : line.text.toLowerCase().includes(options.keyword.toLowerCase())
  }
  return ['log-line', `level-${line.level}`, { matched }]
}

function resultLineClass(line: LogLine, hit: SearchHit) {
  return [...lineClass(line), { 'match-line': hit.matchLines?.includes(line.number) }]
}

function matchTitle(hit: SearchHit) {
  const lines = hit.matchLines?.length ? hit.matchLines : [hit.lineNumber]
  if (lines.length === 1) return `第 ${lines[0]} 行命中`
  return `第 ${lines.join('、')} 行命中`
}

async function reloadActiveFile() {
  if (activeFile.value) {
    await openFile(activeFile.value)
  }
}

function stopTail() {
  tailing.value = false
  tailPaused.value = false
  if (tailTimer !== null) {
    window.clearInterval(tailTimer)
    tailTimer = null
  }
}

function toggleTail() {
  if (tailing.value) {
    stopTail()
    return
  }
  if (!activeFile.value) return

  tailing.value = true
  tailPaused.value = false
  tailTimer = window.setInterval(() => {
    void pollTail()
  }, 1200)
  void pollTail()
}

async function pollTail() {
  if (!activeFile.value || !tailing.value || tailPaused.value) return

  try {
    const update = (await LogService.ReadTailUpdate(activeFile.value.path, tailOffset.value, encoding.value)) as TailUpdateResult
    tailOffset.value = update.size
    if (update.rotated) {
      await openFile(activeFile.value)
      return
    }
    if (update.warning) {
      warnings.value = [update.warning]
    }
    if (update.lines.length && content.value) {
      const base = content.value.lines.length
      const nextLines = update.lines.map((line, index) => ({
        ...line,
        number: base + index + 1,
      }))
      content.value = {
        ...content.value,
        lines: [...content.value.lines, ...nextLines].slice(-5000),
        totalRead: Math.min(content.value.totalRead + nextLines.length, 5000),
        size: update.size,
      }
    }
  } catch (err) {
    setError(err, '实时追踪失败')
    stopTail()
  }
}

onMounted(() => {
  isDarkMode.value = localStorage.getItem('wails-log-viewer-theme') === 'dark'
  applyTheme()

  removeDropListener = Events.On('log-files-dropped', (event) => {
    const paths = Array.isArray(event.data) ? event.data.filter((item): item is string => typeof item === 'string') : []
    void openDroppedFiles(paths)
  })
})

onUnmounted(() => {
  removeDropListener?.()
  stopTail()
})
</script>

<template>
  <main class="app-shell">
    <header class="topbar">
      <div class="brand">
        <img src="/loglite-logo.png" alt="" />
        <div>
          <p class="eyebrow">本地日志查看器</p>
          <h1>LogLite</h1>
        </div>
      </div>
      <div class="top-actions">
        <button type="button" class="theme-toggle" @click="toggleTheme">
          {{ isDarkMode ? '日间模式' : '黑夜模式' }}
        </button>
        <button type="button" @click="chooseDirectory">
          {{ loadingFiles ? '扫描中...' : '选择目录' }}
        </button>
        <button type="button" class="secondary" :disabled="!directory || loadingFiles" @click="scanDirectory">
          重新扫描
        </button>
      </div>
    </header>

    <section class="control-bar">
      <input
        v-model="directory"
        class="path-input"
        type="text"
        spellcheck="false"
        placeholder="输入或选择日志目录，例如 D:\\logs"
        @keyup.enter="scanDirectory"
      />
      <input
        v-model="search"
        class="search-input"
        type="search"
        spellcheck="false"
        placeholder="搜索 error、接口名、订单号"
        :disabled="searchScope === 'current' && !activeFile"
        @keyup.enter="runSearch"
      />
      <button type="button" :disabled="(searchScope === 'current' && !activeFile) || (searchScope === 'all' && !files.length) || searching" @click="runSearch">
        {{ searching ? '搜索中...' : '搜索' }}
      </button>
      <button type="button" class="ghost" :disabled="!searchResult && !multiSearchResult" @click="clearSearch">清除</button>
    </section>

    <section class="option-bar">
      <div class="option-main">
        <div class="segmented">
          <button type="button" :class="{ active: searchScope === 'current' }" @click="searchScope = 'current'">当前文件</button>
          <button type="button" :class="{ active: searchScope === 'all' }" @click="searchScope = 'all'">全部文件</button>
        </div>
        <label>
          编码
          <select v-model="encoding" @change="reloadActiveFile">
            <option value="auto">自动</option>
            <option value="utf-8">UTF-8</option>
            <option value="gbk">GBK</option>
          </select>
        </label>
        <label class="check">
          <input v-model="caseSensitive" type="checkbox" />
          大小写
        </label>
        <label class="check">
          <input v-model="useRegex" type="checkbox" />
          正则
        </label>
      </div>
      <div class="tail-actions">
        <button type="button" class="secondary" :disabled="!activeFile" @click="toggleTail">
          {{ tailing ? '停止 tail' : '实时 tail' }}
        </button>
        <button type="button" class="ghost" :disabled="!tailing" @click="tailPaused = !tailPaused">
          {{ tailPaused ? '继续' : '暂停' }}
        </button>
      </div>
      <div class="time-row">
        <label>
          开始时间
          <input v-model="startTime" class="time-input" type="text" placeholder="2026-05-21 10:00:00" @keyup.enter="runSearch" />
        </label>
        <label>
          结束时间
          <input v-model="endTime" class="time-input" type="text" placeholder="2026-05-21 10:30:00" @keyup.enter="runSearch" />
        </label>
      </div>
    </section>

    <section class="level-tabs" aria-label="日志级别">
      <button type="button" :class="{ active: activeLevel === 'all' }" @click="activeLevel = 'all'">全部</button>
      <button type="button" :class="{ active: activeLevel === 'error' }" @click="activeLevel = 'error'">ERROR</button>
      <button type="button" :class="{ active: activeLevel === 'warn' }" @click="activeLevel = 'warn'">WARN</button>
      <button type="button" :class="{ active: activeLevel === 'info' }" @click="activeLevel = 'info'">INFO</button>
      <button type="button" :class="{ active: activeLevel === 'debug' }" @click="activeLevel = 'debug'">DEBUG</button>
    </section>

    <p v-if="error" class="message error">{{ error }}</p>
    <p v-if="warnings.length" class="message warning">{{ warnings[0] }}</p>

    <section class="workspace">
      <aside id="log-file-drop-zone" class="file-panel" data-file-drop-target>
        <div class="panel-head">
          <div>
            <strong>日志文件</strong>
            <span>拖入 .log / .txt 或选择目录</span>
          </div>
          <span>{{ formatNumber(files.length) }} 个</span>
        </div>
        <div class="drop-hint">把日志文件拖到这里</div>
        <div class="file-list">
          <button
            v-for="file in files"
            :key="file.path"
            type="button"
            :class="['file-item', { active: activeFile?.path === file.path }]"
            @click="openFile(file)"
          >
            <span class="file-name">{{ file.name }}</span>
            <span class="file-meta">{{ formatSize(file.size) }} · {{ file.modTime }}</span>
          </button>
          <div v-if="!files.length" class="empty">还没有扫描到 .log / .txt 文件</div>
        </div>
      </aside>

      <section class="log-panel">
        <div class="panel-head">
          <div>
            <strong>{{ activeFile?.name ?? '日志内容' }}</strong>
            <span>{{ statusText }}</span>
          </div>
          <span v-if="content?.warning || searchResult?.warning" class="soft-warning">
            {{ content?.warning || searchResult?.warning }}
          </span>
        </div>

        <div v-if="loadingContent" class="empty fill">正在读取日志...</div>

        <div v-else-if="searchResult" class="hit-list">
          <article v-for="hit in visibleHits" :key="hit.lineNumber" class="hit-block">
            <div class="hit-title">{{ matchTitle(hit) }}</div>
            <div v-for="line in hit.lines" :key="`${hit.lineNumber}-${line.number}`" :class="resultLineClass(line, hit)">
              <span class="line-no">{{ line.number }}</span>
              <code>
                <template v-for="(part, index) in splitMatchedText(line.text)" :key="index">
                  <mark v-if="part.matched">{{ part.text }}</mark>
                  <span v-else>{{ part.text }}</span>
                </template>
              </code>
            </div>
          </article>
          <div v-if="!visibleHits.length" class="empty fill">当前级别筛选下没有命中结果</div>
        </div>

        <div v-else-if="multiSearchResult" class="hit-list">
          <article v-for="item in visibleMultiFiles" :key="item.file.path" class="file-hit-block">
            <div class="file-hit-title">
              <strong>{{ item.file.name }}</strong>
              <span>{{ item.hitCount }} 处命中 · 扫描 {{ formatNumber(item.scanned) }} 行</span>
            </div>
            <article v-for="hit in item.hits" :key="`${item.file.path}-${hit.lineNumber}`" class="hit-block">
              <div class="hit-title">{{ matchTitle(hit) }}</div>
              <div v-for="line in hit.lines" :key="`${item.file.path}-${hit.lineNumber}-${line.number}`" :class="resultLineClass(line, hit)">
                <span class="line-no">{{ line.number }}</span>
                <code>
                  <template v-for="(part, index) in splitMatchedText(line.text)" :key="index">
                    <mark v-if="part.matched">{{ part.text }}</mark>
                    <span v-else>{{ part.text }}</span>
                  </template>
                </code>
              </div>
            </article>
          </article>
          <div v-if="!visibleMultiFiles.length" class="empty fill">当前筛选下没有多文件命中结果</div>
        </div>

        <div v-else-if="content" class="log-list">
          <div v-for="line in selectedLines" :key="line.number" :class="lineClass(line)">
            <span class="line-no">{{ line.number }}</span>
            <code>
              <template v-for="(part, index) in splitMatchedText(line.text || ' ')" :key="index">
                <mark v-if="part.matched">{{ part.text }}</mark>
                <span v-else>{{ part.text }}</span>
              </template>
            </code>
          </div>
          <div v-if="!selectedLines.length" class="empty fill">当前级别筛选下没有日志行</div>
        </div>

        <div v-else class="empty fill">选择左侧日志文件后显示尾部内容</div>
      </section>
    </section>
  </main>
</template>

<style>
:root {
  font-family: Inter, "Segoe UI", "Microsoft YaHei", Arial, sans-serif;
  color: var(--text);
  background: var(--app-bg);
  --app-bg: #eef2f6;
  --panel-bg: #ffffff;
  --panel-border: #d8e0ea;
  --text: #172033;
  --heading: #0f172a;
  --muted: #64748b;
  --control-bg: #ffffff;
  --input-bg: #f8fafc;
  --input-border: #cbd5e1;
  --primary-bg: #38bdf8;
  --primary-text: #06121f;
  --secondary-bg: #dbeafe;
  --secondary-text: #1e3a8a;
  --ghost-bg: #e2e8f0;
  --ghost-text: #334155;
  --tab-active-bg: #c7f9ee;
  --drop-bg: #f8fafc;
  --drop-active-bg: #cffafe;
  --file-hover-bg: #e0f2fe;
  --file-border: #edf2f7;
  --log-bg: #0f172a;
  --log-text: #d6deea;
  --error-text: #991b1b;
  --error-bg: #fee2e2;
  --warning-text: #854d0e;
  --warning-bg: #fef3c7;
  --brand: #0f766e;
  --soft-warning: #b45309;
  --drop-border: #0891b2;
  --log-border: rgba(148, 163, 184, 0.16);
  --line-no: #64748b;
  --hit-title: #7dd3fc;
  --match-line: #22d3ee;
  --mark-bg: #facc15;
  --mark-text: #111827;
  --scrollbar-track: #e8eef5;
  --scrollbar-thumb: #94a3b8;
  --scrollbar-thumb-hover: #64748b;
  font-synthesis: none;
  text-rendering: optimizeLegibility;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  -webkit-text-size-adjust: 100%;
}

:root[data-theme='dark'] {
  --app-bg: #111827;
  --panel-bg: #172033;
  --panel-border: #334155;
  --text: #dbeafe;
  --heading: #f8fafc;
  --muted: #94a3b8;
  --control-bg: #172033;
  --input-bg: #0f172a;
  --input-border: #475569;
  --primary-bg: #22d3ee;
  --primary-text: #082f49;
  --secondary-bg: #1e3a8a;
  --secondary-text: #dbeafe;
  --ghost-bg: #334155;
  --ghost-text: #e2e8f0;
  --tab-active-bg: #115e59;
  --drop-bg: #0f172a;
  --drop-active-bg: #164e63;
  --file-hover-bg: #1e293b;
  --file-border: #263244;
  --log-bg: #0b1120;
  --log-text: #dbeafe;
  --error-text: #fecaca;
  --error-bg: #7f1d1d;
  --warning-text: #fde68a;
  --warning-bg: #78350f;
  --brand: #2dd4bf;
  --soft-warning: #fbbf24;
  --drop-border: #22d3ee;
  --log-border: rgba(148, 163, 184, 0.18);
  --line-no: #94a3b8;
  --hit-title: #67e8f9;
  --match-line: #22d3ee;
  --mark-bg: #fde047;
  --mark-text: #111827;
  --scrollbar-track: #101827;
  --scrollbar-thumb: #475569;
  --scrollbar-thumb-hover: #64748b;
}

html,
body,
#app {
  height: 100%;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  min-width: 900px;
  overflow: hidden;
}

button,
input {
  font: inherit;
}

button {
  border: 0;
  border-radius: 8px;
  padding: 9px 14px;
  color: var(--primary-text);
  background: var(--primary-bg);
  font-weight: 800;
  cursor: pointer;
}

button:disabled {
  cursor: not-allowed;
  opacity: 0.48;
}

.secondary {
  background: var(--secondary-bg);
  color: var(--secondary-text);
}

.ghost {
  background: var(--ghost-bg);
  color: var(--ghost-text);
}

.theme-toggle {
  background: var(--ghost-bg);
  color: var(--ghost-text);
}

.app-shell {
  display: flex;
  height: 100vh;
  flex-direction: column;
  gap: 12px;
  overflow: hidden;
  padding: 22px;
}

.topbar,
.control-bar,
.level-tabs,
.workspace,
.message {
  width: 100%;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
}

.brand img {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  box-shadow: 0 10px 24px rgba(15, 23, 42, 0.18);
}

.eyebrow {
  margin: 0 0 4px;
  color: var(--brand);
  font-size: 13px;
  font-weight: 900;
}

h1 {
  margin: 0;
  color: var(--heading);
  font-size: 28px;
  line-height: 1.18;
}

.top-actions,
.control-bar,
.level-tabs {
  display: flex;
  align-items: center;
  gap: 8px;
}

.control-bar {
  border: 1px solid var(--panel-border);
  border-radius: 8px;
  padding: 10px;
  background: var(--control-bg);
}

input {
  min-width: 0;
  border: 1px solid var(--input-border);
  border-radius: 8px;
  padding: 9px 11px;
  color: var(--text);
  background: var(--input-bg);
  outline: none;
}

select {
  border: 1px solid var(--input-border);
  border-radius: 8px;
  padding: 8px 10px;
  color: var(--text);
  background: var(--input-bg);
  font: inherit;
  outline: none;
}

input:focus {
  border-color: var(--primary-bg);
  background: var(--panel-bg);
}

.path-input {
  flex: 1.4;
}

.search-input {
  flex: 1;
}

.option-bar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 10px;
  border: 1px solid var(--panel-border);
  border-radius: 8px;
  padding: 10px;
  background: var(--control-bg);
}

.option-bar label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 800;
  white-space: nowrap;
}

.option-bar input[type='checkbox'] {
  width: 15px;
  height: 15px;
  accent-color: var(--primary-bg);
}

.option-bar .time-input {
  width: 178px;
  padding: 8px 9px;
}

.option-main,
.tail-actions,
.time-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.option-main {
  flex-wrap: wrap;
}

.tail-actions {
  justify-content: flex-end;
}

.tail-actions button {
  min-width: 76px;
}

.time-row {
  grid-column: 1 / -1;
  flex-wrap: wrap;
  border-top: 1px solid var(--panel-border);
  padding-top: 10px;
}

.segmented {
  display: inline-flex;
  overflow: hidden;
  border: 1px solid var(--input-border);
  border-radius: 8px;
}

.segmented button {
  border-radius: 0;
  padding: 8px 11px;
  color: var(--muted);
  background: transparent;
}

.segmented button.active {
  color: var(--heading);
  background: var(--tab-active-bg);
}

.level-tabs button {
  padding: 7px 10px;
  color: var(--muted);
  background: transparent;
}

.level-tabs button.active {
  color: var(--heading);
  background: var(--tab-active-bg);
}

.message {
  margin: 0;
  border-radius: 8px;
  padding: 9px 12px;
  font-weight: 700;
}

.error {
  color: var(--error-text);
  background: var(--error-bg);
}

.warning {
  color: var(--warning-text);
  background: var(--warning-bg);
}

.workspace {
  display: grid;
  flex: 1;
  min-height: 0;
  grid-template-columns: 320px minmax(0, 1fr);
  gap: 14px;
  overflow: hidden;
}

.file-panel,
.log-panel {
  overflow: hidden;
  border: 1px solid var(--panel-border);
  border-radius: 8px;
  background: var(--panel-bg);
}

.file-panel {
  position: relative;
  width: 320px;
  min-width: 320px;
  max-width: 320px;
}

.file-panel.file-drop-target-active {
  border-color: var(--drop-border);
  box-shadow: 0 0 0 3px rgba(34, 211, 238, 0.18);
}

.file-panel.file-drop-target-active .drop-hint {
  color: var(--heading);
  background: var(--drop-active-bg);
}

.file-panel,
.log-panel {
  display: flex;
  min-height: 0;
  flex-direction: column;
}

.panel-head {
  display: flex;
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-bottom: 1px solid var(--panel-border);
  padding: 12px 14px;
}

.panel-head strong {
  display: block;
  color: var(--heading);
}

.panel-head span {
  display: block;
  margin-top: 2px;
  color: var(--muted);
  font-size: 12px;
}

.soft-warning {
  color: var(--soft-warning);
  font-size: 12px;
  white-space: nowrap;
}

.drop-hint {
  border-bottom: 1px dashed var(--input-border);
  padding: 9px 14px;
  color: var(--muted);
  background: var(--drop-bg);
  font-size: 12px;
  font-weight: 800;
  text-align: center;
}

.file-list,
.log-list,
.hit-list {
  overflow: auto;
  min-height: 0;
  flex: 1;
  scrollbar-color: var(--scrollbar-thumb) transparent;
  scrollbar-width: thin;
}

.file-list::-webkit-scrollbar,
.log-list::-webkit-scrollbar,
.hit-list::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

.file-list::-webkit-scrollbar-track,
.log-list::-webkit-scrollbar-track,
.hit-list::-webkit-scrollbar-track {
  background: transparent;
}

.file-list::-webkit-scrollbar-thumb,
.log-list::-webkit-scrollbar-thumb,
.hit-list::-webkit-scrollbar-thumb {
  border: 2px solid transparent;
  border-radius: 999px;
  background: var(--scrollbar-thumb);
  background-clip: padding-box;
}

.file-list::-webkit-scrollbar-thumb:hover,
.log-list::-webkit-scrollbar-thumb:hover,
.hit-list::-webkit-scrollbar-thumb:hover {
  background: var(--scrollbar-thumb-hover);
  background-clip: padding-box;
}

.file-list::-webkit-scrollbar-corner,
.log-list::-webkit-scrollbar-corner,
.hit-list::-webkit-scrollbar-corner {
  background: transparent;
}

.file-item {
  display: block;
  width: 100%;
  border-radius: 0;
  border-bottom: 1px solid var(--file-border);
  padding: 12px 14px;
  text-align: left;
  color: var(--text);
  background: var(--panel-bg);
}

.file-item:hover,
.file-item.active {
  background: var(--file-hover-bg);
}

.file-name {
  display: block;
  overflow: hidden;
  font-weight: 850;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-meta {
  display: block;
  margin-top: 5px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 600;
}

.log-list,
.hit-list {
  background: var(--log-bg);
}

.log-line {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr);
  gap: 12px;
  border-bottom: 1px solid var(--log-border);
  padding: 5px 12px;
  color: var(--log-text);
  font-size: 13px;
  line-height: 1.5;
}

.log-line code {
  overflow-wrap: anywhere;
  font-family: "Cascadia Code", Consolas, "Courier New", monospace;
  white-space: pre-wrap;
}

.log-line mark {
  border-radius: 3px;
  padding: 0 2px;
  color: var(--mark-text);
  background: var(--mark-bg);
}

.line-no {
  color: var(--line-no);
  font-variant-numeric: tabular-nums;
  text-align: right;
  user-select: none;
}

.level-error {
  color: #fecaca;
  background: rgba(127, 29, 29, 0.35);
}

.level-warn {
  color: #fde68a;
  background: rgba(120, 53, 15, 0.28);
}

.level-info {
  color: #bfdbfe;
}

.level-debug {
  color: #c4b5fd;
}

.matched {
  box-shadow: inset 3px 0 0 var(--match-line);
}

.match-line {
  background: rgba(8, 145, 178, 0.22);
  box-shadow: inset 3px 0 0 var(--match-line);
}

.match-line .line-no {
  color: var(--hit-title);
  font-weight: 900;
}

.hit-block {
  border-bottom: 1px solid var(--log-border);
  padding: 8px 0 10px;
}

.file-hit-block {
  border-bottom: 1px solid var(--log-border);
}

.file-hit-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 12px;
  color: var(--log-text);
  background: rgba(15, 23, 42, 0.48);
}

.file-hit-title strong {
  overflow: hidden;
  color: var(--hit-title);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-hit-title span {
  color: var(--line-no);
  font-size: 12px;
  white-space: nowrap;
}

.hit-title {
  padding: 0 12px 7px 76px;
  color: var(--hit-title);
  font-size: 12px;
  font-weight: 900;
}

.empty {
  padding: 24px;
  color: var(--muted);
  text-align: center;
}

.fill {
  display: grid;
  min-height: 100%;
  place-items: center;
}
</style>
