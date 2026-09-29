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

agent 的 CLI 或 MCP 适配器请求时，以进程锁保证只启动一个 broker。直接使用 JSON-RPC socket 的客户端先调用公开的 `chrome-connector broker ensure` 启动命令。broker 监听当前用户专属 Unix socket，等待各配置文件宿主登记，完成路由、请求关联、标签页占用权和策略校验。最后一个客户端断开、没有执行中的请求或占用权且达到空闲超时后，broker 退出；小宿主可随 Chrome 继续运行并等待下一次 broker 启动。Chrome 或扩展退出时，宿主断开，broker 将该配置文件标记为离线。首版默认空闲超时建议为 120 秒，可配置。

| 阶段 | broker 与宿主行为 | 客户端可见结果 |
| --- | --- | --- |
| `STARTING` | CLI/MCP 在单实例锁下启动 broker，等待 socket 就绪，最多 3 秒 | 超时返回 `BROKER_START_FAILED` |
| `DISCOVERING` | broker 已就绪，宿主检测到 socket 后连接并登记；首轮登记等待最多 3 秒 | `system.ping` 返回 `discovering`；`browser.list` 等待至首个登记或时限 |
| `READY` | 至少一个配置文件已登记，迟到的宿主仍可随时加入 | `browser.list` 返回每个 profile 的在线状态；标签页方法按指定 profile 路由 |
| `OFFLINE` | 登记时限内无宿主，或已登记宿主后来断开 | `browser.list` 返回明确的 `offline` 状态；指定离线 profile 的调用返回 `BROWSER_OFFLINE`，不伪装为空标签页 |
| `IDLE_EXIT` | 无客户端、请求与有效占用权达 120 秒，broker 关闭 socket；宿主继续等待 | 下次调用重新启动 broker 并进入 `DISCOVERING` |

宿主通过运行目录变化或短间隔重试发现新 broker；broker 重启后所有宿主重新握手登记。`browser.list` 的首次登记等待不影响后续热接入，`doctor` 可区分未安装扩展、Chrome 未运行和宿主连接失败。

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

**关键步骤：** 安装清单的 `allowed_origins` 只写入本机实际加载的扩展 ID；按 Chrome Native Messaging 帧格式读取与写入消息；完成扩展握手，监视用户私有运行目录中的 broker socket，出现时连接并登记 `profileId`。宿主进程与扩展连接同生命周期，broker 退出后等待其下次启动。

**技术约束：** Native Messaging 宿主到扩展的单条消息上限为 1 MiB，反向为 64 MiB；外部请求设更小的明确上限，超限结果返回 `PAYLOAD_TOO_LARGE`。`stdout` 仅输出协议帧，诊断日志写入 `stderr`。

**验收标准：** 错误帧、超限消息及断线均不会污染后续通信；多个 Chrome 配置文件可分别登记；宿主不能向 agent 提供绕过 broker 策略与占用权的通道。

### 4.3 按需 broker

**目标：** 为同一 macOS 用户下的 agent 提供共享的公开接口和并发控制。

**关键步骤：** 首个客户端通过单实例锁启动 broker；broker 绑定用户私有 Unix socket，检查对端 UID，等待宿主登记并按 `profileId` 路由。读请求允许并发；每个标签页的写操作使用短期 `leaseToken`，续租、显式释放或到期清理；CLI 可跨命令携带该令牌。非幂等页面操作在连接不明时不自动重试；已发出但未收到确认时返回 `OUTCOME_UNKNOWN`。

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
| 健康 | `system.ping`、`system.capabilities`、`system.policy`、`browser.list` | broker、配置文件、CDP 能力及只读策略状态 |
| 标签页 | `tab.list`、`tab.open`、`tab.navigate` | 标签页 ID、标题、URL 与导航结果 |
| 读取 | `tab.snapshot`、`tab.screenshot` | 带版本的节点引用、页面截图 |
| 操作 | `tab.click`、`tab.type`、`tab.key`、`tab.scroll`、`tab.wait` | 操作结果和当前页面版本 |
| 协调 | `tab.claim`、`tab.renew`、`tab.release` | 短期写入占用权 |
| 调试 | `cdp.send` | 受支持 CDP 命令的响应或明确的不支持错误 |

### 5.1 最小请求与响应合同

所有针对标签页的方法都要求显式传入 `profileId` 与 `tabId`，不按“当前窗口”或“唯一在线 profile”猜测目标。`tabId` 只保证在一个 `profileId` 内唯一；完整目标标识是两者的组合。`browser.list` 在没有在线 profile 时仍返回状态，不把“浏览器离线”解释为空列表。

| 方法 | 最小请求 | 最小响应及零/多 profile 规则 |
| --- | --- | --- |
| `browser.list` | 无 | `discoveryState`、`profiles[{profileId, status, browserVersion}]`；未登记时返回 `offline`，多个 profile 全部列出 |
| `tab.list` | `profileId` | `tabs[{tabId, title, url}]`；未给 profile 报参数错误，离线报 `BROWSER_OFFLINE` |
| `tab.open` | `profileId, url` | `profileId, tabId, leaseToken, expiresAt`；新标签页自动给调用方 30 秒占用权 |
| `tab.navigate` | `profileId, tabId, url, leaseToken` | `profileId, tabId, navigationId`；未持有占用权报 `TAB_BUSY` |

`tab.snapshot` 返回 `snapshotId`、文档标识、`nodes[{nodeRef, role, name, bounds?}]`；`nodeRef` 是不暴露 CDP 内部 ID 的不透明值，只能与对应 `snapshotId` 一起使用。顶层导航或节点脱离时返回 `STALE_SNAPSHOT`，调用方重新获取快照。`tab.screenshot` 在本地协议中返回 `mimeType`、尺寸及 `dataBase64`；首版默认上限 8 MiB，超限返回 `PAYLOAD_TOO_LARGE`。MCP 转成图像内容，CLI 仅在调用方指定 `--output` 时写入权限为 `0600` 的目标文件，默认不保留截图临时文件。

节点点击的最小请求为 `{profileId, tabId, leaseToken, snapshotId, nodeRef}`；输入在此基础上增加 `text`。按键与滚动要求显式标签页目标和占用权，坐标操作须使用最近一次截图的视口尺寸并在视口变化后重新观察。

`system.capabilities` 返回协议版本、各组件版本、可用页面方法、Chrome 实际支持的 CDP 域和配置允许的 CDP 域；`system.policy` 只读返回站点允许/阻止规则及 CDP 开关状态。`cdp.send` 要求 `profileId, tabId, leaseToken, method, params`，方法所在域必须同时受 Chrome 和本地策略支持。

### 5.2 标签页占用权与结果未知

`tab.claim(profileId, tabId)` 返回不可预测的 `leaseToken` 和 `expiresAt`，首版 TTL 为 30 秒；同一令牌可 `tab.renew`，重复 `tab.release` 不报错。其他令牌在到期前获取同一标签页时返回 `TAB_BUSY`。`tab.navigate`、`tab.click`、`tab.type`、`tab.key`、`tab.scroll` 和 `cdp.send` 均强制携带有效占用权；读取方法不需要。CLI 可把令牌显式传给后续命令，MCP 适配器可在一次会话中持有并续租。broker 重启后全部令牌立即失效；客户端断开但未释放的令牌到期清理，不由新连接自动继承。

动作发送到扩展前失败时返回可重试错误。动作已发送、响应丢失或目标调试器在执行中断开时，返回 `OUTCOME_UNKNOWN`，附 `requestId` 与目标信息；调用方必须重新连接、重新获取页面快照并核实页面状态，再决定下一动作，不自动重复点击或输入。宿主连接失效时撤销该 profile 的占用权；仅目标调试器失效时撤销对应标签页的占用权。恢复后需重新 `tab.claim`。

统一错误至少包含 `BROKER_START_FAILED`、`BROWSER_OFFLINE`、`TAB_GONE`、`TAB_BUSY`、`LEASE_EXPIRED`、`TARGET_DETACHED`、`OUTCOME_UNKNOWN`、`STALE_SNAPSHOT`、`UNSUPPORTED_CDP_DOMAIN`、`PAYLOAD_TOO_LARGE`、`POLICY_DENIED` 和 `VERSION_MISMATCH`。错误附带可重试性；非幂等动作不由适配器自动重试。

## 6. 非功能需求

- **权限边界：** 当前 macOS 用户的进程可直接访问，这是已确认的信任模型。跨 UID 连接拒绝；浏览器内部页面、`file://` 与无痕标签页默认不开放。默认允许普通 `http`/`https` 网站，用户可设置站点允许与阻止规则，阻止规则优先。broker 读取仅当前用户可写的策略文件并在每次请求执行时校验目标站点，扩展再次检查当前页面 URL 与受限 scheme。RPC 只提供策略查询，不允许请求参数临时覆盖策略。
- **CDP 默认值：** `cdp.send` 默认关闭，允许的协议域默认为空；用户修改本地策略文件后才开放指定域，任何同一 UID 客户端届时均可调用。broker 检查配置允许域，扩展检查 Chrome 实际支持域。
- **性能：** 空闲 broker 自动退出；小宿主保持最少状态。页面快照和截图设置大小与耗时上限，慢操作可取消，broker 不因一个标签页阻塞所有配置文件。
- **可靠性：** 扩展、宿主和 broker 分别检测断线；恢复时重新握手和登记，失效或到期的标签页占用权及时清理。断线后已发出动作使用 `OUTCOME_UNKNOWN` 恢复流程。
- **兼容性：** 首版以本机加载未打包扩展的实际 ID 生成 Native Messaging 清单，`allowed_origins` 精确匹配；协议版本协商，CLI、MCP 与扩展版本不兼容时明确报错。
- **可观察性：** `doctor` 检查扩展、宿主清单、宿主发现、broker 启停、目标附着能力；日志避免保留敏感页面数据。

Go 可执行文件同时包含 CLI、MCP、broker 和 Native Messaging 宿主模式。`1.x` 协议只追加可选字段；broker 接受当前及前一个 minor 版本的宿主、扩展和外部客户端，主版本不同则返回 `VERSION_MISMATCH`。

| 组件组合 | 支持规则 | 不兼容时的 `doctor` 指引 |
| --- | --- | --- |
| CLI/MCP ↔ broker | 相同主版本，当前或前一个 minor | 更新本地可执行文件并重新连接 broker |
| broker ↔ 小宿主 | 相同主版本，当前或前一个 minor | 结束旧宿主并重载扩展，使 Chrome 启动新宿主 |
| 小宿主 ↔ 扩展 | 相同主版本，当前或前一个 minor | 更新并重载未打包扩展，不要求重启 Chrome |

同主版本内可以先升级可执行文件，再更新扩展；旧宿主可在兼容窗口内服务，更新完成后重载扩展切换新宿主。跨主版本先提供兼容新旧合同的桥接版本，或在 `doctor` 中明确提示停机升级，不能静默降级。扩展重新安装后 `profileId` 可能变化，外部客户端应重新执行 `browser.list`，不得把它作为永久用户标识。

默认不持久化页面内容、输入内容或截图；错误信息只含错误码、目标 ID 和经过脱敏的网站 origin。诊断日志默认仅输出到本地 `stderr`；如开启持久日志，按大小轮转且保留不超过 7 天，回滚时列出供用户清理。

## 7. 安装、验证与回滚

首版采用**本机开发安装**：安装器把未打包扩展复制到当前用户 Application Support 下的稳定路径，把单文件可执行程序放入版本化 `releases/<version>/` 目录，并以稳定入口指向当前版本。用户在现有 Chrome 中从该稳定路径加载扩展；安装器读取已加载扩展的实际 ID（无法可靠自动发现时由用户从扩展页面提供），据此生成用户级 Native Messaging 清单与精确的 `allowed_origins`，并安装 CLI、MCP 配置示例和 skill。不使用 Chrome Web Store 发布作为首版前提。

升级时先把新版本放入独立目录并验证，再原子切换稳定入口；扩展内容在稳定路径更新后重载扩展。`doctor` 对比实际扩展 ID、清单 origin、宿主入口及组件协议版本；扩展 ID 变化时重写清单并要求重新发现 profile。回滚时切回上一版本的可执行文件和扩展内容备份，重新生成清单并重载扩展。正常安装与升级均以“不重启整个 Chrome”为验收目标；首次本机联调须验证 Chrome 运行中注册宿主与重载扩展的行为，若不成立则回到方案评审，不在实现中悄悄放宽目标。

验证覆盖：在 Chrome PID 不变且无远程调试端口的条件下，两个独立客户端发现并读取同一配置文件；对同一标签页的写入互斥；多配置文件正确路由；broker 空闲退出和重新唤起；扩展重载、DevTools 占用、宿主断开及超限消息；`cdp.send` 对支持与不支持协议域分别给出预期结果。浏览器交互验收使用非敏感测试页面。

回滚时停止 broker 与小宿主，移除扩展、宿主清单和运行目录中的 socket；不迁移或修改 Chrome 用户配置文件中的 Cookie 与浏览历史。若用户启用了持久诊断日志，回滚程序先列出文件再提供清理选项。

## 8. 风险与待确认项

1. **同用户无认证：** 同一用户下的其他进程可访问登录态页面；这是已接受的授权范围，安装界面和文档须明确说明。
2. **CDP 能力差异：** `chrome.debugger` 对协议域有限制；公开 API 与 skill 必须展示实际支持范围，不以“完整 CDP”宣传。
3. **调试器竞争：** DevTools 或其他扩展可能占用同一标签页；返回 `TAB_BUSY` 或 `TARGET_DETACHED`，不静默抢占。
4. **连接生命周期：** `connectNative` 会维持 service worker；小宿主可随 Chrome 存活，broker 才是按需进程。安装验收须测量 Chrome 启动、扩展重载与休眠唤醒后的恢复。
5. **安装验证：** 未打包扩展和用户级宿主清单在已运行 Chrome 中生效的时序需要首个实施任务实测；失败会推翻“不重启 Chrome”的验收目标，须回到方案评审。
6. **低影响待定参数：** 可执行程序的具体安装目录名可在实施计划中定值，不改变模块边界。

## 9. 相关决策与资料

- 方向选择：用户确认 macOS Chrome 首版、当前用户无逐客户端授权、页面操作加按需 CDP 子集、小宿主加按需 broker，以及本机加载未打包扩展的首版交付方式。
- [ADR-0001：本地浏览器 agent 的公开接口与信任边界](../../../decisions/0001-local-browser-agent-contract.md)
- [Chrome Native Messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging)
- [Chrome 扩展 service worker 生命周期](https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle)
- [Chrome debugger API](https://developer.chrome.com/docs/extensions/reference/api/debugger)
