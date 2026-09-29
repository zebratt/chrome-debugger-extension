# 运行中 Chrome 接入可行性验证

验证时间：2026-09-29 15:42–15:53（Asia/Shanghai）

## 假设与判据

假设：在不重启已有 Chrome、不添加远程调试启动参数的情况下，当前配置文件可加载未打包扩展；随后注册用户级 Native Messaging 宿主并重载扩展，扩展能够与宿主完成请求/响应。扩展再次重载后应能恢复连接。

成功判据：Chrome 主进程 PID 保持不变，启动参数无 `--remote-debugging-*`；扩展状态显示 `connected` 和宿主返回的 `pong`；宿主观察记录中的 origin 与扩展 ID 一致；扩展重载后由新宿主进程再次完成握手。

## 入口与步骤

1. 在项目外的临时目录建立最小 MV3 扩展与 Python Native Messaging 宿主；先运行 `python3 -m unittest test_host.py`，观察预期失败，再实现宿主并确认测试通过。
2. 记录运行中的 Chrome 主进程 PID 为 `39254`，启动参数无远程调试标志。在 `chrome://extensions` 的开发者模式下加载临时未打包扩展，实际 ID 为 `oghcfmadpipbjkpcaiofhpagdmeolfob`。
3. 在当前用户的 Chrome `NativeMessagingHosts` 目录创建测试清单，`allowed_origins` 精确指定上述 ID；重载扩展并读取其状态页。
4. 初次宿主路径放在 `Documents/Codex`：扩展报告 `Native host has exited`，没有得到 `pong`。shell 单测可通过，但 Chrome 启动路径与 shell 环境不同。
5. 只改变宿主路径到 `/tmp` 后重试：扩展状态变为 `connected`，收到 `{"type":"pong","nonce":"chrome-spike"}`，宿主观察记录 origin 与 ID 一致。
6. 把宿主移至拟采用的用户 Application Support 路径，重试后再次收到 `pong`。随后从扩展管理页重载扩展，新的宿主 PID `23952`、父进程 PID `39254` 再次写入握手记录。
7. 再次检查 Chrome 主进程 PID 为 `39254`，远程调试启动参数为空。

## 观察结果

| 检查点 | 结果 |
| --- | --- |
| 运行中加载未打包扩展 | 成功，Chrome 主进程未重启 |
| 运行中注册 Native Messaging 清单 | 成功，无需重启 Chrome；重载扩展后可连接 |
| 扩展重载后的宿主恢复 | 成功，新宿主 PID `23952`，PPID `39254`，收到 `pong` |
| 调试端口要求 | Chrome 启动参数无 `--remote-debugging-*` |
| 宿主位于 `Documents/Codex` | 连接失败，状态 `Native host has exited`；未取得底层 OS 拒绝日志，不能断言精确原因 |
| 宿主位于 Application Support | 连接与重载恢复均成功 |

## 结论与方案影响

核心运行时前提在本机 Chrome 上成立。首版安装器应把宿主可执行文件放在当前用户 Application Support 下的稳定位置；不要把 `Documents/Codex` 下的临时脚本路径当作可工作的生产安装位置。此验证使用的是最小 Python 宿主，正式 Go 二进制及多配置文件仍须在实现阶段验收。测试结束后移除临时扩展、宿主清单和测试安装目录。
