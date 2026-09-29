# chrome-connector 本机验收记录

验证环境：2026-09-29，macOS arm64、Google Chrome 当前用户配置文件、Go 1.21.10、Node.js 24.14.0。初版代码基线：`e9b0dc7`；下列增量验收针对审阅修复后的最终工作树。浏览器操作只使用仓库内 `tests/fixtures/index.html` 的本地测试页，不读取业务页面正文作为验收数据。

## 构建与静态验证

| 命令 | 观察 |
| --- | --- |
| `make test` | Go 全部 11 个包通过（其中 `internal/version` 无测试文件）；扩展 31 个 Node 测试通过 |
| `make check` | `go vet ./...` 与 TypeScript `tsc --noEmit` 通过 |
| `make build` | Go 单文件可执行程序与 MV3 扩展构建成功 |
| skill `quick_validate.py` | `Skill is valid!` |
| `python3 -m json.tool protocol/schema.json` | JSON 解析通过 |

## 现有 Chrome 连接

1. Chrome 主进程在安装、扩展重载、宿主更新和浏览器操作前后均为 PID `39254`；启动参数没有 `--remote-debugging-*`。
2. 未打包项目扩展从仓库 `extension/dist` 加载。用户级 Native Messaging 清单精确登记扩展 ID。Go 宿主从 Application Support 下的稳定 `current` 入口由 Chrome 启动。
3. `chrome-connector doctor` 报告宿主清单、二进制与私有策略有效，`browserState: ready`，一个配置文件在线，协议 `1.0`，broker、宿主和扩展均为 `0.1.0`。
4. CLI 在按需 broker 启动后完成 `browser.list` 和 `tab.list`；两个独立 CLI 进程争用同一标签页时，第二个返回 `TAB_BUSY`，首个释放后第二个能领取。

## 页面与 MCP 操作

| 场景 | 实测结果 |
| --- | --- |
| `tab.open` | 本地测试页打开，返回 `tabId` 与 30 秒占用权 |
| `tab.snapshot` | 返回可访问性节点与不透明节点引用；测试按钮与输入框可定位 |
| `tab.screenshot` | PNG 约 65 KiB；MCP `tab_screenshot` 返回 `image/png` 图像内容 |
| `tab.click` | 点击 “Click me” 后，重新快照显示 “Clicked” |
| `tab.type` + `tab.wait` | 输入 `Matt` 后，等待文本 “Hello Matt” 成功 |
| `tab.key` | Enter 返回成功，调试器会话正常释放 |
| `tab.scroll` | 加长测试页后，`scrollY` 从 `0` 变为 `439.5` |
| MCP stdio | 完成 initialize、列出 18 个工具、调用 `browser_list` 与图像工具 |
| 修复后的快照隔离 | 对同一标签页连续取两个快照，使用第一个快照的按钮引用仍可点击；截图保存 42,101 字节，文件权限 `0600`，CLI JSON 不含图片 Base64 |
| 修复后的 MCP 截图 | 最终代码的 stdio 服务列出 18 个工具，`tab_screenshot` 返回 `image/png` 内容 |

## CDP 策略与故障恢复

- 默认 `cdp.send` 返回 `POLICY_DENIED`。仅临时在私有策略文件开放 `Page` 域时，`Page.getLayoutMetrics` 成功；临时开放 `Runtime` 域用于测量滚动位置；测试后均恢复，复核活跃策略为 `rawCdp: false`、允许域为空。
- Chrome 的 `Input.dispatchMouseEvent(mouseWheel)` 在本地短页面上未返回。原实现导致 broker 在 15 秒后返回 `OUTCOME_UNKNOWN`，调试器仍占用标签页；已改用受约束的 `Runtime.evaluate(window.scrollBy)` 实现结构化滚动，并为 CDP 命令增加 10 秒超时及调试器释放。再次故意调用会挂起的原始滚轮命令时，返回 `OUTCOME_UNKNOWN`，随后 `tab.snapshot` 正常返回，无需重载扩展。
- broker 在刚启动、宿主尚未登记时曾过早返回 `BROWSER_OFFLINE`；新增延迟登记回归测试后，首次配置文件请求等待至登记或 3 秒时限。
- Chrome 文件选择器无法选择 Application Support 下的未打包扩展目录，放宽目录权限仍无效；仓库 `extension/dist` 可正常选择。宿主二进制仍可从 Application Support 启动。最小 Python 宿主放在 `Documents/Codex` 下曾报告 `Native host has exited`，其精确 OS 拒绝原因未取到日志，因此正式宿主不使用该路径。
- 最终代码在私有策略默认关闭状态下返回 `POLICY_DENIED`；临时允许 `Page` 与 `Browser` 域后，`Page.getLayoutMetrics` 成功，`Browser.getVersion` 返回 `UNSUPPORTED_CDP_DOMAIN`。测试结束后恢复原策略并重启 broker，复核 `rawCdp: false`。

## 升级与回滚增量验证

1. 从不支持 `extension.reload` 的先前开发版升级时，安装器在切换二进制后返回 `METHOD_NOT_FOUND` 和明确的手动重载指引。在 `chrome://extensions` 手动重载一次后，配置文件重连，连接 ID 与宿主版本字段均可见。
2. 扩展已有 `extension.reload` 方法后，再次运行安装器自动停止旧 broker、启动新版、重载扩展；连接 ID 从 `6f49a427-...` 变为 `3a26cdf5-...`。执行 `rollback` 后变为 `3ef0daf5-...`，重新 `install` 恢复新版后变为 `1466d1f7-...`。最终一次二进制更新后 `doctor` 仍报告 `ready`，且无问题。
3. 升级、回滚、再次安装后 Chrome 主进程仍为 PID `39254`，启动命令没有远程调试参数。测试结束时关闭本地测试标签页和 `127.0.0.1:8765` 测试服务，保留用户授权安装的项目扩展与宿主。

## 证据边界

真实浏览器验收使用一个 Chrome 配置文件与当前系统用户。多配置文件路由、协议大小上限、空闲退出、同标签页租约、超时和断线处理有 Go 单元或集成测试；没有在第二个真实 Chrome 配置文件或另一个 macOS 用户账号上执行现场验证。DevTools 与本扩展争用同一标签页的错误映射经过扩展测试；曾尝试在本地测试页打开 DevTools，但未观察到实际占用，因而不把它写作浏览器现场通过。回滚及恢复已在当前 Chrome 中现场验证。
