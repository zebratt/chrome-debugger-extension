# 本地连续操作 0.4.0 验证记录

- 完整 `make test check build` 通过：Go 全包测试、go vet、46 项扩展测试、TypeScript 检查与构建。
- 无 API 密钥环境下，本地 6 项用例符合预期：同页连续表单、子标签页接管、未授权跟随时回交、Enter keypress、固定填表验证、MCP 连续操作。
- 三个真实网页小游戏由预先给定的明确步骤完成，分别验证日期填写、StaticText 显式点击和限时三连点；使用实时快照和实际点击，不修改页面状态。
- 这组计时为已规划步骤的本地执行阶段，不包含主模型理解任务和规划时间，不与此前逐步模式的端到端耗时直接计算加速比。

| 用例 | 结果 | 已完成步骤 | 执行阶段毫秒 |
|---|---|---:|---:|
| same_tab_sequence | completed | 6 | 690 |
| new_tab_sequence | completed | 6 | 709 |
| new_tab_opt_in | 回交 NEW_TAB_OPENED | 1 | 59 |
| keypress | completed | 2 | 115 |
| fixed_fill | completed | 1 | 108 |
| mcp_sequence | completed | 1 | 79 |
| date | completed | 2 | 51 |
| buttons | completed | 1 | 50 |
| click-cubed | completed | 3 | 75 |

故障注入覆盖明确未执行的 stale 恢复、动作成功后的观察失效、未知结果不重放、未验证输入停止、已完成前缀保留、子标签页独立租约、加载过程中迟到目标等待，以及精确 URL/文本/字段验收。

模型客户端、模型参数、评分和相关使用内容已从工作树移除；运行时不需要模型服务或 API 密钥。仓库 skill 与本机全局 skill 均通过 quick_validate，文档引用可解析。

实测原始数据为 `work/sequence-live-final.json` 与 `work/sequence-webgames.json`。本机连接器、宿主与扩展更新为 0.4.0，doctor 连接 ready。
