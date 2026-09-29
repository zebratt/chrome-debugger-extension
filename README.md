# chrome-connector

chrome-connector 让本机 agent 通过公开的 CLI、MCP 或 Unix socket 接口操作当前用户已经运行的 Google Chrome。Chrome 扩展使用 Native Messaging 连接一个小型 Go 宿主；agent 有请求时才启动 broker，空闲后退出。无需以远程调试参数重启 Chrome。

首版支持 macOS Chrome。当前登录用户下的进程可直接访问连接器，因此也可能读取已登录网页；跨系统用户通过私有 socket 目录和 UID 检查隔离。CDP 命令默认关闭，开启后仍限于 Chrome 扩展 `debugger` API 支持的协议域。

## 快速开始

1. 按 [macOS 安装说明](docs/install-macos.md)构建、加载未打包扩展、注册宿主。
2. 运行 `./bin/chrome-connector doctor`，确认配置文件在线。
3. 运行 `./bin/chrome-connector call browser.list`，取得 `profileId`，再读取标签页。
4. 让其他 agent 使用仓库中的 [chrome-connector skill](skills/chrome-connector/SKILL.md)，或将安装后的 `current` 二进制配置成 stdio MCP 服务，参数为 `mcp`。

公开方法、请求字段、占用权和错误语义见 [接口合同](docs/protocol.md)；机器可读的基础结构见 [JSON Schema](protocol/schema.json)。设计与首轮评审记录位于 [方案目录](docs/repeatedly-discussed-plans/active/2026-09-29-chrome-connector/)。

## 开发验证

```bash
cd extension && npm install && cd ..
make test
make check
make build
```

浏览器交互验收使用 `tests/fixtures/index.html` 的本地页面。项目不依赖 ChatGPT 扩展的代码、私有协议或服务。
