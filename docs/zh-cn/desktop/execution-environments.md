# 执行环境

在 **设置 → 运行 → 执行环境** 中，可以让工作区工具运行在容器或远程机器上。默认仍在本机执行。环境内应安装与宿主相同版本的 Wuu；协议不匹配时会停止执行。

创建配置后，将其选为新会话的默认环境。会话创建时会保存环境选择和配置；修改或删除配置不会迁移已有会话。旧会话继续在本机执行。分叉会话继承配置，默认使用独立环境，启用共享时才共用文件系统。原地执行的子代理和只读侧聊使用父会话的环境。

文件、搜索、命令、Git、程序化工具调用及后台进程都进入该环境。桌面可以接收进程事件、查看输出和停止进程。展示产物会将不可变的文件快照传回宿主，单个文件上限为 256 MiB。模型连接、插件、浏览器集成、笔记和历史仍在宿主运行。此设置不会隔离扩展。

## 从 Docker 开始

在 Wuu 源码目录构建内置镜像：

```sh
docker build -f containers/execution/Dockerfile -t wuu-execution:local .
```

创建 Docker 配置，镜像填写 `wuu-execution:local`，工作目录填写 `/workspace`。镜像包含 Wuu、Git、ripgrep、Python 和 Node.js。项目所需的其他依赖可以添加到派生镜像。Docker 服务需要预先启动。

工作目录初始为空，可以在环境中克隆仓库，也可以显式挂载本机目录。使用同一挂载目录的会话会共享其中的文件，即使容器彼此独立。只读挂载可以阻止容器修改该目录。Linux 上挂载目录默认使用宿主用户和用户组，以保留文件归属；可通过容器用户字段覆盖。本机项目与执行环境不会自动同步。桌面编辑器、终端和 Git 面板仍对应本机项目；请通过代理的文件、Git 和进程工具查看执行环境，通过展示产物预览结果。

## 后端

| 后端 | 前置条件 | 资源与网络 |
| --- | --- | --- |
| Docker | 已启动的 Docker 服务及包含执行程序的镜像 | CPU、内存限制及可选的网络隔离 |
| SSH | 可连接的 Unix 主机、Wuu 程序、SSH 身份及已验证的主机密钥 | 资源和网络限制由远程服务器配置 |
| Singularity / Apptainer | 已安装的运行时及包含执行程序的镜像 | 使用运行时的 CPU、内存及网络命名空间选项，需要宿主允许 |
| Modal | Python 3.10+、`modal` 1.5+、已配置 SDK 凭据及可访问的执行镜像 | CPU、内存限制及网络隔离 |
| Daytona | Python 3.10+、`daytona` SDK、已配置凭据及可访问的执行镜像 | 整数 CPU 核数、向上取整至 GiB 的内存及网络隔离 |
| Vercel Sandbox | Python 3.10+、已登录的 `sandbox` CLI 3+ 及位于服务镜像仓库中的执行镜像 | 整数 CPU 核数、随 CPU 分配的内存及网络隔离 |
| Command | 已安装且可信的私有执行协议适配器 | 由适配器负责创建环境及落实限制 |

云端依赖是可选的，不会自动安装。Python 字段可以指向安装了 SDK 的虚拟环境。通过服务提供方的 SDK 或 CLI 登录流程配置凭据。宿主适配器使用这些凭据创建环境，不会自动将其交给工作区命令；环境自身的镜像或服务器可能已有凭据。

SSH 可填写主机或 SSH 别名、端口、身份文件和已知主机文件。必须严格校验主机密钥，请先通过常规 SSH 流程连接并验证。Wuu 会在指定的远程工作目录下创建会话专属子目录，远程账号需要相应写权限。程序化工具调用需要 Node.js 22.19+，Git 和搜索工具分别需要 Git 和 ripgrep。

Linux 命令写入限制需要 Landlock ABI 3 或更新版本。若不可用，标准或只读模式的命令会报错，不会自动放弃限制。参见[权限](../reference/permissions.md)。容器或虚拟机隔离与会话权限模式分别生效。

## 共享、保留与恢复

默认每个会话具有独立环境标识。**在会话间共享** 会让文件系统共用，但工具状态和进程归属仍按会话区分。显式挂载的本机目录始终共享，不受此开关影响。

**保留环境文件** 可以跨正常断开保留 Docker 容器、Singularity 工作目录、SSH 工作目录或云端文件系统。Modal 创建文件系统快照，Daytona 停止环境，Vercel 使用持久化沙箱。共享环境会保留供其他会话使用。保留文件不保证进程能跨云端停止、服务超时或机器重启继续运行。已保留的状态丢失时会报错，不会悄悄换成空文件系统。

未启用保留时，Docker 容器在运行时关闭时移除；Singularity 状态在连接关闭后删除；SSH 工作目录在工作进程断开并达到空闲超时后删除；云端环境在正常断开时停止或删除。宿主异常退出可能遗留资源。保留、共享的云端环境和快照可能持续计费，不再需要时请通过服务控制台或 CLI 清理。删除配置只删除配置本身。

寿命字段控制云端环境寿命或自动停止间隔，以及断开连接后的工作进程空闲超时。零值使用 600 秒。Docker 容器保留不受工作进程空闲超时影响。连接断开时，不会自动重放结果未知的工具操作；下一次调用可重新连接已保存的工作进程。后台进程控制始终使用环境内的进程登记信息，不操作宿主 PID。

仅转发明确列出的环境变量，变量不存在会阻止启动。变量值通过已认证的执行连接传输。不要转发工作区命令不应读取的密钥。

此执行路径由内置引擎提供。外部引擎遇到远程环境配置时会拒绝执行。子代理通过原地隔离继承环境，不支持自动创建宿主 Git worktree；需要时可在执行环境内显式创建 Git worktree。

## 用户配置

环境配置由用户管理。项目配置不能安装或覆盖执行适配器。

```json
{
  "execution_environments": {
    "default": "isolated",
    "profiles": {
      "isolated": {
        "backend": "docker",
        "image": "wuu-execution:local",
        "workspace": "/workspace",
        "network": "none",
        "cpus": 2,
        "memory_mb": 2048,
        "persistent": true
      }
    }
  }
}
```

可选字段包括 `shared`、`user`、`host_workspace`、`mount_read_only`、`forward_env`、`worker`、`lifetime_seconds`、`python`，以及 SSH 的 `host`、`port`、`identity_file`、`known_hosts_file`。命令适配器在 `command` 中填写可执行程序及参数组成的数组。适配器是受信任的本机程序，拥有 Wuu 的宿主权限。

## 可重复验证

构建当前执行镜像并启动 Docker 后运行：

```sh
WUU_EXECUTION_E2E_IMAGE=wuu-execution:local go test ./internal/executionworker -run TestDocker -count=1 -v
```

Singularity/Apptainer 检查将 `WUU_EXECUTION_E2E_SIF` 指向包含当前执行程序的 SIF 镜像，运行 `go test ./internal/executionworker -run TestSingularity -count=1 -v`。

SSH 检查使用一次性测试服务器：将 JSON 格式的 SSH 配置赋给 `WUU_EXECUTION_E2E_SSH`，运行 `go test ./internal/executionworker -run TestSSHEnvironmentEndToEnd -count=1 -v`。测试会在专属环境目录内创建文件和进程。

桌面验收运行 `npm --prefix desktop run test:e2e:execution-environments`。它使用真实渲染器和模拟设置桥接，将截图及 JSON 记录保存到 `desktop/out/e2e/execution-environments`，不验证云端账号。云端实测需要对应凭据及已发布到该服务的执行镜像。
