---
plan: chrome-connector
dir: 2026-09-29-chrome-connector
status: final
created: 2026-09-29
updated: 2026-09-29
schema_version: 1
related_adrs:
  - docs/decisions/0001-local-browser-agent-contract.md
evidence_paths: []
review_profile:
  schema: 1
  core: [C1, C2, C3, C4]
  conditional: [X2, X3]
  skipped_conditional: [X1, X4, X5]
  skip_reasons:
    X1: "没有首版 SLA、吞吐或大数据量要求"
    X4: "用户选择保持小范围评审，首轮不单列可维护性轴"
    X5: "本项目是本机按需工具，没有生产告警或值守要求"
  round_policy: incremental_from_r2
---

# Plan: chrome-connector

## 流程状态

- [x] 前提确认
- [ ] 关键前提验证（运行中注册宿主的本机验证待实施首项完成）
- [x] 方案方向选定
- [x] 初版方案生成（plan_v1）
- [x] 第 1 轮评审（review_r1）
- [x] 第 1 次修改（plan_v2）
- [x] 最终方案确认
- [ ] 实施完成
- [ ] Promote 完成

## 文件索引

| 文件 | 说明 |
| --- | --- |
| `meta.md` | 需求、决策及流程状态 |
| `plan.md` | 当前修改版架构方案 |
| `history/plan_v1.md` | 初版方案归档 |
| `history/review_r1.md` | 第 1 轮独立子代理预审 |
| `history/plan_v2.md` | 采纳首轮评审并记录用户交付选择后的方案 |
| `history/plan_v3.md` | 最终确认后追加 ADR 关联的方案归档 |
| `../../../decisions/0001-local-browser-agent-contract.md` | 公开接口与信任边界 ADR |
| `tasks.md` | 用户选择 L2 编排后的完整范围实施清单 |

## 需求摘要

开发一个独立的 Chrome 扩展及本地连接器，让当前 macOS 登录用户下的本地 agent 复用正在运行的 Chrome，无需重启浏览器或开放远程调试端口。公开版本化的本地接口，提供页面读取与操作、按需启用的 CDP 子集，并通过 CLI、MCP 和可复用 skill 供不同 agent 使用。

## 已确认前提

- 首版仅支持 macOS 与 Google Chrome，架构保留扩展到其他平台和 Chromium 浏览器的边界。
- 同一 macOS 用户下的进程无需逐个授权；跨用户进程不能连接。
- Chrome 扩展通过 Native Messaging 与小型宿主通信；小宿主可随 Chrome 保持连接。
- broker 由 agent 请求按需启动，最后一个客户端退出且空闲超时后停止。
- 接口以页面操作为主，提供可配置的 CDP 子集透传。
- 用户已选定「小宿主 + 按需 broker」，不采用常驻 broker。

## 待确认项与已接受风险

- 已接受：同一用户下的任意进程均可访问登录态页面；不设置每个 agent 的配对或令牌。
- 已接受：`chrome.debugger` 仅支持部分 CDP 协议域，不能宣称与外部 CDP 直连等价。
- 已确认：首版使用本机加载未打包扩展；安装器读取实际扩展 ID 并生成精确的 Native Messaging 清单。
- 初版评审后确定：broker 默认空闲超时 120 秒，CDP 透传默认关闭且允许域为空，普通 `http`/`https` 网站默认可访问。
- 待实施时确定：可执行程序的具体安装目录名，不改变已选架构。

## 前置证据摘要

- 本机 ChatGPT Chrome 扩展声明 `debugger`、`nativeMessaging`、`tabs` 权限，代码使用 `chrome.debugger.attach`、`sendCommand` 和 `chrome.runtime.connectNative`；本方案仅参考公开能力组合，不复用其私有协议。
- Chrome 官方 Native Messaging 文档规定宿主通过标准输入输出与获准扩展通信，`allowed_origins` 为扩展 ID 列表，不支持通配。
- Chrome 官方文档说明 `connectNative` 可维持扩展 service worker 生命周期，且 `chrome.debugger` 可用的 CDP 协议域受限制。
- 项目目录创建前为空，没有既有 ADR 或方案；已初始化独立 Git 仓库。
- 尚未在现有 Chrome 上安装本项目扩展；运行中注册宿主、扩展重载与无需重启的验收仍待本机 spike，方案要求失败时回到评审。

## 决策备注

- 公开协议是长期兼容合同，需要版本号和错误语义；内部传输可替换。
- 首版不复制 ChatGPT 扩展代码或私有通信协议，使用 Chrome 官方扩展 API 独立实现。
- 第 1 轮评审的 P0 安装交付分歧由用户选择本机未打包扩展方案；C2/C3/C4/X2/X3 的其余合理建议已自动纳入修改版。
- 用户确认 plan_v2 为最终方向；ADR-0001 记录公开接口与同用户信任边界，plan_v3 仅新增 ADR 引用。

## 任务档位

按文件数、系统边界和新抽象估算为 L3；用户明确选择**保留完整范围，采用 L2 编排**，并已确认 `tasks.md`、授权开始实施。实施由主上下文顺序推进，不采用 L3 的 `subagent-driven-development` 和任务间 review checkpoint。范围不缩小，因此实际工作量仍按大型项目估计；遇到首项可行性验证失败时回到方案评审。

## 评审 Profile

首轮启用 C1–C4、X2 安全与隐私、X3 迁移与兼容；用户选择保持小范围。X1、X4、X5 本轮跳过，理由见 frontmatter。

## Promote 记录

待实施完成后填写。
