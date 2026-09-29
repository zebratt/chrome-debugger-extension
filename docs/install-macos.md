# macOS Chrome 本机开发安装

首版使用未打包扩展和用户级 Native Messaging 宿主。安装后复用已经运行的 Chrome，不需要远程调试端口。当前登录用户下的任何本地进程都能通过连接器访问该用户的普通网页标签页；安装前请确认这一信任边界符合你的机器使用方式。

## 准备与构建

在项目根目录运行：

```bash
cd /path/to/chrome-connector
cd extension && npm install && cd ..
make test
make build
```

本机较旧的 Go 1.21 工具链在启用 cgo 时可能被 macOS `dyld` 拒绝；`Makefile` 对 Go 构建和测试设置了 `CGO_ENABLED=0`。扩展构建产物位于 `extension/dist`。

## 加载扩展与注册宿主

1. 在现有 Chrome 中打开 `chrome://extensions`，启用开发者模式。
2. 选择 **Load unpacked**，选择仓库中的绝对路径 `.../chrome-connector/extension/dist`。开发验证中，macOS 文件选择器无法选中 `~/Library/Application Support` 下的未打包扩展目录，因此扩展从仓库构建目录加载；Go 宿主仍安装在 Application Support。
3. 在扩展卡片上复制实际显示的 32 位扩展 ID。
4. 在项目根目录执行：

```bash
./bin/chrome-connector install <EXTENSION_ID> "$(pwd)/extension/dist"
```

安装器把当前二进制写入 `~/Library/Application Support/chrome-connector/releases/<digest>/`，以 `current` 稳定入口指向该版本；在 Chrome 用户级 `NativeMessagingHosts` 中注册只允许这个扩展 ID 的宿主清单，并创建权限为 `0600` 的默认策略文件。安装器不会发布扩展，也不会修改 Chrome 的 Cookie 或浏览历史。

5. 在 `chrome://extensions` 点击 Chrome Connector 的 **Reload**，再运行：

```bash
./bin/chrome-connector doctor
./bin/chrome-connector call browser.list
```

`doctor` 应报告 `hostManifest`、`hostBinary`、`policyValid` 为 `true`，`browserState` 为 `ready`，`connectedProfiles` 至少为 1。若扩展不在线，检查扩展卡片是否开启、扩展 ID 是否与宿主清单一致、宿主可执行文件是否存在，然后重载扩展。重载扩展不会重启整个 Chrome。

## agent 接入

CLI 可直接调用公开方法：

```bash
./bin/chrome-connector call browser.list
./bin/chrome-connector call tab.list '{"profileId":"<PROFILE_ID>"}'
```

支持 MCP 的 agent 将 `~/Library/Application Support/chrome-connector/current` 设为 stdio MCP 命令，参数为 `mcp`。此进程会在实际工具调用时唤起 broker。详细方法与错误语义见 [本地接口](protocol.md)；agent 操作流程见仓库的 [chrome-connector skill](../skills/chrome-connector/SKILL.md)。

## 更新与回滚

更新代码后重新运行 `make test && make build`。扩展从仓库 `extension/dist` 加载，须在扩展管理页点击 **Reload** 才会应用新代码。再次运行 `install` 会创建内容哈希对应的二进制版本并切换 `current`；旧版本入口保存在 `previous`。若需回退宿主，运行 `./bin/chrome-connector rollback`，再重载扩展，使 Chrome 结束旧宿主并启动回退版本。扩展代码本身按仓库版本回退后重新构建、重载。

`cdp.send` 默认关闭。若确需使用，在 `config.json` 中将 `rawCdp` 设为 `true` 并把需要的协议域加入 `cdpDomains`，然后让按需 broker 退出或重新启动以加载配置。完成调试后恢复关闭状态。连接器公开的是 Chrome 扩展支持的 CDP 子集，不是完整外部 CDP 端口。
