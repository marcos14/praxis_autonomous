package estrategista

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// stubMotor é um Motor programável para os testes (não chama nenhum CLI).
type stubMotor struct {
	nome string
	fn   func(op motor.OpcoesRun) (*motor.ResultadoRun, error)
}

func (s stubMotor) Nome() string                   { return s.nome }
func (s stubMotor) Capacidades() motor.Capacidades { return motor.Capacidades{SchemaNativo: true} }
func (s stubMotor) Rodar(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
	return s.fn(op)
}

func abrirDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Abrir(filepath.Join(t.TempDir(), "praxis.db"))
	if err != nil {
		t.Fatalf("abrir db: %v", err)
	}
	t.Cleanup(func() { _ = d.Fechar() })
	return d
}

func criarProjetoComOverview(t *testing.T, d *db.DB, nome, overview string) db.Projeto {
	t.Helper()
	ctx := context.Background()
	p, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: nome, Slug: strings.ToLower(nome), Pasta: t.TempDir(),
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	if overview != "" {
		if err := d.AtualizarOverview(ctx, p.ID, overview); err != nil {
			t.Fatalf("gravar overview: %v", err)
		}
	}
	proj, err := d.ObterProjeto(ctx, p.ID)
	if err != nil {
		t.Fatalf("reler projeto: %v", err)
	}
	return proj
}

// servicoDeTeste monta um Servico com todos os seams stubados. A função fn é o
// comportamento do motor.
func servicoDeTeste(t *testing.T, d *db.DB, fn func(op motor.OpcoesRun) (*motor.ResultadoRun, error)) *Servico {
	t.Helper()
	return NovoServico(OpcoesServico{
		Store:            d,
		DirLogs:          t.TempDir(),
		DirPlanejamentos: t.TempDir(),
		Ctx:              context.Background(),
		Selecionar: func(nome string) (motor.Motor, error) {
			return stubMotor{nome: "claude", fn: fn}, nil
		},
		Prompt: func(_ context.Context, nome string) (string, error) {
			return "estrategista: {CONTEXTO_REPOS} ### {HISTORICO} ### {FOCO} ### {NIVEL_VISUAL}", nil
		},
		StatPasta:     func(string) error { return nil },
		StatusRepo:    func(string) (string, error) { return "", nil },
		AtualizarRepo: func(string, string) (string, error) { return "", nil },
	})
}

func criarPlanejamentoProjeto(t *testing.T, d *db.DB, proj db.Projeto, foco, nivel, mensagem string) db.Planejamento {
	t.Helper()
	p, _, err := d.CriarPlanejamentoComChat(context.Background(),
		db.Planejamento{ProjectID: &proj.ID, Titulo: "Planejamento teste", Foco: foco, NivelVisual: nivel},
		db.MensagemPlanejamento{Conteudo: mensagem})
	if err != nil {
		t.Fatalf("criar planejamento: %v", err)
	}
	return p
}

func resultadoJSON(t *testing.T, v any) (*motor.ResultadoRun, error) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal saída: %v", err)
	}
	return &motor.ResultadoRun{Estruturado: b, CustoUSD: 0.40, LogPath: "estrategista.jsonl"}, nil
}

func TestTurnoRespostaIngereDocumentosEArtefatos(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP", "## Objetivo\nSistema de cobrança.")
	plan := criarPlanejamentoProjeto(t, d, proj, "prd", "apresentacao", "quero um portal de boletos")

	var opRecebida motor.OpcoesRun
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		opRecebida = op
		// O harness escreve os arquivos na pasta de trabalho (op.Dir).
		if err := os.WriteFile(filepath.Join(op.Dir, "prd.md"), []byte("# PRD do portal\n\nObjetivo…"), 0o644); err != nil {
			t.Fatalf("escrever prd.md: %v", err)
		}
		if err := os.WriteFile(filepath.Join(op.Dir, "apresentacao.html"), []byte("<h1>Portal</h1>"), 0o644); err != nil {
			t.Fatalf("escrever apresentacao.html: %v", err)
		}
		return resultadoJSON(t, SaidaEstrategista{
			Tipo: TurnoResposta, RespostaMD: "Criei a primeira versão do PRD.",
			DocumentosAlterados: []string{"prd.md"},
			Artefatos:           []ArtefatoDeclarado{{Arquivo: "apresentacao.html", Titulo: "Apresentação do plano"}},
			Confianca:           "media",
		})
	})

	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}

	// O harness roda na pasta do planejamento, com o repo protegido em leitura.
	if opRecebida.Dir != s.Pasta(plan.ID) {
		t.Fatalf("Dir = %q, quero a pasta do planejamento %q", opRecebida.Dir, s.Pasta(plan.ID))
	}
	if opRecebida.SomenteLeitura {
		t.Fatal("o estrategista precisa escrever na pasta de trabalho — não pode ser SomenteLeitura")
	}
	if !opRecebida.ProibirCommit {
		t.Fatal("o estrategista nunca commita")
	}
	achouRepo := false
	for _, ad := range opRecebida.AddDirs {
		if ad == proj.Pasta {
			achouRepo = true
		}
	}
	if !achouRepo {
		t.Fatalf("AddDirs = %v, quero conter a pasta do repo %q", opRecebida.AddDirs, proj.Pasta)
	}
	protegido := false
	for _, dp := range opRecebida.DirsProtegidos {
		if dp == proj.Pasta {
			protegido = true
		}
	}
	if !protegido {
		t.Fatalf("DirsProtegidos = %v, quero conter a pasta do repo", opRecebida.DirsProtegidos)
	}
	for _, trecho := range []string{"Sistema de cobrança.", "Usuário: quero um portal de boletos",
		"PRD (visão de negócio)", "apresentacao.html"} {
		if !strings.Contains(opRecebida.Prompt, trecho) {
			t.Fatalf("prompt sem %q:\n%s", trecho, opRecebida.Prompt)
		}
	}
	if opRecebida.Schema != SchemaEstrategista {
		t.Fatal("schema do estrategista não foi passado ao motor")
	}

	// Documento versionado no banco.
	doc, err := d.ObterDocumentoPlanejamento(ctx, plan.ID, "prd.md", 0)
	if err != nil {
		t.Fatalf("ObterDocumentoPlanejamento: %v", err)
	}
	if doc.Revisao != 1 || !strings.Contains(doc.Conteudo, "PRD do portal") {
		t.Fatalf("doc = %+v, quero revisão 1 com o conteúdo do arquivo", doc)
	}

	// Artefato indexado com o título declarado.
	art, err := d.ObterArtefatoPlanejamento(ctx, plan.ID, "apresentacao.html")
	if err != nil {
		t.Fatalf("ObterArtefatoPlanejamento: %v", err)
	}
	if art.Titulo != "Apresentação do plano" || art.Revisao != 1 || art.Tamanho == 0 || art.Hash == "" {
		t.Fatalf("artefato = %+v, quero título/revisão/tamanho/hash preenchidos", art)
	}

	// Fala persistida com o meta do turno.
	msgs, _ := d.ListarMensagensPlanejamento(ctx, plan.ID)
	ultima := msgs[len(msgs)-1]
	if ultima.Papel != db.PapelPlanejamentoEstrategista {
		t.Fatalf("última fala papel = %q, quero estrategista", ultima.Papel)
	}
	var meta struct {
		Tipo       string              `json:"tipo"`
		Documentos []string            `json:"documentos"`
		Artefatos  []map[string]string `json:"artefatos"`
	}
	if err := json.Unmarshal(ultima.Meta, &meta); err != nil {
		t.Fatalf("meta inválido: %v", err)
	}
	if meta.Tipo != TurnoResposta || len(meta.Documentos) != 1 || meta.Documentos[0] != "prd.md" ||
		len(meta.Artefatos) != 1 || meta.Artefatos[0]["arquivo"] != "apresentacao.html" {
		t.Fatalf("meta = %+v, quero documentos e artefatos do turno", meta)
	}

	got, _ := d.ObterPlanejamento(ctx, plan.ID)
	if got.Status != db.StatusPlanejamentoOcioso {
		t.Fatalf("status = %q, quero ocioso", got.Status)
	}
	if got.CustoUSD != 0.40 {
		t.Fatalf("custo acumulado = %v, quero 0.40", got.CustoUSD)
	}
}

func TestTurnoSemMudancaNaoCriaRevisaoNova(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP2", "")
	plan := criarPlanejamentoProjeto(t, d, proj, "prd", "documento", "necessidade")

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		// Sempre grava o MESMO conteúdo — só o primeiro turno deve gerar revisão.
		if err := os.WriteFile(filepath.Join(op.Dir, "prd.md"), []byte("# PRD estável"), 0o644); err != nil {
			t.Fatal(err)
		}
		return resultadoJSON(t, SaidaEstrategista{Tipo: TurnoResposta, RespostaMD: "ok."})
	})

	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("turno 1: %v", err)
	}
	if _, err := d.CriarMensagemPlanejamento(ctx, db.MensagemPlanejamento{
		PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoUser, Conteudo: "mais um pedido"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("turno 2: %v", err)
	}

	doc, err := d.ObterDocumentoPlanejamento(ctx, plan.ID, "prd.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Revisao != 1 {
		t.Fatalf("revisão = %d, quero 1 (conteúdo idêntico não versiona)", doc.Revisao)
	}
}

func TestRedeDeSegurancaDetectaEscritaNoRepo(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP3", "")
	plan := criarPlanejamentoProjeto(t, d, proj, "prd", "documento", "necessidade")

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return resultadoJSON(t, SaidaEstrategista{Tipo: TurnoResposta, RespostaMD: "ok."})
	})
	// Antes do turno o repo está limpo; depois aparece um arquivo modificado.
	chamadas := 0
	s.statusRepo = func(dir string) (string, error) {
		chamadas++
		if chamadas > 1 {
			return " M internal/core/faturamento.go\n", nil
		}
		return "", nil
	}

	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("Responder deveria absorver a falha lógica: %v", err)
	}
	got, _ := d.ObterPlanejamento(ctx, plan.ID)
	if got.Status != db.StatusPlanejamentoFalhou {
		t.Fatalf("status = %q, quero falhou (rede de segurança)", got.Status)
	}
	if !strings.Contains(got.Erro, "faturamento.go") {
		t.Fatalf("erro = %q, quero citar o arquivo tocado", got.Erro)
	}
	msgs, _ := d.ListarMensagensPlanejamento(ctx, plan.ID)
	ultima := msgs[len(msgs)-1]
	if ultima.Papel != db.PapelPlanejamentoSistema || !strings.Contains(ultima.Conteudo, "faturamento.go") {
		t.Fatalf("última fala = %+v, quero fala de sistema citando o arquivo", ultima)
	}
}

func TestTurnoPerguntasDeClarificacao(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP4", "")
	plan := criarPlanejamentoProjeto(t, d, proj, "ambos", "prototipo", "quero melhorar a cobrança")

	var opRecebida motor.OpcoesRun
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		opRecebida = op
		return resultadoJSON(t, SaidaEstrategista{
			Tipo: TurnoPerguntas,
			Perguntas: []PerguntaEstrategista{
				{Pergunta: "Melhorar a régua de cobrança ou a experiência de pagamento?", Contexto: "mudam o escopo do PRD"},
				{Pergunta: "Há prazo regulatório?"},
			},
		})
	})

	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	// Fragmentos de foco/nível corretos para "ambos" + "prototipo".
	for _, trecho := range []string{"PRD + ADRs", "prototipo.html"} {
		if !strings.Contains(opRecebida.Prompt, trecho) {
			t.Fatalf("prompt sem %q:\n%s", trecho, opRecebida.Prompt)
		}
	}
	msgs, _ := d.ListarMensagensPlanejamento(ctx, plan.ID)
	ultima := msgs[len(msgs)-1]
	if !strings.Contains(ultima.Conteudo, "1. Melhorar a régua") ||
		!strings.Contains(ultima.Conteudo, "2. Há prazo regulatório?") {
		t.Fatalf("perguntas não formatadas: %q", ultima.Conteudo)
	}
}

func TestErroDoMotorCarimbaFalhouComFalaDeSistema(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP5", "")
	plan := criarPlanejamentoProjeto(t, d, proj, "prd", "apresentacao", "necessidade")

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return nil, fmt.Errorf("cli explodiu")
	})
	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("Responder deveria absorver a falha lógica: %v", err)
	}
	got, _ := d.ObterPlanejamento(ctx, plan.ID)
	if got.Status != db.StatusPlanejamentoFalhou || got.Erro == "" {
		t.Fatalf("planejamento = %+v, quero status falhou com erro", got)
	}
	msgs, _ := d.ListarMensagensPlanejamento(ctx, plan.ID)
	ultima := msgs[len(msgs)-1]
	if ultima.Papel != db.PapelPlanejamentoSistema {
		t.Fatalf("última fala = %+v, quero fala de sistema explicando a falha", ultima)
	}
}

func TestArtefatoRemovidoDoDiscoSaiDoIndice(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP6", "")
	plan := criarPlanejamentoProjeto(t, d, proj, "prd", "apresentacao", "necessidade")

	turno := 0
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		turno++
		if turno == 1 {
			if err := os.WriteFile(filepath.Join(op.Dir, "apresentacao.html"), []byte("<p>v1</p>"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			// O estrategista removeu o artefato obsoleto no segundo turno.
			if err := os.Remove(filepath.Join(op.Dir, "apresentacao.html")); err != nil {
				t.Fatal(err)
			}
		}
		return resultadoJSON(t, SaidaEstrategista{Tipo: TurnoResposta, RespostaMD: "ok."})
	})

	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("turno 1: %v", err)
	}
	if _, err := d.ObterArtefatoPlanejamento(ctx, plan.ID, "apresentacao.html"); err != nil {
		t.Fatalf("artefato deveria estar indexado após o turno 1: %v", err)
	}
	if _, err := d.CriarMensagemPlanejamento(ctx, db.MensagemPlanejamento{
		PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoUser, Conteudo: "tira o html"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Responder(ctx, plan.ID); err != nil {
		t.Fatalf("turno 2: %v", err)
	}
	if _, err := d.ObterArtefatoPlanejamento(ctx, plan.ID, "apresentacao.html"); err == nil {
		t.Fatal("artefato removido do disco deveria sair do índice")
	}
}
