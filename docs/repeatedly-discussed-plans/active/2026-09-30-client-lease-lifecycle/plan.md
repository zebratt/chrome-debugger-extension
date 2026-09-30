# 调用端租约自动续租与退出释放实施计划

**目标：** MCP 会话及 browser.run 执行期间自动维护标签页租约，正常结束、取消或终止时释放自身持有的租约，消除模型思考及页面等待期间的自然过期。

**执行方式：** 按 Matt 的要求，由当前开发者直接实现，不使用 TDD、SDD 或子代理；实现后补充回归测试并执行验证。本机安装更新及 Git 提交、推送按后续明确授权执行。

## 设计与边界

- broker 的 30 秒 TTL、标签页互斥及协议字段不变。调用端共享租约管理器，以 `(profileId, tabId, leaseToken)` 标识租约。
- 成功 claim/open 后登记自有租约；成功 renew 后可登记借用租约。每个租约独立续租，间隔最多 10 秒（根据返回的 expiresAt 提前续租）；慢页面请求不阻塞心跳。
- 续租请求超时为 2 秒。续租失败即停止该租约心跳，后续使用相同令牌的写请求返回失败；不自动 claim，不重放动作。读请求仍可用于恢复观察。
- 关闭时先停止并等待心跳，再在统一的 2 秒清理窗口内释放所有自有租约；借用租约只停止维护。显式 release 与关闭幂等，关闭过程中不能有心跳重新建立占用权。
- MCP 生命周期从 stdio 会话开始到 EOF、协议/输出错误或 SIGINT/SIGTERM。MCP 新领取的租约在会话间隔内继续续租；任务提前结束时仍可显式 tab_release。不能从自然语言对话推断业务任务何时结束。
- browser.run 生命周期覆盖执行及等待；仍只释放本次申请的租约，包括跟随新标签页申请的租约。外部传入令牌不由工作流释放。取消/超时保留已完成进度及未知动作语义。
- 单次低级 CLI 和直接 socket 请求继续使用手工租约；短命进程无法跨独立命令保持心跳。SIGKILL/崩溃不能清理时由原有 TTL 兜底。

## Task 1：共享租约管理器与可取消传输

文件：`internal/client/leases.go`、`internal/client/call.go`。

- [x] 实现 `NewLeaseSession(context.Context, func(context.Context, string, any) (protocol.Response, error)) *LeaseSession`，暴露 `Call` 和幂等的 `Close() error`。登记租约、后台续租、失效阻止写请求、停止心跳和有界释放均集中在此。
- [x] 为 Unix socket 调用增加 `CallContext`，让 context 取消能中断等待；已发送动作响应丢失仍返回 OUTCOME_UNKNOWN。原 `Call` 作为兼容入口保留。

接入形式：

```go
leases := client.NewLeaseSession(ctx, call)
response, err := leases.Call(ctx, method, params)
cleanupErr := leases.Close()
```

## Task 2：接入 MCP 与工作流

文件：`internal/mcp/server.go`、`internal/mcp/input.go`、`internal/automation/runner.go`、`internal/automation/recovery.go`、`cmd/chrome-connector/main.go`、`cmd/chrome-connector/workflow.go`。

- [x] MCP 添加 context 入口，保留原 Run 兼容函数；对单条工具调用使用会话租约管理器，EOF/错误时关闭并传播清理失败。
- [x] MCP 读取输入时允许 context 取消；生产入口在信号触发时关闭 stdin，终止读取。工具调用使用取消 context。
- [x] 工作流在 Run 中创建租约会话，替换重复的租约清理记录；保持借用所有权和 RELEASE_FAILED 语义，后台调用不并发修改步骤/指标。
- [x] CLI/MCP 的 SIGINT/SIGTERM 触发取消及清理；心跳直接调用已存在 broker 的 socket，不因清理/续租启动新 broker。

## Task 3：回归验证与文档

文件：`tests/lease_lifecycle.py`、`internal/client/leases_test.go`、`internal/client/call_test.go`、`internal/mcp/server_test.go`、`internal/automation/runner_test.go`、`README.md`、`docs/protocol.md`、`docs/browser-workflows.md`、`skills/chrome-connector/SKILL.md`。

- [x] 以真实 LeaseStore 验证跨多个 TTL 自动续租、另一个写者持续被阻止、关闭后立即可 claim，以及借用租约未被释放。
- [x] 验证后台续租失败、显式释放、重复关闭、多标签页/多配置文件、失效后不重放、慢调用期间心跳和取消清理。
- [x] 验证 MCP EOF/输入错误/取消释放，工作流长等待仍能写入并在结束后释放，外部令牌保留。
- [x] 执行 `CGO_ENABLED=0 go test ./...`、`make check build`；执行相关包 race 检查（使用可运行的 macOS Go 工具链）。
- [x] 更新文档及仓库 skill，说明自动维护的生命周期、低级 CLI 边界、异常终止的 TTL 兜底和续租失败的恢复。

## 验收标准

1. MCP claim/open 后跨越 30 秒仍能用原令牌写入，无需模型主动 renew。
2. 工作流在连续等待期间保持租约，完成/失败/取消后释放所有自有标签页；借用租约保留。
3. EOF、信号、失败不会留下无限续租的后台任务；其他调用者能够再次 claim。
4. 断线、失效、未知动作均不触发自动抢占或动作重放；回归测试与静态检查通过。

## 实施与验收记录

- 已由当前开发者直接实现，没有使用 TDD、SDD 或子代理。
- 新增 LeaseSession 共用组件，MCP 与 browser.run 均已接入；维护请求计入工作流 browser_calls，借用租约保持原有所有权。
- 对 MCP 的 SIGPIPE 进行处理：输出管道断开返回写入错误后清理，避免进程直接退出而跳过释放。
- 全量 Go 测试、46 个扩展测试、Go vet、TypeScript 检查及构建通过。
- `CGO_ENABLED=1 go test -race -ldflags=-linkmode=external ./internal/client ./internal/mcp ./internal/automation ./cmd/chrome-connector` 通过；external 链接兼容本机 Go 1.21/macOS 工具链。
- `python3 tests/lease_lifecycle.py` 通过：隔离的真实 broker/MCP 进程空闲 35 秒后，原令牌仍可续租；其他调用者仍收到 TAB_BUSY；SIGTERM、EOF、SIGINT、输出管道断开后均能立即重新 claim。
- 自动化验收未连接或操作真实 Chrome 页面；新二进制生成于 bin/chrome-connector。
- 后续按授权通过安装器更新本机版本：current 指向 releases/e9856bd76ac7/chrome-connector，previous 保留 releases/1783407cdc9e/chrome-connector。已核对安装文件与构建文件的 SHA-256 一致，broker 与浏览器宿主重新启动；doctor 无问题，浏览器连接为 ready。
