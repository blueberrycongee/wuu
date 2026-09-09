# @wuu/mobile-web — Wuu Web

Wuu Web 不再维护一套独立聊天界面。配对成功后，它直接加载
`desktop/src/renderer` 的工作台，并通过浏览器宿主适配器把 renderer 的
`window.wuu` 调用转发到现有加密远程通道。

```text
shared desktop renderer
          │ WuuDesktopApi
          ▼
RemoteDesktopBridge
          │ app-server line JSON
          ▼
@wuu/remote-core → sealed WebSocket → remote host → desktop service pool → Go
```

桌面开启手机访问时，remote host 通过带临时凭据的本机连接转发请求，
与 Electron 使用同一批 Go app-server。它不为手机创建独立 runtime。
请求和通知携带工作区；各连接的响应相互隔离，会话事件同时发给桌面和手机。
关闭页面、重新配对或停止手机访问仅断开传输，电脑上的任务继续执行。
重连复用有序事件回放，必要时重新读取共享服务快照；离线操作不自动重发。
页面选择、滚动位置和未发送草稿仍由各客户端保留。

保留的旧 Web 核心能力是 `@wuu/remote-core`、凭据存储和 Go remote host。旧的
Home/Thread/Drawer 等页面已退出入口，不再作为新 Web 产品的实现基础。

## 运行

```bash
cd clients/mobile-web
npm install
npm run dev       # http://localhost:5174
```

电脑与手机连接同一个 Wi-Fi，在桌面端 **设置 → 手机访问** 开启访问，
用手机相机扫描二维码即可打开网页并自动配对。配对链接一次有效，十分钟后过期。
已配对的浏览器再次打开该地址会自动连接。桌面记住访问开关，重启后恢复服务，
不会重新打开配对窗口；添加设备时再点击显示配对二维码。手动关闭开关会停止服务，
并阻止下次启动时恢复。可在电脑端撤销设备。
Agent 和工作区仍运行在电脑上，使用期间需要保持电脑与 Wuu 运行。

桌面开发和打包前执行 `npm ci --prefix clients/mobile-web`（仓库根目录）；
桌面构建会自动生成并附带 Web 静态资源。

```bash
npm run typecheck
npm run build
```

## 自托管与网络入口

Wuu 不要求特定组网服务，也不提供必须依赖的公共 relay。用户选择局域网、私网组网、
反向代理或独立 relay；所有模式保留同一套配对、设备撤销和加密协议。

桌面启动进程支持以下环境变量，修改后重启桌面：

| 变量 | 用途 |
| --- | --- |
| `WUU_WEB_LISTEN` | 本地网页与 relay 的监听地址，例如 `100.64.0.1:8787`、`[::1]:8787`；默认自动选择局域网 IPv4 |
| `WUU_WEB_URL` | 二维码使用的浏览器访问 origin，例如 `https://wuu.example.com`；配置它后监听默认改为 `127.0.0.1:8787` |
| `WUU_WEB_TLS_CERT` / `WUU_WEB_TLS_KEY` | 本地服务直接提供 TLS 时的 PEM 证书和私钥，必须一起设置 |
| `WUU_WEB_RELAY_URL` | 独立 relay 的 `wss://.../v1/connect` 地址；必须同时设置网页 URL，此时桌面不启动本地网页监听器 |

访问 URL 使用独立 origin，不支持子路径。监听 `0.0.0.0` 或 `[::]` 时必须提供可达的
`WUU_WEB_URL`。稳定的 origin 可保留浏览器配对身份；更换域名或端口后需在新 origin 配对。
环境变量必须传给实际桌面进程，单独启动 CLI remote host 不会接入桌面的服务池。

**同机反向代理示例**：桌面使用 `WUU_WEB_URL=https://wuu.example.com`，代理把这个
域名的 HTTP 与 WebSocket 请求都转发到 `127.0.0.1:8787`。例如 Caddy：

```caddyfile
wuu.example.com {
    reverse_proxy 127.0.0.1:8787
}
```

代理需要可用的域名、网络路由和可信证书。Caddy 的代理直接支持 WebSocket；参见
[官方说明](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)。

**独立 relay 示例**：在用户自己的服务器构建 Web 静态资源，然后运行：

```bash
wuu relay --addr 127.0.0.1:8787 --web-root /srv/wuu-web \
  --public-url https://wuu.example.com
```

在该服务器前配置 HTTPS 代理。桌面使用 `WUU_WEB_URL=https://wuu.example.com` 和
`WUU_WEB_RELAY_URL=wss://wuu.example.com/v1/connect`，再从桌面开启访问、显示配对码。
网页也可以放在另一个可信 HTTPS origin；与桌面版本配套更新静态资源。
`--public-url` 只控制 CLI 打印的连接地址，不会配置 DNS、证书或启动桌面 host。

**直接 TLS 示例**：不使用反向代理时，CLI 支持：

```bash
wuu relay --addr 0.0.0.0:8787 --web-root /srv/wuu-web \
  --tls-cert /etc/wuu/fullchain.pem --tls-key /etc/wuu/privkey.pem \
  --public-url https://wuu.example.com:8787
```

证书更新后重启 relay 重新加载。桌面内置监听器对应设置上述 TLS 环境变量。
HTTP 仅适合用户自行信任的网络环境；应用层加密不能阻止 HTTP 网页脚本被替换。
远程部署应保护网页代码交付，并使用 WSS。网页托管方能够修改运行在浏览器里的代码，
因此必须可信；单独转发密文的 relay 不需要取得会话明文或设备私钥。

## 当前边界

Web bridge 使用完整的类型检查接口，并通过 `unsupportedMethods` 声明不可用操作。
共享 renderer 据此限制原生窗口、文件管理器、语音、桌宠和本机文件选择器等入口。
会话、模型配置、会话组织、用户问题及 Agent 管理的进程使用已有 app-server RPC。
新建原生 PTY 不可用；Agent 启动的持久进程仍可读取、输入、调整尺寸和停止。

重连优先恢复当前可见会话、排队消息和用户问题，保留本地草稿与标签选择。后台标签的历史在切换到该会话时读取。
离线操作立即失败，恢复失败可重试。旧连接的请求结果不能覆盖新连接。

文件面板需要目录导航、预览和聊天文件引用解析，因此增加三个只读 RPC：
`workspace/directory/list`、`workspace/file/read`、`workspace/file/resolve`。
它们只访问宿主已知的工作区、会话目录及其子目录，使用 rooted 文件访问阻止路径和符号链接逃逸。
文本预览上限为 512 KiB；PNG、JPEG、GIF、WebP 和 PDF 在 2 MiB 内通过同一加密通道返回。
文件编辑没有现有 renderer 调用方，因此没有增加远程写入接口。

工作区选择复用 `workspace/list`，新会话通过现有 `thread/start` 传递所选目录和工作区 ID。
后台事件按会话所属项目路由，重连保留工作区与跨工作区打开的会话。

Git 审阅面板需要仓库状态、变更列表和单文件 diff，因此增加三个只读 RPC：
`workspace/git/status`、`workspace/git/changes`、`workspace/git/diff`。
它们读取已知工作区或会话仓库，比较 HEAD 与工作目录（包含暂存改动和未跟踪文件），
支持首次提交前的仓库、重命名和二进制文件。单份 patch 或文本最多返回 512 KiB，
仓库列表超过 8 MiB 或读取超过 25 秒会明确报错。外部 diff 和 textconv 不会执行。
提交、切分支和创建 PR 仍由电脑上的 Agent 或桌面操作完成，没有新增远程 Git 写入接口。

Web 扩展模块加载暂时关闭，直到存在与 Electron 自定义协议同等的公共加载路径。

浏览器凭据保存在当前 origin 的 `localStorage`，隔离强度低于 Keychain/Keystore。
不要把该页面部署到不受信任的公共 origin；远程链路继续使用端到端加密和既有配对信任。

## 端到端验证

```bash
npm install
npx playwright install chromium
npm run test:e2e
# 或使用已安装的 Chrome：
WUU_BROWSER_CHANNEL=chrome npm run test:e2e
```

测试需要 Go、Git 和 Node。它会构建当前 Go 宿主，在临时目录启动桌面服务池、
relay、远程宿主、Web 服务及确定性的本地模型服务，结束后清理进程和数据。可通过 `WUU_E2E_BINARY`
指定已构建的宿主。测试不读取模型凭据，也不调用付费模型。

浏览器通过真实配对和加密链路发起任务，宿主 Agent 执行 `write_file`。
桌面侧使用生产 AppServerClientPool，手机侧使用真实浏览器，验证双向消息实时可见。
测试在工具执行后断开浏览器，让电脑独立完成任务，再验证最终消息、文件改动、
草稿和会话快照恢复；还会重启宿主验证重新连接。随后验证 Git diff、文件树与代码预览、
返回导航，以及 320/390/430px 宽度和缩短视口中的输入控件可见性。
还验证电脑停止手机任务，以及远程宿主关闭时共享任务仍在运行。
该测试使用浏览器的触屏与视口仿真，不模拟操作系统软键盘。

弱网回归可以限制浏览器连接的实际 TCP 下行速度，同时覆盖 HTTP 和 WebSocket：

```bash
WUU_E2E_DOWNLOAD_BPS=40960 npm run test:e2e
```

此模式生成约 1.2 MB 的工具输入历史，验证配对、共享任务执行、后台完成、
快照与草稿恢复、宿主重启、桌面停止任务和 Git RPC。手势与布局断言仍由默认模式覆盖。
限速按每条 TCP 连接计算，不代表移动运营商或中转服务器的完整网络模型。

网页服务对浏览器支持的文本资源使用 gzip。列表请求使用 `summary_only` 省略历史，
具体会话仍由 `thread/resume` 返回完整内容。支持 `DecompressionStream` 的客户端在
加密的 attach 消息中发送 `accept_line_compression: "gzip"`；宿主可以用独立压缩的
`line_gzip`（无填充 base64url）替代大 RPC 的 `line`，再对整个消息进行端到端加密。
每条记录独立压缩，因此重放不依赖上一条消息的字典。解压上限为 32 MiB，解压失败不会
推进确认序号。旧宿主、旧客户端和没有解压能力的运行环境保留原来的 JSON 消息格式。

## 远程历史与图片传输

Web 在 `workspace/list` 上协商 `remote_delivery: 1`。桌面桥为该连接发送近期历史，
每页最多 20 轮、目标 256 KiB；单轮内容不会截断。更早历史通过 `remote/history/read`
读取，游标对应不可变快照。发送方保留完整记录，浏览器向上查看时才加载旧页。
`thread/resume` 的 `response_only` 避免重复广播，浏览器在收到响应后本地同步状态。

大于 16 KiB 的图片通过 `remote_ref` 引用保留在桌面，点击后才通过
`remote/attachment/read` 分块读取，每块最多 128 KiB 编码字符，最多四块并发。
这些 RPC 使用已有配对和加密连接，没有公开图片下载地址。编辑重发引用图片时，
桌面先还原原始字节。图片缓存最多约 128 MiB 编码字符（单图保留），历史缓存每连接
最多约 64 MiB / 64 页；过期引用会明确失败，重新打开会话可取得新引用。

额外的真实远程回归覆盖 23 轮历史和图片分块往返：

```bash
WUU_E2E_DOWNLOAD_BPS=40960 WUU_E2E_REMOTE_HISTORY=1 npm run test:e2e
```
