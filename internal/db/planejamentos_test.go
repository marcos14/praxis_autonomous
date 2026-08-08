package db

import (
	"context"
	"errors"
	"testing"
)

func TestCriarPlanejamentoExigeAlvoExclusivoEEnumsValidos(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-a")

	// Sem projeto nem grupo: CHECK do banco recusa.
	if _, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{},
		MensagemPlanejamento{Conteudo: "x"}); err == nil {
		t.Fatal("planejamento sem alvo deveria falhar")
	}
	// Foco inválido é barrado na app.
	if _, _, err := d.CriarPlanejamentoComChat(ctx,
		Planejamento{ProjectID: &projID, Foco: "roadmap"},
		MensagemPlanejamento{Conteudo: "x"}); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("foco inválido = %v, quero ErrValorInvalido", err)
	}
	// Nível inválido idem.
	if _, _, err := d.CriarPlanejamentoComChat(ctx,
		Planejamento{ProjectID: &projID, NivelVisual: "cinema"},
		MensagemPlanejamento{Conteudo: "x"}); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("nível inválido = %v, quero ErrValorInvalido", err)
	}
}

func TestCriarPlanejamentoComChatTransacionalEDefaults(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-b")

	p, msg, err := d.CriarPlanejamentoComChat(ctx,
		Planejamento{ProjectID: &projID, Titulo: "Portal de boletos"},
		MensagemPlanejamento{Conteudo: "quero um portal"})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}
	if p.Status != StatusPlanejamentoOcioso || p.Foco != FocoPlanejamentoPRD ||
		p.NivelVisual != NivelVisualApresentacao {
		t.Fatalf("defaults = %+v, quero ocioso/prd/apresentacao", p)
	}
	if msg.PlanejamentoID != p.ID || msg.Papel != PapelPlanejamentoUser {
		t.Fatalf("primeira fala = %+v, quero papel user vinculado", msg)
	}

	msgs, err := d.ListarMensagensPlanejamento(ctx, p.ID)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("mensagens = %v (%v), quero exatamente a primeira", msgs, err)
	}
}

func TestListarPlanejamentosComNomesEFiltros(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-c")
	grupo := criarGrupoTeste(t, d, "plan-g", projID)

	if _, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{GroupID: &grupo.ID},
		MensagemPlanejamento{Conteudo: "b"}); err != nil {
		t.Fatal(err)
	}

	todos, err := d.ListarPlanejamentos(ctx, 0, 0)
	if err != nil || len(todos) != 2 {
		t.Fatalf("listar todos = %v (%v), quero 2", todos, err)
	}
	doProjeto, err := d.ListarPlanejamentos(ctx, projID, 0)
	if err != nil || len(doProjeto) != 1 || doProjeto[0].ProjetoNome == "" {
		t.Fatalf("filtro por projeto = %+v (%v), quero 1 com nome resolvido", doProjeto, err)
	}
	doGrupo, err := d.ListarPlanejamentos(ctx, 0, grupo.ID)
	if err != nil || len(doGrupo) != 1 || doGrupo[0].GrupoNome == "" {
		t.Fatalf("filtro por grupo = %+v (%v), quero 1 com nome resolvido", doGrupo, err)
	}
}

func TestAtualizarPlanejamentoStatusECusto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-d")
	p, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}

	p.Status = StatusPlanejamentoPensando
	p.CustoUSD = 1.5
	p.Foco = FocoPlanejamentoAmbos
	atual, err := d.AtualizarPlanejamento(ctx, p)
	if err != nil {
		t.Fatalf("atualizar: %v", err)
	}
	if atual.Status != StatusPlanejamentoPensando || atual.CustoUSD != 1.5 ||
		atual.Foco != FocoPlanejamentoAmbos {
		t.Fatalf("atualizado = %+v", atual)
	}

	p.Foco = "errado"
	if _, err := d.AtualizarPlanejamento(ctx, p); !errors.Is(err, ErrValorInvalido) {
		t.Fatalf("foco inválido no update = %v, quero ErrValorInvalido", err)
	}

	inexistente := atual
	inexistente.ID = 9999
	if _, err := d.AtualizarPlanejamento(ctx, inexistente); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("id inexistente = %v, quero ErrNaoEncontrado", err)
	}
}

func TestVinculosPlanejamentoDemanda(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-vinc")
	p, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}
	dem1, err := d.CriarDemanda(ctx, Demanda{ProjectID: projID, Titulo: "Entrega 1", Status: StatusDemandaRecebida})
	if err != nil {
		t.Fatal(err)
	}
	dem2, err := d.CriarDemanda(ctx, Demanda{ProjectID: projID, Titulo: "Variante B", Status: StatusDemandaRecebida})
	if err != nil {
		t.Fatal(err)
	}

	v1, err := d.CriarVinculoPlanejamentoDemanda(ctx, VinculoPlanejamentoDemanda{
		PlanejamentoID: p.ID, DemandID: dem1.ID, PRDRev: 3, ADRsRev: 1})
	if err != nil || v1.ID == 0 || v1.Tipo != TipoDemandaPlanejamentoCompleta {
		t.Fatalf("vínculo 1 = %+v (%v), quero tipo completa por default", v1, err)
	}
	// Um planejamento gera N demandas.
	if _, err := d.CriarVinculoPlanejamentoDemanda(ctx, VinculoPlanejamentoDemanda{
		PlanejamentoID: p.ID, DemandID: dem2.ID, PRDRev: 5}); err != nil {
		t.Fatalf("segundo vínculo: %v", err)
	}
	// A mesma demanda não entra duas vezes (UNIQUE).
	if _, err := d.CriarVinculoPlanejamentoDemanda(ctx, VinculoPlanejamentoDemanda{
		PlanejamentoID: p.ID, DemandID: dem1.ID}); err == nil {
		t.Fatal("vínculo duplicado deveria falhar")
	}

	// Listagem com título/status da demanda resolvidos.
	vinculos, err := d.ListarDemandasDoPlanejamento(ctx, p.ID)
	if err != nil || len(vinculos) != 2 {
		t.Fatalf("listar = %+v (%v), quero 2", vinculos, err)
	}
	if vinculos[0].DemandaTitulo != "Entrega 1" || vinculos[0].DemandaStatus != StatusDemandaRecebida ||
		vinculos[0].PRDRev != 3 || vinculos[0].ADRsRev != 1 {
		t.Fatalf("vínculo listado = %+v", vinculos[0])
	}

	// A contagem aparece no planejamento (Obter e Listar).
	got, err := d.ObterPlanejamento(ctx, p.ID)
	if err != nil || got.DemandasCriadas != 2 {
		t.Fatalf("demandas_criadas no Obter = %d (%v), quero 2", got.DemandasCriadas, err)
	}
	lista, err := d.ListarPlanejamentos(ctx, projID, 0)
	if err != nil || len(lista) != 1 || lista[0].DemandasCriadas != 2 {
		t.Fatalf("demandas_criadas no Listar = %+v (%v)", lista, err)
	}

	// Excluir o planejamento leva os vínculos (cascade); a demanda sobrevive.
	if err := d.ExcluirPlanejamento(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ObterDemanda(ctx, dem1.ID); err != nil {
		t.Fatalf("demanda deveria sobreviver à exclusão do planejamento: %v", err)
	}
}

func TestMensagemPlanejamentoPapelInvalido(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-e")
	p, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CriarMensagemPlanejamento(ctx, MensagemPlanejamento{
		PlanejamentoID: p.ID, Papel: "consultor", Conteudo: "x"}); !errors.Is(err, ErrPapelInvalido) {
		t.Fatalf("papel de outra feature = %v, quero ErrPapelInvalido", err)
	}
}

func TestRevisoesDeDocumentoDoPlanejamento(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-f")
	p, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}

	r1, err := d.SalvarRevisaoDocumento(ctx, p.ID, "prd.md", "v1")
	if err != nil || r1.Revisao != 1 {
		t.Fatalf("revisão 1 = %+v (%v)", r1, err)
	}
	r2, err := d.SalvarRevisaoDocumento(ctx, p.ID, "prd.md", "v2")
	if err != nil || r2.Revisao != 2 {
		t.Fatalf("revisão 2 = %+v (%v)", r2, err)
	}
	if _, err := d.SalvarRevisaoDocumento(ctx, p.ID, "adrs.md", "adr v1"); err != nil {
		t.Fatal(err)
	}

	// Mais recente sem informar revisão.
	ult, err := d.ObterDocumentoPlanejamento(ctx, p.ID, "prd.md", 0)
	if err != nil || ult.Revisao != 2 || ult.Conteudo != "v2" {
		t.Fatalf("mais recente = %+v (%v), quero a revisão 2", ult, err)
	}
	// Revisão específica.
	antiga, err := d.ObterDocumentoPlanejamento(ctx, p.ID, "prd.md", 1)
	if err != nil || antiga.Conteudo != "v1" {
		t.Fatalf("revisão 1 = %+v (%v)", antiga, err)
	}
	// Inexistente.
	if _, err := d.ObterDocumentoPlanejamento(ctx, p.ID, "prd.md", 9); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("revisão inexistente = %v, quero ErrNaoEncontrado", err)
	}

	// Listagem devolve só a mais recente de cada arquivo.
	docs, err := d.ListarDocumentosPlanejamento(ctx, p.ID)
	if err != nil || len(docs) != 2 {
		t.Fatalf("listar = %+v (%v), quero prd.md e adrs.md", docs, err)
	}
	for _, doc := range docs {
		if doc.Arquivo == "prd.md" && doc.Revisao != 2 {
			t.Fatalf("prd.md listado na revisão %d, quero 2", doc.Revisao)
		}
	}
}

func TestUpsertArtefatoIncrementaRevisaoSoQuandoHashMuda(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-g2")
	p, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}

	a1, err := d.UpsertArtefatoPlanejamento(ctx, ArtefatoPlanejamento{
		PlanejamentoID: p.ID, Arquivo: "apresentacao.html", Titulo: "Plano", Tamanho: 10, Hash: "h1"})
	if err != nil || a1.Revisao != 1 {
		t.Fatalf("primeiro upsert = %+v (%v)", a1, err)
	}
	// Mesmo hash: revisão não muda; título vazio não apaga o existente.
	a2, err := d.UpsertArtefatoPlanejamento(ctx, ArtefatoPlanejamento{
		PlanejamentoID: p.ID, Arquivo: "apresentacao.html", Tamanho: 10, Hash: "h1"})
	if err != nil || a2.Revisao != 1 || a2.Titulo != "Plano" {
		t.Fatalf("upsert idempotente = %+v (%v), quero revisão 1 e título preservado", a2, err)
	}
	// Hash novo: revisão sobe e a descrição nova entra.
	a3, err := d.UpsertArtefatoPlanejamento(ctx, ArtefatoPlanejamento{
		PlanejamentoID: p.ID, Arquivo: "apresentacao.html", Descricao: "com fluxograma", Tamanho: 20, Hash: "h2"})
	if err != nil || a3.Revisao != 2 || a3.Descricao != "com fluxograma" || a3.Tamanho != 20 {
		t.Fatalf("upsert com mudança = %+v (%v), quero revisão 2", a3, err)
	}

	// Remoção dos ausentes.
	if _, err := d.UpsertArtefatoPlanejamento(ctx, ArtefatoPlanejamento{
		PlanejamentoID: p.ID, Arquivo: "prototipo.html", Hash: "h3"}); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoverArtefatosAusentes(ctx, p.ID, []string{"apresentacao.html"}); err != nil {
		t.Fatal(err)
	}
	arts, err := d.ListarArtefatosPlanejamento(ctx, p.ID)
	if err != nil || len(arts) != 1 || arts[0].Arquivo != "apresentacao.html" {
		t.Fatalf("após remoção = %+v (%v), quero só apresentacao.html", arts, err)
	}
}

func TestExecucaoPlanejamentoCicloCompleto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-h")
	p, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}

	exec, err := d.CriarExecucaoPlanejamento(ctx, ExecucaoPlanejamento{
		PlanejamentoID: p.ID, Engine: "claude", Conta: "conta-a", Modelo: "opus"})
	if err != nil || exec.ID == 0 || exec.IniciadoEm == "" {
		t.Fatalf("criar execução = %+v (%v)", exec, err)
	}

	// Antes do log não há "última com log".
	if _, tem, err := d.UltimaExecucaoPlanejamentoComLog(ctx, p.ID); err != nil || tem {
		t.Fatalf("sem log = tem=%v (%v), quero false", tem, err)
	}

	exec.LogRef = "logs/estrategista-p1.jsonl"
	exec.CustoUSD = 0.4
	exec.TokensIn = 100
	exec.TokensOut = 50
	exec.TerminadoEm = "2026-08-07T12:00:00.000Z"
	atual, err := d.AtualizarExecucaoPlanejamento(ctx, exec)
	if err != nil || atual.CustoUSD != 0.4 || atual.LogRef == "" {
		t.Fatalf("atualizar execução = %+v (%v)", atual, err)
	}

	ult, tem, err := d.UltimaExecucaoPlanejamentoComLog(ctx, p.ID)
	if err != nil || !tem || ult.ID != exec.ID {
		t.Fatalf("última com log = %+v tem=%v (%v)", ult, tem, err)
	}

	// Cascade: excluir o planejamento leva execuções, mensagens, docs e artefatos.
	if err := d.ExcluirPlanejamento(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ObterExecucaoPlanejamento(ctx, exec.ID); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("execução após cascade = %v, quero ErrNaoEncontrado", err)
	}
}

func TestUsuarioVePlanejamentoSegueACLDoProjeto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projID := criarProjetoTeste(t, d, "plan-acl")
	dono := criarUsuarioTeste(t, d, "dono-plan")
	outro := criarUsuarioTeste(t, d, "outro-plan")

	p, _, err := d.CriarPlanejamentoComChat(ctx, Planejamento{ProjectID: &projID},
		MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}

	// Sem ACL: todos veem.
	if ve, err := d.UsuarioVePlanejamento(ctx, outro, p.ID); err != nil || !ve {
		t.Fatalf("sem ACL = %v (%v), quero visível", ve, err)
	}
	// Restringe o projeto ao dono.
	if err := d.DefinirAcessoProjeto(ctx, projID, []int64{dono}, nil); err != nil {
		t.Fatalf("definir acesso: %v", err)
	}
	if ve, _ := d.UsuarioVePlanejamento(ctx, dono, p.ID); !ve {
		t.Fatal("dono deveria ver o planejamento")
	}
	if ve, _ := d.UsuarioVePlanejamento(ctx, outro, p.ID); ve {
		t.Fatal("usuário fora da ACL não deveria ver o planejamento")
	}
	// Inexistente devolve true (o handler responde 404).
	if ve, err := d.UsuarioVePlanejamento(ctx, outro, 9999); err != nil || !ve {
		t.Fatalf("inexistente = %v (%v), quero true", ve, err)
	}
}
