package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// assuntos devolve os assuntos dos commits do worktree, do mais novo para o mais
// antigo — a leitura que os testes deste arquivo fazem do historico da branch.
func assuntos(t *testing.T, dir string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v — %s", err, out)
	}
	texto := strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if texto == "" {
		return nil
	}
	return strings.Split(texto, "\n")
}

func exigirArvoreLimpa(t *testing.T, dir string) {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v — %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("worktree deveria estar limpo, mas tem:\n%s", out)
	}
}

// TestFaseQueFalhaPreservaOTrabalhoEReexecucaoFunde cobre o caso que travava a
// demanda na pratica: o executor estoura o budget no meio, morre e deixa
// trabalho solto no worktree. Antes, a arvore suja fazia a proxima passada
// falhar na pre-checagem — "Retomar" errava para sempre e o usuario ficava sem
// saida na UI. Agora o trabalho vira commit de resguardo, a reexecucao parte
// dele (nao se repaga o que ja foi feito) e o commit final funde tudo: o
// historico continua com um commit por fase.
func TestFaseQueFalhaPreservaOTrabalhoEReexecucaoFunde(t *testing.T) {
	tentativa := 0
	m := stubMotor{nome: "claude", fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		if strings.Contains(op.RotuloLog, "revisor") {
			return &motor.ResultadoRun{Resultado: `{"veredito":"APROVADO","problemas":[]}`, LogPath: "rev.jsonl"}, nil
		}
		tentativa++
		if tentativa == 1 {
			// primeira tentativa: escreve metade do trabalho e morre no budget.
			if err := os.WriteFile(filepath.Join(op.Dir, "parte1.txt"), []byte("metade cara\n"), 0o644); err != nil {
				return nil, err
			}
			return &motor.ResultadoRun{IsError: true, Subtipo: "error_max_budget_usd", CustoUSD: 6, LogPath: "exec1.jsonl"}, nil
		}
		if err := os.WriteFile(filepath.Join(op.Dir, "parte2.txt"), []byte("resto\n"), 0o644); err != nil {
			return nil, err
		}
		return &motor.ResultadoRun{Resultado: "terminei a fase", CustoUSD: 1, LogPath: "exec2.jsonl"}, nil
	}}

	c, fase := contexto(t, seletorStub(m))

	// 1a passada: falha, mas o trabalho fica guardado e a arvore limpa.
	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("erro de infra inesperado: %v", err)
	}
	if res.Situacao != SituacaoFalhou {
		t.Fatalf("situacao = %q, esperava falhou", res.Situacao)
	}
	exigirArvoreLimpa(t, c.Worktree)
	if topo := assuntos(t, c.Worktree)[0]; !strings.HasPrefix(topo, PrefixoParcial("1")) || !strings.Contains(topo, MarcadorParcial) {
		t.Fatalf("topo = %q, esperava o commit de resguardo da fase 1", topo)
	}

	// 2a passada: e o que o botao "Retomar" faz — fase volta a pendente.
	fase, err = c.Store.ObterFase(c.Ctx, fase.ID)
	if err != nil {
		t.Fatalf("ObterFase: %v", err)
	}
	fase.Status = db.StatusFasePendente
	if fase, err = c.Store.AtualizarFase(c.Ctx, fase); err != nil {
		t.Fatalf("AtualizarFase: %v", err)
	}
	c.Fase = fase

	res, err = c.ExecutarFase()
	if err != nil {
		t.Fatalf("erro de infra inesperado na 2a passada: %v", err)
	}
	if res.Situacao != SituacaoConcluida {
		t.Fatalf("situacao = %q (erro %s), esperava concluida", res.Situacao, res.Erro)
	}
	exigirArvoreLimpa(t, c.Worktree)

	// o trabalho da 1a tentativa sobreviveu: nao foi descartado nem refeito.
	for _, nome := range []string{"parte1.txt", "parte2.txt"} {
		if _, err := os.Stat(filepath.Join(c.Worktree, nome)); err != nil {
			t.Fatalf("%s deveria estar no worktree: %v", nome, err)
		}
	}

	// e o historico ficou com um commit por fase (o parcial foi fundido).
	log := assuntos(t, c.Worktree)
	if len(log) != 2 {
		t.Fatalf("esperava 2 commits (inicial + fase), veio %d: %v", len(log), log)
	}
	if !strings.Contains(log[0], "Fase 1: Fase um [praxis]") {
		t.Fatalf("topo = %q, esperava o commit da fase concluida", log[0])
	}
	for _, a := range log {
		if strings.Contains(a, MarcadorParcial) {
			t.Fatalf("commit parcial deveria ter sido fundido, log: %v", log)
		}
	}
}

// TestPreChecagemPreservaTrabalhoExternoEmVezDeFalhar: com a arvore suja por
// trabalho que NAO e da pipeline (tipicamente a edicao manual de uma fase
// requer_humano, que ao ser concluida pela UI nao commita nada), a fase nao
// falha mais. O trabalho vira commit proprio — nem descartado (destruiria o
// trabalho da pessoa) nem misturado ao commit da fase.
func TestPreChecagemPreservaTrabalhoExternoEmVezDeFalhar(t *testing.T) {
	c, _ := contexto(t, seletorStub(motorHappy("claude")))
	if err := os.WriteFile(filepath.Join(c.Worktree, "feito-a-mao.txt"), []byte("trabalho manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("erro de infra inesperado: %v", err)
	}
	if res.Situacao != SituacaoConcluida {
		t.Fatalf("situacao = %q (erro %s), esperava concluida — arvore suja nao pode mais barrar a fase", res.Situacao, res.Erro)
	}
	exigirArvoreLimpa(t, c.Worktree)
	if _, err := os.Stat(filepath.Join(c.Worktree, "feito-a-mao.txt")); err != nil {
		t.Fatalf("o trabalho manual deveria ter sido preservado: %v", err)
	}

	log := assuntos(t, c.Worktree)
	achou := false
	for _, a := range log {
		if strings.Contains(a, MarcadorExterno) {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("esperava um commit %s no historico, veio: %v", MarcadorExterno, log)
	}
}

// TestContarParciaisDoTopoParaNoPrimeiroCommitDeOutraNatureza garante que so os
// resguardos DA FASE e no TOPO entram na conta: e essa conta que decide o que o
// commit final funde e o que o "reiniciar fase" joga fora. Um commit de fase
// concluida, de outra fase ou de trabalho manual nunca pode ser desfeito.
func TestContarParciaisDoTopoParaNoPrimeiroCommitDeOutraNatureza(t *testing.T) {
	dir := gitInit(t)
	commitar := func(arquivo, assunto string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, arquivo), []byte(assunto+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", assunto}} {
			if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v — %s", args, err, out)
			}
		}
	}

	if n := ContarParciaisDoTopo(dir, "1"); n != 0 {
		t.Fatalf("branch sem resguardo: n = %d, esperava 0", n)
	}

	commitar("a.txt", AssuntoParcial("1", "Fase um"))
	commitar("b.txt", AssuntoParcial("1", "Fase um"))
	if n := ContarParciaisDoTopo(dir, "1"); n != 2 {
		t.Fatalf("n = %d, esperava 2", n)
	}
	// o resguardo e de outra fase: nao conta.
	if n := ContarParciaisDoTopo(dir, "2"); n != 0 {
		t.Fatalf("fase 2: n = %d, esperava 0", n)
	}

	// trabalho manual por cima interrompe a contagem — nunca e desfeito.
	commitar("c.txt", "Trabalho manual antes da Fase 1 "+MarcadorExterno)
	if n := ContarParciaisDoTopo(dir, "1"); n != 0 {
		t.Fatalf("com commit externo no topo: n = %d, esperava 0", n)
	}
}
