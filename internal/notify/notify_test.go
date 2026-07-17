package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// coletor guarda os corpos recebidos pelo webhook de teste.
type coletor struct {
	mu     sync.Mutex
	corpos []map[string]string
}

func (c *coletor) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		c.mu.Lock()
		c.corpos = append(c.corpos, m)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
}

func (c *coletor) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.corpos)
}

func TestEnviarWebhookGenerico(t *testing.T) {
	col := &coletor{}
	ts := httptest.NewServer(col.handler())
	defer ts.Close()

	cfg := Config{Canais: map[string]Canal{
		"webhook": {Ativo: true, URL: ts.URL},
	}}
	Novo().Enviar(context.Background(), cfg, "Título", "Corpo")
	if col.len() != 1 {
		t.Fatalf("recebidos = %d, quero 1", col.len())
	}
	if col.corpos[0]["titulo"] != "Título" || col.corpos[0]["texto"] != "Corpo" {
		t.Fatalf("corpo = %+v", col.corpos[0])
	}
}

func TestEnviarDiscordSlackGoogleChat(t *testing.T) {
	for _, canal := range []struct {
		nome  string
		campo string
	}{{"discord", "content"}, {"slack", "text"}, {"google_chat", "text"}} {
		col := &coletor{}
		ts := httptest.NewServer(col.handler())
		cfg := Config{Canais: map[string]Canal{
			canal.nome: {Ativo: true, WebhookURL: ts.URL},
		}}
		Novo().Enviar(context.Background(), cfg, "T", "C")
		if col.len() != 1 {
			ts.Close()
			t.Fatalf("%s: recebidos = %d, quero 1", canal.nome, col.len())
		}
		if col.corpos[0][canal.campo] == "" {
			ts.Close()
			t.Fatalf("%s: campo %q vazio (%+v)", canal.nome, canal.campo, col.corpos[0])
		}
		ts.Close()
	}
}

func TestEventoLigadoFiltra(t *testing.T) {
	cfg := Config{Eventos: map[string]bool{"conflito": false}}
	if EventoLigado(cfg, "conflito") {
		t.Fatal("conflito deveria estar desligado")
	}
	if !EventoLigado(cfg, "demanda_integrada") {
		t.Fatal("evento sem entrada deveria cair no default ligado")
	}
}

// fonteFake implementa FonteEventos com uma lista em memória protegida por mutex
// (o despachante lê de outra goroutine enquanto o teste acrescenta eventos).
type fonteFake struct {
	mu      sync.Mutex
	eventos []db.Evento
}

func (f *fonteFake) adicionar(e db.Evento) {
	f.mu.Lock()
	f.eventos = append(f.eventos, e)
	f.mu.Unlock()
}

func (f *fonteFake) EventosApos(_ context.Context, aposID int64, _ int) ([]db.Evento, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.Evento
	for _, e := range f.eventos {
		if e.ID > aposID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fonteFake) UltimoEventoID(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var max int64
	for _, e := range f.eventos {
		if e.ID > max {
			max = e.ID
		}
	}
	return max, nil
}

func TestDespachanteNotificaEventoNovo(t *testing.T) {
	col := &coletor{}
	ts := httptest.NewServer(col.handler())
	defer ts.Close()

	fonte := &fonteFake{} // vazio no boot → cursor inicial 0
	cfg := Config{Canais: map[string]Canal{"webhook": {Ativo: true, URL: ts.URL}}}
	pronto := make(chan struct{})
	desp := NovoDespachante(OpcoesDespachante{
		Fonte:     fonte,
		Config:    func(context.Context) (Config, error) { return cfg, nil },
		Intervalo: 5 * time.Millisecond,
		AoIniciar: func() { close(pronto) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go desp.Rodar(ctx)
	<-pronto // garante que o cursor inicial (0) já foi capturado

	// evento surge após o boot → deve ser notificado.
	fonte.adicionar(db.Evento{ID: 1, Tipo: "demanda_integrada", Titulo: "Praxis: integrada"})

	prazo := time.After(3 * time.Second)
	for col.len() == 0 {
		select {
		case <-prazo:
			t.Fatal("timeout esperando notificação do evento")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if col.corpos[0]["titulo"] != "Praxis: integrada" {
		t.Fatalf("notificação = %+v", col.corpos[0])
	}
}

func TestDespachanteRespeitaFiltroEvento(t *testing.T) {
	col := &coletor{}
	ts := httptest.NewServer(col.handler())
	defer ts.Close()

	fonte := &fonteFake{}
	cfg := Config{
		Canais:  map[string]Canal{"webhook": {Ativo: true, URL: ts.URL}},
		Eventos: map[string]bool{"push_falhou": false},
	}
	pronto := make(chan struct{})
	desp := NovoDespachante(OpcoesDespachante{
		Fonte:     fonte,
		Config:    func(context.Context) (Config, error) { return cfg, nil },
		Intervalo: 5 * time.Millisecond,
		AoIniciar: func() { close(pronto) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go desp.Rodar(ctx)
	<-pronto

	fonte.adicionar(db.Evento{ID: 1, Tipo: "push_falhou", Titulo: "x"})
	// dá tempo de o despachante processar (e ignorar).
	time.Sleep(100 * time.Millisecond)
	if col.len() != 0 {
		t.Fatalf("evento desligado não deveria notificar, recebidos %d", col.len())
	}
}
