# nova-reader-go

使用 **Golang** 重构的 [novel-reader](https://github.com/765362546/novel_reader_old) 服务端。

原项目基于 Kotlin + Vert.x，本项目用 Go 重新实现了全部后端能力，**前端代码保持原样、零修改**，可直接复用原 Vue 前端。

## 特性

- 兼容原有 `/reader3/*` REST API，前端无需任何改动
- 书源规则引擎：CSS 选择器、XPath、JSONPath、正则、JavaScript（goja）
- 支持 Legado（阅读）书源语法：`&&` / `||` / `%%`、`@` 管道、`##` 替换、`{{...}}`、`@put:` / `@get:`
- 书籍搜索、发现、详情、目录、正文、换源
- 书架管理、阅读进度、分组、缓存、导出（TXT / EPUB）
- 本地书籍：TXT（目录规则）、EPUB、CBZ
- RSS 订阅（规则解析 + 默认 XML/Atom 解析）
- 多用户、登录鉴权、邀请码、管理密码
- WebDAV：文件管理、备份与恢复（兼容 Legado 备份包）
- SSE 流式接口：多书源搜索、书籍缓存、书源调试

## 技术栈

| 组件 | 用途 |
| --- | --- |
| `go-chi/chi` | HTTP 路由 |
| `goquery` / `cascadia` | CSS 选择器解析 |
| `antchfx/htmlquery` | XPath 解析 |
| `ohler55/ojg` | JSONPath 解析 |
| `dop251/goja` | JavaScript 规则执行 |
| `dlclark/regexp2` | 支持 lookbehind 的正则（TXT 目录规则） |
| `golang.org/x/text` | 编码检测与转换（GBK/UTF-8 等） |

## 目录结构

```
.
├── cmd/server/            # 程序入口
├── internal/
│   ├── config/            # 配置加载（READER_* 环境变量）
│   ├── store/             # JSON 文件存储
│   ├── model/             # 数据模型（书籍、书源、RSS 等）
│   ├── rule/              # 规则引擎（HTML / XPath / JSONPath / JS / HTTP 抓取）
│   ├── webbook/           # 网络书籍：搜索、详情、目录、正文
│   ├── localbook/         # 本地书籍：TXT / EPUB / CBZ
│   ├── rss/               # RSS 解析
│   └── httpapi/           # HTTP 接口与鉴权
└── web/                   # 原 Vue 前端（未修改）
```

## 快速开始

### 1. 环境要求

- Go **1.26+**
- Node.js **14 ~ 16**（推荐 16 LTS）及配套 npm，仅用于构建前端

> 前端基于 Vue CLI 4（webpack 4）。Node.js 17 及以上版本构建时需设置
> `NODE_OPTIONS=--openssl-legacy-provider`，否则会因 OpenSSL 3 报错。

### 2. 一键构建（推荐）

```bash
./build.sh
./novel-server
```

脚本会依次执行 `npm ci`、`npm run build` 和 `go build`，确保前端产物被内嵌，
不会漏编译。只想重新编译后端（复用已有 `web/dist`）时：

```bash
SKIP_FRONTEND=1 ./build.sh
```

默认监听 `:8080`，浏览器访问 `http://localhost:8080/`。

### 3. 分步构建

手动构建前端：

```bash
cd web
npm install
npm run build
```

Node.js 17+ 请改用：

```bash
cd web
npm install
NODE_OPTIONS=--openssl-legacy-provider npm run build
```

构建产物位于 `web/dist`。`go build` 时该目录会通过 `go:embed` 打包进二进制，
因此最终产物是单文件，部署时无需再携带 `web/`。若目录尚未构建，程序会退回到
从磁盘读取 `web/dist`（开发时优先使用磁盘文件，便于热更新）。

再编译并运行后端：

```bash
go build -o novel-server ./cmd/server
./novel-server
```

开发调试也可直接运行（不会内嵌前端，仍从磁盘读取）：

```bash
go run ./cmd/server
```

## 配置

配置全部通过环境变量提供（12-factor 风格），代码内置默认值，无需配置文件。
未设置的变量使用默认值：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `READER_PORT` | `8080` | 监听端口 |
| `READER_STORAGE_PATH` | `storage` | 数据目录 |
| `READER_SECURE` | `false` | 是否启用多用户 |
| `READER_SECURE_KEY` | 空 | 管理密码 |
| `READER_INVITE_CODE` | 空 | 邀请码 |
| `READER_USER_LIMIT` | `50` | 用户数量上限 |
| `READER_USER_BOOK_LIMIT` | `200` | 单用户书架上限 |
| `READER_CACHE_CHAPTER_CONTENT` | `true` | 是否缓存章节正文 |
| `READER_DEBUG` | `false` | 调试模式 |
| `READER_DEBUG_LOG` | `false` | 输出调试日志 |
| `READER_SHOW_UI` | `false` | 是否展示内置前端 |
| `READER_PACKAGED` | `false` | 打包运行模式 |
| `READER_AUTO_CLEAR_INACTIVE_USER` | `0` | 自动清理未登录用户的天数（0 关闭） |
| `READER_EXPORT_USE_REPLACE` | `false` | 导出时应用替换规则 |
| `READER_EXPORT_CHARSET` | `UTF-8` | 导出编码 |
| `READER_EXPORT_NO_CHAPTER_NAME` | `false` | 导出时不写章节名 |
| `READER_EXPORT_PICTURE_FILE` | `false` | 导出图片文件 |
| `READER_WEB_URL` | 空 | 对外访问地址 |
| `READER_FONTS` | 空 | 系统可用字体列表（返回给前端） |

布尔值支持 `true/false`、`1/0`、`yes/no`、`on/off`。

示例：

```bash
READER_PORT=18080 READER_STORAGE_PATH=/data/reader READER_SECURE=true \
READER_SECURE_KEY=secret ./novel-server
```

## 数据目录

默认 `storage/`：

```
storage/
├── users.json                    # 用户与鉴权信息
├── data/
│   └── <namespace>/              # 每个用户一个命名空间（default 为单用户）
│       ├── bookshelf.json        # 书架
│       ├── bookSource.json       # 书源
│       ├── rssSources.json       # RSS 源
│       ├── bookGroups.json       # 分组
│       ├── bookProgress.json     # 阅读进度
│       └── <书名>_<作者>/         # 单本书数据（章节缓存、封面等）
├── assets/<namespace>/           # 封面、上传的本地书
└── cache/                        # 临时缓存
```

> `storage/` 已在 `.gitignore` 中排除，不会提交到仓库。

## 性能与运行时

- **内嵌前端与预压缩**：`web/dist` 通过 `go:embed` 打包进二进制；`./build.sh` 会额外生成
  `.js` / `.css` / `.html` 等静态资源的 `.gz` 文件，服务端在客户端支持
  `Accept-Encoding: gzip` 时直接返回压缩内容。
- **有界缓存**：章节列表、章节正文、书籍详情、替换规则、CSS 选择器、正则表达式和
  JS 规则编译结果均使用有容量上限的 LRU 缓存，避免长时间运行后内存无界增长。
  章节正文按“每书一个 `cache.json`”存储，兼容旧的逐章 `.txt` 缓存。
- **HTTP 优化**：API 的 JSON 响应启用 gzip；带哈希的静态资源返回
  `Cache-Control: immutable`，`index.html` 不缓存；SSE 流保持非压缩以即时推送。
- **优雅关闭**：收到 `SIGINT` / `SIGTERM` 后停止接收新请求，最多等待 10 秒让进行中的
  请求完成；服务端不设置 `WriteTimeout`，以支持 SSE 长连接。
- **后台调度**：书架更新在不同命名空间之间并行执行，服务端总并发上限为 8，
  并跳过 5 分钟内已检查过的书籍。

## 接口概览

所有业务接口以 `/reader3/` 为前缀，统一返回：

```json
{ "isSuccess": true, "errorMsg": "", "data": {} }
```

主要接口：

- 书源：`getBookSources` / `saveBookSource` / `saveBookSources` / `deleteBookSource`
- 搜索：`searchBook` / `searchBookMulti` / `searchBookMultiSSE`
- 书籍：`getBookInfo` / `getChapterList` / `getBookContent` / `saveBook` / `getBookshelf`
- 缓存与导出：`cacheBookSSE` / `exportBook` / `deleteBookCache`
- 本地书：`importBookPreview` / `getLocalStoreFileList` / `uploadFileToLocalStore`
- RSS：`getRssSources` / `getRssArticles` / `getRssContent`
- 用户：`login` / `logout` / `getUserInfo` / `getUserConfig`
- WebDAV：`getWebdavFileList` / `backupToWebdav` / `restoreFromWebdav`
- 管理：`getSystemInfo` / `getUserList` / `addUser`

## 测试

```bash
go vet ./...
go test ./...
```

规则引擎的兼容性测试位于 `internal/rule/`，覆盖：

- JS 规则返回对象数组后按字段取值（`chapterName: "title"`）
- JSON 响应体下普通规则自动按 JsonPath 解析
- CSS 索引规则：`!0:1`（排除）、`.0:2`（选取）、`[!0:3]`、`[0,2]`
- 以数字结尾的标签/类名（如 `tag.h2@text`）不被误判为索引
- 字符串结果按行拆分（`getStringList`）

## 说明

- 本项目仅实现服务端，前端完全沿用原项目，未做任何修改。
- 书源规则、数据格式与 Legado / 原项目保持一致，可直接导入原有书源备份。
- 目录与正文缓存在 `storage/data/<namespace>/` 下，修改书源规则后需带 `refresh=1` 重新拉取。
