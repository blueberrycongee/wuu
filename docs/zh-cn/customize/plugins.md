# Wuu 插件

Wuu 插件把 agent 行为、桌面界面或其他扩展贡献打包在一起，可以包含工具、技能、Hook、MCP 服务、主题和设置。只启用你信任的代码：插件拥有运行 Wuu 的用户权限，不在 Wuu 沙箱内运行。

## 安装本地包

打开**技能与插件**，选择本地目录或 zip 包，再打开详情。当前包管理流程中的**批准并启用**会在一次操作中确认信任并激活包。Wuu 没有中央市场，安装器也不会直接下载 npm 包或 Git 仓库。

CLI 将本地包操作分开提供：

```bash
wuu plugin install ./my-plugin
wuu plugin approve my-plugin
wuu plugin list
```

请使用包中声明的 ID。安装会把文件复制到 Wuu 主目录的 `plugins/` 下，默认是 `~/.wuu/plugins/`，设置 `WUU_HOME` 后位置随之改变。CLI 的 install 只放入包，不会单独批准代码执行。

## 更新、禁用与移除

可以从目录或 zip 安装替换包，也可以使用：

```bash
wuu plugin update my-plugin ./my-plugin-next.zip
wuu plugin approve my-plugin
```

当前本地更新器会暂存替换版本的指纹，在接受更新前保留原有版本。请在详情页检查待更新内容。包内容变化后可能需要重新确认信任；不能认为复制了新文件就已经激活。

禁用会让之后新建的会话停止该包的贡献，但保留文件。已经开始的会话继续使用它启动时的 generation。不再需要时可以移除：

```bash
wuu plugin disable my-plugin
wuu plugin enable my-plugin
wuu plugin remove my-plugin
```

默认会保留设置和插件存储。移除不会擦除插件创建的全部数据，也不会撤销已完成操作。

## 恢复与排查

详情页区分待信任、已禁用、启动中、运行中、失败和待更新等状态。请查看实际错误，不要把所有功能缺失都当作安装失败。缺少依赖或其他包不兼容，也可能阻止激活。

桌面代码渲染失败时，Wuu 会尽可能隔离出错的贡献，并保留插件管理和默认界面恢复入口。禁用可疑插件后，使用默认界面重试。插件自身界面损坏时，可以使用 CLI 操作。安全模式用于从插件相关启动故障中恢复，并不代表重新启用该包就安全。

## 插件能提供什么

agent 运行时可以注册工具、补充上下文、观察支持的生命周期事件，以及提供或调用有版本的服务。桌面模块可以添加视图、固定插入点、语义渲染替换、会话卡片和样式。声明式主题和设置不需要桌面模块，见[主题与设置](themes-settings.md)。

内置功能也使用这些机制。具体用法见[子代理](../desktop/subagents.md)、[自动化](../automation/scheduled-tasks.md)和[记忆](memory.md)。Peers 插件让 agent 联系同一工作区已有的会话，与创建子代理或使用具名协作身份是不同功能。

## 开发插件

从 [agent 快速开始](plugin-quickstart.md)或[桌面快速开始](desktop-plugin-quickstart.md)入手。本地开发与安装可分发包是不同流程。[编写参考](plugin-authoring.md)介绍清单、开发命令、版本要求和包关系。

Wuu 不审计、认证或托管第三方扩展。运行陌生代码前，请确认它与当前 Wuu 构建兼容，并阅读[安全模型](../reference/security-model.md)。
