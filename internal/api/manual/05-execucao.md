# 5. Durante a execução

- Cada demanda roda em um **git worktree próprio**, numa branch `praxis/d<id>-<slug>`. Seu working tree nunca é tocado — continue trabalhando normal.
- Cada fase: executor → gates → correções (se preciso) → revisor → commit na branch. Acompanhe pelo log ao vivo no card.
- Pode **pausar** ou **cancelar** a qualquer momento pelo card. Pausa é retomável do ponto em que parou.
- Franquia do motor esgotou? O Praxis troca para o próximo motor da prioridade, ou espera o reset — o card mostra o estado.
