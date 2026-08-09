# 6. 集成

- 在 **merge_request** 模式下，分支会在每次提交时发布；已完成的需求变为“Pronta para MR（等待 MR）”，并附带在平台上打开 Merge Request（合并请求）的链接，以及与 main 的冲突预览。
- 在 **merge_local** 模式下，**Integrar（集成）** 按钮会在 main 上执行 `merge --no-ff`；没有冲突时，worktree 和分支会被删除，卡片进入 `Integrada`。
- 出现冲突 → 卡片会带着高亮返回，并列出相关文件。请使用 **Atualizar branch（更新分支）**（把 main 合入该分支），或在 worktree 中手动解决。
