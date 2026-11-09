# 执行授权验收夹具

`cases.json` 在实现前列出失败边界。运行真实生产网关、已打包 Personal
以及独立身份、资源策略、执行授权进程提供者；不是单独的 JSONL 模拟测试。
提供者在仓库外仅使用打包后的公开 `@wuu/plugin-sdk` 与 Node 内置模块编译。
所有身份、凭据、账号与持久化目录均为合成测试数据，不访问真实账号。

在仓库根目录运行 `node scripts/execution-grant-acceptance.mjs`。
`WUU_GRANTS_BINARY` 指定仅用于安装测试插件的 CLI；`WUU_GRANTS_TRANSPORT`
复用当前源码构建的传输夹具；`WUU_GRANTS_SDK_TARBALL` 复用 SDK 包；
`WUU_GRANTS_OUTPUT` 指定证据目录。脚本不会提交、推送或修改真实用户数据。

每次运行保存明确选中的组合、SDK/二进制哈希、脱敏协议记录、提供者自己的
授权账本与审计、消费者审计、命令输出及 `report.json`。前置步骤失败后，
依赖步骤属于未运行，不是通过。传输夹具仅用 `internal/sdk` 将独立管道
接到真实 UserHost，始终使用 `ServeProviderAuthenticated`，不合成主体，
不调用可信本地 `Serve`。独立插件没有内部导入。

登录令牌只代表连接权限；执行提供者根据主机证明的 issuer、tenant、subject
及身份种类维护独立的持久账号有效性。撤销登录令牌不自动代表账号停用；
账号停用与执行授权撤销必须阻止后续无人值守效果。原生拒绝案例验证外部
提供者认证账号，不声称验证真实手机配对、生产 OIDC、真实 GUI 或设备。
