# Chrome Connector 实现现状

## 组件与请求路径

```text
agent CLI / MCP / JSON-RPC socket 客户端
            │ 按需启动；私有 Unix socket
            ▼
        Go broker ── 读取本地策略、管理标签页占用权
            │ 每行一条 JSON 消息
            ▼
    Go Native Messaging 宿主 ── 长度前缀帧适配
            │ Chrome connectNative
            ▼
      MV3 扩展 service worker
            │ tabs / debugger API
            ▼
       当前用户的 Chrome 标签页
```

同一 Go 可执行文件通过子命令运行 CLI、MCP、broker 或 Native Messaging 宿主。宿主连接随扩展保持；CLI 或 MCP 客户端按需唤起 broker，无活跃客户端及占用权满 120 秒后退出。宿主不断尝试发现 broker socket，broker 重启后重新登记。安装器更新时先停止旧 broker，再启动新版本并请求扩展重载；早期不支持重载 RPC 的扩展需在 `chrome://extensions` 手动重载一次。

## 边界与状态

- 运行目录位于当前用户的系统临时目录 `chrome-connector` 子目录，权限为 `0700`；socket 为 `0600`。broker 检查 Unix socket 对端 UID。当前登录用户下的进程共享访问，无单独 agent 授权。
- 安装目录位于当前用户的 `~/Library/Application Support/chrome-connector`。`current` 和 `previous` 指向内容哈希版本；Chrome 的 Native Messaging 清单只允许安装时输入的实际扩展 ID。扩展从仓库 `extension/dist` 加载。
- `browser.list` 返回 `discovering`、`ready`、`offline` 或 `version_mismatch`。同一用户可以有多个 Chrome 配置文件宿主；所有标签页 RPC 要求显式 `profileId`，不会按前台窗口猜测目标。
- broker 负责站点及 CDP 策略、标签页 30 秒占用权和请求路由；扩展在执行前再次检查页面 URL、无痕状态和 CDP 域。`cdp.send` 默认关闭。页面内容、输入和截图不会持久化到服务端。
- 扩展按标签页串行使用 `chrome.debugger`，读取间也释放调试器。每个标签页最多保留 16 个快照，每个快照保留 60 秒。客户端之间的快照互不覆盖。动作已发送但响应丢失时 broker 返回 `OUTCOME_UNKNOWN`，客户端重新观察后自行决定下一步。
- MCP 与 CLI 复用同一个 broker RPC 合同。MCP 截图结果是 `image/png`；CLI 可用 `--output` 将图片写到不覆盖已有文件的私有路径。

接口字段和错误语义见 [协议](../protocol.md)，本机安装与回滚见 [安装说明](../install-macos.md)。

## 本地工作流执行层（0.4.0）

CLI/MCP 在客户端拦截 browser.run；internal/automation 将调用者提供的动作清单或固定工作流组织为基础 RPC。broker 与 Native Messaging 只处理低级接口。工作流不依赖外部模型服务。

sequence 按调用者明确的顺序执行 click/type/select/key/scroll/wait/assert，目标按当前快照的名称、角色、href 唯一匹配。只有明确未执行的 stale 动作才重新定位；执行成功先记录进度，再观察页面。未知结果和未验证输入回交调用者。completedActions/nextAction 保留已完成前缀。

扩展 detailed 快照提供文档身份、当前视口文字、控件状态与下拉选项。guards.ts 保留文档/语义/几何守卫；短时焦点模拟在调试连接结束时恢复。显式 StaticText 目标可用于非标准点击。动作授权由调用者负责，执行器不根据按钮文字猜测用户权限。

新子标签页仍按 opener、allowedOrigins、followNewTabs 和独立租约接管。最终显式检查同时核对 URL、当前视口文字和字段值；没有最终条件只报告清单执行完成。
