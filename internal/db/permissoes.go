package db

// Catálogo de permissões (capabilities) do RBAC. São validadas na aplicação (não
// por CHECK no banco) para não exigir migração a cada capacidade nova. Um papel
// é composto livremente a partir deste catálogo; um usuário acumula a UNIÃO das
// permissões de todos os seus papéis.
//
// PermVisualizar é o "básico": implícito a todo usuário autenticado (todas as
// leituras). Não precisa ser concedido por papel — não faz parte do catálogo
// atribuível. PermCuringa concede tudo e é exclusiva do papel de sistema `admin`.
const (
	PermVisualizar        = "visualizar"         // implícito: acompanhar andamento e métricas
	PermDemandasCriar     = "demandas.criar"     // criar demanda
	PermDemandasResponder = "demandas.responder" // chat, responder perguntas, editar fases, aprovar/rejeitar plano
	PermDemandasOperar    = "demandas.operar"    // pausar/retomar/cancelar; reordenar prioridade
	PermIntegracaoGerir   = "integracao.gerir"   // publicar_branch, integrar, atualizar_branch (worktree/merge)
	PermProjetosGerir     = "projetos.gerir"     // criar/editar projetos + config de projeto + grupos/overview
	PermConfigGerir       = "config.gerir"       // motores/contas e config global (configurações avançadas)
	PermUsuariosGerir     = "usuarios.gerir"     // usuários, papéis e tokens de API
	PermConsultasUsar     = "consultas.usar"     // abrir consultas e conversar com o consultor (produto/suporte)
	PermPlanejamentosUsar = "planejamentos.usar" // abrir planejamentos e lapidar PRDs/ADRs com o estrategista
	PermCodigoEditar      = "codigo.editar"      // abrir o IDE web (VS Code) no worktree de uma demanda
	PermCuringa           = "*"                  // todas (só o papel de sistema admin)
)

// Permissao descreve uma permissão atribuível para a UI montar as caixas de
// seleção de um papel (rótulo e explicação legíveis).
type Permissao struct {
	Chave     string `json:"chave"`
	Rotulo    string `json:"rotulo"`
	Descricao string `json:"descricao"`
}

// CatalogoPermissoes é a lista ORDENADA das permissões atribuíveis a um papel
// (não inclui PermVisualizar, implícito, nem PermCuringa, exclusiva do admin). É
// o que o endpoint GET /api/v1/permissions devolve.
var CatalogoPermissoes = []Permissao{
	{PermDemandasCriar, "Criar demandas", "Abrir novas demandas (chat/PRD)."},
	{PermDemandasResponder, "Responder demandas", "Conversar no chat, responder perguntas, editar fases e aprovar/rejeitar o plano."},
	{PermDemandasOperar, "Operar demandas", "Pausar, retomar, cancelar e reordenar a prioridade."},
	{PermIntegracaoGerir, "Integração", "Publicar branch, integrar (merge) e atualizar a branch / worktree."},
	{PermProjetosGerir, "Projetos", "Cadastrar e editar projetos, grupos de repositórios e overviews."},
	{PermConfigGerir, "Configurações avançadas", "Gerir motores, contas e a configuração global."},
	{PermUsuariosGerir, "Usuários e acessos", "Gerir usuários, papéis e tokens de API."},
	{PermConsultasUsar, "Consultar o código", "Conversar com o consultor sobre o comportamento do sistema (sem acesso ao código-fonte)."},
	{PermPlanejamentosUsar, "Planejar PRDs e ADRs", "Trabalhar com o estrategista na elaboração de PRDs e ADRs (os documentos e ADRs podem citar detalhes internos do código)."},
	{PermCodigoEditar, "Editar código (IDE web)", "Abrir o VS Code no navegador para ajustes manuais no worktree de uma demanda. O IDE dá acesso de desenvolvedor ao servidor (terminal incluído)."},
}

// permissoesAtribuiveis é o conjunto (para validação) das chaves do catálogo.
var permissoesAtribuiveis = func() map[string]bool {
	m := make(map[string]bool, len(CatalogoPermissoes))
	for _, p := range CatalogoPermissoes {
		m[p.Chave] = true
	}
	return m
}()

// PermissaoValida informa se p pode ser atribuída a um papel: uma chave do
// catálogo ou o curinga `*`. PermVisualizar não é atribuível (é implícita).
func PermissaoValida(p string) bool {
	return p == PermCuringa || permissoesAtribuiveis[p]
}
