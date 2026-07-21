package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// intervaloPollPadrao é a cadência com que o despachante relê a tabela events em
// busca de novos eventos para notificar.
const intervaloPollPadrao = 2 * time.Second

// FonteEventos é o mínimo do store que o despachante precisa: tailing dos
// eventos por id. Satisfeito por *db.DB.
type FonteEventos interface {
	EventosApos(ctx context.Context, aposID int64, limite int, visiveisPara *int64) ([]db.Evento, error)
	UltimoEventoID(ctx context.Context) (int64, error)
}

// ProvedorConfig devolve a config de notificações corrente (relida a cada ciclo
// para refletir mudanças feitas na tela de Configurações sem reiniciar).
type ProvedorConfig func(ctx context.Context) (Config, error)

// ProvedorOverride resolve o override de notificações de um projeto (quais
// eventos notificar). Devolve (override, existe, erro): existe=false quando o
// projeto não tem override e todos os seus eventos caem no padrão global. nil no
// despachante desliga o override por projeto (comportamento só-global).
type ProvedorOverride func(ctx context.Context, projectID int64) (OverrideProjeto, bool, error)

// Despachante observa os eventos persistidos e envia notificações para os canais
// configurados, respeitando o filtro por tipo de evento. É o elo "eventos do
// banco → webhooks" da Fase 4e.
type Despachante struct {
	fonte     FonteEventos
	cfg       ProvedorConfig
	override  ProvedorOverride
	notif     *Notificador
	intervalo time.Duration
	log       func(string)
	// aoIniciar, quando definido, é chamado logo após o cursor inicial ser
	// capturado (antes do primeiro ciclo). Usado nos testes para sincronizar a
	// injeção de eventos com o fim do boot (evita a corrida cursor-inicial vs.
	// novo evento). Em produção fica nil.
	aoIniciar func()
}

// OpcoesDespachante configura o despachante.
type OpcoesDespachante struct {
	Fonte     FonteEventos
	Config    ProvedorConfig
	Override  ProvedorOverride // nil = sem override por projeto (só-global)
	Notif     *Notificador     // nil = Novo()
	Intervalo time.Duration    // <=0 = intervaloPollPadrao
	Log       func(string)
	AoIniciar func() // hook de teste; chamado após capturar o cursor inicial
}

// NovoDespachante monta o despachante.
func NovoDespachante(o OpcoesDespachante) *Despachante {
	n := o.Notif
	if n == nil {
		n = Novo()
	}
	iv := o.Intervalo
	if iv <= 0 {
		iv = intervaloPollPadrao
	}
	logf := o.Log
	if logf == nil {
		logf = func(string) {}
	}
	if n.Aviso == nil {
		n.Aviso = func(msg string) { logf("notify: " + msg) }
	}
	return &Despachante{fonte: o.Fonte, cfg: o.Config, override: o.Override, notif: n, intervalo: iv, log: logf, aoIniciar: o.AoIniciar}
}

// Rodar tail-a a tabela de eventos até ctx ser cancelado. O cursor inicial é o
// último id existente (não notifica o backlog anterior à subida do serviço).
func (d *Despachante) Rodar(ctx context.Context) {
	cursor := int64(0)
	if ultimo, err := d.fonte.UltimoEventoID(ctx); err == nil {
		cursor = ultimo
	}
	if d.aoIniciar != nil {
		d.aoIniciar()
	}
	ticker := time.NewTicker(d.intervalo)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cursor = d.processar(ctx, cursor)
		}
	}
}

// processar lê os eventos após cursor e notifica os que estiverem habilitados;
// devolve o novo cursor. Config relida a cada ciclo. Best-effort.
func (d *Despachante) processar(ctx context.Context, cursor int64) int64 {
	novos, err := d.fonte.EventosApos(ctx, cursor, 0, nil)
	if err != nil || len(novos) == 0 {
		return cursor
	}
	cfg, err := d.cfg(ctx)
	if err != nil {
		// não avança o cursor: tenta de novo no próximo ciclo com a config lida.
		return cursor
	}
	// Os canais/cabeçalho são globais; o override de projeto só troca o mapa de
	// eventos. Como os canais não mudam por projeto, um único teste basta para
	// decidir se há para onde enviar.
	notificar := AlgumCanalAtivo(cfg)
	efetivaPorProjeto := map[int64]Config{} // cache dentro do ciclo (evita relê-lo por evento)
	for _, ev := range novos {
		if notificar {
			d.notif.EnviarEvento(ctx, d.configDoEvento(ctx, cfg, ev, efetivaPorProjeto), ev.Tipo, ev.Titulo, ev.Detalhe)
		}
		cursor = ev.ID
	}
	return cursor
}

// configDoEvento resolve a config efetiva a aplicar a um evento: a global, com o
// mapa de eventos trocado pelo override do projeto do evento (quando há um e ele
// não usa o padrão). Eventos sem projeto, ou sem provedor de override, usam a
// global. O cache evita reconsultar o mesmo projeto no ciclo; erro ao ler o
// override cai no global (best-effort, nunca silencia por falha de leitura).
func (d *Despachante) configDoEvento(ctx context.Context, global Config, ev db.Evento, cache map[int64]Config) Config {
	if ev.ProjectID == nil || d.override == nil {
		return global
	}
	pid := *ev.ProjectID
	if c, ok := cache[pid]; ok {
		return c
	}
	efetiva := global
	if ov, existe, err := d.override(ctx, pid); err == nil {
		efetiva = ParaProjeto(global, ov, existe)
	} else {
		d.log("notify: override do projeto " + fmt.Sprint(pid) + ": " + err.Error())
	}
	cache[pid] = efetiva
	return efetiva
}
