# MarkdownUI compatibility copy / MarkdownUI 兼容副本

This directory contains the production Swift sources and MIT license from
[MarkdownUI 2.4.1](https://github.com/gonzalezreal/swift-markdown-ui/tree/5f613358148239d0292c0cef674a3c2314737f9e),
commit `5f613358148239d0292c0cef674a3c2314737f9e`. The package manifest retains the
upstream production dependencies; upstream tests, examples and DocC resources
are omitted. Xcode's `Package.resolved` continues to pin the transitive dependencies.

The only production source change is in `Parser/MarkdownParser.swift`: free the
caller-owned cmark output buffers after copying them into Swift strings in
`renderPlainText`, `renderMarkdown` and `renderHTML`. The document tree and its
rendered output have separate ownership. Streaming snapshots otherwise leak a
buffer on every conversion, including conversions made while rendering blocks.

Remove this compatibility copy and restore the remote package reference once an
upstream release frees all three output buffers. Keep the version and MIT notice
in `clients/native/licenses/` aligned when replacing this copy.

本目录保留 MarkdownUI 2.4.1 的生产 Swift 源码和 MIT 许可证，上游提交为
`5f613358148239d0292c0cef674a3c2314737f9e`。包清单保留生产依赖，省略上游测试、
示例和 DocC 资源；间接依赖仍由 Xcode 的 `Package.resolved` 锁定。

唯一的生产源码改动位于 `Parser/MarkdownParser.swift`：三个输出方法在将 cmark
返回的缓冲区复制到 Swift 字符串后释放它。文档树与输出缓冲区分别持有内存；
仅释放文档树会让每次流式快照和段落转换都遗留输出缓冲区。

上游发布包含这三处释放修复的版本后，删除本兼容副本并恢复远程依赖，同时更新
`clients/native/licenses/` 中的版本与许可证信息。
