# 9. Users, roles and project access

## Users and roles

- The first access to Praxis asks you to create the **first administrator**; from then on every access requires a login.
- Under `Usuários` (Users) you register people and assign **roles**. Each role is a set of permissions (create demands, reply, operate, merge, manage projects, queries and so on) — build roles such as "Desenvolvedor", "Produto" or "Suporte" with only what is needed.
- Anyone with no role at all can still **view** (follow progress and metrics); it is the actions that are blocked.

## User groups

Under `Grupos de usuários` (User groups) you group people (each user belongs to at most one group, defined in the user's registration). The group serves two purposes:

1. **Queries**: pinning the engine/model for its members' queries (e.g. a "Suporte" group with an economical model) — see the Queries section.
2. **Project access**: granting restricted projects to every member at once, as described below.

## Project access (who sees what)

By default **every project is visible to all authenticated users**. As more people in the company start using Praxis, you can restrict it project by project:

1. Open the project under `Projetos` (Projects) and go to the **Acesso — quem enxerga este projeto** (Access — who sees this project) section.
2. Select the **user groups** and/or **users** that are granted access and save.
   - Nothing selected = project open to everyone (the default).
   - With a selection, only those granted access see the project — besides the **administrators** and anyone holding the **Projetos** permission (`projetos.gerir`), who always see everything. API tokens (integrations) are not filtered either.

The restriction applies to the whole product, not just to the project list: demands (kanban, cards, chat, logs, diff), pending items and Home metrics, recent activity and live events, and the project's queries. For anyone not granted access, it is as if the project did not exist.

Notes:

- A **repository group** (`Grupos` screen, used in the queries) only appears to those who see **all** of its projects — a single restricted project hides the whole group.
- If every user/group in a restriction is deleted from the system, the project becomes open to everyone again.
- The Acesso section only appears/saves for those holding the **Projetos** permission (`projetos.gerir`).

## Your account and sessions

- Click your name (or `My account`) at the bottom of the menu to change your password, pick the language and see **where you are signed in**.
- You stay signed in while you use Praxis: the browser session renews itself and only ends after a period without use (default 30 days) or when it reaches the maximum length (default 90 days) — the administrator sets both in `Settings → Sessions and sign-in`.
- If the session ends while a screen is open, the sign-in form appears on top of it: sign in again and carry on where you were (what you had typed stays).
- Each sign-in (browser, phone) is a **session**. In `My account` you can end a session you do not recognize, or **all the others** at once; changing the password also ends the others.
- `Sign out` ends this browser's session on the server.
- After 10 wrong passwords in 15 minutes, sign-in is blocked for a few minutes (the message says how long).

## Who sees each query, planning and demand

Besides project access, every query, planning and demand has a **visibility**, chosen at creation (and changeable later by the creator or an administrator, in the panel or card):

- 🔒 **Private** — only you and administrators.
- 👥 **Group** — you and everyone in your user group (set on the user record; without a group the option is disabled).
- 🌐 **Public** — every user who can see the project.

New items start private; Praxis remembers your last choice. Lists, the kanban and Home show only what you can see, with the visibility pill and the author when it is not you. The **All · Mine · My group** filter narrows the list. Administrators see everything. Items that existed before this version were made public. Items created by integrations (API tokens) have no owner: the administrator decides in `Settings → Visibility` whether only administrators, one group or everyone sees them.
