# 快速开始

第一次尝试，选一个不含敏感资料的小项目。你需要一台 Apple 芯片 Mac，以及可用的模型。

从 GitHub Releases [安装 wuu](installation.md)。桌面预览版尚未签名；如果 macOS 阻止打开，安装指南中有处理方法。

打开设置，[接上模型](model-services.md)，按对方要求填好 API Key。再把本地项目文件夹添加为工作区，开始对话。

wuu 可以修改文件和运行命令。发送任务前，确认选中的工作区和[权限](../reference/permissions.md)。你发给它的内容和相关文件可能会送到接上的模型，具体见[安全模型](../reference/security-model.md)。

先让 Agent 了解项目：

```text
阅读这个项目，不要修改文件。告诉我它是做什么的，以及怎样运行测试。
```

接着选一个范围小、容易检查的改动，说明你想要的结果，并让 Agent 运行相关测试。[第一个任务](first-task.md)中有更多示例。

完成后，检查改过的文件和命令输出，再决定是否接受结果。需要修正或稍后继续时，回到同一个对话即可。

## 使用命令行

桌面应用不需要另装 CLI。如果想从终端或脚本调用 wuu，先按[安装指南](installation.md)安装 CLI，再进入项目目录运行：

```bash
wuu exec "阅读这个项目，告诉我怎样运行测试"
```

更多用法见 [`wuu exec` 指南](../automation/exec.md)。遇到问题时，先看[故障排查](../help/troubleshooting.md)。
