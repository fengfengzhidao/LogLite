# LogLite

LogLite 是一个用 Wails v3 + Go + Vue 做的轻量日志查看器。

它不是 ELK、Loki 这类日志平台，而是面向本地开发、客户现场排查、临时翻日志这些场景：选择目录或拖入日志文件，快速查看尾部内容、搜索关键字、看上下文和实时追踪新增日志。

## 功能

- 选择日志目录，列出当前目录下的 `.log` / `.txt`
- 支持拖拽 `.log` / `.txt` 文件到左侧文件列表
- 显示日志文件大小、修改时间
- 点击文件后读取尾部内容，避免大文件一次性塞进界面
- 支持实时 `tail`，可暂停、继续、停止
- 支持当前文件搜索和全部文件搜索
- 支持普通关键字搜索、正则搜索、大小写敏感
- 支持时间范围过滤，例如 `2025-03-09` 到 `2025-03-10`
- 支持 UTF-8、GBK、自动编码识别
- 搜索命中展示上下文，相邻命中会合并，减少重复内容
- 命中文字高亮，命中行单独标记
- 自动清理 ANSI 控制字符，例如 `\x1b[36m`
- 支持 `ERROR` / `WARN` / `INFO` / `DEBUG` 级别识别和筛选
- 支持日间模式 / 黑夜模式
- 内置细滚动条，适配黑夜模式
- 支持连接远程 LogLite Agent，查看服务器上的日志文件
- 远程日志实时 `tail` 使用 WebSocket 推送
- Agent 只允许读取启动时配置的日志目录，不开放任意文件路径

## 运行

```powershell
cd wails-log-viewer
wails3 dev
```

## 构建

```powershell
wails3 task build
```

构建产物：

```text
bin/LogLite.exe
```

## 远程日志 Demo

先在远程机器启动 Agent。当前 Demo 复用 LogLite 可执行文件，通过 `agent` 子命令进入轻量服务模式：

```powershell
bin\LogLite.exe agent -listen 0.0.0.0:8089 -token loglite-demo -log-dir D:\logs
```

Linux agent 是纯 Go 服务，不依赖桌面 GUI。可以在 Windows 上交叉编译：

```powershell
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -tags logliteagent -trimpath -ldflags '-s -w' -o bin/loglite-agent-linux-amd64 .
```

上传到 Linux 服务器后直接运行，不需要再传 `agent` 子命令：

```bash
chmod +x ./loglite-agent-linux-amd64
./loglite-agent-linux-amd64 -listen 0.0.0.0:8089 -token loglite-demo -log-dir /var/log/my-app
```

打开桌面端后切换到“远程服务器”，填写 Agent 地址和 Token，点击“连接 agent”即可查看日志。远程实时 `tail` 通过 WebSocket 接收新增内容。

## 技术栈

- Wails v3
- Go
- Vue 3
- TypeScript
- Vite

## 样例日志

可以选择当前项目里的 `sample-logs` 目录测试搜索和高亮。

## 当前边界

- 目录扫描默认只扫描当前目录第一层，不递归子目录。
- 大文件查看优先读取尾部内容，完整文件搜索会逐行扫描。
- 实时 tail 使用轮询增量读取，适合本地日志排查。
- 远程模式当前支持文件列表、单文件搜索和实时 tail；暂未做多机器聚合搜索、用户体系和 Agent 管理。
- 时间过滤依赖日志行里出现可识别日期或时间，例如 `2025-03-09`、`2025-03-09 10:00:00`。
