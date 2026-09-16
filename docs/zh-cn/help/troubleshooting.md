# 故障排查

按下面的症状排查。报告问题前不要直接上传整个 `~/.wuu`，其中
可能包含源代码、会话、工具输出和凭据信息。

## 桌面应用无法启动

1. 完全退出 wuu，再重新打开。
2. 确认应用来自官方 [GitHub Releases](https://github.com/blueberrycongee/wuu/releases)。
3. 如果 macOS 拦截应用，按照[安装说明](../getting-started/installation.md)使用**仍要打开**，
   不要关闭 Gatekeeper。
4. 如果界面一直停在初始化状态，记录错误信息和应用版本，便于报告问题。

## 模型服务不可用

- **缺少 API Key：**桌面端检查**设置 → 模型服务**；CLI 检查 `api_key_env` 对应的
  环境变量是否真的存在于启动 wuu 的进程中。
- **模型不存在：**使用服务端接受的模型 ID，不要填产品展示名称。
- **能聊天但不能改文件：**确认模型和兼容网关完整支持工具调用，而不只是文本对话。
- **自定义端点失败：**检查协议类型、API 前缀和网关是否原样转发流式响应及工具结果。

## 看到错误的文件或会话

桌面端检查当前项目；CLI 检查当前目录或 `--workdir`。会话默认按工作区筛选。目录被
移动后，在侧边栏对项目使用**重新定位…**，不要把它当作同名新项目重复添加。

## Agent 不能修改文件

检查[权限模式](../reference/permissions.md)和目标目录。只读模式禁止写入；标准模式下，
目标需要位于已登记工作区。根据工具错误确认被拦截的路径，或沙箱后端是否不可用。
不要为了绕过错误直接切换到 `unconfined`。

## 命令运行时间很长

命令可能作为后台进程继续运行。查看消息流中的进程状态和增量输出；不要仅因界面没有
新文本就重复启动同一命令。完整输出过长时，结果会提供日志引用。

## CLI 自检

```bash
wuu --version
wuu session list --json
wuu session show --json --last
wuu session trace --json --last
```

`session trace` 可以重放已经保存的事件，不会重新调用模型或工具。需要检查自动化运行
记录时使用 `wuu runs` 和 `wuu runs read RUN_ID`。

## 本地数据在哪里

默认用户状态位于 `~/.wuu`，包括配置、认证、会话、记忆和日志。设置 `WUU_HOME` 后，
这些路径整体移动到指定目录。加入问题报告前，只复制解决问题所需的最小片段，并删除
API Key、OAuth 信息、源代码和私人对话。

仍无法解决时，在 [GitHub Issues](https://github.com/blueberrycongee/wuu/issues) 提供：

- wuu 版本和操作系统；
- 使用桌面端还是 CLI；
- 最小复现步骤；
- 已脱敏的错误和相关日志片段；
- 预期行为与实际行为。

安全漏洞不要公开提交，按照仓库的 [SECURITY.md](../../../SECURITY.md) 私下报告。
