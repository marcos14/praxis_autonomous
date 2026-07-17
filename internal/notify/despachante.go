package notify

import (
	"context"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// intervaloPollPadrao é a cadência com que o despachante relê a tabela events em
// busca de novos eventos para notificar.
const intervaloPollPadrao = 2 * time.Second

// FonteEventos é o mínimo do store que o despachante precisa: tailing dos
// eventos por id. Satisfeito por *db.DB.
type FonteEventos interface {
	EventosApos(ctx context.Context, aposID int64, limite int) ([]db.Evento, error)
	UltimoEventoID(ctx context.Context) (int64, error)
}

// ProvedorConfig devolve a config de notificações corrente (relida a cada ciclo
// para refletir mudanças feitas na tela de Configurações sem reiniciar).
type ProvedorConfig func(ctx context.Context) (Config, error)

// Despachante observa os eventos persistidos e envia notificações para os canais
// configurados, respeitando o filtro por tipo de evento. É o elo "eventos do
// banco → webhooks" da Fase 4e.
type Despachante struct {
	fonte     FonteEventos
	cfg       ProvedorConfig
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
	Notif     *Notificador  // nil = Novo()
	Intervalo time.Duration // <=0 = intervaloPollPadrao
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
	return &Despachante{fonte: o.Fonte, cfg: o.Config, notif: n, intervalo: iv, log: logf, aoIniciar: o.AoIniciar}
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
	novos, err := d.fonte.EventosApos(ctx, cursor, 0)
	if err != nil || len(novos) == 0 {
		return cursor
	}
	cfg, err := d.cfg(ctx)
	if err != nil {
		// não avança o cursor: tenta de novo no próximo ciclo com a config lida.
		return cursor
	}
	notificar := AlgumCanalAtivo(cfg)
	for _, ev := range novos {
		if notificar {
			d.notif.EnviarEvento(ctx, cfg, ev.Tipo, ev.Titulo, ev.Detalhe)
		}
		cursor = ev.ID
	}
	return cursor
}
