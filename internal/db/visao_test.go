package db

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

// grupoDeUsuariosTeste cria um grupo de usuários e vincula os membros.
func grupoDeUsuariosTeste(t *testing.T, d *DB, nome string, membros ...int64) int64 {
	t.Helper()
	ctx := context.Background()
	g, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: nome})
	if err != nil {
		t.Fatalf("criar grupo de usuários %s: %v", nome, err)
	}
	for _, m := range membros {
		if err := d.DefinirGrupoDoUsuario(ctx, m, &g.ID); err != nil {
			t.Fatalf("vincular %d ao grupo %s: %v", m, nome, err)
		}
	}
	return g.ID
}

// consultaVis cria uma consulta no projeto com criador (nil = sem dono) e
// visibilidade dados; o título identifica a consulta nas asserções.
func consultaVis(t *testing.T, d *DB, proj int64, criador *int64, vis, titulo string) Consulta {
	t.Helper()
	c, _, err := d.CriarConsultaComChat(context.Background(),
		Consulta{ProjectID: &proj, Titulo: titulo, CriadoPor: criador, Visibilidade: vis},
		MensagemConsulta{Conteudo: "pergunta"})
	if err != nil {
		t.Fatalf("criar consulta %s: %v", titulo, err)
	}
	return c
}

// titulos devolve os títulos de uma listagem, ordenados, como "a b c".
func titulos(cs []Consulta) string {
	ts := make([]string, 0, len(cs))
	for _, c := range cs {
		ts = append(ts, c.Titulo)
	}
	sort.Strings(ts)
	return strings.Join(ts, " ")
}

func ptr(v int64) *int64 { return &v }

// cenarioVisibilidade monta: grupo G1 = {ana, bia}; G2 = {caio}; dan sem grupo.
// Consultas: ana-privada, ana-grupo, ana-publica, caio-grupo, dan-grupo (dan
// não tem grupo: só ele vê) e uma sem dono.
type cenarioVis struct {
	ana, bia, caio, dan int64
	g1, g2              int64
}

func montarCenarioVisibilidade(t *testing.T, d *DB) cenarioVis {
	t.Helper()
	proj := criarProjetoTeste(t, d, "vis")
	c := cenarioVis{
		ana: criarUsuarioTeste(t, d, "ana"), bia: criarUsuarioTeste(t, d, "bia"),
		caio: criarUsuarioTeste(t, d, "caio"), dan: criarUsuarioTeste(t, d, "dan"),
	}
	c.g1 = grupoDeUsuariosTeste(t, d, "G1", c.ana, c.bia)
	c.g2 = grupoDeUsuariosTeste(t, d, "G2", c.caio)
	consultaVis(t, d, proj, ptr(c.ana), VisibilidadePrivada, "ana-privada")
	consultaVis(t, d, proj, ptr(c.ana), VisibilidadeGrupo, "ana-grupo")
	consultaVis(t, d, proj, ptr(c.ana), VisibilidadePublica, "ana-publica")
	consultaVis(t, d, proj, ptr(c.caio), VisibilidadeGrupo, "caio-grupo")
	consultaVis(t, d, proj, ptr(c.dan), VisibilidadeGrupo, "dan-grupo")
	consultaVis(t, d, proj, nil, "", "sem-dono")
	return c
}

func TestVisaoRegraDeDonoEmConsultas(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	cen := montarCenarioVisibilidade(t, d)

	listar := func(v Visao) string {
		t.Helper()
		cs, err := d.ListarConsultas(ctx, FiltroConsultas{Visao: v})
		if err != nil {
			t.Fatalf("ListarConsultas: %v", err)
		}
		return titulos(cs)
	}
	dono := func(uid int64, semDono string, grupo int64) Visao {
		return Visao{Usuario: uid, Dono: &uid, SemDono: semDono, SemDonoGrupo: grupo}
	}

	casos := []struct {
		nome  string
		visao Visao
		quero string
	}{
		{"visão total (admin/token) vê tudo", Visao{}, "ana-grupo ana-privada ana-publica caio-grupo dan-grupo sem-dono"},
		{"ana vê as próprias (3)", dono(cen.ana, SemDonoAdmins, 0), "ana-grupo ana-privada ana-publica"},
		{"bia (mesmo grupo de ana) vê grupo e pública de ana", dono(cen.bia, SemDonoAdmins, 0), "ana-grupo ana-publica"},
		{"caio (outro grupo) vê só a pública e a própria", dono(cen.caio, SemDonoAdmins, 0), "ana-publica caio-grupo"},
		{"dan (sem grupo) vê a pública e a própria 'grupo'", dono(cen.dan, SemDonoAdmins, 0), "ana-publica dan-grupo"},
		{"sem dono = publica → todos veem", dono(cen.caio, SemDonoPublica, 0), "ana-publica caio-grupo sem-dono"},
		{"sem dono = grupo G1 → bia vê", dono(cen.bia, SemDonoGrupo, cen.g1), "ana-grupo ana-publica sem-dono"},
		{"sem dono = grupo G1 → caio não vê", dono(cen.caio, SemDonoGrupo, cen.g1), "ana-publica caio-grupo"},
		{"config ilegível cai em admins", dono(cen.caio, "qualquer", 0), "ana-publica caio-grupo"},
		{"escopo meus (ana)", Visao{Usuario: cen.ana, Dono: ptr(cen.ana), Escopo: EscopoMeus}, "ana-grupo ana-privada ana-publica"},
		{"escopo meus (bia, sem consultas)", Visao{Usuario: cen.bia, Dono: ptr(cen.bia), Escopo: EscopoMeus}, ""},
		{"escopo grupo (bia): criadas por G1 e visíveis", Visao{Usuario: cen.bia, Dono: ptr(cen.bia), Escopo: EscopoGrupo}, "ana-grupo ana-publica"},
		{"escopo grupo para admin (bia sem regra de dono) vê a privada de ana", Visao{Usuario: cen.bia, Escopo: EscopoGrupo}, "ana-grupo ana-privada ana-publica"},
		{"escopo todos explícito = sem escopo", Visao{Usuario: cen.bia, Dono: ptr(cen.bia), Escopo: EscopoTodos}, "ana-grupo ana-publica"},
		{"escopo sem usuário (token) não filtra", Visao{Escopo: EscopoMeus}, "ana-grupo ana-privada ana-publica caio-grupo dan-grupo sem-dono"},
	}
	for _, tc := range casos {
		if got := listar(tc.visao); got != tc.quero {
			t.Errorf("%s: %q, quero %q", tc.nome, got, tc.quero)
		}
	}
}

func TestVisaoCombinaComFiltroDeProjetoEVisibilidadeDefault(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	p1 := criarProjetoTeste(t, d, "p1")
	p2 := criarProjetoTeste(t, d, "p2")
	ana := criarUsuarioTeste(t, d, "ana")
	bia := criarUsuarioTeste(t, d, "bia")
	consultaVis(t, d, p1, ptr(ana), VisibilidadePublica, "p1-publica")
	consultaVis(t, d, p2, ptr(ana), VisibilidadePublica, "p2-publica")
	semVis := consultaVis(t, d, p2, ptr(ana), "", "p2-default")

	if semVis.Visibilidade != VisibilidadePrivada {
		t.Fatalf("visibilidade default = %q, quero privada", semVis.Visibilidade)
	}
	obtida, err := d.ObterConsulta(ctx, semVis.ID)
	if err != nil || obtida.Visibilidade != VisibilidadePrivada {
		t.Fatalf("ObterConsulta: %+v %v", obtida, err)
	}
	cs, err := d.ListarConsultas(ctx, FiltroConsultas{ProjectID: p2, Visao: Visao{Usuario: bia, Dono: &bia}})
	if err != nil || titulos(cs) != "p2-publica" {
		t.Fatalf("filtro de projeto + dono = %q (%v), quero p2-publica", titulos(cs), err)
	}
	if _, _, err := d.CriarConsultaComChat(ctx, Consulta{ProjectID: &p1, Titulo: "x", Visibilidade: "secreta"},
		MensagemConsulta{Conteudo: "?"}); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("visibilidade inválida: err = %v, quero ErrValorInvalido", err)
	}
}

func TestDefinirVisibilidadeConsulta(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	p := criarProjetoTeste(t, d, "p")
	ana := criarUsuarioTeste(t, d, "ana")
	bia := criarUsuarioTeste(t, d, "bia")
	c := consultaVis(t, d, p, ptr(ana), VisibilidadePrivada, "c")

	visBia := Visao{Usuario: bia, Dono: &bia}
	if cs, _ := d.ListarConsultas(ctx, FiltroConsultas{Visao: visBia}); len(cs) != 0 {
		t.Fatalf("privada visível a bia: %+v", cs)
	}
	if err := d.DefinirVisibilidadeConsulta(ctx, c.ID, VisibilidadePublica); err != nil {
		t.Fatalf("DefinirVisibilidadeConsulta: %v", err)
	}
	if cs, _ := d.ListarConsultas(ctx, FiltroConsultas{Visao: visBia}); titulos(cs) != "c" {
		t.Fatalf("após tornar pública: %q, quero c", titulos(cs))
	}
	if err := d.DefinirVisibilidadeConsulta(ctx, c.ID, "x"); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("valor inválido: err = %v", err)
	}
	if err := d.DefinirVisibilidadeConsulta(ctx, 9999, VisibilidadeGrupo); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("inexistente: err = %v", err)
	}
}
