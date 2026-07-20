package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// janelaGraficoDias é o número de dias exibidos no gráfico de gastos da Home.
const janelaGraficoDias = 14

// registrarRotasHome registra as rotas da Home (Fase 4b): métricas agregadas, a
// lista "Precisa de você" e a atividade recente (backlog do SSE global).
func (s *Servidor) registrarRotasHome(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/metrics", s.handleMetricas)
	mux.HandleFunc("GET /api/v1/pendencias", s.handlePendencias)
	mux.HandleFunc("GET /api/v1/activity", s.handleAtividade)
}

// respMetricas é o payload dos tiles + gráfico + tabela por projeto da Home.
type respMetricas struct {
	db.ResumoHome
	Hoje string `json:"hoje"` // 'YYYY-MM-DD' de referência (fuso local do servidor)
}

// handleMetricas devolve os agregados da Home. As datas de corte são derivadas
// de agora (fuso local do servidor): início do mês, hoje-7 e a janela do
// gráfico. O parâmetro ?periodo= é aceito por compatibilidade com a rota do
// plano, mas hoje só a Home padrão é calculada.
func (s *Servidor) handleMetricas(w http.ResponseWriter, r *http.Request) {
	agora := time.Now()
	inicioMes := time.Date(agora.Year(), agora.Month(), 1, 0, 0, 0, 0, agora.Location())
	corte7d := agora.AddDate(0, 0, -7)
	corteGrafico := agora.AddDate(0, 0, -(janelaGraficoDias - 1))

	const iso = "2006-01-02"
	resumo, err := s.banco.ResumoHome(r.Context(),
		inicioMes.Format(iso), corte7d.Format(iso), corteGrafico.Format(iso),
		visibilidadeDaRequisicao(r))
	if err != nil {
		s.log.Error("resumo home", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		return
	}
	responderJSON(w, http.StatusOK, respMetricas{ResumoHome: resumo, Hoje: agora.Format(iso)})
}

// statusPrecisaDeVoce são os estados que exigem ação humana e alimentam a lista
// "Precisa de você" da Home.
var statusPrecisaDeVoce = []string{
	db.StatusDemandaAguardandoRespostas,
	db.StatusDemandaAguardandoAprovacao,
	db.StatusDemandaConflito,
}

// handlePendencias devolve as demandas que precisam do usuário (perguntas a
// responder, plano a aprovar, conflito a resolver ou fase que exige intervenção
// humana aguardando o "feito").
func (s *Servidor) handlePendencias(w http.ResponseWriter, r *http.Request) {
	vis := visibilidadeDaRequisicao(r)
	demandas, err := s.banco.ListarDemandasPorStatus(r.Context(), statusPrecisaDeVoce, vis)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	// Demandas `pausada` também podem precisar de você — mas só quando a pausa é
	// por uma fase que exige intervenção humana (o scheduler pausa a demanda ao só
	// restarem fases requer_humano). Uma pausa manual do usuário NÃO é pendência,
	// então filtramos por fase para não poluir a lista "Precisa de você".
	pausadas, err := s.banco.ListarDemandasPorStatus(r.Context(), []string{db.StatusDemandaPausada}, vis)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	for _, d := range pausadas {
		fases, err := s.banco.ListarFases(r.Context(), d.ID)
		if err != nil {
			s.log.Warn("listar fases para pendências", "erro", err, "demanda", d.ID)
			continue
		}
		if temFaseHumanaPendente(fases) {
			demandas = append(demandas, d)
		}
	}

	responderJSON(w, http.StatusOK, demandas)
}

// temFaseHumanaPendente informa se há ao menos uma fase que exige intervenção
// humana ainda por concluir (pendente/pausada) — o sinal de que a demanda
// pausada aguarda alguém fazer o trabalho manual e clicar em "feito".
func temFaseHumanaPendente(fases []db.Fase) bool {
	for _, f := range fases {
		if f.RequerHumano && (f.Status == db.StatusFasePendente || f.Status == db.StatusFasePausada) {
			return true
		}
	}
	return false
}

// handleAtividade devolve os eventos mais recentes (backlog inicial que a Home
// mostra antes de o SSE global assumir as atualizações ao vivo). ?limite= (1..200,
// default 30).
func (s *Servidor) handleAtividade(w http.ResponseWriter, r *http.Request) {
	limite := 30
	if v := strings.TrimSpace(r.URL.Query().Get("limite")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limite = n
		}
	}
	eventos, err := s.banco.ListarEventos(r.Context(), db.FiltroEventos{
		Limite: limite, VisiveisPara: visibilidadeDaRequisicao(r)})
	if err != nil {
		s.log.Error("listar atividade", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		return
	}
	responderJSON(w, http.StatusOK, eventos)
}
