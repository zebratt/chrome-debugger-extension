---
name: chrome-connector
description: 当 agent 需要在 macOS 复用当前用户正在运行的 Chrome 标签页，读取页面、截图、交互或连续执行已规划的浏览器步骤时使用。适用于本地 CLI 或 MCP；不用于远程浏览器或要求完整 CDP 端口的任务。
---

# Chrome Connector

使用公开 CLI 或 MCP 操作当前 Chrome。先运行 `chrome-connector doctor`；命令不在 PATH 时，使用项目 `bin/chrome-connector` 或安装后的 `~/Library/Application Support/chrome-connector/current`。连接未就绪时查[安装说明](../../docs/install-macos.md)。

## 观察与操作

1. 用 `browser.list` 发现 profileId，再用 `tab.list` 选择标签页，或 `tab.open` 打开新页面。不要猜测“当前标签页”。
2. `tab.snapshot` 使用 `compact:true,detailed:true` 获取文档状态、当前视口文字、控件信息、snapshotId/nodeRef。引用仅对当前观察有效；截图使用 `tab.screenshot`。
3. 低级写操作先 `tab.claim` 获取 leaseToken，结束后 `tab.release`；令牌 30 秒过期，可续租。`tab.open` 已返回新标签页的令牌。

```bash
chrome-connector call browser.list
chrome-connector call tab.list '{"profileId":"实际ID"}'
chrome-connector call tab.snapshot '{"profileId":"实际ID","tabId":123,"compact":true,"detailed":true}'
```

MCP 工具使用下划线命名，如 browser_list、tab_snapshot、browser_run。截图可用 `tab screenshot '<JSON>' --output /absolute/path.png` 保存为不覆盖已有文件的私有文件。

## 优先连续执行明确步骤

任务的操作顺序和目标已经明确时，使用 `browser.run` / `browser_run` 的 `workflow:"sequence"` 一次提交步骤，减少逐次返回主模型。步骤中的精确名称、角色或 href 来自页面观察；执行器每一步重新定位，不复用过期引用，不猜测歧义目标。

```json
{
  "profileId":"实际ID",
  "tabId":123,
  "workflow":"sequence",
  "actions":[
    {"type":"type","target":"搜索","text":"用户给定的查询词"},
    {"type":"key","target":"搜索","key":"Enter"},
    {"type":"wait","expectText":"已知的结果文字"},
    {"type":"click","target":"实际结果链接名称","role":"link"}
  ],
  "expectURL":"https://example.com/预期页面",
  "timeoutMs":60000
}
```

- 支持 click、type、select、key、scroll、wait、assert。type 必须显式 text，空串表示清空；select 使用已观察选项的标签或值。
- role/href 用于消歧；仅给 role 时必须只有一个符合条件的节点。非标准可点击文字可以显式指定 `role:"StaticText"`，仍会做文档、语义和几何检查。
- 提供明确的最终 expectText / expectURL / verifyFields；字段验证针对最终页面。没有最终条件时 outcome=executed 仅表示步骤执行结束。
- 对新页面、未知分支或需要理解内容的任务，在关键位置结束本次清单、读取结果后再由主模型规划下一段。
- 只提交用户任务已经授权的动作；发布、发送、付款、删除等操作继续遵守任务自身的授权和确认要求。执行器只执行明确清单，不根据按钮名称推断权限。
- allowedOrigins 只能列任务允许的额外精确 origin。followNewTabs:true 只跟随唯一、opener 可归因且 origin 已允许的子标签页。已知 target=_blank 点击会先激活来源标签页。
- 只读失效会自动恢复；明确未执行的 stale 目标会重新定位。OUTCOME_UNKNOWN 或 INPUT_UNVERIFIED 必须检查真实页面后再继续，不重放已尝试动作。
- 检查 status、outcome、steps[].execution、completedActions 和 nextAction。nextAction 从零开始；动作成功后的观察失败也会保留已完成前缀，不要从头重跑整个清单。
- 可传已有 leaseToken；工作流续租但不释放调用者的令牌，自行申请的租约会释放。

固定填表、搜索、同源导航也可用 fill/search/navigate。目标必须唯一精确匹配；fill 替换并验证文字但不提交，search 需要显式结果条件。

完整字段、恢复限制与示例见[连续操作说明](../../docs/browser-workflows.md)，低级接口见[协议](../../docs/protocol.md)。

## 运行边界

- 不需要模型服务或 API 密钥。页面读取与执行在本机连接器中进行。
- 后台受控操作使用短时焦点模拟，调试连接结束后恢复；目标已有其他调试器时返回 TAB_BUSY，不抢占。
- `tab.type` 默认追加；明确 replace:true 才替换。`tab.scroll` 滚动顶层页面。iframe、Shadow DOM、Canvas、富文本、文件上传需要合适的其他工具。
- cdp.send 默认关闭；使用前查 system.capabilities / system.policy，只支持扩展调试 API 的部分协议域，不提供持久事件订阅。

连接故障先运行 doctor。BROWSER_OFFLINE 检查扩展；VERSION_MISMATCH 更新并重载；TARGET_DETACHED 检查同页 DevTools 或其他调试器。
