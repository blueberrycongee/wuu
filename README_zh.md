# wuu

[English](README.md) · [文档](https://blueberrycongee.github.io/wuu/zh-cn/) · [下载](https://github.com/blueberrycongee/wuu/releases)

wuu 是一个开源桌面应用，让你和 AI Agent 一起处理本地项目。接上 OpenAI、Anthropic 这类服务，选好文件夹，就可以让 Agent 阅读代码、修改文件或运行命令，再在应用里查看文件、改动和执行结果。

你可以回到已有对话继续工作，也可以让多个 Agent 在群聊中协作。插件可以添加工具和桌面功能，见[扩展 Wuu](docs/zh-cn/customize/index.md)。

![wuu 桌面应用](https://github.com/user-attachments/assets/2d9030aa-ca03-42b1-9333-f79cc5aff95b)

## 开始使用

桌面预览版支持 Apple 芯片 Mac。从 [GitHub Releases](https://github.com/blueberrycongee/wuu/releases/latest) 下载，将 `wuu.app` 放入 `/Applications` 后打开。预览版使用自签身份，没有 Apple Developer ID 和公证；如果 macOS 阻止打开，请按[安装指南](docs/zh-cn/getting-started/installation.md)处理。

打开设置，接上模型，再把本地项目文件夹添加为工作区。可以先试一个小任务，完成后检查改动和测试结果。[快速开始](docs/zh-cn/getting-started/index.md)里有一个示例。

## 命令行

桌面应用自带运行所需的核心。如果还想在终端或脚本里使用 wuu，先安装 [go.mod](go.mod) 要求的 Go 版本，再从源码构建 CLI：

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu init
```

确认 Go 的二进制目录在 `PATH` 中，并[配置模型服务](docs/zh-cn/getting-started/model-services.md#配置-cli)，再进入项目目录运行：

```bash
cd /path/to/your/project
wuu exec --permission-mode read_only "阅读这个项目，告诉我怎样运行测试"
```

脚本调用、JSONL 输出和会话控制见 [`wuu exec` 指南](docs/zh-cn/automation/exec.md)。

## 文件与数据

wuu 会在当前权限允许的范围内读写本地文件、运行命令。你发给它的内容和相关上下文会发送给你选择的服务商，会话和设置默认保存在 `~/.wuu`。处理敏感资料或不可信项目之前，请阅读[安全模型](docs/zh-cn/reference/security-model.md)。

## 参与项目

开发相关说明见[贡献指南](CONTRIBUTING.md)。遇到问题可以[提交 issue](https://github.com/blueberrycongee/wuu/issues)；安全漏洞请按 [SECURITY.md](SECURITY.md) 报告。

项目采用 [MIT 许可证](LICENSE)。Agent 头像使用同为 MIT 许可的 [blobatar](https://github.com/Alain00/blobatar)。
