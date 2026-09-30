# 浏览器连续操作（0.4.0）

调用者规划明确的步骤，连接器在本地连续执行。无需切换模型、配置网关或提供 API 密钥。CLI 为 `chrome-connector browser run '<JSON>'`，MCP 为 `browser_run`。

## 连续步骤

```json
{
  "profileId": "已发现的实际 profileId",
  "tabId": 123,
  "workflow": "sequence",
  "actions": [
    {"type": "type", "target": "City", "text": "Lisbon"},
    {"type": "select", "target": "Style", "option": "Design"},
    {"type": "click", "target": "Free cancellation"},
    {"type": "click", "target": "Search hotels"},
    {"type": "wait", "expectText": "Matching hotel: Casa Flora"},
    {"type": "click", "target": "Open Casa Flora", "role": "link"}
  ],
  "expectText": "Casa Flora",
  "expectURL": "https://example.com/hotel/casa-flora",
  "followNewTabs": true,
  "allowedOrigins": ["https://example.com"],
  "timeoutMs": 60000
}
```

仅提交任务已经授权的动作。主模型负责理解任务、选择输入、规划步骤和必要的确认；连接器不根据按钮名称推断业务权限，也不执行任意脚本。

| type | 参数与行为 |
|---|---|
| click | 精确 target 名称，可加 role / href；运行时检查文档、语义、遮挡和命中点 |
| type | 同样的目标定位，必须显式 text；替换并验证，空串表示清空 |
| select | 原生下拉的精确目标及 option 标签或值；必须唯一匹配可用选项 |
| key | 指定可编辑目标及 key；Enter、Tab、Escape、Backspace、方向键 |
| scroll | deltaY，滚动顶层文档并检查文档身份 |
| wait | 等待 expectText / expectURL / verifyFields 全部成立；默认 3 秒，可设 timeoutMs，最多 10 秒 |
| assert | 立即核对上述条件，失败即停止 |

每步重新读取快照并定位，目标临时未出现时最多等待 3 秒（可用步骤 timeoutMs 调整，上限 10 秒）。名称忽略大小写和连续空白；role 精确匹配；href 保留路径、查询参数、片段。只给 role 或 href 时也必须唯一匹配，重复目标返回 AMBIGUOUS_TARGET，交回调用者消歧。

例如只有一个动态游戏按钮时，可使用三个 `{"type":"click","role":"button"}` 连续步骤，每次都匹配新状态。非标准可点击文本须指定已观察到的角色，例如 `{"type":"click","target":"this does nothing","role":"StaticText"}`。这只扩展明确目标的点击，仍检查实际命中点。

## 验收与恢复

- 最多 60 个步骤；sequence 默认 60 秒，其他工作流默认 15 秒；timeoutMs 为 100–60000。输入和请求大小有界。
- 最终 expectText 验证当前视口文字；expectURL 验证精确 URL；verifyFields 为 `{target,text}` 数组，所有条件须同时成立。checkbox/radio/switch 使用 `"true"` / `"false"`，下拉可用选中值或标签。
- 每次动作后重新观察，已完成前缀先记账。读取瞬态故障最多 5 次尝试，动作前确定未执行的 stale 最多恢复 3 次。
- 文档 complete 可执行；interactive 须保持观察稳定至少 500 ms；loading 等待且不输入，等待上限 10 秒。
- OUTCOME_UNKNOWN、未验证输入、目标歧义、站点越界或超时均停止。未知或未验证的步骤必须先检查真实页面，再决定是否继续。
- nextAction 是从零开始的下一步骤位置；completedActions 包含成功的动作、wait/assert 和已满足而跳过的输入。若动作执行后观察失败，nextAction 已前移，不得从头重跑。
- status 为 completed / needs_attention。最终显式条件通过时 outcome 为 verified；sequence 无最终条件时 outcome 为 executed，表示步骤执行结束，不代表业务目标已由执行器证明。
- CLI 对 needs_attention 返回退出码 1，同时输出完整进度与步骤。steps[].execution 区分 executed、observed、not_executed、unknown、executed_unverified。

默认只允许起始 origin，allowedOrigins 指定额外精确 origin。followNewTabs 默认 false，新标签页出现时返回 NEW_TAB_OPENED 和 nextTabId；显式开启后只接管唯一、可归因于 opener 且 origin 已允许的子标签页。每个标签页使用独立租约。

调用者可提供已有 leaseToken；连接器续租但不释放调用者的令牌。工作流使用独立心跳，在等待页面、连续 wait/assert 或慢请求期间每隔最多 10 秒续租，不依赖下一次写动作才续租。由工作流申请的全部租约（包括跟随的新标签页）在完成、失败、超时或取消时释放。先停止心跳，再在统一的 2 秒清理窗口内释放；清理失败且步骤已完成时返回 RELEASE_FAILED，并保留完成进度。metrics.browser_calls 包含本次工作流的后台续租和释放请求。

后台续租失败后不自动重新 claim，后续写请求返回租约失败原因；确认连接和页面状态后由调用者决定恢复。SIGINT/SIGTERM 会取消执行并清理租约；SIGKILL/崩溃由 30 秒 TTL 兜底。MCP 外层会话申请的令牌可以传给工作流，工作流返回后仍由该 MCP 会话维护，直到显式 tab_release 或会话退出。

## 固定工作流

- `fill`：fields 数组包含 target/text；全部精确匹配，替换并核对文字，不提交。
- `search`：target/text，可选 pressEnter:true；必须提供结果检查或 emptyText。预先已存在的结果文字不能单独证明新查询已生效。
- `navigate`：精确 target 链接；验证实际到达所选 URL，可附加结果条件。固定导航只允许同源。

```json
{"profileId":"实际ID","tabId":123,"workflow":"fill","fields":[{"target":"姓名","text":"Matt"}]}
```

模型参数、自然语言目标和候选评分不属于本地执行接口；不认识的参数会被拒绝。iframe、Shadow DOM、Canvas、文件上传和富文本仍需其他合适的工具。

## 验证

`make test check build` 覆盖协议、进度保留、失效恢复、未知动作不重放、精确验收及扩展守卫。`tests/sequence_workflows.py` 在本机虚构页面上验证连续表单、新标签页、MCP 和按键；显式文本点击另通过网页小游戏验收。
