package db

import "strings"

// Visibilidade de consultas, planejamentos e demandas (coluna `visibilidade`,
// migração 17 — M2 do PLANO_INTERNET).
const (
	VisibilidadePrivada = "privada" // só o criador (e quem ignora a regra: admin, token de API)
	VisibilidadeGrupo   = "grupo"   // criador + quem está no MESMO grupo de usuários que ele
	VisibilidadePublica = "publica" // todos os usuários autenticados
)

// VisibilidadeValida informa se v é uma visibilidade conhecida.
func VisibilidadeValida(v string) bool {
	switch v {
	case VisibilidadePrivada, VisibilidadeGrupo, VisibilidadePublica:
		return true
	}
	return false
}

// Como itens SEM criador (token de API, modo bootstrap) aparecem para quem não
// ignora a regra de dono — config global `sem_dono_visibilidade`.
const (
	SemDonoAdmins  = "admins"  // só admin vê (padrão)
	SemDonoGrupo   = "grupo"   // membros do grupo de usuários `sem_dono_grupo_id`
	SemDonoPublica = "publica" // todos
)

// SemDonoValido informa se v é um modo conhecido.
func SemDonoValido(v string) bool {
	switch v {
	case SemDonoAdmins, SemDonoGrupo, SemDonoPublica:
		return true
	}
	return false
}

// Escopos de listagem (filtro "Meus · Grupo · Todos" da UI).
const (
	EscopoMeus  = "meus"  // só o que o usuário criou
	EscopoGrupo = "grupo" // criado por membros do grupo do usuário (ele incluso)
	EscopoTodos = "todos" // tudo que a regra deixa ver (padrão)
)

// EscopoValido informa se e é um escopo conhecido ("" vale como todos).
func EscopoValido(e string) bool {
	switch strings.TrimSpace(e) {
	case "", EscopoMeus, EscopoGrupo, EscopoTodos:
		return true
	}
	return false
}

// Visao é o ponto de vista de quem lê: as restrições que as listagens e as
// checagens por id aplicam. O valor zero é a visão total (nenhuma restrição),
// usada por chamadores internos (scheduler, despachante) e por quem enxerga
// tudo. A camada da API monta a Visao a partir do principal da requisição.
type Visao struct {
	// Usuario é o id do usuário da requisição (0 = token de API/bootstrap). É a
	// base do escopo meus/grupo mesmo para quem ignora a regra de dono (admin).
	Usuario int64
	// ACL, quando não-nil, restringe aos projetos visíveis pela ACL de projeto
	// (project_access) para este usuário. nil = ignora a ACL (projetos.gerir,
	// token de API, bootstrap) — mesma semântica do antigo visiveisPara.
	ACL *int64
	// Dono, quando não-nil, aplica a regra de dono para este usuário: vê o que
	// criou, o que é público, o que é do grupo dele e os itens sem dono conforme
	// SemDono. nil = ignora a regra (admin `*`, token de API, bootstrap).
	Dono *int64
	// Escopo estreita a listagem: EscopoMeus, EscopoGrupo ou ""/EscopoTodos.
	Escopo string
	// SemDono e SemDonoGrupo espelham a config global sem_dono_visibilidade /
	// sem_dono_grupo_id; só importam quando Dono não é nil.
	SemDono      string
	SemDonoGrupo int64
}

// condDono devolve a condição SQL da regra de dono para a tabela/alias tab
// (colunas tab.criado_por e tab.visibilidade). Consome os argumentos de
// argsDono, nesta ordem.
func condDono(tab string) string {
	return `(` + tab + `.criado_por = ?
		OR ` + tab + `.visibilidade = 'publica'
		OR (` + tab + `.visibilidade = 'grupo' AND EXISTS (
			SELECT 1 FROM user_group_members m1
			JOIN user_group_members m2 ON m2.group_id = m1.group_id
			WHERE m1.user_id = ` + tab + `.criado_por AND m2.user_id = ?))
		OR (` + tab + `.criado_por IS NULL AND (
			? = 'publica'
			OR (? = 'grupo' AND EXISTS (
				SELECT 1 FROM user_group_members m WHERE m.group_id = ? AND m.user_id = ?)))))`
}

// argsDono são os argumentos consumidos por condDono (v.Dono não pode ser nil).
func argsDono(v Visao) []any {
	uid := *v.Dono
	semDono := v.SemDono
	if !SemDonoValido(semDono) {
		semDono = SemDonoAdmins // config ausente/ilegível: o mais restritivo
	}
	return []any{uid, uid, semDono, semDono, v.SemDonoGrupo, uid}
}

// anexarCondDono junta a regra de dono ao WHERE de uma listagem quando a visão
// a aplica (v.Dono não-nil).
func anexarCondDono(cond []string, args []any, tab string, v Visao) ([]string, []any) {
	if v.Dono == nil {
		return cond, args
	}
	return append(cond, condDono(tab)), append(args, argsDono(v)...)
}

// anexarCondEscopo junta o escopo meus/grupo ao WHERE de uma listagem. Sem
// usuário (token de API) ou com escopo vazio/todos, não filtra. O escopo
// estreita; a regra de dono (anexarCondDono) continua valendo por cima.
func anexarCondEscopo(cond []string, args []any, tab string, v Visao) ([]string, []any) {
	if v.Usuario <= 0 {
		return cond, args
	}
	switch strings.TrimSpace(v.Escopo) {
	case EscopoMeus:
		return append(cond, tab+".criado_por = ?"), append(args, v.Usuario)
	case EscopoGrupo:
		return append(cond, `(`+tab+`.criado_por = ? OR `+tab+`.criado_por IN (
			SELECT m2.user_id FROM user_group_members m1
			JOIN user_group_members m2 ON m2.group_id = m1.group_id
			WHERE m1.user_id = ?))`), append(args, v.Usuario, v.Usuario)
	}
	return cond, args
}
