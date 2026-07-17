package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCriarMensagemChatPreencheDefaults(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "chat-a")
	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "Demanda chat"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}

	m, err := d.CriarMensagemChat(ctx, MensagemChat{DemandID: dem.ID, Conteudo: "cole aqui o PRD"})
	if err != nil {
		t.Fatalf("CriarMensagemChat: %v", err)
	}
	if m.ID == 0 || m.CriadoEm == "" {
		t.Fatalf("id/criado_em não preenchidos: %+v", m)
	}
	if m.Papel != PapelUser {
		t.Fatalf("papel = %q, quero default %q", m.Papel, PapelUser)
	}
	if string(m.Meta) != "{}" {
		t.Fatalf("meta = %q, quero default {}", string(m.Meta))
	}
}

func TestCriarMensagemChatPapelInvalido(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "chat-b")
	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "Demanda chat"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}
	_, err = d.CriarMensagemChat(ctx, MensagemChat{DemandID: dem.ID, Papel: "robo", Conteudo: "x"})
	if !errors.Is(err, ErrPapelInvalido) {
		t.Fatalf("erro = %v, quero ErrPapelInvalido", err)
	}
}

func TestCriarMensagemChatDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.CriarMensagemChat(context.Background(), MensagemChat{DemandID: 999, Conteudo: "x"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestListarMensagensChatEmOrdemCronologica(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "chat-c")
	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "Demanda chat"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}

	if _, err := d.CriarMensagemChat(ctx, MensagemChat{DemandID: dem.ID, Papel: PapelUser, Conteudo: "PRD"}); err != nil {
		t.Fatalf("msg 1: %v", err)
	}
	if _, err := d.CriarMensagemChat(ctx, MensagemChat{DemandID: dem.ID, Papel: PapelAnalista, Conteudo: "recebi"}); err != nil {
		t.Fatalf("msg 2: %v", err)
	}
	// mensagem de outra demanda não deve vazar na listagem.
	outra, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "Outra"})
	if err != nil {
		t.Fatalf("outra demanda: %v", err)
	}
	if _, err := d.CriarMensagemChat(ctx, MensagemChat{DemandID: outra.ID, Conteudo: "ruído"}); err != nil {
		t.Fatalf("msg outra: %v", err)
	}

	msgs, err := d.ListarMensagensChat(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ListarMensagensChat: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, quero 2 (sem vazar de outra demanda)", len(msgs))
	}
	if msgs[0].Papel != PapelUser || msgs[0].Conteudo != "PRD" {
		t.Fatalf("primeira mensagem = %+v, quero o PRD do user", msgs[0])
	}
	if msgs[1].Papel != PapelAnalista {
		t.Fatalf("segunda mensagem papel = %q, quero analista", msgs[1].Papel)
	}
}

func TestCriarDemandaComChatTransacional(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "chat-d")

	dem, msg, err := d.CriarDemandaComChat(ctx,
		Demanda{ProjectID: proj, Titulo: "Boleto híbrido"},
		MensagemChat{Conteudo: "PRD: incluir QR Code PIX no boleto", Meta: json.RawMessage(`{"origem":"ui"}`)},
	)
	if err != nil {
		t.Fatalf("CriarDemandaComChat: %v", err)
	}
	if dem.ID == 0 || dem.Status != StatusDemandaRecebida {
		t.Fatalf("demanda = %+v, quero id e status recebida", dem)
	}
	if msg.ID == 0 || msg.DemandID != dem.ID || msg.Papel != PapelUser {
		t.Fatalf("mensagem = %+v, quero vinculada à demanda como user", msg)
	}

	msgs, err := d.ListarMensagensChat(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ListarMensagensChat: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Conteudo != "PRD: incluir QR Code PIX no boleto" {
		t.Fatalf("chat persistido = %+v, quero a primeira mensagem", msgs)
	}
	if string(msgs[0].Meta) != `{"origem":"ui"}` {
		t.Fatalf("meta persistida = %q", string(msgs[0].Meta))
	}
}

func TestCriarDemandaComChatProjetoInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, _, err := d.CriarDemandaComChat(context.Background(),
		Demanda{ProjectID: 999, Titulo: "x"}, MensagemChat{Conteudo: "y"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}
