# Chrome Connector 本地接口 v1.0

Chrome Connector 在 macOS 当前用户的私有 Unix socket 上提供 JSON-RPC 2.0。CLI 和 MCP 适配器自动启动按需 broker；直接连接 socket 的客户端先运行 `chrome-connector broker ensure`。socket 上每行是一条 UTF-8 JSON 消息。扩展与宿主之间使用 Chrome Native Messaging 的长度前缀帧，承载同一请求语义，不对普通 agent 直接开放。

## 生命周期与目标

- `browser.list` 返回 `profileId`、状态、浏览器版本、扩展版本与宿主版本；浏览器版本来自扩展运行环境的 User-Agent，通常只精确到主版本。标签页身份是 `(profileId, tabId)`，所有标签页方法都必须显式给出这两个字段，`tab.list` 只需 `profileId`。
- broker 首次启动后等待宿主登记最多 3 秒，随后返回 `ready` 或 `offline`。宿主可稍后加入。最后一个客户端离开且没有请求或有效占用权时，broker 空闲 120 秒后退出。
- 单次请求的 `params` 最多 512 KiB；超过上限返回 `PAYLOAD_TOO_LARGE`，不会把超限消息送入 Native Messaging 宿主。
- 同一 Chrome 配置文件可以同时被多个 agent 读取。写操作需要该标签页的 `leaseToken`；令牌有效期 30 秒，可续租。`tab.open` 自动返回新标签页的令牌。
- 扩展重载、broker 重启或页面导航会使旧节点引用失效。每个快照最多保留 60 秒，同一标签页最多保留 16 个；过期后重新调用 `tab.snapshot`。broker 重启后还需重新领取标签页占用权。

## 方法

| 方法 | 必要参数 | 结果要点 |
| --- | --- | --- |
| `system.ping` | 无 | 协议版本、发现状态 |
| `system.policy` | 无 | 只读的站点与 CDP 策略 |
| `system.capabilities` | 无 | broker 版本、公开方法、各配置文件的宿主/扩展版本、扩展声明的 CDP 域和当前策略下可尝试的域 |
| `browser.list` | 无 | 在线配置文件 |
| `tab.list` | `profileId` | 可访问的普通网页标签页 |
| `tab.open` | `profileId`, `url` | `tabId`, `leaseToken`, `expiresAt` |
| `tab.info` | `profileId`, `tabId` | 当前标题与 URL；受站点策略约束 |
| `tab.navigate` | `profileId`, `tabId`, `url`, `leaseToken` | 导航结果 |
| `tab.snapshot` | `profileId`, `tabId` | `snapshotId`, `nodes[{nodeRef, role, name}]` |
| `tab.screenshot` | `profileId`, `tabId` | `mimeType`, `dataBase64`, `size`；PNG 最多 8 MiB |
| `tab.claim` | `profileId`, `tabId` | `leaseToken`, `expiresAt` |
| `tab.renew` / `tab.release` | `profileId`, `tabId`, `leaseToken` | 续租 / 释放结果 |
| `tab.click` | `profileId`, `tabId`, `leaseToken`, `snapshotId`, `nodeRef` | 点击结果 |
| `tab.type` | 上述字段及 `text` | 在节点中追加输入文本 |
| `tab.key` | `profileId`, `tabId`, `leaseToken`, `key` | 按键结果；支持 Enter、Tab、Escape、Backspace 和方向键 |
| `tab.scroll` | `profileId`, `tabId`, `leaseToken`, `deltaY` | 滚动顶层页面；嵌套滚动容器未覆盖 |
| `tab.wait` | `profileId`, `tabId`, `text`, 可选 `timeoutMs` | 文本出现后返回 `found: true`；最长 10 秒 |
| `cdp.send` | `profileId`, `tabId`, `leaseToken`, `method`, 可选 `commandParams` | 单次 CDP 命令响应 |

`snapshotId` 和 `nodeRef` 是不透明、短期有效的引用，不要缓存到下一次任务。多个客户端读取同一标签页不会互相覆盖快照。`tab.type` 追加文本，不自动清空原有值。`cdp.send` 每次调用单独附着和释放调试器；它不提供持久 CDP 事件订阅，也不能访问 Chrome 扩展 `debugger` API 不支持的协议域。能力发现中的 CDP 域来自扩展静态支持表，单个命令是否可用仍以该 Chrome 版本的实际调用结果为准。`system.shutdown` 和 `extension.reload` 仅供安装器维护生命周期，不列入普通 agent 的能力发现列表。

## 响应与错误

成功响应使用 JSON-RPC 的 `result`，错误使用数值 `error.code: -32000`，并在 `error.data.kind` 中给出稳定的机器可读类别，`error.data.retryable` 标记是否适合重新观察后重试。

主要错误：

- `BROWSER_OFFLINE`：配置文件宿主未连接；运行 `doctor` 并检查扩展。
- `VERSION_MISMATCH`：扩展与 broker 协议不兼容；更新或重载扩展，再运行 `doctor`。
- `TAB_BUSY` / `LEASE_EXPIRED`：写入占用权冲突或过期；按需续租或重新 `tab.claim`。
- `STALE_SNAPSHOT`：页面或节点引用已变化；重新 `tab.snapshot`。
- `POLICY_DENIED`：站点、页面类型或 CDP 域被本地策略拒绝。
- `UNSUPPORTED_CDP_DOMAIN`：Chrome 扩展调试 API 不支持该协议域。
- `OUTCOME_UNKNOWN`：动作可能已经发出，但确认响应丢失。**先重新读取页面状态，不能自动重复点击、输入或导航。**
- `TARGET_DETACHED` / `CHROME_ERROR`：Chrome 调试目标断开或浏览器 API 出错；查看是否有 DevTools 或其他扩展占用同一标签页。

本机同一用户下的进程无需逐个授权。`cdp.send` 默认关闭，策略文件位于当前用户的 `~/Library/Application Support/chrome-connector/config.json`，权限必须为 `0600`。允许的站点默认为普通 `http`/`https` 页面；`chrome://`、`file://` 和无痕页面不可访问。请求参数不能临时覆盖策略。
