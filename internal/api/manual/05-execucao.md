# 5. Durante a execução

- Cada demanda roda em um **git worktree próprio**, numa branch `praxis/d<id>-<slug>`. Seu working tree nunca é tocado — continue trabalhando normal.
- Cada fase: executor → gates → correções (se preciso) → revisor → commit na branch. Acompanhe pelo log ao vivo no card.
- Pode **pausar** ou **cancelar** a qualquer momento pelo card. Pausa é retomável do ponto em que parou.
- Demanda **falhou** (ex.: run estourou o budget do motor)? Corrija a causa — p.ex. aumente o budget na tela Motores — e use **Tentar novamente** no card: a demanda retoma do estágio que falhou (análise, planejamento ou a fase que parou), sem perder o que já foi feito.
- Franquia do motor esgotou? O Praxis troca para o próximo motor da prioridade, ou espera o reset — o card mostra o estado.
