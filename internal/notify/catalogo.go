package notify

// TiposConhecidos é o catálogo dos tipos de evento que o backend registra na
// tabela events — espelho, na mesma ordem, de GRUPOS_EVENTOS em
// web/js/notify-events.js (o teste TestCatalogoEspelhaOJS garante a paridade).
// A API de preferências só aceita tipos daqui.
var TiposConhecidos = []string{
	// demandas
	"demanda_criada", "demanda_pausada", "demanda_retomada", "demanda_cancelada",
	"demanda_concluida", "demanda_integrada", "aguardando_humano",
	// fases
	"fase_iniciada", "fase_concluida", "fase_falhou", "fase_nova", "fase_pausada",
	"gates_falharam", "revisor_reprovou", "correcao_iniciada", "troca_de_harness",
	"franquia_esgotada", "worktree_criado",
	// planejamento (intake)
	"analise_iniciada", "analise_concluida", "planejamento_iniciado", "planejamento_concluido",
	"planejamento_falhou", "analise_falhou", "plano_aprovado", "plano_rejeitado", "respostas_recebidas",
	// git
	"branch_publicada", "branch_atualizada", "push_falhou", "merge_falhou",
	// consultas e sistema
	"consulta_respondida", "consulta_falhou", "estrategia_respondida", "estrategia_falhou",
	"overview_gerado", "overview_falhou", "projeto_criado", "config_alterada",
	"recuperada_pos_restart", "codigo_acessado", "aviso",
}

// TipoConhecido diz se o tipo está no catálogo.
func TipoConhecido(tipo string) bool {
	for _, t := range TiposConhecidos {
		if t == tipo {
			return true
		}
	}
	return false
}
