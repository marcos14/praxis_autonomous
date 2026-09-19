package db

import (
	"context"
	"errors"
	"testing"
)

func criarGrupoTeste(t *testing.T, d *DB, sufixo string, membros ...int64) Grupo {
	t.Helper()
	g, err := d.CriarGrupo(context.Background(), Grupo{
		Nome: "Grupo " + sufixo, Slug: "grupo-" + sufixo, Descricao: "solução " + sufixo, Ativo: true,
	}, membros)
	if err != nil {
		t.Fatalf("criar grupo de teste: %v", err)
	}
	return g
}

func TestCriarGrupoComMembrosOrdenados(t *testing.T) {
	d := abrirTemp(t)
	p1 := criarProjetoTeste(t, d, "g1")
	p2 := criarProjetoTeste(t, d, "g2")

	g := criarGrupoTeste(t, d, "a", p2, p1)
	if g.ID == 0 || g.CriadoEm == "" {
		t.Fatalf("grupo sem id/criado_em: %+v", g)
	}
	if len(g.Membros) != 2 {
		t.Fatalf("membros = %d, quero 2", len(g.Membros))
	}
	// A ordem do slice é preservada: p2 é o principal (ordem 0).
	if g.Membros[0].ProjectID != p2 || g.Membros[0].Ordem != 0 {
		t.Fatalf("principal = %+v, quero projeto %d na ordem 0", g.Membros[0], p2)
	}
	if g.Membros[1].ProjectID != p1 || g.Membros[1].Ordem != 1 {
		t.Fatalf("secundário = %+v, quero projeto %d na ordem 1", g.Membros[1], p1)
	}
	if g.Membros[0].Nome == "" || g.Membros[0].Pasta == "" {
		t.Fatalf("membro sem nome/pasta resolvidos: %+v", g.Membros[0])
	}

	// N:N — o mesmo projeto pode estar em outro grupo.
	g2 := criarGrupoTeste(t, d, "b", p1)
	if len(g2.Membros) != 1 || g2.Membros[0].ProjectID != p1 {
		t.Fatalf("segundo grupo = %+v, quero p1 como membro", g2.Membros)
	}
}

func TestCriarGrupoSlugDuplicado(t *testing.T) {
	d := abrirTemp(t)
	p1 := criarProjetoTeste(t, d, "gd")
	criarGrupoTeste(t, d, "dup", p1)
	_, err := d.CriarGrupo(context.Background(), Grupo{Nome: "Outro", Slug: "grupo-dup", Ativo: true}, []int64{p1})
	if !errors.Is(err, ErrSlugDuplicado) {
		t.Fatalf("erro = %v, quero ErrSlugDuplicado", err)
	}
}

func TestCriarGrupoMembroInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.CriarGrupo(context.Background(), Grupo{Nome: "X", Slug: "grupo-x", Ativo: true}, []int64{999})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestAtualizarGrupoRedefineMembros(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	p1 := criarProjetoTeste(t, d, "ga1")
	p2 := criarProjetoTeste(t, d, "ga2")
	g := criarGrupoTeste(t, d, "att", p1)

	g.Nome = "Grupo renomeado"
	g.Descricao = "nova descrição"
	atualizado, err := d.AtualizarGrupo(ctx, g, []int64{p2, p1})
	if err != nil {
		t.Fatalf("AtualizarGrupo: %v", err)
	}
	if atualizado.Nome != "Grupo renomeado" || atualizado.Descricao != "nova descrição" {
		t.Fatalf("dados não atualizados: %+v", atualizado)
	}
	if len(atualizado.Membros) != 2 || atualizado.Membros[0].ProjectID != p2 {
		t.Fatalf("membros = %+v, quero p2 como novo principal", atualizado.Membros)
	}
}

func TestExcluirGrupoCascateiaMembrosEConsultas(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	p1 := criarProjetoTeste(t, d, "gc")
	g := criarGrupoTeste(t, d, "casc", p1)

	c, _, err := d.CriarConsultaComChat(ctx,
		Consulta{GroupID: &g.ID, Titulo: "Como implantar?"},
		MensagemConsulta{Conteudo: "Como faço a implantação do Vulcano Chat?"})
	if err != nil {
		t.Fatalf("CriarConsultaComChat: %v", err)
	}

	if err := d.ExcluirGrupo(ctx, g.ID); err != nil {
		t.Fatalf("ExcluirGrupo: %v", err)
	}
	if _, err := d.ObterConsulta(ctx, c.ID); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("consulta do grupo sobreviveu ao cascade: %v", err)
	}
	var n int
	if err := d.Leitor.QueryRow(`SELECT COUNT(*) FROM project_group_members WHERE group_id = ?`, g.ID).Scan(&n); err != nil {
		t.Fatalf("contar membros: %v", err)
	}
	if n != 0 {
		t.Fatalf("membros sobraram após excluir grupo: %d", n)
	}
}

func TestCriarConsultaExigeProjetoOuGrupoExclusivo(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := criarProjetoTeste(t, d, "cx")
	g := criarGrupoTeste(t, d, "cx", pid)

	// Ambos preenchidos viola o CHECK.
	if _, _, err := d.CriarConsultaComChat(ctx,
		Consulta{ProjectID: &pid, GroupID: &g.ID}, MensagemConsulta{Conteudo: "x"}); err == nil {
		t.Fatal("consulta com projeto E grupo deveria falhar (CHECK)")
	}
	// Nenhum preenchido também viola.
	if _, _, err := d.CriarConsultaComChat(ctx,
		Consulta{}, MensagemConsulta{Conteudo: "x"}); err == nil {
		t.Fatal("consulta sem projeto nem grupo deveria falhar (CHECK)")
	}
}

func TestCriarConsultaComChatTransacional(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := criarProjetoTeste(t, d, "ct")

	c, msg, err := d.CriarConsultaComChat(ctx,
		Consulta{ProjectID: &pid, Titulo: "Baixa de títulos"},
		MensagemConsulta{Conteudo: "Como funciona a baixa de títulos?"})
	if err != nil {
		t.Fatalf("CriarConsultaComChat: %v", err)
	}
	if c.ID == 0 || c.Status != StatusConsultaOciosa {
		t.Fatalf("consulta = %+v, quero id e status ociosa", c)
	}
	if msg.ID == 0 || msg.ConsultaID != c.ID || msg.Papel != PapelConsultaUser {
		t.Fatalf("mensagem = %+v, quero vinculada como user", msg)
	}

	msgs, err := d.ListarMensagensConsulta(ctx, c.ID)
	if err != nil {
		t.Fatalf("ListarMensagensConsulta: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Conteudo != "Como funciona a baixa de títulos?" {
		t.Fatalf("chat persistido = %+v", msgs)
	}
}

func TestListarConsultasComNomesEFiltros(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := criarProjetoTeste(t, d, "cl")
	g := criarGrupoTeste(t, d, "cl", pid)

	c1, _, err := d.CriarConsultaComChat(ctx, Consulta{ProjectID: &pid}, MensagemConsulta{Conteudo: "a"})
	if err != nil {
		t.Fatalf("consulta de projeto: %v", err)
	}
	c2, _, err := d.CriarConsultaComChat(ctx, Consulta{GroupID: &g.ID}, MensagemConsulta{Conteudo: "b"})
	if err != nil {
		t.Fatalf("consulta de grupo: %v", err)
	}

	todas, err := d.ListarConsultas(ctx, FiltroConsultas{})
	if err != nil {
		t.Fatalf("ListarConsultas: %v", err)
	}
	if len(todas) != 2 {
		t.Fatalf("len = %d, quero 2", len(todas))
	}
	for _, c := range todas {
		switch c.ID {
		case c1.ID:
			if c.ProjetoNome == "" || c.GrupoNome != "" {
				t.Fatalf("consulta de projeto sem nome resolvido: %+v", c)
			}
		case c2.ID:
			if c.GrupoNome == "" || c.ProjetoNome != "" {
				t.Fatalf("consulta de grupo sem nome resolvido: %+v", c)
			}
		}
	}

	soProjeto, err := d.ListarConsultas(ctx, FiltroConsultas{ProjectID: pid})
	if err != nil {
		t.Fatalf("filtrar por projeto: %v", err)
	}
	if len(soProjeto) != 1 || soProjeto[0].ID != c1.ID {
		t.Fatalf("filtro por projeto = %+v, quero só c1", soProjeto)
	}
	soGrupo, err := d.ListarConsultas(ctx, FiltroConsultas{GroupID: g.ID})
	if err != nil {
		t.Fatalf("filtrar por grupo: %v", err)
	}
	if len(soGrupo) != 1 || soGrupo[0].ID != c2.ID {
		t.Fatalf("filtro por grupo = %+v, quero só c2", soGrupo)
	}
}

func TestAtualizarConsultaStatusECusto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := criarProjetoTeste(t, d, "cs")
	c, _, err := d.CriarConsultaComChat(ctx, Consulta{ProjectID: &pid}, MensagemConsulta{Conteudo: "x"})
	if err != nil {
		t.Fatalf("CriarConsultaComChat: %v", err)
	}

	c.Status = StatusConsultaPensando
	c.CustoUSD = 0.42
	c.Titulo = "Título derivado"
	atual, err := d.AtualizarConsulta(ctx, c)
	if err != nil {
		t.Fatalf("AtualizarConsulta: %v", err)
	}
	if atual.Status != StatusConsultaPensando || atual.CustoUSD != 0.42 || atual.Titulo != "Título derivado" {
		t.Fatalf("consulta atualizada = %+v", atual)
	}
}

func TestMensagemConsultaPapelInvalido(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := criarProjetoTeste(t, d, "cp")
	c, _, err := d.CriarConsultaComChat(ctx, Consulta{ProjectID: &pid}, MensagemConsulta{Conteudo: "x"})
	if err != nil {
		t.Fatalf("CriarConsultaComChat: %v", err)
	}
	// analista é papel do chat de DEMANDAS, não de consultas.
	if _, err := d.CriarMensagemConsulta(ctx, MensagemConsulta{ConsultaID: c.ID, Papel: "analista", Conteudo: "x"}); !errors.Is(err, ErrPapelInvalido) {
		t.Fatalf("erro = %v, quero ErrPapelInvalido", err)
	}
}

func TestExecucaoConsultaCicloCompleto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := criarProjetoTeste(t, d, "cr")
	c, _, err := d.CriarConsultaComChat(ctx, Consulta{ProjectID: &pid}, MensagemConsulta{Conteudo: "x"})
	if err != nil {
		t.Fatalf("CriarConsultaComChat: %v", err)
	}

	// Antes de qualquer execução com log, não há alvo para o SSE.
	if _, tem, err := d.UltimaExecucaoConsultaComLog(ctx, c.ID); err != nil || tem {
		t.Fatalf("UltimaExecucaoConsultaComLog antes = tem %v, err %v; quero false, nil", tem, err)
	}

	e, err := d.CriarExecucaoConsulta(ctx, ExecucaoConsulta{
		ConsultaID: &c.ID, Operacao: OperacaoConsultor, Engine: "claude", Conta: "principal",
	})
	if err != nil {
		t.Fatalf("CriarExecucaoConsulta: %v", err)
	}
	if e.ID == 0 || e.IniciadoEm == "" {
		t.Fatalf("execução sem id/iniciado_em: %+v", e)
	}

	e.CustoUSD = 0.10
	e.TokensIn = 1000
	e.TokensOut = 200
	e.LogRef = `C:\logs\consultor-1.jsonl`
	e.TerminadoEm = "2026-07-18T12:00:00.000Z"
	fechada, err := d.AtualizarExecucaoConsulta(ctx, e)
	if err != nil {
		t.Fatalf("AtualizarExecucaoConsulta: %v", err)
	}
	if fechada.CustoUSD != 0.10 || fechada.LogRef == "" {
		t.Fatalf("execução fechada = %+v", fechada)
	}
	if fechada.Conta != "principal" {
		t.Fatalf("conta = %q, quero principal", fechada.Conta)
	}

	alvo, tem, err := d.UltimaExecucaoConsultaComLog(ctx, c.ID)
	if err != nil || !tem || alvo.ID != e.ID {
		t.Fatalf("UltimaExecucaoConsultaComLog = %+v, tem %v, err %v", alvo, tem, err)
	}

	// Execução de overview (sem consulta, com projeto).
	ov, err := d.CriarExecucaoConsulta(ctx, ExecucaoConsulta{
		ProjectID: &pid, Operacao: OperacaoOverview, Engine: "claude",
	})
	if err != nil {
		t.Fatalf("execução de overview: %v", err)
	}
	if ov.ConsultaID != nil || ov.ProjectID == nil {
		t.Fatalf("execução de overview = %+v, quero consulta nula e projeto preenchido", ov)
	}
}

func TestAtualizarOverviewDoProjeto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := criarProjetoTeste(t, d, "ov")

	if err := d.AtualizarOverview(ctx, pid, "## Objetivo\nSistema de cobrança."); err != nil {
		t.Fatalf("AtualizarOverview: %v", err)
	}
	p, err := d.ObterProjeto(ctx, pid)
	if err != nil {
		t.Fatalf("ObterProjeto: %v", err)
	}
	if p.OverviewMD != "## Objetivo\nSistema de cobrança." || p.OverviewEm == "" {
		t.Fatalf("overview = %q / em %q", p.OverviewMD, p.OverviewEm)
	}

	// O CRUD comum não pode apagar o overview.
	p.Nome = "Renomeado"
	if _, err := d.AtualizarProjeto(ctx, p); err != nil {
		t.Fatalf("AtualizarProjeto: %v", err)
	}
	depois, err := d.ObterProjeto(ctx, pid)
	if err != nil {
		t.Fatalf("ObterProjeto 2: %v", err)
	}
	if depois.OverviewMD == "" {
		t.Fatal("AtualizarProjeto apagou o overview")
	}

	if err := d.AtualizarOverview(ctx, 999, "x"); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("overview de projeto inexistente = %v, quero ErrNaoEncontrado", err)
	}
}
