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

// planejamentoVis cria um planejamento no projeto com criador e visibilidade dados.
func planejamentoVis(t *testing.T, d *DB, proj int64, criador *int64, vis, titulo string) Planejamento {
	t.Helper()
	p, _, err := d.CriarPlanejamentoComChat(context.Background(),
		Planejamento{ProjectID: &proj, Titulo: titulo, CriadoPor: criador, Visibilidade: vis},
		MensagemPlanejamento{Conteudo: "necessidade"})
	if err != nil {
		t.Fatalf("criar planejamento %s: %v", titulo, err)
	}
	return p
}

func titulosPlan(ps []Planejamento) string {
	ts := make([]string, 0, len(ps))
	for _, p := range ps {
		ts = append(ts, p.Titulo)
	}
	sort.Strings(ts)
	return strings.Join(ts, " ")
}

func TestVisaoRegraDeDonoEmPlanejamentos(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "pl")
	ana := criarUsuarioTeste(t, d, "ana")
	bia := criarUsuarioTeste(t, d, "bia")
	caio := criarUsuarioTeste(t, d, "caio")
	grupoDeUsuariosTeste(t, d, "G1", ana, bia)
	planejamentoVis(t, d, proj, ptr(ana), VisibilidadePrivada, "ana-privada")
	planejamentoVis(t, d, proj, ptr(ana), VisibilidadeGrupo, "ana-grupo")
	planejamentoVis(t, d, proj, ptr(ana), VisibilidadePublica, "ana-publica")
	semVis := planejamentoVis(t, d, proj, nil, "", "sem-dono")
	if semVis.Visibilidade != VisibilidadePrivada {
		t.Fatalf("default = %q, quero privada", semVis.Visibilidade)
	}

	listar := func(v Visao) string {
		t.Helper()
		ps, err := d.ListarPlanejamentos(ctx, FiltroPlanejamentos{Visao: v})
		if err != nil {
			t.Fatalf("ListarPlanejamentos: %v", err)
		}
		return titulosPlan(ps)
	}
	if got := listar(Visao{}); got != "ana-grupo ana-privada ana-publica sem-dono" {
		t.Errorf("visão total: %q", got)
	}
	if got := listar(Visao{Usuario: bia, Dono: &bia, SemDono: SemDonoAdmins}); got != "ana-grupo ana-publica" {
		t.Errorf("bia: %q", got)
	}
	if got := listar(Visao{Usuario: caio, Dono: &caio, SemDono: SemDonoPublica}); got != "ana-publica sem-dono" {
		t.Errorf("caio com sem-dono público: %q", got)
	}
	if got := listar(Visao{Usuario: ana, Dono: &ana, Escopo: EscopoMeus}); got != "ana-grupo ana-privada ana-publica" {
		t.Errorf("escopo meus: %q", got)
	}

	// visibilidade persistida e alterável.
	obtido, err := d.ObterPlanejamento(ctx, semVis.ID)
	if err != nil || obtido.Visibilidade != VisibilidadePrivada {
		t.Fatalf("ObterPlanejamento: %+v %v", obtido, err)
	}
	if err := d.DefinirVisibilidadePlanejamento(ctx, semVis.ID, VisibilidadePublica); err != nil {
		t.Fatalf("DefinirVisibilidadePlanejamento: %v", err)
	}
	if got := listar(Visao{Usuario: bia, Dono: &bia}); got != "ana-grupo ana-publica sem-dono" {
		t.Errorf("sem-dono tornado público: %q", got)
	}
	if err := d.DefinirVisibilidadePlanejamento(ctx, 9999, VisibilidadePublica); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("inexistente: %v", err)
	}
	if _, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &proj, Titulo: "x", Visibilidade: "oculta"},
		MensagemPlanejamento{Conteudo: "?"}); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("visibilidade inválida: %v", err)
	}
}

func TestVisaoACLNoSQLEChecagemPorID(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	aberto := criarProjetoTeste(t, d, "aberto")
	restrito := criarProjetoTeste(t, d, "restrito")
	ana := criarUsuarioTeste(t, d, "ana")
	bia := criarUsuarioTeste(t, d, "bia")
	// só ana enxerga o projeto restrito.
	if err := d.DefinirAcessoProjeto(ctx, restrito, []int64{ana}, nil); err != nil {
		t.Fatalf("ACL: %v", err)
	}
	grupo := criarGrupoTeste(t, d, "misto", aberto, restrito)

	cAberta := consultaVis(t, d, aberto, ptr(ana), VisibilidadePublica, "c-aberta")
	cRestrita := consultaVis(t, d, restrito, ptr(ana), VisibilidadePublica, "c-restrita")
	cPrivada := consultaVis(t, d, aberto, ptr(ana), VisibilidadePrivada, "c-privada")
	cGrupo, _, err := d.CriarConsultaComChat(ctx, Consulta{GroupID: &grupo.ID, Titulo: "c-grupo", CriadoPor: ptr(ana), Visibilidade: VisibilidadePublica},
		MensagemConsulta{Conteudo: "?"})
	if err != nil {
		t.Fatalf("consulta de grupo: %v", err)
	}
	pRestrito := planejamentoVis(t, d, restrito, ptr(ana), VisibilidadePublica, "p-restrito")
	pAberto := planejamentoVis(t, d, aberto, ptr(ana), VisibilidadePublica, "p-aberto")

	visBia := Visao{Usuario: bia, ACL: &bia, Dono: &bia}
	visAna := Visao{Usuario: ana, ACL: &ana, Dono: &ana}

	// bia: só o projeto aberto; o grupo esconde (um membro restrito); a privada não.
	cs, err := d.ListarConsultas(ctx, FiltroConsultas{Visao: visBia})
	if err != nil || titulos(cs) != "c-aberta" {
		t.Fatalf("consultas de bia: %q (%v)", titulos(cs), err)
	}
	if cs[0].CriadoPorNome != "ana" {
		t.Fatalf("criado_por_nome = %q, quero ana", cs[0].CriadoPorNome)
	}
	// ana: tudo dela, inclusive o grupo (vê os dois projetos).
	cs, _ = d.ListarConsultas(ctx, FiltroConsultas{Visao: visAna})
	if titulos(cs) != "c-aberta c-grupo c-privada c-restrita" {
		t.Fatalf("consultas de ana: %q", titulos(cs))
	}
	ps, _ := d.ListarPlanejamentos(ctx, FiltroPlanejamentos{Visao: visBia})
	if titulosPlan(ps) != "p-aberto" || ps[0].CriadoPorNome != "ana" {
		t.Fatalf("planejamentos de bia: %+v", ps)
	}
	// ACL sozinha (projetos.gerir não tem: ACL sim, dono não): admin de projetos
	// sem `*` continua não vendo o restrito.
	cs, _ = d.ListarConsultas(ctx, FiltroConsultas{Visao: Visao{Usuario: bia, Dono: &bia}})
	if titulos(cs) != "c-aberta c-grupo c-restrita" {
		t.Fatalf("bia ignorando ACL (só dono): %q", titulos(cs))
	}

	// checagem por id segue a mesma regra; inexistente devolve true.
	casos := []struct {
		nome  string
		fn    func() (bool, error)
		quero bool
	}{
		{"bia vê c-aberta", func() (bool, error) { return d.ConsultaVisivel(ctx, cAberta.ID, visBia) }, true},
		{"bia não vê c-restrita (ACL)", func() (bool, error) { return d.ConsultaVisivel(ctx, cRestrita.ID, visBia) }, false},
		{"bia não vê c-privada (dono)", func() (bool, error) { return d.ConsultaVisivel(ctx, cPrivada.ID, visBia) }, false},
		{"bia não vê c-grupo (grupo com projeto restrito)", func() (bool, error) { return d.ConsultaVisivel(ctx, cGrupo.ID, visBia) }, false},
		{"ana vê c-grupo", func() (bool, error) { return d.ConsultaVisivel(ctx, cGrupo.ID, visAna) }, true},
		{"admin vê c-privada", func() (bool, error) { return d.ConsultaVisivel(ctx, cPrivada.ID, Visao{}) }, true},
		{"inexistente → true", func() (bool, error) { return d.ConsultaVisivel(ctx, 9999, visBia) }, true},
		{"bia não vê p-restrito", func() (bool, error) { return d.PlanejamentoVisivel(ctx, pRestrito.ID, visBia) }, false},
		{"bia vê p-aberto", func() (bool, error) { return d.PlanejamentoVisivel(ctx, pAberto.ID, visBia) }, true},
	}
	for _, tc := range casos {
		got, err := tc.fn()
		if err != nil || got != tc.quero {
			t.Errorf("%s: %v (%v), quero %v", tc.nome, got, err, tc.quero)
		}
	}
}

func titulosDem(ds []Demanda) string {
	ts := make([]string, 0, len(ds))
	for _, d := range ds {
		ts = append(ts, d.Titulo)
	}
	sort.Strings(ts)
	return strings.Join(ts, " ")
}

func TestVisaoDemandasEEventos(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "dem")
	ana := criarUsuarioTeste(t, d, "ana")
	bia := criarUsuarioTeste(t, d, "bia")
	caio := criarUsuarioTeste(t, d, "caio")
	grupoDeUsuariosTeste(t, d, "G1", ana, bia)

	mk := func(criador *int64, vis, titulo string) Demanda {
		t.Helper()
		dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: titulo, CriadoPor: criador, Visibilidade: vis})
		if err != nil {
			t.Fatalf("criar demanda %s: %v", titulo, err)
		}
		return dem
	}
	privada := mk(ptr(ana), VisibilidadePrivada, "ana-privada")
	grupo := mk(ptr(ana), VisibilidadeGrupo, "ana-grupo")
	publica := mk(ptr(ana), VisibilidadePublica, "ana-publica")
	semDono := mk(nil, "", "sem-dono")
	if semDono.Visibilidade != VisibilidadePrivada {
		t.Fatalf("default = %q, quero privada", semDono.Visibilidade)
	}
	if _, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "x", Visibilidade: "oculta"}); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("visibilidade inválida: %v", err)
	}

	visBia := Visao{Usuario: bia, ACL: &bia, Dono: &bia}
	visCaio := Visao{Usuario: caio, ACL: &caio, Dono: &caio}

	// Listagens: dono + autor + visibilidade.
	lista, err := d.ListarDemandas(ctx, FiltroDemandas{Visao: visBia})
	if err != nil || titulosDem(lista) != "ana-grupo ana-publica" {
		t.Fatalf("ListarDemandas(bia) = %q (%v)", titulosDem(lista), err)
	}
	if lista[0].CriadoPorNome != "ana" || lista[0].Visibilidade == "" {
		t.Fatalf("autor/visibilidade não preenchidos: %+v", lista[0])
	}
	if lista, _ = d.ListarDemandas(ctx, FiltroDemandas{Visao: visCaio}); titulosDem(lista) != "ana-publica" {
		t.Fatalf("ListarDemandas(caio) = %q", titulosDem(lista))
	}
	if lista, _ = d.ListarDemandas(ctx, FiltroDemandas{Visao: Visao{Usuario: ana, Dono: &ana, Escopo: EscopoMeus}}); len(lista) != 3 {
		t.Fatalf("escopo meus (ana) = %d, quero 3", len(lista))
	}
	if lista, _ = d.ListarDemandas(ctx, FiltroDemandas{}); len(lista) != 4 {
		t.Fatalf("visão total = %d, quero 4", len(lista))
	}
	resumo, err := d.ListarDemandasResumo(ctx, FiltroDemandas{Visao: visBia})
	if err != nil || len(resumo) != 2 || resumo[0].Visibilidade == "" || resumo[0].CriadoPorNome != "ana" {
		t.Fatalf("ListarDemandasResumo(bia) = %+v (%v)", resumo, err)
	}
	porStatus, err := d.ListarDemandasPorStatus(ctx, []string{StatusDemandaRecebida}, visBia)
	if err != nil || titulosDem(porStatus) != "ana-grupo ana-publica" {
		t.Fatalf("ListarDemandasPorStatus(bia) = %q (%v)", titulosDem(porStatus), err)
	}
	obtida, err := d.ObterDemanda(ctx, grupo.ID)
	if err != nil || obtida.Visibilidade != VisibilidadeGrupo {
		t.Fatalf("ObterDemanda: %+v %v", obtida, err)
	}

	// Checagem por id.
	for _, tc := range []struct {
		nome  string
		id    int64
		v     Visao
		quero bool
	}{
		{"bia vê grupo", grupo.ID, visBia, true},
		{"bia não vê privada", privada.ID, visBia, false},
		{"caio não vê grupo", grupo.ID, visCaio, false},
		{"admin vê privada", privada.ID, Visao{}, true},
		{"inexistente → true", 9999, visBia, true},
	} {
		if got, err := d.DemandaVisivel(ctx, tc.id, tc.v); err != nil || got != tc.quero {
			t.Errorf("%s: %v (%v), quero %v", tc.nome, got, err, tc.quero)
		}
	}

	// Eventos: os de demanda invisível somem; globais e de demanda visível ficam.
	for _, ev := range []Evento{
		{ProjectID: &proj, DemandID: &privada.ID, Tipo: "t", Titulo: "ev-privada"},
		{ProjectID: &proj, DemandID: &publica.ID, Tipo: "t", Titulo: "ev-publica"},
		{Tipo: "t", Titulo: "ev-global"},
	} {
		if _, err := d.RegistrarEvento(ctx, ev); err != nil {
			t.Fatalf("registrar %s: %v", ev.Titulo, err)
		}
	}
	evs, err := d.ListarEventos(ctx, FiltroEventos{Visao: visCaio})
	if err != nil || len(evs) != 2 {
		t.Fatalf("ListarEventos(caio) = %+v (%v), quero ev-publica e ev-global", evs, err)
	}
	for _, e := range evs {
		if e.Titulo == "ev-privada" {
			t.Fatalf("evento de demanda privada vazou para caio")
		}
	}
	apos, err := d.EventosApos(ctx, 0, 10, visCaio)
	if err != nil || len(apos) != 2 {
		t.Fatalf("EventosApos(caio) = %d (%v), quero 2", len(apos), err)
	}
	if todos, _ := d.EventosApos(ctx, 0, 10, Visao{}); len(todos) != 3 {
		t.Fatalf("EventosApos(total) = %d, quero 3", len(todos))
	}

	// Mudar a visibilidade libera a demanda e os eventos dela.
	if err := d.DefinirVisibilidadeDemanda(ctx, privada.ID, VisibilidadePublica); err != nil {
		t.Fatalf("DefinirVisibilidadeDemanda: %v", err)
	}
	if lista, _ = d.ListarDemandas(ctx, FiltroDemandas{Visao: visCaio}); titulosDem(lista) != "ana-privada ana-publica" {
		t.Fatalf("após tornar pública: %q", titulosDem(lista))
	}
	if evs, _ = d.ListarEventos(ctx, FiltroEventos{Visao: visCaio}); len(evs) != 3 {
		t.Fatalf("eventos após tornar pública = %d, quero 3", len(evs))
	}
	if err := d.DefinirVisibilidadeDemanda(ctx, 9999, VisibilidadePublica); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("inexistente: %v", err)
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
