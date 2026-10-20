# 快速开始

使用桌面预览版需要一台 Apple 芯片 Mac，以及可用的模型连接。第一次尝试时，选一个不含敏感资料的小型本地项目。

## 完成设置

1. [安装 Wuu](installation.md)，打开 `/Applications` 中的应用。正式发布版由 Apple Developer ID 签名并完成公证；如果使用较早的 ad-hoc 预览版，macOS 可能拦截首次打开，按安装指南处理。
2. 在首次设置中选择需要的插件。推荐组合为 TODO 和 Automation，之后可以在插件设置中调整。
3. 选择执行引擎。Wuu 无需额外安装；Codex 和 Claude Code 需要本机已有可用的 CLI。
4. 使用 Wuu 引擎时，[配置模型服务](model-services.md)，或复用支持的本机订阅登录。
5. 按需从本机浏览器导入网站 Cookie。近期活跃的配置排在前面；找到需要的配置后点击**导入 cookies**，或点**暂不导入**。之后也可以在**设置 → 浏览器**中导入。
6. 勾选从本机 Codex 和 Claude Code 使用记录中发现的最近项目，或手动选择文件夹。导入后直接进入所选工作区，也可以稍后添加。此步骤只添加工作区目录，历史对话和项目文件保留原处。

Cookie 导入目前支持 macOS 上的 Chrome、Chromium、豆包、Edge、Brave、Vivaldi 和 Opera。识别时只读取配置元数据，不读取 Cookie 内容或密码；最近活跃时间用于排序，不代表使用频率。导入时可能请求 macOS 钥匙串访问权限。导入的 Cookie 在 Wuu 浏览器标签间共享，Agent 可据此使用已登录的网站；部分网站仍需重新登录。目前不支持 Safari 和 Firefox。

之后也可以在**设置 → 模型服务**中调整连接。发送前看一眼输入框中的模型：当前对话的选择可能与保存的默认值不同。

## 阅读、修改、检查

在权限菜单中选择**只读**，先让 Agent 了解项目：

```text
阅读这个项目，不要修改文件。告诉我它是做什么的，以及怎样运行测试。
```

确认回答中的项目和测试命令无误后，切换到**标准**模式，尝试一个小改动。说明期望结果，并要求运行相关检查。[第一个任务](first-task.md)介绍了具体做法。

完成后，用 `/diff` 查看 Git 改动，并核对命令结果，再决定是否接受。一句“已完成”不等于测试已经通过。需要修正或接着做时，继续当前对话即可。

提示词、相关文件内容和工具结果可能发送给所选模型服务。处理私有项目之前，请了解[权限](../reference/permissions.md)和[安全边界](../reference/security-model.md)。

## 使用命令行

桌面应用不需要另装 CLI。如果想从终端调用，先[安装 CLI](installation.md#从源码安装-cli)并[配置模型服务](model-services.md#配置-cli)，再进入项目目录运行：

```bash
wuu exec --permission-mode read_only "阅读这个项目，告诉我怎样运行测试"
```

脚本输入、会话恢复和机器可读输出见 [`wuu exec`](../automation/exec.md)。设置失败时，可按[故障排查](../help/troubleshooting.md)定位问题。
