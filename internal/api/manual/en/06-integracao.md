# 6. Merging

- In **merge_request** mode, the branch is published on every commit; the finished demand becomes "Pronta para MR" (Ready for MR) with the link to open the Merge Request on the platform and the conflict preview against main.
- In **merge_local** mode, the **Integrar** (Integrate) button runs `merge --no-ff` into main; with no conflict, the worktree and the branch are removed and the card moves to `Integrada`.
- With a conflict → the card comes back highlighted with the list of files. Use **Atualizar branch** (Update branch), which brings main into the branch, or resolve it manually in the worktree.
