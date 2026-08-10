package api

// Fase A do PLANO_MULTIUSUARIO.md — donos e visibilidade.
//
// Projetos e motores ganham um DONO (quem os criou) e uma visibilidade simples
// escolhida no cadastro: "publica" (todos os usuários autenticados), "privada"
// (só o dono) ou "grupo" (um grupo de usuários). A visibilidade é a fachada
// amigável sobre as ACLs (project_access/engine_access): cada valor vira o
// conjunto de linhas correspondente; a ACL fina de projetos (PUT /access, com
// vários usuários e grupos) continua existindo para quem tem projetos.gerir.

import (
	"fmt"
	"net/http"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// visibilidadesValidas são os valores aceitos no campo `visibilidade`.
var visibilidadesValidas = map[string]bool{
	db.VisibilidadePublica: true,
	db.VisibilidadePrivada: true,
	db.VisibilidadeGrupo:   true,
}

// resolverVisibilidadeACL traduz (visibilidade, grupoID) nas listas de ACL a
// gravar. owner é o dono do recurso (âncora do "privada" e liberado junto no
// "grupo", para continuar vendo o recurso se sair do grupo). Quando o chamador
// NÃO pode apontar qualquer grupo (usuário comum criando projeto), o grupo é
// validado contra o grupo do próprio usuário. Devolve msg != "" em erro de
// validação (mesmo contrato dos montarX).
func (s *Servidor) resolverVisibilidadeACL(r *http.Request, visibilidade string, grupoID, owner *int64,
	qualquerGrupo bool) (usuarios, grupos []int64, msg string) {

	switch visibilidade {
	case db.VisibilidadePublica:
		return []int64{}, []int64{}, ""

	case db.VisibilidadePrivada:
		if owner == nil {
			return nil, nil, "visibilidade privada exige um usuário logado (tokens de API e o modo bootstrap não têm dono)"
		}
		return []int64{*owner}, []int64{}, ""

	case db.VisibilidadeGrupo:
		gid := grupoID
		if gid == nil {
			// Sem grupo explícito: o grupo do próprio criador.
			if owner == nil {
				return nil, nil, "visibilidade de grupo exige um usuário logado ou um grupo_id explícito"
			}
			g, ok, err := s.banco.GrupoDoUsuario(r.Context(), *owner)
			if err != nil {
				return nil, nil, "resolver o grupo do usuário: " + err.Error()
			}
			if !ok {
				return nil, nil, "você não pertence a nenhum grupo de usuários; informe grupo_id ou peça a um administrador"
			}
			gid = &g.ID
		} else if !qualquerGrupo {
			// Usuário comum só compartilha com o PRÓPRIO grupo.
			if owner == nil {
				return nil, nil, "visibilidade de grupo exige um usuário logado"
			}
			g, ok, err := s.banco.GrupoDoUsuario(r.Context(), *owner)
			if err != nil {
				return nil, nil, "resolver o grupo do usuário: " + err.Error()
			}
			if !ok || g.ID != *gid {
				return nil, nil, fmt.Sprintf("grupo_id %d não é o seu grupo; só administradores compartilham com outros grupos", *gid)
			}
		}
		if owner != nil {
			usuarios = []int64{*owner}
		} else {
			usuarios = []int64{}
		}
		return usuarios, []int64{*gid}, ""
	}
	return nil, nil, "visibilidade deve ser 'publica', 'privada' ou 'grupo'"
}

// podeGerirProjeto informa se o principal pode alterar o projeto: quem tem
// projetos.gerir (ou admin) gerencia qualquer um; o DONO gerencia o próprio.
func podeGerirProjeto(r *http.Request, p db.Projeto) bool {
	if temPermissao(r, db.PermProjetosGerir) {
		return true
	}
	uid := usuarioDaRequisicao(r)
	return uid != nil && p.OwnerUserID != nil && *uid == *p.OwnerUserID
}

// exigirGestaoProjeto responde 403 quando o principal não pode alterar o
// projeto; devolve true quando pode (sem escrever nada).
func exigirGestaoProjeto(w http.ResponseWriter, r *http.Request, p db.Projeto) bool {
	if podeGerirProjeto(r, p) {
		return true
	}
	erroT(w, r, http.StatusForbidden, "sem_permissao", "erro.sem_permissao", "permissao", db.PermProjetosGerir)
	return false
}

// filtroVisibilidadeMotores decide como filtrar as leituras de motores para o
// principal: quem tem config.gerir (ou admin) enxerga todos (restringe=false);
// um usuário comum enxerga públicos + os liberados a ele (uid); um principal
// sem usuário (token não-admin) enxerga só os públicos (uid=nil).
func filtroVisibilidadeMotores(pr *principal) (uid *int64, restringe bool) {
	if pr == nil || pr.tem(db.PermConfigGerir) {
		return nil, false
	}
	if pr.userID > 0 {
		u := pr.userID
		return &u, true
	}
	return nil, true
}

// motoresVisiveisAoPrincipal filtra a lista de motores conforme o principal da
// requisição (sem filtro para quem tem config.gerir).
func (s *Servidor) motoresVisiveisAoPrincipal(r *http.Request, motores []db.Motor) ([]db.Motor, error) {
	uid, restringe := filtroVisibilidadeMotores(principalDaRequisicao(r))
	if !restringe {
		return motores, nil
	}
	return s.banco.FiltrarMotoresVisiveis(r.Context(), motores, uid)
}
