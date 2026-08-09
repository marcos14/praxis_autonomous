# 5. Durante la ejecución

- Cada demanda se ejecuta en un **git worktree propio**, en una branch `praxis/d<id>-<slug>`. Su working tree nunca es tocado — siga trabajando normalmente.
- Cada fase: ejecutor → gates → correcciones (si es necesario) → revisor → commit en la branch. Acompañe por el log en vivo en el card.
- Puede **pausar** o **cancelar** en cualquier momento desde el card. La pausa es reanudable desde el punto en que se detuvo.
- ¿La demanda **falló** (p. ej.: el run superó el budget del motor)? Corrija la causa — p. ej. aumente el budget en la pantalla `Motores` — y use **Tentar novamente** (Reintentar) en el card: la demanda se reanuda desde la etapa que falló (análisis, planificación o la fase que se detuvo), sin perder lo que ya se hizo.
- ¿Se agotó la cuota del motor? Praxis cambia al siguiente motor de la prioridad, o espera el reset — el card muestra el estado.
