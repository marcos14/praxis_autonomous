# 6. Integrando

- En el modo **merge_request**, la branch se publica en cada commit; la demanda concluida pasa a "Pronta para MR" (Lista para MR) con el enlace para abrir el Merge Request en la plataforma y la vista previa del conflicto con la main.
- En el modo **merge_local**, el botón **Integrar** hace `merge --no-ff` en la main; sin conflicto, worktree y branch se eliminan y el card va a `Integrada`.
- Con conflicto → el card vuelve destacado con la lista de archivos. Use **Atualizar branch** (Actualizar branch), que trae la main a la branch, o resuelva manualmente en el worktree.
