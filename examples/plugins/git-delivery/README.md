# Git Delivery / Git 交付

This optional trusted extension adds **GitHub draft PR** to project candidate review.
It requires Node 22+, Git, an authenticated `gh`, and an `origin` remote. Build the
SDK with `npm --prefix packages/plugin-sdk run build`, run `npm install` here, then
install this directory through Wuu's local extension installer. Disabling the
extension removes its action. It uses the public command context
`project-candidate.publish`, which receives `{ candidate, title }`, and the public
runtime invocation contract.

The user reviews a managed session's frozen candidate and clicks Open PR. The
extension replays only that candidate's diff in a temporary checkout of the
repository's default branch, creates a new branch, pushes without force, and opens
a draft PR titled after the session and listing the changed files. Conflicts fail
without changing the user's checkout. It never merges. Repeated publication of
the same candidate returns the existing PR. Review the generated PR before merging.

这个可选受信任扩展在项目改动提案审查中提供 **GitHub 草稿 PR**。需要 Node 22+、
Git、已登录的 `gh` 和 `origin` 远端。先在仓库根目录运行
`npm --prefix packages/plugin-sdk run build`，再在本目录运行 `npm install`，
通过 Wuu 本地扩展安装入口安装此目录。禁用扩展会移除交付动作。它使用公开命令上下文
`project-candidate.publish`（接收 `{ candidate, title }`）和公开的运行时调用契约。

用户审查托管会话的改动提案并点击开 PR 后，扩展只把该提案的 diff 应用到默认分支的
临时检出，建立分支、正常推送，并打开以会话标题命名、列出改动文件的草稿 PR。
冲突不会修改用户当前工作树，也不会自动合并。重复发布同一提案会返回已有 PR。
合并前请审查生成的 PR。
