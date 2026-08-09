# 10. Planificaciones (PRD y ADRs con el estratega)

La pantalla **Planejamentos** (Planificaciones) es el espacio de PMs, POs y arquitectos: usted describe una necesidad y el **estratega** — leyendo el código de los repositorios en modo solo lectura — pule con usted un **PRD** (visión de negocio), **ADRs** (decisiones arquitecturales) o ambos, en conversación iterativa. El resultado es un documento fundamentado en lo que el sistema realmente es hoy, listo para convertirse en demanda con un clic.

## Para qué sirve

- **PRD completo antes de la demanda**: en vez de pegar un PRD crudo en la Nova demanda (Nueva demanda), llegue allí con requisitos, criterios de aceptación y cuestiones ya decididas — menos rondas de preguntas del analista, mejor plan.
- **ADRs para decisiones de arquitectura**: registrar contexto, decisión, alternativas y consecuencias — con el estratega confirmando en el código las restricciones reales.
- **Visualizar el plan**: además del texto, el estratega genera una **presentación visual** (infografías, diagramas de flujo) y, si usted lo pide, un **prototipo navegable** de las pantallas propuestas — para alinear la visión antes de gastar desarrollo.

## Cómo usar

1. Vaya a `Planejamentos → Novo planejamento` (Nueva planificación), elija un **projeto** (proyecto) o un **grupo**, el **foco** (PRD, ADRs o ambos) y el **nível visual** (nivel visual):
   - **Documento** — solo los `.md`;
   - **Apresentação** (Presentación, valor por defecto) — + un HTML con resumen ejecutivo, infografías y diagramas de flujo;
   - **Protótipo** (Prototipo) — + una simulación navegable de las pantallas/flujos propuestos.
2. Describa la necesidad a su manera. Si algo es ambiguo, el estratega hace **preguntas de decisión** antes de escribir — responda en el chat.
3. En cada turno, los documentos aparecen en la pestaña **Documentos** (con historial de revisiones) y los artefactos en la pestaña **Artefatos** (Artefactos), que abren en una pestaña nueva. Pida cambios en el chat hasta que el documento quede redondo.
4. Usted puede **cambiar el foco y el nivel visual en medio de la conversación** — el turno siguiente ya obedece.
5. Cuando el PRD esté listo, haga clic en **Criar demanda** (Crear demanda): el documento se vuelve el primer mensaje de una demanda nueva (con los ADRs adjuntos, cuando los haya) y el analista del intake asume desde ahí. El card de la demanda abre al instante, y la planificación registra el vínculo **con la revisión entregada**.

### Una planificación, varias demandas

La planificación es un documento vivo y puede generar **cuantas demandas usted necesite** — rehacer desde cero, probar una variante (A/B), entregar en otro repositorio del grupo. El botón **Demandas (N)** arriba abre el diálogo de handoff, que muestra:

- las demandas ya generadas, con el **estado** de cada una y **qué revisión** del PRD/ADRs recibió;
- el **drift**: si los documentos evolucionaron desde la última entrega, el diálogo avisa que hay cambios no entregados;
- un **aviso cuando hay demanda activa** — crear otra del mismo plan puede generar trabajo superpuesto (no se bloquea: la prueba A/B es legítima, y el Kanban señala la superposición de archivos entre demandas).

La demanda creada aquí es siempre **completa** (todo el documento actual). La demanda **complementaria** — solo lo que evolucionó desde una entrega concluida, con PRD incremental generado por el estratega — llegará en una próxima versión.

## Trabajando por etapas (Producto ↔ Arquitectura)

La planificación fue diseñada para ser **colaborativa y por etapas** — no hace falta decidir todo en la creación:

1. **El PO empieza** con foco **PRD** y pule el documento con el estratega hasta cerrar la visión de negocio.
2. **El arquitecto continúa en la MISMA planificación**: cambia el campo *Produzir* (Producir) a **PRD + ADRs** (o solo ADRs) y manda sus puntos en el chat. El estratega mantiene el `prd.md` intacto, pasa a producir el `adrs.md` — y mantiene ambos consistentes (si una decisión arquitectural contradice el PRD, lo señala).
3. Cada habla del chat muestra **quién habló** (el nombre del usuario logueado), así que la conversación registra la discusión entera: lo que vino de producto, lo que vino de arquitectura.
4. Cualquiera de los dos (con el permiso `demandas.criar`) hace clic en **Criar demanda** cuando el conjunto esté listo — el PRD va con los ADRs adjuntos.

El orden inverso también funciona (arquitecto primero, producto después), y nada impide una planificación solo de ADRs de principio a fin.

## Referências (Referencias — adjuntar documentos de apoyo)

Usted adjunta el material que el estratega debe tomar como base — una **ADR de otro proyecto** para reaprovechar como modelo, la **transcripción de una reunión** que origina el PRD, borradores, planillas de requisitos — en tres lugares:

- **en la creación** (campo *Referências* del formulario Novo planejamento): el primer turno ya parte de ellas — el Praxis retiene al estratega hasta que los archivos suban;
- **en el clip 📎** al lado del campo de mensaje, sin salir de la conversación;
- **en la pestaña Referências** de la planificación abierta, donde también se pueden descargar y eliminar.

Acepta `md`, `txt`, `csv`, `json`, `pdf`, `html` e imágenes (hasta 15 MB cada una).

- Cada adjunto (y su remoción) se vuelve una habla de sistema en el chat, y el turno siguiente del estratega recibe la lista — mencione en el chat qué quiere que haga con cada una ("use la ADR-007 adjunta como modelo de formato").
- Las referencias son **insumo solo lectura**: el estratega las consulta, pero nunca las altera. Quedan en la carpeta de la planificación (subcarpeta `referencias/`) y se pueden descargar de vuelta o eliminar en cualquier momento.

## Documentos y artefactos

- Los `.md` son la **fuente de la verdad** y quedan versionados en la base de datos — cada turno que altera un documento graba una revisión nueva, y usted puede releer cualquier revisión antigua.
- Los artefactos `.html` son capa de presentación, siempre regenerados a partir de los documentos. Son **archivos autocontenidos** (funcionan offline, sin CDN) grabados en `PRAXIS_HOME/planejamentos/p<id>/`.
- Al abrir un artefacto, este corre en una **sandbox del navegador**: el JavaScript del artefacto no tiene acceso a su sesión del Praxis ni a la API — es solo presentación.
- Todo puede ser **descargado a su máquina**: los documentos por el botón *Baixar .md* (Descargar .md, en la revisión exhibida) y los artefactos por el botón *Baixar* (Descargar) — como son autocontenidos, el HTML descargado funciona abierto directamente desde el disco, listo para adjuntar en un e-mail o presentar.

## Motor y modelo

Las planificaciones usan la misma resolución de motor de las consultas (campo **Modelo para consultas** del motor; el grupo de usuarios del creador puede fijar motor/modelo propios). Como es trabajo de análisis y escritura — no de código de producción — un modelo más económico suele bastar.

## Seguridad

- El acceso se controla por el permiso `planejamentos.usar`. **Atención**: a diferencia de las Consultas, aquí NO hay filtro anticódigo — las ADRs citan componentes, rutas y tecnologías por definición. Conceda el permiso a quien pueda ver detalles internos de la arquitectura.
- El estratega **nunca modifica los repositorios**: corre con escritura confinada a la carpeta de la planificación, los repositorios entran como solo lectura y, como red de seguridad final, el Praxis compara el `git status` de cada repositorio antes y después del turno — cualquier alteración detectada falla el turno al instante y lista los archivos para que usted verifique (nada se revierte automáticamente).
- Las planificaciones respetan el **acceso a proyectos** (ACL), como las consultas: un proyecto restringido no aparece para quien no fue habilitado.
- Eliminar una planificación borra la conversación, los documentos, los artefactos y la carpeta de trabajo.
