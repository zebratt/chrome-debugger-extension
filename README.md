# chrome-connector

chrome-connector 让本机 agent 通过公开的 CLI、MCP 或 Unix socket 接口操作当前用户已经运行的 Google Chrome。Chrome 扩展使用 Native Messaging 连接一个小型 Go 宿主；agent 有请求时才启动 broker，空闲后退出。无需以远程调试参数重启 Chrome。

首版支持 macOS Chrome。当前登录用户下的进程可直接访问连接器，因此也可能读取已登录网页；跨系统用户通过私有 socket 目录和 UID 检查隔离。CDP 命令默认关闭，开启后仍限于 Chrome 扩展 `debugger` API 支持的协议域。

## 快速开始

1. 按 [macOS 安装说明](docs/install-macos.md)构建、加载未打包扩展、注册宿主。
2. 运行 `./bin/chrome-connector doctor`，确认配置文件在线。
3. 运行 `./bin/chrome-connector browser list`，取得 `profileId`，再读取标签页。通用入口 `call METHOD [PARAMS_JSON]` 仍可直接调用 JSON-RPC 方法。
4. 让其他 agent 使用仓库中的 [chrome-connector skill](skills/chrome-connector/SKILL.md)，或将安装后的 `current` 二进制配置成 stdio MCP 服务，参数为 `mcp`。

公开方法、请求字段、占用权和错误语义见 [接口合同](docs/protocol.md)；机器可读的基础结构见 [JSON Schema](protocol/schema.json)。设计与首轮评审记录位于 [方案目录](docs/repeatedly-discussed-plans/active/2026-09-29-chrome-connector/)。
当前组件与运行边界见 [实现架构](docs/architecture/current.md)。

## 自动维护标签页租约

MCP 的 `tab_claim` / `tab_open` 成功后，调用端自动续租，模型读取页面或思考期间无需手动 `tab_renew`。显式 `tab_release` 停止维护；MCP 会话结束、输入/输出错误或 SIGINT/SIGTERM 时释放本会话申请的租约。若 MCP 连接继续用于其他任务，当前任务结束时仍应调用 `tab_release`。

`browser.run` 在执行和等待期间自动续租，结束或取消时释放本次申请的租约；传入的外部 leaseToken 只借用，不释放。单次低级 CLI / socket 调用仍需显式 `claim/renew/release`，跨多条独立 CLI 命令不提供后台保活。崩溃或 SIGKILL 时由 broker 的 30 秒有效期兜底。

## 开发验证

```bash
cd extension && npm install && cd ..
make test
make check
make build
```

租约生命周期进程验收：`python3 tests/lease_lifecycle.py`。使用隔离 broker 和测试标签页标识，无需连接 Chrome；默认空闲 35 秒后验证原令牌仍可续租，并验证 EOF、SIGINT/SIGTERM、输出管道断开后的立即释放。

浏览器交互验收使用 `tests/fixtures/index.html` 的本地页面。项目不依赖 ChatGPT 扩展的代码、私有协议或服务。

## 连续浏览器操作（0.4.0）

`browser.run` / MCP `browser_run` 支持明确的 `sequence` 步骤清单，以及确定性的 search / navigate / fill。执行器在本地连续执行，每步重新观察目标，保留失效恢复、结果验证与新标签页接管，无需模型服务或 API 密钥。

见 [使用说明](docs/browser-workflows.md)。测试入口为 `tests/sequence_workflows.py`。
