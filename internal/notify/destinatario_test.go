package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// usuariosFake implementa FonteUsuarios em memória.
type usuariosFake struct {
	mu             sync.Mutex
	consultas      map[int64]*int64 // id → criado_por
	planejamentos  map[int64]*int64
	demandas       map[int64]*int64
	prefs          map[int64]string
	criadas        []db.Notificacao
	consultasLidas int
	prefsLidas     int
}

func novoUsuariosFake() *usuariosFake {
	return &usuariosFake{consultas: map[int64]*int64{}, planejamentos: map[int64]*int64{}, demandas: map[int64]*int64{}, prefs: map[int64]string{}}
}

func (f *usuariosFake) ObterConsulta(_ context.Context, id int64) (db.Consulta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consultasLidas++
	dono, ok := f.consultas[id]
	if !ok {
		return db.Consulta{}, db.ErrNaoEncontrado
	}
	return db.Consulta{ID: id, CriadoPor: dono}, nil
}

func (f *usuariosFake) ObterPlanejamento(_ context.Context, id int64) (db.Planejamento, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dono, ok := f.planejamentos[id]
	if !ok {
		return db.Planejamento{}, db.ErrNaoEncontrado
	}
	return db.Planejamento{ID: id, CriadoPor: dono}, nil
}

func (f *usuariosFake) ObterDemanda(_ context.Context, id int64) (db.Demanda, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dono, ok := f.demandas[id]
	if !ok {
		return db.Demanda{}, errors.New("boom")
	}
	return db.Demanda{ID: id, CriadoPor: dono}, nil
}

func (f *usuariosFake) PreferenciasNotificacao(_ context.Context, userID int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prefsLidas++
	return f.prefs[userID], nil
}

func (f *usuariosFake) CriarNotificacao(_ context.Context, n db.Notificacao) (db.Notificacao, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n.ID = int64(len(f.criadas) + 1)
	f.criadas = append(f.criadas, n)
	return n, nil
}

func (f *usuariosFake) notificacoes() []db.Notificacao {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.Notificacao(nil), f.criadas...)
}

func ptr(v int64) *int64 { return &v }

func TestPreferenciasPadraoEDecodificacao(t *testing.T) {
	p := DecodificarPreferencias("")
	if !p.Navegador || !p.Push || !p.EventoLigado("consulta_respondida") || !p.EventoLigado("demanda_concluida") {
		t.Fatalf("padrão: %+v", p)
	}
	if p.EventoLigado("fase_iniciada") || p.EventoLigado("inexistente") {
		t.Fatal("tipo fora do padrão deveria estar desligado")
	}
	p = DecodificarPreferencias(`{"push":false,"eventos":{"consulta_respondida":false,"fase_iniciada":true}}`)
	if !p.Navegador || p.Push {
		t.Fatalf("campos parciais: %+v", p)
	}
	if p.EventoLigado("consulta_respondida") || !p.EventoLigado("fase_iniciada") || !p.EventoLigado("consulta_falhou") {
		t.Fatalf("eventos mesclados com o padrão: %v", p.Eventos)
	}
	if p := DecodificarPreferencias("{lixo"); !p.Push || !p.EventoLigado("aguardando_humano") {
		t.Fatal("json inválido deveria cair no padrão")
	}
	// O padrão não é compartilhado entre chamadas (mapa novo a cada vez).
	a := PreferenciasPadrao()
	a.Eventos["consulta_respondida"] = false
	if !PreferenciasPadrao().EventoLigado("consulta_respondida") {
		t.Fatal("padrão foi mutado")
	}
}

func TestRotaDoEvento(t *testing.T) {
	casos := []struct {
		ev   db.Evento
		quer string
	}{
		{db.Evento{ConsultaID: ptr(7)}, "#consultas/7"},
		{db.Evento{PlanejamentoID: ptr(3)}, "#planejamentos/3"},
		{db.Evento{DemandID: ptr(12), ProjectID: ptr(1)}, "#demandas/12"},
		{db.Evento{ConsultaID: ptr(7), DemandID: ptr(12)}, "#consultas/7"},
		{db.Evento{ProjectID: ptr(1)}, ""},
	}
	for _, c := range casos {
		if got := RotaDoEvento(c.ev); got != c.quer {
			t.Errorf("%+v → %q, quer %q", c.ev, got, c.quer)
		}
	}
}

// rodarUmCiclo executa processar uma vez sobre a fonte, a partir do cursor 0.
func rodarUmCiclo(t *testing.T, fonte *fonteFake, usuarios FonteUsuarios) []string {
	t.Helper()
	var logs []string
	desp := NovoDespachante(OpcoesDespachante{
		Fonte:    fonte,
		Usuarios: usuarios,
		Config:   func(context.Context) (Config, error) { return Config{}, nil },
		Log:      func(m string) { logs = append(logs, m) },
	})
	if cursor := desp.processar(context.Background(), 0); cursor == 0 {
		t.Fatal("cursor deveria avançar")
	}
	return logs
}

func TestDespachanteGravaNotificacaoParaODono(t *testing.T) {
	u := novoUsuariosFake()
	u.consultas[7] = ptr(42)
	u.planejamentos[3] = ptr(43)
	u.demandas[12] = ptr(44)
	u.consultas[8] = nil // sem dono (token de API)
	fonte := &fonteFake{}
	fonte.adicionar(db.Evento{ID: 1, Tipo: "consulta_respondida", Titulo: "Praxis: consulta respondida", Detalhe: "d1", ConsultaID: ptr(7)})
	fonte.adicionar(db.Evento{ID: 2, Tipo: "consulta_falhou", Titulo: "falhou", ConsultaID: ptr(7)})       // mesmo item: cache
	fonte.adicionar(db.Evento{ID: 3, Tipo: "consulta_respondida", Titulo: "sem dono", ConsultaID: ptr(8)}) // sem dono → ninguém
	fonte.adicionar(db.Evento{ID: 4, Tipo: "consulta_respondida", Titulo: "apagada", ConsultaID: ptr(99)}) // não existe → ninguém, sem log
	fonte.adicionar(db.Evento{ID: 5, Tipo: "estrategia_respondida", Titulo: "plan", PlanejamentoID: ptr(3)})
	fonte.adicionar(db.Evento{ID: 6, Tipo: "demanda_concluida", Titulo: "dem", DemandID: ptr(12), ProjectID: ptr(1)})
	fonte.adicionar(db.Evento{ID: 7, Tipo: "fase_iniciada", Titulo: "fora do padrão", DemandID: ptr(12), ProjectID: ptr(1)}) // tipo desligado
	fonte.adicionar(db.Evento{ID: 8, Tipo: "projeto_criado", Titulo: "sem item", ProjectID: ptr(1)})                         // sem item
	fonte.adicionar(db.Evento{ID: 9, Tipo: "demanda_concluida", Titulo: "erro", DemandID: ptr(13), ProjectID: ptr(1)})       // store falha → log

	logs := rodarUmCiclo(t, fonte, u)

	got := u.notificacoes()
	if len(got) != 4 {
		t.Fatalf("notificações = %d: %+v", len(got), got)
	}
	n := got[0]
	if n.UserID != 42 || n.Tipo != "consulta_respondida" || n.Titulo != "Praxis: consulta respondida" || n.Detalhe != "d1" || n.Rota != "#consultas/7" || n.EventID == nil || *n.EventID != 1 {
		t.Fatalf("primeira: %+v", n)
	}
	if got[1].UserID != 42 || got[1].Rota != "#consultas/7" || got[2].UserID != 43 || got[2].Rota != "#planejamentos/3" || got[3].UserID != 44 || got[3].Rota != "#demandas/12" {
		t.Fatalf("demais: %+v", got[1:])
	}
	if u.consultasLidas != 3 { // 7 (cache no segundo evento), 8, 99
		t.Fatalf("consultas lidas = %d (cache por ciclo)", u.consultasLidas)
	}
	if u.prefsLidas != 3 { // 42, 43, 44 — uma vez cada
		t.Fatalf("preferências lidas = %d", u.prefsLidas)
	}
	if len(logs) != 1 || logs[0] != "notify: dono do item d:13: boom" {
		t.Fatalf("logs = %q (só o erro do store, nunca o item inexistente)", logs)
	}
}

func TestDespachanteRespeitaPreferenciasDoUsuario(t *testing.T) {
	u := novoUsuariosFake()
	u.consultas[7] = ptr(42)
	u.prefs[42] = `{"eventos":{"consulta_respondida":false,"fase_iniciada":true}}`
	u.demandas[1] = ptr(42)
	fonte := &fonteFake{}
	fonte.adicionar(db.Evento{ID: 1, Tipo: "consulta_respondida", Titulo: "desligado", ConsultaID: ptr(7)})
	fonte.adicionar(db.Evento{ID: 2, Tipo: "consulta_falhou", Titulo: "padrão mantido", ConsultaID: ptr(7)})
	fonte.adicionar(db.Evento{ID: 3, Tipo: "fase_iniciada", Titulo: "ligado à mão", DemandID: ptr(1)})
	rodarUmCiclo(t, fonte, u)
	got := u.notificacoes()
	if len(got) != 2 || got[0].Titulo != "padrão mantido" || got[1].Titulo != "ligado à mão" {
		t.Fatalf("notificações = %+v", got)
	}
}

func TestDespachanteSemFonteDeUsuariosSoWebhooks(t *testing.T) {
	fonte := &fonteFake{}
	fonte.adicionar(db.Evento{ID: 1, Tipo: "consulta_respondida", ConsultaID: ptr(7)})
	desp := NovoDespachante(OpcoesDespachante{
		Fonte:  fonte,
		Config: func(context.Context) (Config, error) { return Config{}, nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if cursor := desp.processar(ctx, 0); cursor != 1 {
		t.Fatalf("cursor = %d", cursor)
	}
}
