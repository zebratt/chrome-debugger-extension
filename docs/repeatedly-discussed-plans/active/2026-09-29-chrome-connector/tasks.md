# chrome-connector 任务清单（L2 编排，完整范围）

> **实施说明（L1/L2，必读）**：本清单由主上下文按任务顺序编排实施，**不使用 `superpowers:subagent-driven-development`（SDD）**——不套用「每任务一独立 subagent + 任务间 review checkpoint」的编排；允许主上下文按需把相互独立、可并行或需隔离的任务派发给 sub agent 执行，但**任务间不插入 review checkpoint**，验收与整合由主上下文负责。仅 L3 档才用 SDD 驱动。

> 用户选择保留完整方案，但采用 L2 编排。实际范围仍跨扩展、本地宿主、broker、客户端与安装器。首项可行性验证失败时停止后续实施，回到 `plan.md` 评审。

## 1. 运行中接入可行性验证

- [x] 1.1 在隔离的临时测试扩展与宿主上验证：现有 Chrome 不重启时加载未打包扩展、注册 Native Messaging 清单并建立 `connectNative` 连接。记录 Chrome PID、启动参数和前后连接状态。（证据：`docs/repeatedly-discussed-plans/active/2026-09-29-chrome-connector/history/spike_r1.md`）
- [x] 1.2 验证扩展重载后宿主能重新连接，以及连接持续时 service worker 与宿主的实际生命周期。失败时记录反例并返回方案评审，不继续功能实现。

## 2. 项目基础与公开合同

- [ ] 2.1 建立 Go 模块、TypeScript MV3 扩展构建、统一构建与检查命令。（Files: `go.mod`, `cmd/chrome-connector/`, `extension/package.json`, `extension/tsconfig.json`, `extension/manifest.json`, `Makefile`）
- [ ] 2.2 定义版本化 JSON-RPC schema、方法参数/结果、错误码、能力发现和兼容规则；明确 Native Messaging 帧与本地 socket 帧的适配。（Files: `protocol/schema.json`, `docs/protocol.md`, `internal/protocol/`, `extension/src/protocol.ts`）
- [ ] 2.3 用协议合同测试覆盖请求 ID、版本不兼容、超限消息、非幂等动作的 `OUTCOME_UNKNOWN` 语义。（Files: `internal/protocol/*_test.go`, `extension/src/*.test.ts`）

## 3. 扩展与小宿主

- [ ] 3.1 实现扩展的 `profileId`、启动/重连、Native Messaging 握手与事件分发；只使用本项目协议。（Files: `extension/src/background.ts`, `extension/src/native.ts`）
- [ ] 3.2 实现 Go Native Messaging 宿主的长度前缀帧、`stdout`/`stderr` 分离、broker socket 发现与重新登记；宿主不开放独立 agent 接口。（Files: `internal/native/`, `internal/runtime/`）
- [ ] 3.3 覆盖宿主崩溃、扩展重载、broker 先/后启动、多配置文件登记和消息上限的测试。（Files: `internal/native/*_test.go`, `extension/src/*.test.ts`）

## 4. 按需 broker 与权限边界

- [ ] 4.1 实现当前用户私有运行目录、socket 权限和对端 UID 检查、单实例锁、按需启动及 120 秒空闲退出。（Files: `internal/broker/`, `internal/runtime/`, `cmd/chrome-connector/`）
- [ ] 4.2 实现 `STARTING`、`DISCOVERING`、`READY`、`OFFLINE` 状态与 3 秒登记等待；broker 重启后宿主重新登记。（Files: `internal/broker/`, `internal/native/`）
- [ ] 4.3 实现用户本地策略读取、站点规则、受限 scheme/无痕限制、CDP 默认关闭及允许域列表；RPC 只读查询，调用时重新校验。（Files: `internal/policy/`, `extension/src/policy.ts`, `docs/configuration.md`）
- [ ] 4.4 实现每标签页 30 秒可续租的占用权、过期清理和 broker 重启失效，验证两个客户端的并发行为。（Files: `internal/broker/lease.go`, `internal/broker/*_test.go`）

## 5. 页面操作与 CDP 子集

- [ ] 5.1 实现配置文件和标签页列举、打开、导航、等待及能力发现。（Files: `extension/src/tabs.ts`, `internal/broker/handlers.go`）
- [ ] 5.2 实现页面快照、节点引用、过期检测、截图与 8 MiB 上限；确认不在服务端持久化页面内容和图像。（Files: `extension/src/page.ts`, `internal/broker/handlers.go`）
- [ ] 5.3 实现点击、输入、按键、滚动和受控 `cdp.send`；对 DevTools/其他调试器竞争返回明确错误，不抢占。（Files: `extension/src/debugger.ts`, `extension/src/actions.ts`）
- [ ] 5.4 验证断线发生在动作发送前/后的不同结果，确保已发送但未确认的动作返回 `OUTCOME_UNKNOWN` 且不自动重试。（Files: `internal/broker/*_test.go`, `extension/src/*.test.ts`）

## 6. agent 适配与使用 skill

- [ ] 6.1 实现 CLI 的 `broker ensure`、`doctor`、浏览器/标签页/页面/占用权命令与 JSON 输出。（Files: `internal/cli/`, `cmd/chrome-connector/`）
- [ ] 6.2 实现 MCP stdio 服务，复用同一 socket 客户端；截图返回图像内容，错误与 CLI 保持一致。（Files: `internal/mcp/`, `internal/client/`）
- [ ] 6.3 编写可供其他 agent 使用的 skill，包含安装、MCP/CLI 配置、标准浏览流程、策略与 CDP 限制、并发及故障恢复。（Files: `skills/chrome-connector/SKILL.md`, `skills/chrome-connector/references/`）

## 7. 本机安装、升级与回滚

- [ ] 7.1 实现本机开发安装：扩展稳定路径、版本化可执行程序、读取实际扩展 ID、生成精确的用户级 Native Messaging 清单；无法可靠检测 ID 时给出人工输入流程。（Files: `internal/install/`, `docs/install-macos.md`）
- [ ] 7.2 实现 `doctor` 的组件版本、扩展 ID、清单路径、宿主连接与协议兼容诊断；验证同主版本新旧 minor 的协商。（Files: `internal/doctor/`, `internal/protocol/`）
- [ ] 7.3 验证扩展重载与版本切换、旧宿主替换、清单重建及回滚；不触碰 Chrome Cookie 和浏览历史。（Files: `internal/install/*_test.go`, `docs/install-macos.md`）

## 8. 验收与交付

- [ ] 8.1 运行 Go、TypeScript、协议与安装器的相关测试；区分代码问题和宿主环境问题，记录命令与结果。
- [ ] 8.2 在非敏感测试页面执行浏览器 E2E：现有 Chrome PID 不变、无远程调试端口、两个独立 agent 客户端读写、多配置文件、broker 空闲退出/重启、DevTools 占用、错误恢复与 CDP 支持范围。（证据：`docs/repeatedly-discussed-plans/active/2026-09-29-chrome-connector/history/e2e.md`）
- [ ] 8.3 更新 README、架构现状、协议与 skill 文档；确认文件路径与实际代码一致，检查 Git 状态并提交可复核成果。（Files: `README.md`, `docs/architecture/`, `docs/protocol.md`, `skills/chrome-connector/SKILL.md`）

## 最终验收

- [ ] 正在运行的 Chrome 在安装和使用过程中无需重启或以 CDP 参数另起实例。
- [ ] 当前用户下两个不同 agent 可通过 MCP 或 CLI 共享浏览器连接；跨 UID 连接被拒绝。
- [ ] broker 仅在有请求时运行，空闲后退出；小宿主随 Chrome 连接保持，下一请求可恢复。
- [ ] 写操作受标签页占用权保护；断线后不会自动重放可能已经执行的动作。
- [ ] `cdp.send` 默认关闭，开启后只调用扩展实际支持且策略允许的协议域。
- [ ] 新 agent 可按仓库 skill 完成安装检查、页面读取、操作和验证。
