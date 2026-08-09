# 5. During execution

- Each demand runs in its **own git worktree**, on a `praxis/d<id>-<slug>` branch. Your working tree is never touched — keep working as usual.
- Each phase: executor → gates → fixes (if needed) → reviewer → commit on the branch. Follow along with the live log on the card.
- You can **pausar** (pause) or **cancelar** (cancel) at any moment from the card. A pause can be resumed from the point where it stopped.
- Demand **falhou** (failed) — e.g. the run blew through the engine's budget? Fix the cause — for instance, increase the budget on the Motores (Engines) screen — and use **Tentar novamente** (Retry) on the card: the demand resumes from the stage that failed (analysis, planning or the phase that stopped), without losing what has already been done.
- Engine quota exhausted? Praxis switches to the next engine in the priority order, or waits for the reset — the card shows the state.
