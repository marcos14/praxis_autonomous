# 6. Integrando

- No modo **merge_request**, a branch é publicada a cada commit; a demanda concluída vira "Pronta para MR" com o link para abrir o Merge Request na plataforma e o preview de conflito com a main.
- No modo **merge_local**, o botão **Integrar** faz `merge --no-ff` na main; sem conflito, worktree e branch são removidos e o card vai para `Integrada`.
- Com conflito → o card volta destacado com a lista de arquivos. Use **Atualizar branch** (traz a main para a branch) ou resolva manualmente no worktree.
