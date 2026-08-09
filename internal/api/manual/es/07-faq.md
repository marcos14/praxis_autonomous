# Preguntas frecuentes

- **¿Dos demandas pueden tocar el mismo archivo?** Pueden — cada una en su branch. El conflicto aparece en la integración; el kanban muestra antes un aviso de superposición.
- **¿Dónde quedan los datos?** Todo en la base de datos del Praxis (fuera de los proyectos): PRD, chat, preguntas, planes, fases, costos y logs. En los proyectos, solo los commits en las branches.
- **¿Y el costo?** Cada demanda tiene un tope de budget. Si lo supera → pausa y avisa. La Home muestra el gasto por día y por proyecto.
- **Un run superó el budget y la demanda falló. ¿Perdí todo?** No. Aumente el budget del motor (pantalla **Motores**) y haga clic en **Tentar novamente** (Intentar de nuevo) en el card — la demanda retoma desde la etapa que falló (análisis, planificación o la fase interrumpida).
