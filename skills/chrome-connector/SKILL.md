---
name: chrome-connector
description: 当 agent 需要在 macOS 复用当前用户正在运行的 Chrome 标签页，读取页面、截图、交互或使用受控 CDP 子集时使用。适用于本地 CLI 或 MCP；不用于远程浏览器或要求完整 CDP 端口的任务。
---

# Chrome Connector

使用本项目公开的 CLI 或 MCP 接口操作当前 Chrome。先运行 `chrome-connector doctor`；若命令不在 `PATH`，使用项目 `bin/chrome-connector` 或安装后的 `~/Library/Application Support/chrome-connector/current`。`doctor` 未显示在线配置文件时，先按 [安装说明](../../docs/install-macos.md)处理连接，再操作网页。

## 标准流程

1. 调用 `browser.list`，取得明确的 `profileId`；用 `tab.list` 选标签页，或用 `tab.open` 新建标签页。不要猜测“当前标签页”。
2. 用 `tab.snapshot` 获取 `snapshotId` 和节点的 `nodeRef`；截图用 `tab.screenshot`。`snapshotId` 与 `nodeRef` 只在当前页面状态下使用。
3. 写操作前通过 `tab.claim` 获取 `leaseToken`；`tab.open` 已返回新标签页的令牌。将令牌传给 `tab.click`、`tab.type`、`tab.key`、`tab.scroll` 或 `tab.navigate`，结束后 `tab.release`。令牌 30 秒后过期，可调用 `tab.renew`。
4. 操作后重新获取快照或使用 `tab.wait` 验证页面结果。遇到 `STALE_SNAPSHOT` 时重新获取节点引用；遇到 `TAB_BUSY` 时不要抢占其他 agent。

CLI 格式为 `chrome-connector call METHOD 'PARAMS_JSON'`。例如：

```bash
chrome-connector call browser.list
chrome-connector call tab.list '{"profileId":"<PROFILE_ID>"}'
chrome-connector call tab.snapshot '{"profileId":"<PROFILE_ID>","tabId":123}'
```

也可用 `chrome-connector browser list`、`chrome-connector tab snapshot 'PARAMS_JSON'` 等专用命令。截图要保存时使用 `chrome-connector tab screenshot 'PARAMS_JSON' --output /absolute/path.png`；文件以 `0600` 创建，不会覆盖已有文件，命令输出的 JSON 不再包含图像 Base64。

支持 MCP 的 agent 将安装后的 `current` 可执行文件配置为 stdio MCP 服务，参数为 `mcp`；MCP 工具名与协议方法对应，例如 `browser_list`、`tab_snapshot`、`tab_click`。具体字段见[接口合同](../../docs/protocol.md)。

## 操作边界

- `OUTCOME_UNKNOWN` 表示动作可能已执行但确认丢失。先重新观察页面，不自动重复点击、输入、导航或 CDP 命令。
- `cdp.send` 默认关闭。需要时先查 `system.capabilities` 和 `system.policy`；它只支持 Chrome 扩展调试 API 的部分协议域，单次调用不会持续订阅 CDP 事件。
- `tab.type` 是追加输入；`tab.scroll` 滚动顶层页面。`tab.key` 目前支持 Enter、Tab、Escape、Backspace 和方向键。
- 当前 macOS 用户下的本地进程无需逐个授权，可能读取已登录网页。网页上的发布、上传、付款、权限变更等操作仍遵守发起任务的用户授权与 agent 自身规则；本连接器不授予额外操作许可。

连接故障先运行 `doctor`。`BROWSER_OFFLINE` 检查扩展是否启用并重载；`VERSION_MISMATCH` 更新或重载扩展；`TARGET_DETACHED` 或浏览器提示已有调试器时，检查同一标签页是否被 DevTools 或其他扩展占用。
