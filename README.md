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
| `net/http` (Go 1.22+ 路由) | HTTP 服务 |
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
│   ├── config/            # 配置加载（application.yml + 环境变量）
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
- Node.js（仅用于构建前端）

### 2. 构建前端

```bash
cd web
npm install
npm run build
```

构建产物位于 `web/dist`，后端会自动识别并作为静态资源目录。

### 3. 编译并运行

```bash
go build -o novel-server ./cmd/server
./novel-server
```

默认监听 `:8080`，浏览器访问 `http://localhost:8080/`。

开发调试也可直接运行：

```bash
go run ./cmd/server
```

## 配置

程序会按顺序查找工作目录下的 `application.yml`、`application.yaml`、
`server/conf/application.properties`，结构与原项目一致：

```yaml
reader:
  app:
    storagePath: storage      # 数据目录
    secure: false             # 是否多用户模式
    secureKey: ""             # 管理密码
    inviteCode: ""            # 邀请码
    userLimit: 50
    userBookLimit: 200
    cacheChapterContent: true
  server:
    port: 8080
```

也可使用环境变量覆盖：

| 环境变量 | 说明 |
| --- | --- |
| `READER_PORT` | 监听端口 |
| `READER_STORAGE_PATH` | 数据目录 |
| `READER_SECURE` | 是否启用多用户（`true` / `1`） |
| `READER_SECURE_KEY` | 管理密码 |
| `READER_INVITE_CODE` | 邀请码 |

示例：

```bash
READER_PORT=18080 READER_STORAGE_PATH=/data/reader ./novel-server
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
