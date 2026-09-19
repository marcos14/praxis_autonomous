package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestNotificacoesCaixaDeEntrada(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	ana := criarUsuarioTeste(t, d, "ana")
	bia := criarUsuarioTeste(t, d, "bia")

	if _, err := d.CriarNotificacao(ctx, Notificacao{UserID: 999, Tipo: "x", Titulo: "t"}); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("usuário inexistente: %v", err)
	}
	if _, err := d.CriarNotificacao(ctx, Notificacao{UserID: ana, Titulo: "sem tipo"}); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("sem tipo: %v", err)
	}
	ev, _ := d.RegistrarEvento(ctx, Evento{Tipo: "consulta_respondida", Titulo: "resp"})
	n1, err := d.CriarNotificacao(ctx, Notificacao{UserID: ana, EventID: &ev.ID, Tipo: "consulta_respondida", Titulo: "Consulta respondida", Rota: "#consultas/1"})
	if err != nil || n1.ID == 0 || n1.CriadoEm == "" {
		t.Fatalf("criar: %+v %v", n1, err)
	}
	n2, _ := d.CriarNotificacao(ctx, Notificacao{UserID: ana, Tipo: "fase_falhou", Titulo: "Fase falhou"})
	_, _ = d.CriarNotificacao(ctx, Notificacao{UserID: bia, Tipo: "x", Titulo: "de bia"})

	// listagem: só as de ana, mais recentes primeiro.
	lista, err := d.ListarNotificacoes(ctx, ana, false, 0)
	if err != nil || len(lista) != 2 || lista[0].ID != n2.ID || lista[1].EventID == nil || *lista[1].EventID != ev.ID {
		t.Fatalf("listar: %+v %v", lista, err)
	}
	if n, _ := d.ContarNaoLidas(ctx, ana); n != 2 {
		t.Fatalf("não lidas = %d, quero 2", n)
	}
	// tailing: após n1, só n2.
	apos, _ := d.NotificacoesApos(ctx, ana, n1.ID, 0)
	if len(apos) != 1 || apos[0].ID != n2.ID {
		t.Fatalf("após n1: %+v", apos)
	}
	if ultimo, _ := d.UltimaNotificacaoID(ctx, ana); ultimo != n2.ID {
		t.Fatalf("última = %d, quero %d", ultimo, n2.ID)
	}
	if ultimo, _ := d.UltimaNotificacaoID(ctx, 999); ultimo != 0 {
		t.Fatalf("última de ninguém = %d, quero 0", ultimo)
	}

	// marcar lida: bia não marca a de ana; ana marca; repetir é no-op.
	if err := d.MarcarNotificacaoLida(ctx, n1.ID, bia); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("bia marcando a de ana: %v", err)
	}
	if err := d.MarcarNotificacaoLida(ctx, n1.ID, ana); err != nil {
		t.Fatalf("marcar lida: %v", err)
	}
	if err := d.MarcarNotificacaoLida(ctx, n1.ID, ana); err != nil {
		t.Fatalf("marcar lida 2x: %v", err)
	}
	if soNao, _ := d.ListarNotificacoes(ctx, ana, true, 0); len(soNao) != 1 || soNao[0].ID != n2.ID {
		t.Fatalf("só não lidas: %+v", soNao)
	}
	if n, _ := d.MarcarTodasLidas(ctx, ana); n != 1 {
		t.Fatalf("todas lidas = %d, quero 1", n)
	}
	if n, _ := d.ContarNaoLidas(ctx, ana); n != 0 {
		t.Fatalf("não lidas após marcar = %d", n)
	}
	if err := d.MarcarPushEnviado(ctx, n2.ID); err != nil {
		t.Fatalf("push enviado: %v", err)
	}
	if lista, _ = d.ListarNotificacoes(ctx, ana, false, 1); len(lista) != 1 || lista[0].PushEm == "" || lista[0].LidaEm == "" {
		t.Fatalf("limite/push_em/lida_em: %+v", lista)
	}

	// retenção: lidas antigas e não lidas mais antigas ainda.
	if _, err := d.Escritor.Exec(`UPDATE notificacoes SET criado_em = '2020-01-01T00:00:00.000Z' WHERE id = ?`, n1.ID); err != nil {
		t.Fatal(err)
	}
	agora := time.Now()
	n, err := d.RemoverNotificacoesAntigas(ctx, agora.AddDate(0, 0, -30), agora.AddDate(0, 0, -90))
	if err != nil || n != 1 {
		t.Fatalf("retenção removeu %d (%v), quero 1", n, err)
	}
	// a notificação (não lida) de bia, recente, fica; a lida antiga de ana saiu.
	if lista, _ = d.ListarNotificacoes(ctx, ana, false, 0); len(lista) != 1 {
		t.Fatalf("ana após retenção: %+v", lista)
	}
}

func TestAssinaturasPush(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	ana := criarUsuarioTeste(t, d, "ana")
	bia := criarUsuarioTeste(t, d, "bia")

	if _, err := d.SalvarAssinaturaPush(ctx, AssinaturaPush{UserID: ana, Endpoint: "https://push/1"}); !errors.Is(err, ErrAssinaturaInvalida) {
		t.Fatalf("sem chaves: %v", err)
	}
	if _, err := d.SalvarAssinaturaPush(ctx, AssinaturaPush{UserID: 999, Endpoint: "https://push/1", P256dh: "p", Auth: "a"}); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("usuário inexistente: %v", err)
	}
	a1, err := d.SalvarAssinaturaPush(ctx, AssinaturaPush{UserID: ana, Endpoint: "https://push/1", P256dh: "p1", Auth: "a1", UserAgent: "Chrome"})
	if err != nil || a1.ID == 0 || a1.Falhas != 0 {
		t.Fatalf("salvar: %+v %v", a1, err)
	}
	// reassinar o mesmo endpoint atualiza (mesmo id) e zera falhas.
	if _, err := d.RegistrarFalhaAssinatura(ctx, a1.ID, 5); err != nil {
		t.Fatal(err)
	}
	a1b, _ := d.SalvarAssinaturaPush(ctx, AssinaturaPush{UserID: ana, Endpoint: "https://push/1", P256dh: "p1b", Auth: "a1b"})
	if a1b.ID != a1.ID || a1b.P256dh != "p1b" || a1b.Falhas != 0 {
		t.Fatalf("reassinar: %+v", a1b)
	}
	a2, _ := d.SalvarAssinaturaPush(ctx, AssinaturaPush{UserID: ana, Endpoint: "https://push/2", P256dh: "p2", Auth: "a2"})
	_, _ = d.SalvarAssinaturaPush(ctx, AssinaturaPush{UserID: bia, Endpoint: "https://push/3", P256dh: "p3", Auth: "a3"})

	if lista, _ := d.ListarAssinaturasDoUsuario(ctx, ana); len(lista) != 2 {
		t.Fatalf("assinaturas de ana = %d, quero 2", len(lista))
	}

	// falhas: remove ao atingir o limite.
	for i := 1; i <= 4; i++ {
		if removida, err := d.RegistrarFalhaAssinatura(ctx, a2.ID, 5); err != nil || removida {
			t.Fatalf("falha %d: removida=%v err=%v", i, removida, err)
		}
	}
	if removida, err := d.RegistrarFalhaAssinatura(ctx, a2.ID, 5); err != nil || !removida {
		t.Fatalf("5ª falha deveria remover: %v %v", removida, err)
	}
	if _, err := d.RegistrarFalhaAssinatura(ctx, a2.ID, 5); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("falha em assinatura removida: %v", err)
	}
	// uso aceito carimba e zera.
	_, _ = d.RegistrarFalhaAssinatura(ctx, a1.ID, 5)
	if err := d.MarcarUsoAssinatura(ctx, a1.ID); err != nil {
		t.Fatal(err)
	}
	lista, _ := d.ListarAssinaturasDoUsuario(ctx, ana)
	if len(lista) != 1 || lista[0].Falhas != 0 || lista[0].UltimoUso == "" {
		t.Fatalf("após uso: %+v", lista)
	}

	// remover por endpoint: bia não remove a de ana; ana remove; despachante (0) remove qualquer.
	if err := d.RemoverAssinaturaPush(ctx, "https://push/1", bia); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("bia removendo de ana: %v", err)
	}
	if err := d.RemoverAssinaturaPush(ctx, "https://push/1", ana); err != nil {
		t.Fatalf("ana removendo: %v", err)
	}
	if err := d.RemoverAssinaturaPush(ctx, "https://push/3", 0); err != nil {
		t.Fatalf("despachante removendo: %v", err)
	}

	// retenção por falta de uso.
	a4, _ := d.SalvarAssinaturaPush(ctx, AssinaturaPush{UserID: ana, Endpoint: "https://push/4", P256dh: "p", Auth: "a"})
	if _, err := d.Escritor.Exec(`UPDATE push_subscriptions SET criado_em = '2020-01-01T00:00:00.000Z' WHERE id = ?`, a4.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.RemoverAssinaturasSemUso(ctx, time.Now().AddDate(0, 0, -180)); n != 1 {
		t.Fatalf("retenção removeu %d, quero 1", n)
	}
}

func TestPreferenciasEVAPID(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	ana := criarUsuarioTeste(t, d, "ana")

	if raw, err := d.PreferenciasNotificacao(ctx, ana); err != nil || raw != "" {
		t.Fatalf("prefs iniciais = %q (%v), quero vazio", raw, err)
	}
	if err := d.DefinirPreferenciasNotificacao(ctx, ana, `{"push":false}`); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.PreferenciasNotificacao(ctx, ana); raw != `{"push":false}` {
		t.Fatalf("prefs = %q", raw)
	}
	if err := d.DefinirPreferenciasNotificacao(ctx, 999, "{}"); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("inexistente: %v", err)
	}
	if _, err := d.PreferenciasNotificacao(ctx, 999); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("inexistente: %v", err)
	}

	chamadas := 0
	gerar := func() (string, string, error) { chamadas++; return "PUB", "PRIV", nil }
	pub, priv, err := d.ObterOuGerarVAPID(ctx, gerar)
	if err != nil || pub != "PUB" || priv != "PRIV" {
		t.Fatalf("vapid: %q %q %v", pub, priv, err)
	}
	pub2, _, _ := d.ObterOuGerarVAPID(ctx, func() (string, string, error) { chamadas++; return "OUTRA", "X", nil })
	if pub2 != "PUB" || chamadas != 1 {
		t.Fatalf("vapid deveria ser memorizado: %q (gerador chamado %d vezes)", pub2, chamadas)
	}
}

func TestContatoPush(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	// Sem admin e sem config: nada a informar.
	if _, err := d.ContatoPush(ctx); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("sem admin: %v", err)
	}
	if _, err := d.EmailPrimeiroAdmin(ctx); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("sem admin: %v", err)
	}
	// Usuário comum não conta; o primeiro admin (menor id) é o contato padrão.
	criarUsuarioTeste(t, d, "comum")
	admin := idPapelAdmin(t, d)
	a1, err := d.CriarUsuario(ctx, "Primeira", "primeira@x.com", "senha-123", []int64{admin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CriarUsuario(ctx, "Segunda", "segunda@x.com", "senha-123", []int64{admin}); err != nil {
		t.Fatal(err)
	}
	if e, err := d.EmailPrimeiroAdmin(ctx); err != nil || e != "primeira@x.com" {
		t.Fatalf("primeiro admin: %q %v", e, err)
	}
	if c, err := d.ContatoPush(ctx); err != nil || c != "mailto:primeira@x.com" {
		t.Fatalf("contato padrão: %q %v", c, err)
	}
	// Admin desativado deixa de contar.
	if _, err := d.AtualizarUsuario(ctx, a1.ID, "Primeira", "primeira@x.com", false, []int64{admin}); err != nil {
		t.Fatal(err)
	}
	if c, _ := d.ContatoPush(ctx); c != "mailto:segunda@x.com" {
		t.Fatalf("admin inativo ainda conta: %q", c)
	}
	// Config push_contato prevalece; e-mail ganha mailto:, URL passa como está.
	for entrada, quer := range map[string]string{
		"  ops@empresa.com ":          "mailto:ops@empresa.com",
		"mailto:x@y.com":              "mailto:x@y.com",
		"https://empresa.com/contato": "https://empresa.com/contato",
		"":                            "mailto:segunda@x.com",
	} {
		raw, _ := json.Marshal(entrada)
		if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{ChavePushContato: raw}); err != nil {
			t.Fatal(err)
		}
		if c, err := d.ContatoPush(ctx); err != nil || c != quer {
			t.Fatalf("push_contato %q: %q %v (quer %q)", entrada, c, err, quer)
		}
	}
}
