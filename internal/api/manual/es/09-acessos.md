# 9. Usuarios, roles y acceso a proyectos

## Usuarios y roles

- El primer acceso al Praxis pide la creación del **primer administrador**; a partir de ahí todo acceso exige login.
- En `Usuários` (Usuarios) usted registra a las personas y les atribuye **papéis** (roles). Cada rol es un conjunto de permisos (crear demandas, responder, operar, integrar, gestionar proyectos, consultas, etc.) — arme roles como "Desenvolvedor", "Produto" o "Suporte" solo con lo necesario.
- Quien todavía no tiene ningún rol igual consigue **visualizar** (acompañar avance y métricas); lo que se bloquea son las acciones.

## Grupos de usuarios

En `Grupos de usuários` (Grupos de usuarios) usted agrupa personas (cada usuario pertenece como máximo a un grupo, definido en el registro del usuario). El grupo sirve para dos cosas:

1. **Consultas**: fijar el motor/modelo de las consultas de los miembros (ej.: grupo "Suporte" con un modelo económico) — ver la sección Consultas.
2. **Acceso a proyectos**: habilitar proyectos restringidos para todos los miembros de una vez, como se describe abajo.

## Acceso a proyectos (quién ve qué)

Por defecto **todo proyecto es visible para todos los usuarios autenticados**. Cuando más personas de la empresa pasan a usar el Praxis, usted puede restringir proyecto por proyecto:

1. Abra el proyecto en `Projetos` (Proyectos) y vaya a la sección **Acesso — quem enxerga este projeto** (Acceso — quién ve este proyecto).
2. Seleccione los **grupos de usuarios** y/o **usuarios** habilitados y guarde.
   - Nada seleccionado = proyecto abierto a todos (el valor por defecto).
   - Con selección, solo los habilitados ven el proyecto — además de los **administradores** y de quien tiene el permiso **Projetos** (`projetos.gerir`), que siempre ven todo. Los tokens de API (integraciones) tampoco se filtran.

La restricción vale para el producto entero, no solo para la lista de proyectos: demandas (kanban, cards, chat, logs, diff), pendientes y métricas de la Home, actividad reciente y eventos en vivo, y las Consultas del proyecto. Para quien no fue habilitado, es como si el proyecto no existiera.

Notas:

- Un **grupo de repositorios** (pantalla `Grupos`, usada en las Consultas) solo aparece para quien ve **todos** sus proyectos — un único proyecto restringido esconde el grupo entero.
- Si todos los usuarios/grupos de una restricción se eliminan del sistema, el proyecto vuelve a quedar abierto a todos.
- La sección Acesso solo aparece/guarda para quien tiene el permiso **Projetos** (`projetos.gerir`).

## Su cuenta y sesiones

- Haga clic en su nombre (o en `Mi cuenta`) al pie del menú para cambiar la contraseña, elegir el idioma y ver **dónde ha iniciado sesión**.
- Permanece conectado mientras use Praxis: la sesión del navegador se renueva sola y solo caduca tras un período sin uso (predeterminado 30 días) o al alcanzar la duración máxima (predeterminado 90 días) — el administrador ajusta ambos en `Configuración → Sesiones e inicio de sesión`.
- Si la sesión caduca con una pantalla abierta, el inicio de sesión aparece encima: entre de nuevo y continúe donde estaba (lo que había escrito se conserva).
- Cada inicio de sesión (navegador, móvil) es una **sesión**. En `Mi cuenta` puede cerrar una sesión que no reconozca o **todas las demás** a la vez; cambiar la contraseña también cierra las demás.
- `Salir` cierra la sesión de este navegador en el servidor.
- Tras 10 contraseñas incorrectas en 15 minutos, el inicio de sesión queda bloqueado unos minutos (el aviso indica cuánto).
