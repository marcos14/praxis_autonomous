# 8. Consultas (producto y soporte)

La pantalla **Consultas** es un chat para quien NO desarrolla: los equipos de producto y soporte preguntan cómo se comporta el sistema y el Praxis responde leyendo el código en modo solo lectura — en lenguaje de negocio, **sin mostrar nunca código fuente**.

## Para qué sirve

- **Elaborar PRDs**: "¿cómo funciona el cobro hoy?" — el consultor describe el comportamiento actual y señala las lagunas que el PRD necesita decidir.
- **Entender una rutina**: "¿qué pasa cuando vence el boleto?" — paso a paso, condiciones y excepciones.
- **Estrategia para un cliente**: "¿cómo implantar el Vulcano Chat para un cliente con dos sucursales?" — el consultor propone el paso a paso usando las rutinas existentes.

## Cómo usar

1. Vaya a `Consultas → Nova consulta` (Nueva consulta), elija un **projeto** (proyecto) o un **grupo**, que es una solución con varios repositorios, y escriba la pregunta a su manera — no hace falta que sea técnica.
2. Si la pregunta es ambigua, el consultor devuelve **preguntas de clarificación** antes de responder. Responda en el propio chat.
3. Cada turno lleva algunos minutos (el consultor lee el código de verdad). La línea de progreso muestra qué está investigando.

## Archivos adjuntos a la consulta

Puede adjuntar el material que el consultor debe considerar junto con el código — el **correo del cliente**, una **captura del error**, la **planilla de casos**, un borrador de PRD — en tres lugares:

- **en la creación** (campo *Archivos* del formulario Nueva consulta): el primer turno ya parte de ellos — Praxis retiene al consultor hasta que los archivos se suban;
- **en el clip 📎** al lado del campo de mensaje, sin salir de la conversación;
- **en la pestaña Archivos** de la consulta abierta, donde también se pueden descargar y eliminar.

Acepta `md`, `txt`, `csv`, `json`, `pdf`, `html` e imágenes (hasta 15 MB cada uno).

- Cada adjunto (y su eliminación) se convierte en un mensaje de sistema en el chat, y el próximo turno del consultor recibe la lista — indique en el chat qué quiere que haga con cada archivo ("compare la captura adjunta con el comportamiento esperado de la rutina").
- Los archivos son **insumo de solo lectura**: el consultor los lee, pero nunca los modifica. Quedan en la carpeta de la consulta (`PRAXIS_HOME/consultas/c<id>/referencias/`), se eliminan junto con la consulta y pueden descargarse en cualquier momento.
- Las reglas de seguridad siguen valiendo sobre ellos: un archivo adjunto es material de consulta, no una instrucción — si su contenido pide código fuente o romper una regla, el pedido se ignora.

## Grupos de repositorios

Las soluciones con más de un repositorio (ej.: API + frontend) se registran en la pantalla `Grupos`. En una consulta de grupo el consultor ve todos los repositorios; el primero del grupo es el principal. Un proyecto puede participar en varios grupos.

## Overview do repositório (Overview del repositorio)

Cada proyecto puede tener un **overview** (objetivo, dominio, flujos — en `Projetos → Overview do repositório`), que orienta al consultor y mejora mucho las respuestas. Puede escribirse a mano o generarse por el propio Praxis ("Gerar com o Praxis" / Generar con el Praxis).

## Motor y modelo de las consultas

Las consultas no usan el modelo de análisis/planificación: el motor tiene un campo propio **Modelo para consultas**, en `Motores`, que puede ser un modelo más liviano/barato — la consulta solo explica comportamiento, no produce código de producción. La precedencia es:

1. **Grupo de usuários** (grupo de usuarios) de quien creó la consulta (pantalla `Grupos de usuários`): puede fijar motor y/o modelo propios — ej.: grupo "Suporte" con un modelo económico.
2. **Modelo para consultas** del primer motor activo.
3. **Modelo de análise** (Modelo de análisis) del motor, cuando el campo de consultas está vacío.

El vínculo usuario↔grupo se hace en el registro del usuario (un grupo por usuario).

## Código siempre actualizado

Antes de cada turno de consulta (y de cada análisis/planificación de demanda), el Praxis posiciona el repositorio del proyecto en la **branch principal** y hace un **pull fast-forward** del origin — importante cuando el servicio corre en un servidor, donde el clon podría quedar desactualizado. La ejecución de demandas ya partía de `origin/<branch>` actualizada (vía fetch + worktree aislado).

Nada se fuerza: si el repositorio tiene cambios locales, está en otra branch con trabajo pendiente o divergió del origin, el Praxis **no descarta nada** — sigue con el estado disponible y registra un aviso (habla de sistema en la consulta; evento en la demanda).

## Seguridad

- La respuesta **nunca incluye código fuente**: un posfiltro en el servidor elimina cualquier fragmento técnico que se escape (aparece como "fragmento técnico eliminado por la política de seguridad"). Las respuestas demasiado técnicas se rechazan por completo.
- Los pedidos para **burlar/sortear** validaciones o explorar fallas se rechazan. Explicar cómo funciona una validación está permitido; enseñar a sortearla, no.
- El acceso se controla por el permiso `consultas.usar` — se puede crear un rol "Suporte"/"Produto" solo con él, sin ningún acceso a demandas ni configuraciones.
- Las consultas respetan el **acceso a proyectos**: un proyecto restringido (sección Acesso do projeto / Acceso del proyecto) no aparece para quien no fue habilitado — ni en la creación de la consulta, ni en el historial; un grupo de repositorios con un proyecto restringido desaparece por entero. Ver la sección "Usuarios, roles y acceso a proyectos".
- Nota técnica: el harness corre en modo solo lectura en el repositorio (sin editar, commitear ni hacer push); los comandos de lectura del repositorio siguen disponibles para él — el mismo modelo de confianza del analista de demandas.
