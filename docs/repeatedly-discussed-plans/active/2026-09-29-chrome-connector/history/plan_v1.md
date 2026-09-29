# chrome-connector 技术方案（初版）

## 1. 背景与目标

本地 agent 若从 Chrome 外部连接 CDP，通常需要远程调试端口，进而要求以特定参数重启 Chrome 或另起实例。本项目通过独立扩展连接当前 Chrome，让同一 macOS 登录用户下的多个 agent 使用已打开的标签页和登录态。

首版验收目标：安装连接器后，在不重启既有 Chrome、不开启远程调试端口的情况下，两个独立 agent 客户端均可发现标签页、读取页面、操作页面；并发写操作受控；可按配置调用 Chrome 扩展支持的 CDP 命令。CLI、MCP 和 skill 使用同一公开合同。

## 2. 范围与非目标

### 范围

- macOS 上的 Google Chrome；同一用户会话中的多个 Chrome 配置文件。
- 标签页枚举、打开与导航，页面快照和截图，点击、输入、按键、滚动及等待。
- 版本化的本地 JSON-RPC 接口，以及 CLI 和 MCP 适配层。
- 可配置开启的 `cdp.send`，仅透传 `chrome.debugger` 支持的 CDP 协议域。
- 可复用的 agent skill、安装检查、断线恢复和浏览器交互验收。

### 非目标

- 首版不支持 Windows、Linux、Edge、Brave、Firefox 或远程机器接入。
- 不承诺完整 CDP 覆盖；不提供远程调试 TCP 端口。
- 不读取或复制 ChatGPT 扩展私有协议、会话与数据。
- 不把 agent 的业务确认流程内置为浏览器连接器的统一决策器；调用方仍负责其自身的操作授权。

## 3. 整体架构

```text
本地 agent ── CLI / MCP / JSON-RPC ── Unix socket ── 按需 broker
                                                    ▲
                                      每配置文件宿主主动连接
                                                    │
                                           Native Messaging 小宿主
                                                    │ Chrome 管理的 stdio
                                              Chrome MV3 扩展
                                                    │
                                          tabs / debugger / 页面 API
                                                    │
                                               现有 Chrome 标签页
```

扩展使用 `chrome.runtime.connectNative` 建立与小宿主的长连接。扩展在安装、Chrome 启动和连接断开时重建连接；每个配置文件发送持久的随机 `profileId`。Chrome 启动对应宿主进程，小宿主监视当前用户专属运行目录；broker socket 出现后，宿主主动连接 broker 并登记 `profileId`。小宿主仅维护扩展连接和消息转发，不承担多 agent 调度，也不开放可绕过 broker 的 agent 接口。

agent 的 CLI 或 MCP 适配器请求时，以进程锁保证只启动一个 broker。broker 监听当前用户专属 Unix socket，等待各配置文件宿主登记，完成路由、请求关联、标签页占用权和策略校验。最后一个客户端断开、没有执行中的请求或占用权且达到空闲超时后，broker 退出；小宿主可随 Chrome 继续运行并等待下一次 broker 启动。Chrome 或扩展退出时，宿主断开，broker 将该配置文件标记为离线。首版默认空闲超时建议为 120 秒，可配置。

### 方向比较与选择

| 方向 | 核心思路 | Effort | Risk | 主要取舍 |
| --- | --- | --- | --- | --- |
| 最小方案 | 宿主直接提供本地 socket 与 agent 调度 | S | 中 | 进程少，但宿主随 Chrome 长期承担 broker 职责，不符合已确认的按需 broker 目标 |
| **选定方案** | 小宿主维持 Chrome 连接，broker 由 agent 请求启动并在空闲后退出 | M | 中 | 多一个进程边界，换取清晰生命周期与多配置文件路由 |
| 零空闲进程 | 扩展轮询本地服务或由用户手动唤醒 | L | 高 | 无空闲宿主，但启动延迟、唤醒和故障恢复更复杂 |

## 4. 模块设计

### 4.1 Chrome 扩展

**目标：** 在现有 Chrome 配置文件中发现标签页、执行页面操作、按需附着调试器，并通过 Native Messaging 响应宿主请求。

**关键步骤：** 使用 Manifest V3 service worker 管理 Native Messaging 连接；以 `chrome.tabs` 获取标签页；通过 `chrome.debugger` 获取可访问性或 DOM 信息、截图和执行受支持 CDP 命令。仅在请求涉及调试能力时附着目标，空闲时释放。扩展维持 `profileId`，为响应附带 `requestId` 与明确错误码。

**技术约束：** Chrome 扩展的 `debugger` 权限可访问的协议域受 Chrome 限制。打开同一标签页的 DevTools 或其他调试器占用目标时，返回占用或断开错误，不主动抢占。扩展不收集或存储网页正文、Cookie 或截图副本。

**验收标准：** 能在已运行的 Chrome 中连接，按 `profileId` 列出标签页；调试器目标冲突有明确错误；扩展重载后可恢复连接。

### 4.2 Native Messaging 小宿主

**目标：** 桥接 Chrome 管理的标准输入输出与本地 broker，不向 agent 暴露 Chrome 的私有扩展通道。

**关键步骤：** 安装清单只允许本项目的固定扩展 ID；按 Chrome Native Messaging 帧格式读取与写入消息；完成扩展握手，监视用户私有运行目录中的 broker socket，出现时连接并登记 `profileId`。宿主进程与扩展连接同生命周期，broker 退出后等待其下次启动。

**技术约束：** Native Messaging 宿主到扩展的单条消息上限为 1 MiB，反向为 64 MiB；外部请求需设更小的明确上限，大型结果以受控文件或分块方式交付。`stdout` 仅输出协议帧，诊断日志写入 `stderr`。

**验收标准：** 错误帧、超限消息及断线均不会污染后续通信；多个 Chrome 配置文件可分别登记；宿主不能向 agent 提供绕过 broker 策略与占用权的通道。

### 4.3 按需 broker

**目标：** 为同一 macOS 用户下的 agent 提供共享的公开接口和并发控制。

**关键步骤：** 首个客户端通过单实例锁启动 broker；broker 绑定用户私有 Unix socket，检查对端 UID，等待宿主登记并按 `profileId` 路由。读请求允许并发；每个标签页的写操作使用短期 `leaseToken`，续租、显式释放或到期清理；CLI 可跨命令携带该令牌。非幂等页面操作在连接不明时不自动重试。

**技术约束：** 运行目录权限为 `0700`、socket 为 `0600`；本机同一用户无需客户端令牌。broker 日志记录命令、结果码和网站 origin，不记录页面正文、完整 URL 查询参数或输入文本。

**验收标准：** 两个 agent 能同时读取；同一标签页写入冲突返回 `TAB_BUSY`；broker 空闲退出后，下一请求自动唤起并恢复配置文件发现。

### 4.4 CLI、MCP 与 skill

**目标：** 让支持 MCP 的 agent 和只会执行命令的 agent 使用同一接口。

**关键步骤：** CLI 与 MCP 服务都作为 broker 客户端，不各自实现浏览器控制；共享公开 schema 和错误码。skill 说明安装、`doctor` 检查、标准浏览流程、快照过期处理、写入占用权、CDP 子集、失败恢复和浏览器操作边界。

**验收标准：** 两类客户端对同一请求返回等价结果；仅按 skill 文档即可完成发现、读取、操作、验证的完整示例。

## 5. 数据结构与 API 设计

本地公开协议使用 JSON-RPC 2.0，定义 `protocolVersion`、`requestId`、`profileId`、`tabId`、`snapshotId` 和结构化错误。Native Messaging 传输同一语义的消息，但 Chrome 的帧格式只用于扩展与宿主之间。schema 与兼容策略公开；同一主版本内只追加可选字段，破坏性变更提升主版本。

| 方法组 | 方法 | 主要结果 |
| --- | --- | --- |
| 健康 | `system.ping`、`browser.list` | broker 与配置文件连接状态 |
| 标签页 | `tab.list`、`tab.open`、`tab.navigate` | 标签页 ID、标题、URL 与导航结果 |
| 读取 | `tab.snapshot`、`tab.screenshot` | 带版本的节点引用、页面截图 |
| 操作 | `tab.click`、`tab.type`、`tab.key`、`tab.scroll`、`tab.wait` | 操作结果和当前页面版本 |
| 协调 | `tab.claim`、`tab.release` | 短期写入占用权 |
| 调试 | `cdp.send` | 受支持 CDP 命令的响应或明确的不支持错误 |

节点操作必须附带生成该节点引用的 `snapshotId`；页面导航或节点失效时返回 `STALE_SNAPSHOT`，要求调用方重新获取快照。截图由 broker 控制大小与临时文件生命周期；MCP 返回图像内容，CLI 可输出受控临时路径。`cdp.send` 由配置开关控制，broker 和扩展都校验允许的协议域；开启后同一用户的所有客户端均可调用。

统一错误至少包含 `BROWSER_OFFLINE`、`TAB_GONE`、`TAB_BUSY`、`TARGET_DETACHED`、`STALE_SNAPSHOT`、`UNSUPPORTED_CDP_DOMAIN`、`PAYLOAD_TOO_LARGE` 和 `POLICY_DENIED`。错误附带可重试性，但点击、输入等非幂等动作不由适配器自动重试。

## 6. 非功能需求

- **权限边界：** 当前 macOS 用户的进程可直接访问，这是已确认的信任模型。跨 UID 连接拒绝；浏览器内部页面、`file://` 与无痕标签页默认不开放，网站允许/阻止规则在请求执行时再次校验。
- **性能：** 空闲 broker 自动退出；小宿主保持最少状态。页面快照和截图设置大小与耗时上限，慢操作可取消，broker 不因一个标签页阻塞所有配置文件。
- **可靠性：** 扩展、宿主和 broker 分别检测断线；恢复时重新握手和登记，失效或到期的标签页占用权及时清理。断线期间不假定页面操作已失败或成功。
- **兼容性：** Chrome 扩展 ID 固定，Native Messaging 清单使用精确 `allowed_origins`；协议版本协商，CLI、MCP 与扩展版本不兼容时明确报错。
- **可观察性：** `doctor` 检查扩展、宿主清单、宿主发现、broker 启停、目标附着能力；日志避免保留敏感页面数据。

## 7. 安装、验证与回滚

安装程序部署固定 ID 的扩展、Native Messaging 宿主清单、单文件 CLI/broker 与 skill；安装后 `doctor` 验证连接，不要求重启 Chrome。实际安装路径和分发方式在实施前按本机验证确定。

验证覆盖：在 Chrome PID 不变且无远程调试端口的条件下，两个独立客户端发现并读取同一配置文件；对同一标签页的写入互斥；多配置文件正确路由；broker 空闲退出和重新唤起；扩展重载、DevTools 占用、宿主断开及超限消息；`cdp.send` 对支持与不支持协议域分别给出预期结果。浏览器交互验收使用非敏感测试页面。

回滚时停止 broker 与小宿主，移除扩展、宿主清单和运行目录中的 socket；不迁移或修改 Chrome 用户配置文件中的 Cookie 与浏览历史。回滚前保留诊断日志供故障分析，再按用户意愿清理。

## 8. 风险与待确认项

1. **同用户无认证：** 同一用户下的其他进程可访问登录态页面；这是已接受的授权范围，安装界面和文档须明确说明。
2. **CDP 能力差异：** `chrome.debugger` 对协议域有限制；公开 API 与 skill 必须展示实际支持范围，不以“完整 CDP”宣传。
3. **调试器竞争：** DevTools 或其他扩展可能占用同一标签页；返回 `TAB_BUSY` 或 `TARGET_DETACHED`，不静默抢占。
4. **连接生命周期：** `connectNative` 会维持 service worker；小宿主可随 Chrome 存活，broker 才是按需进程。安装验收须测量 Chrome 启动、扩展重载与休眠唤醒后的恢复。
5. **低影响待定参数：** 默认空闲超时、截图上限和分发形式可在实施计划中定值，不改变模块边界。

## 9. 相关决策与资料

- 方向选择：用户确认 macOS Chrome 首版、当前用户无逐客户端授权、页面操作加按需 CDP 子集，以及小宿主加按需 broker。
- [Chrome Native Messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging)
- [Chrome 扩展 service worker 生命周期](https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle)
- [Chrome debugger API](https://developer.chrome.com/docs/extensions/reference/api/debugger)
