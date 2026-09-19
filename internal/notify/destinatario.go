package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// TiposPadraoUsuario são os eventos que, sem preferência salva, notificam o
// dono do item ("sua atividade terminou / precisa de você") — espelha o flag
// padrao_usuario do catálogo em web/js/notify-events.js (PLANO_INTERNET 4.4.1).
var TiposPadraoUsuario = []string{
	"consulta_respondida", "consulta_falhou",
	"estrategia_respondida", "estrategia_falhou",
	"analise_concluida", "planejamento_concluido",
	"aguardando_humano", "fase_falhou", "gates_falharam", "franquia_esgotada",
	"demanda_concluida", "demanda_integrada",
	"push_falhou", "merge_falhou",
}

// Preferencias são as escolhas de notificação de um usuário (users.notificacoes,
// JSON). Navegador liga o toast/sino na aba aberta; Push o Web Push nos
// dispositivos assinados; Eventos diz, por tipo, se o evento gera notificação
// (ausente = desligado, exceto os TiposPadraoUsuario, que nascem ligados).
type Preferencias struct {
	Navegador bool            `json:"navegador"`
	Push      bool            `json:"push"`
	Eventos   map[string]bool `json:"eventos"`
}

// PreferenciasPadrao é o que vale sem nada salvo: navegador e push ligados,
// eventos = TiposPadraoUsuario.
func PreferenciasPadrao() Preferencias {
	p := Preferencias{Navegador: true, Push: true, Eventos: make(map[string]bool, len(TiposPadraoUsuario))}
	for _, tipo := range TiposPadraoUsuario {
		p.Eventos[tipo] = true
	}
	return p
}

// DecodificarPreferencias interpreta o JSON salvo por cima do padrão: campos
// ausentes mantêm o padrão; JSON vazio ou inválido = padrão inteiro.
func DecodificarPreferencias(raw string) Preferencias {
	p := PreferenciasPadrao()
	if strings.TrimSpace(raw) == "" {
		return p
	}
	var bruto struct {
		Navegador *bool           `json:"navegador"`
		Push      *bool           `json:"push"`
		Eventos   map[string]bool `json:"eventos"`
	}
	if err := json.Unmarshal([]byte(raw), &bruto); err != nil {
		return p
	}
	if bruto.Navegador != nil {
		p.Navegador = *bruto.Navegador
	}
	if bruto.Push != nil {
		p.Push = *bruto.Push
	}
	for tipo, ligado := range bruto.Eventos {
		p.Eventos[tipo] = ligado
	}
	return p
}

// EventoLigado diz se o tipo gera notificação para este usuário.
func (p Preferencias) EventoLigado(tipo string) bool {
	return p.Eventos[tipo]
}

// FonteUsuarios é o que o despachante precisa do store para notificar o dono
// de um item: resolver o criador pela chave do evento, ler as preferências e
// gravar a notificação. Satisfeito por *db.DB.
type FonteUsuarios interface {
	ObterDemanda(ctx context.Context, id int64) (db.Demanda, error)
	ObterConsulta(ctx context.Context, id int64) (db.Consulta, error)
	ObterPlanejamento(ctx context.Context, id int64) (db.Planejamento, error)
	PreferenciasNotificacao(ctx context.Context, userID int64) (string, error)
	CriarNotificacao(ctx context.Context, n db.Notificacao) (db.Notificacao, error)
}

// cicloUsuarios são os caches de um ciclo do despachante: o dono de cada item
// (chave "c:7", "p:3", "d:12") e as preferências de cada usuário — vários
// eventos do mesmo item/usuário no mesmo ciclo custam uma consulta só.
type cicloUsuarios struct {
	donos map[string]*int64
	prefs map[int64]Preferencias
}

func novoCiclo() *cicloUsuarios {
	return &cicloUsuarios{donos: map[string]*int64{}, prefs: map[int64]Preferencias{}}
}

// RotaDoEvento devolve a rota da SPA do item do evento ("#consultas/7",
// "#planejamentos/3", "#demandas/12") ou "" para eventos sem item.
func RotaDoEvento(ev db.Evento) string {
	switch {
	case ev.ConsultaID != nil:
		return fmt.Sprintf("#consultas/%d", *ev.ConsultaID)
	case ev.PlanejamentoID != nil:
		return fmt.Sprintf("#planejamentos/%d", *ev.PlanejamentoID)
	case ev.DemandID != nil:
		return fmt.Sprintf("#demandas/%d", *ev.DemandID)
	}
	return ""
}

// destinatario resolve o dono do item do evento (criado_por da consulta, do
// planejamento ou da demanda, nessa ordem de precedência). Sem item, sem dono
// ou item já apagado → nil. Erros de leitura viram log e contam como sem dono
// (best-effort: nunca trava o cursor).
func (d *Despachante) destinatario(ctx context.Context, ev db.Evento, c *cicloUsuarios) *int64 {
	var chave string
	var buscar func() (*int64, error)
	switch {
	case ev.ConsultaID != nil:
		id := *ev.ConsultaID
		chave = fmt.Sprintf("c:%d", id)
		buscar = func() (*int64, error) { x, err := d.usuarios.ObterConsulta(ctx, id); return x.CriadoPor, err }
	case ev.PlanejamentoID != nil:
		id := *ev.PlanejamentoID
		chave = fmt.Sprintf("p:%d", id)
		buscar = func() (*int64, error) { x, err := d.usuarios.ObterPlanejamento(ctx, id); return x.CriadoPor, err }
	case ev.DemandID != nil:
		id := *ev.DemandID
		chave = fmt.Sprintf("d:%d", id)
		buscar = func() (*int64, error) { x, err := d.usuarios.ObterDemanda(ctx, id); return x.CriadoPor, err }
	default:
		return nil
	}
	if dono, ok := c.donos[chave]; ok {
		return dono
	}
	dono, err := buscar()
	if err != nil {
		// Item apagado entre o evento e o ciclo é silencioso; o resto vai ao log.
		if !errors.Is(err, db.ErrNaoEncontrado) {
			d.log("notify: dono do item " + chave + ": " + err.Error())
		}
		dono = nil
	}
	c.donos[chave] = dono
	return dono
}

// preferencias devolve as preferências do usuário (cache do ciclo). Erro de
// leitura → padrão.
func (d *Despachante) preferencias(ctx context.Context, userID int64, c *cicloUsuarios) Preferencias {
	if p, ok := c.prefs[userID]; ok {
		return p
	}
	raw, err := d.usuarios.PreferenciasNotificacao(ctx, userID)
	if err != nil {
		raw = ""
	}
	p := DecodificarPreferencias(raw)
	c.prefs[userID] = p
	return p
}

// notificarUsuario grava a notificação do evento para o dono do item, se ele
// existir e tiver o tipo ligado. Devolve a linha criada, as preferências do
// dono e true quando gravou (o envio push parte daqui).
func (d *Despachante) notificarUsuario(ctx context.Context, ev db.Evento, c *cicloUsuarios) (db.Notificacao, Preferencias, bool) {
	if d.usuarios == nil {
		return db.Notificacao{}, Preferencias{}, false
	}
	dono := d.destinatario(ctx, ev, c)
	if dono == nil {
		return db.Notificacao{}, Preferencias{}, false
	}
	prefs := d.preferencias(ctx, *dono, c)
	if !prefs.EventoLigado(ev.Tipo) {
		return db.Notificacao{}, prefs, false
	}
	evID := ev.ID
	n, err := d.usuarios.CriarNotificacao(ctx, db.Notificacao{
		UserID:  *dono,
		EventID: &evID,
		Tipo:    ev.Tipo,
		Titulo:  ev.Titulo,
		Detalhe: ev.Detalhe,
		Rota:    RotaDoEvento(ev),
	})
	if err != nil {
		d.log("notify: gravar notificação do evento " + fmt.Sprint(ev.ID) + ": " + err.Error())
		return db.Notificacao{}, prefs, false
	}
	return n, prefs, true
}
