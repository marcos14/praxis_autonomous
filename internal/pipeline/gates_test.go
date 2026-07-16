package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// TestRunnerGatesVerde: todos os comandos passam → Ok e log criado.
func TestRunnerGatesVerde(t *testing.T) {
	dir := t.TempDir()
	logs := t.TempDir()
	r := &RunnerGates{
		Gates:   []Gate{{Nome: "go", Comandos: []string{"build", "test"}}},
		DirLogs: logs,
	}
	var rodados []string
	r.Exec = func(_ context.Context, d, comando string, _ time.Duration) ([]byte, error) {
		if d != dir {
			t.Errorf("dir do comando = %q, esperava worktree %q", d, dir)
		}
		rodados = append(rodados, comando)
		return []byte("ok\n"), nil
	}

	res, err := r.Rodar(context.Background(), dir, db.Fase{Codigo: "1"})
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}
	if !res.Ok {
		t.Fatalf("esperava Ok, veio Gate=%q Erro=%q", res.Gate, res.Erro)
	}
	if strings.Join(rodados, ",") != "build,test" {
		t.Fatalf("comandos rodados = %v", rodados)
	}
	if _, err := os.Stat(res.LogPath); err != nil {
		t.Fatalf("log de gates ausente: %v", err)
	}
}

// TestRunnerGatesVermelhoParaNoPrimeiro: comando falha → Ok=false, Gate/Erro
// preenchidos e comandos seguintes NAO rodam.
func TestRunnerGatesVermelhoParaNoPrimeiro(t *testing.T) {
	r := &RunnerGates{Gates: []Gate{{Nome: "go", Comandos: []string{"build", "test"}}}}
	var rodados int
	r.Exec = func(context.Context, string, string, time.Duration) ([]byte, error) {
		rodados++
		return []byte("erro de compilacao\nlinha 2\n"), errors.New("exit status 1")
	}

	res, err := r.Rodar(context.Background(), t.TempDir(), db.Fase{Codigo: "2"})
	if err != nil {
		t.Fatalf("Rodar (nao deveria ser erro de infra): %v", err)
	}
	if res.Ok {
		t.Fatal("esperava gate vermelho")
	}
	if res.Gate != "go: build" {
		t.Fatalf("Gate = %q, esperava 'go: build'", res.Gate)
	}
	if !strings.Contains(res.Erro, "erro de compilacao") {
		t.Fatalf("Erro nao traz a saida do comando: %q", res.Erro)
	}
	if res.Ambiente {
		t.Fatal("nao deveria ser falha de ambiente")
	}
	if rodados != 1 {
		t.Fatalf("comandos rodados = %d, esperava parar no 1o", rodados)
	}
}

// TestRunnerGatesVermelhoReprovaAFase: end-to-end pela pipeline — gate vermelho
// que nao e corrigido reprova a fase (SituacaoFalhou).
func TestRunnerGatesVermelhoReprovaAFase(t *testing.T) {
	c, _ := contexto(t, seletorStub(motorHappy("claude")))
	c.Config.MaxCorrecoes = 0 // sem chance de correcao: 1o vermelho ja reprova
	c.Gates = &RunnerGates{
		Gates: []Gate{{Nome: "go", Comandos: []string{"go build ./..."}}},
		Exec: func(context.Context, string, string, time.Duration) ([]byte, error) {
			return []byte("./x.go:1: erro\n"), errors.New("exit status 2")
		},
	}

	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("ExecutarFase (erro de infra inesperado): %v", err)
	}
	if res.Situacao != SituacaoFalhou {
		t.Fatalf("situacao = %q, esperava falhou", res.Situacao)
	}
	fase, _ := c.Store.ObterFase(c.Ctx, c.Fase.ID)
	if fase.Status != db.StatusFaseFalhou {
		t.Fatalf("fase = %q, esperava falhou", fase.Status)
	}
}

// TestRunnerGatesAmbiente: comando nao encontrado → Ambiente=true (o pipeline
// para e pede intervencao humana, sem gastar correcoes).
func TestRunnerGatesAmbiente(t *testing.T) {
	r := &RunnerGates{Gates: []Gate{{Nome: "go", Comandos: []string{"gxo build"}}}}
	r.Exec = func(context.Context, string, string, time.Duration) ([]byte, error) {
		return []byte("sh: gxo: command not found\n"), errors.New("exit status 127")
	}
	res, err := r.Rodar(context.Background(), t.TempDir(), db.Fase{Codigo: "3"})
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}
	if res.Ok || !res.Ambiente {
		t.Fatalf("esperava vermelho de ambiente, veio Ok=%v Ambiente=%v", res.Ok, res.Ambiente)
	}
}

// TestRunnerGatesExtra: gate_extra da fase e rodado apos os gates fixos; um
// gate_extra inexistente e ignorado (nao reprova).
func TestRunnerGatesExtra(t *testing.T) {
	r := &RunnerGates{
		Gates:      []Gate{{Nome: "base", Comandos: []string{"base-cmd"}}},
		GatesExtra: []GateExtra{{Nome: "integracao", Comandos: []string{"int-cmd"}}},
	}
	var rodados []string
	r.Exec = func(_ context.Context, _, comando string, _ time.Duration) ([]byte, error) {
		rodados = append(rodados, comando)
		return nil, nil
	}

	res, err := r.Rodar(context.Background(), t.TempDir(), db.Fase{Codigo: "4", GateExtra: "integracao"})
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}
	if !res.Ok || strings.Join(rodados, ",") != "base-cmd,int-cmd" {
		t.Fatalf("Ok=%v rodados=%v", res.Ok, rodados)
	}

	// gate_extra inexistente: ignorado, permanece verde.
	rodados = nil
	res, err = r.Rodar(context.Background(), t.TempDir(), db.Fase{Codigo: "5", GateExtra: "nao-existe"})
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}
	if !res.Ok || strings.Join(rodados, ",") != "base-cmd" {
		t.Fatalf("gate_extra inexistente devia ser ignorado; Ok=%v rodados=%v", res.Ok, rodados)
	}
}

// TestRunnerGatesSomenteSeMudou: gate marcado como somente_se_mudou e pulado
// quando a arvore esta limpa.
func TestRunnerGatesSomenteSeMudou(t *testing.T) {
	wt := gitInit(t) // repo com arvore limpa
	r := &RunnerGates{
		Gates:   []Gate{{Nome: "cond", SomenteSeMudou: true, Comandos: []string{"cmd"}}},
		DirLogs: t.TempDir(), // fora do worktree, para nao sujar a arvore
	}
	var rodou bool
	r.Exec = func(context.Context, string, string, time.Duration) ([]byte, error) {
		rodou = true
		return nil, nil
	}
	res, err := r.Rodar(context.Background(), wt, db.Fase{Codigo: "6"})
	if err != nil {
		t.Fatalf("Rodar: %v", err)
	}
	if !res.Ok || rodou {
		t.Fatalf("gate somente_se_mudou devia ser pulado em arvore limpa (rodou=%v)", rodou)
	}
}

// TestSemaforoGatesSerializa: com limite 1, dois Rodar concorrentes nunca rodam
// gates ao mesmo tempo (semaforo GLOBAL serializa demandas paralelas).
func TestSemaforoGatesSerializa(t *testing.T) {
	sem := NovoSemaforoGates(1)
	var ativos, maxAtivos atomic.Int64
	exec := func(context.Context, string, string, time.Duration) ([]byte, error) {
		n := ativos.Add(1)
		for {
			m := maxAtivos.Load()
			if n <= m || maxAtivos.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		ativos.Add(-1)
		return nil, nil
	}

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := &RunnerGates{
				Gates: []Gate{{Nome: "g", Comandos: []string{"c1", "c2"}}},
				Sem:   sem,
				Exec:  exec,
			}
			if _, err := r.Rodar(context.Background(), t.TempDir(), db.Fase{Codigo: string(rune('a' + i))}); err != nil {
				t.Errorf("Rodar: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := maxAtivos.Load(); got != 1 {
		t.Fatalf("concorrencia maxima = %d, esperava 1 (semaforo global)", got)
	}
}

// TestSemaforoGatesLimiteMaior: com limite 2, ate 2 gates rodam juntos, nunca 3.
func TestSemaforoGatesLimiteMaior(t *testing.T) {
	sem := NovoSemaforoGates(2)
	var ativos, maxAtivos atomic.Int64
	exec := func(context.Context, string, string, time.Duration) ([]byte, error) {
		n := ativos.Add(1)
		for {
			m := maxAtivos.Load()
			if n <= m || maxAtivos.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		ativos.Add(-1)
		return nil, nil
	}

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := &RunnerGates{Gates: []Gate{{Nome: "g", Comandos: []string{"c"}}}, Sem: sem, Exec: exec}
			if _, err := r.Rodar(context.Background(), t.TempDir(), db.Fase{Codigo: string(rune('a' + i))}); err != nil {
				t.Errorf("Rodar: %v", err)
			}
		}(i)
	}
	wg.Wait()
	got := maxAtivos.Load()
	if got < 1 || got > 2 {
		t.Fatalf("concorrencia maxima = %d, esperava entre 1 e 2", got)
	}
}

// TestSemaforoGatesCtxCancelado: se o ctx e cancelado enquanto se espera o
// semaforo, Rodar devolve o erro do ctx (falha de infra tratada como pausa pela
// pipeline).
func TestSemaforoGatesCtxCancelado(t *testing.T) {
	sem := NovoSemaforoGates(1)
	// ocupa o unico slot e nao libera durante o teste
	if err := sem.Adquirir(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer sem.Liberar()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &RunnerGates{Gates: []Gate{{Nome: "g", Comandos: []string{"c"}}}, Sem: sem}
	_, err := r.Rodar(ctx, t.TempDir(), db.Fase{Codigo: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erro = %v, esperava context.Canceled", err)
	}
}

// TestNovoSemaforoGatesDefault: valores < 1 caem no default (1).
func TestNovoSemaforoGatesDefault(t *testing.T) {
	if cap(NovoSemaforoGates(0).slots) != MaxGatesSimultaneosDefault {
		t.Fatalf("limite 0 devia cair no default %d", MaxGatesSimultaneosDefault)
	}
	if cap(NovoSemaforoGates(-3).slots) != MaxGatesSimultaneosDefault {
		t.Fatal("limite negativo devia cair no default")
	}
	if cap(NovoSemaforoGates(4).slots) != 4 {
		t.Fatal("limite 4 devia dar capacidade 4")
	}
}

// TestSemaforoGatesNilLibera: semaforo nil nao bloqueia (usado em teste/sem
// limite configurado).
func TestSemaforoGatesNilLibera(t *testing.T) {
	var s *SemaforoGates
	if err := s.Adquirir(context.Background()); err != nil {
		t.Fatalf("nil.Adquirir devia liberar: %v", err)
	}
	s.Liberar() // nao deve entrar em panico
}

// TestResolverDirGate cobre a resolucao de dir de gate relativa ao worktree.
func TestResolverDirGate(t *testing.T) {
	raiz := t.TempDir()
	if got := resolverDirGate(raiz, ""); got != raiz {
		t.Errorf("vazio → %q, esperava %q", got, raiz)
	}
	if got := resolverDirGate(raiz, "."); got != raiz {
		t.Errorf(". → %q, esperava %q", got, raiz)
	}
	if got := resolverDirGate(raiz, "sub"); got != filepath.Join(raiz, "sub") {
		t.Errorf("sub → %q", got)
	}
	abs := filepath.Join(t.TempDir(), "outro")
	if got := resolverDirGate(raiz, abs); got != abs {
		t.Errorf("abs → %q, esperava %q", got, abs)
	}
}

// TestExecShellReal roda um comando de verdade pelo shell do sistema (build/test
// tipicos): valida a integracao com cmd/sh sem stub.
func TestExecShellReal(t *testing.T) {
	saida, err := execShell(context.Background(), t.TempDir(), "echo praxis", 30*time.Second)
	if err != nil {
		t.Fatalf("execShell: %v — %s", err, saida)
	}
	if !strings.Contains(string(saida), "praxis") {
		t.Fatalf("saida inesperada: %q", saida)
	}
}

// TestFalhaDeAmbienteExitCode cobre a deteccao por exit code (127/9009) alem das
// mensagens de texto.
func TestFalhaDeAmbienteExitCode(t *testing.T) {
	// exit code real 127 via shell
	err := runExit(127)
	if !falhaDeAmbiente([]byte("qualquer saida"), err) {
		t.Fatal("exit 127 devia ser falha de ambiente")
	}
	if falhaDeAmbiente([]byte("teste reprovado"), runExit(1)) {
		t.Fatal("exit 1 sem marcador nao e falha de ambiente")
	}
}

func runExit(code int) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit", strconv.Itoa(code))
	} else {
		cmd = exec.Command("sh", "-c", "exit "+strconv.Itoa(code))
	}
	return cmd.Run()
}
