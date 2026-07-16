package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// MaxGatesSimultaneosDefault e o limite padrao de gates rodando ao mesmo tempo
// no processo inteiro (semaforo global). O plano fixa 1 como default: os gates
// (go build/vet/test) sao pesados e serializa-los evita brigar por CPU/disco
// quando varias demandas executam em paralelo.
const MaxGatesSimultaneosDefault = 1

// Gate e um bloco de comandos deterministicos de verificacao (build/lint/test)
// rodados pelo orquestrador. Portado de config.go do Praxis atual.
type Gate struct {
	Nome           string   `json:"nome"`
	Dir            string   `json:"dir,omitempty"`
	SomenteSeMudou bool     `json:"somente_se_mudou,omitempty"`
	Comandos       []string `json:"comandos"`
}

// GateExtra e um gate opcional referenciado pela coluna gate_extra da fase.
// Portado de config.go do Praxis atual.
type GateExtra struct {
	Nome     string   `json:"nome"`
	Dir      string   `json:"dir,omitempty"`
	Comandos []string `json:"comandos"`
}

// SemaforoGates limita quantos conjuntos de gates rodam simultaneamente no
// processo. E compartilhado por TODOS os RunnerGates (um por demanda/projeto),
// de modo que o limite seja global e nao por demanda. O zero-value / nil se
// comporta como "sem limite" (util em teste); em producao o scheduler cria um
// com NovoSemaforoGates e o injeta em cada RunnerGates.
type SemaforoGates struct {
	slots chan struct{}
}

// NovoSemaforoGates cria um semaforo global que permite ate max gates
// simultaneos. Valores < 1 caem no default (1).
func NovoSemaforoGates(max int) *SemaforoGates {
	if max < 1 {
		max = MaxGatesSimultaneosDefault
	}
	return &SemaforoGates{slots: make(chan struct{}, max)}
}

// Adquirir toma um slot, bloqueando ate haver vaga ou o ctx ser cancelado.
// Nil (sem semaforo) sempre libera na hora.
func (s *SemaforoGates) Adquirir(ctx context.Context) error {
	if s == nil || s.slots == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Liberar devolve o slot tomado por Adquirir.
func (s *SemaforoGates) Liberar() {
	if s == nil || s.slots == nil {
		return
	}
	<-s.slots
}

// RunnerGates implementa a interface Gates da pipeline: roda os comandos de
// verificacao configurados (mais o gate_extra da fase, se houver) num worktree,
// respeitando o semaforo global. Portado de rodarGates (gates.go) do Praxis
// atual; adaptacoes:
//   - a config dos gates chega no proprio runner (o scheduler a resolve do
//     banco), nao de um autopilot.json em disco;
//   - o semaforo global serializa gates de demandas concorrentes;
//   - sem saida em stdout (servico sem CLI): tudo vai para o arquivo de log;
//   - o ctx da fase e propagado ao comando (pausa/cancelamento aborta o gate).
type RunnerGates struct {
	Gates      []Gate         // gates fixos rodados em toda fase
	GatesExtra []GateExtra    // gates opcionais referenciados por fase.GateExtra
	DirLogs    string         // pasta dos .log dos gates (vazio → o proprio worktree)
	Sem        *SemaforoGates // semaforo global (nil → sem limite)
	Timeout    time.Duration  // timeout por comando (0 → 30 min)

	// Seams de teste (nil em producao):
	Exec  func(ctx context.Context, dir, comando string, timeout time.Duration) ([]byte, error)
	Agora func() time.Time
}

// contadorGates garante nomes de log unicos mesmo quando dois conjuntos de
// gates comecam no mesmo segundo (a resolucao de agoraTS e de 1s).
var contadorGates atomic.Int64

// Rodar satisfaz a interface pipeline.Gates. Adquire um slot do semaforo global
// (bloqueando conforme o limite), roda os gates e libera o slot. Devolve erro
// apenas em falha de infraestrutura (criacao do log, ctx cancelado ao esperar o
// semaforo); o desfecho dos comandos vive em ResultadoGates.Ok/Gate/Erro.
func (r *RunnerGates) Rodar(ctx context.Context, dir string, fase db.Fase) (ResultadoGates, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.Sem.Adquirir(ctx); err != nil {
		return ResultadoGates{}, err
	}
	defer r.Sem.Liberar()
	return r.rodar(ctx, dir, fase)
}

func (r *RunnerGates) rodar(ctx context.Context, dir string, fase db.Fase) (ResultadoGates, error) {
	dirLogs := strings.TrimSpace(r.DirLogs)
	if dirLogs == "" {
		dirLogs = dir
	}
	if err := os.MkdirAll(dirLogs, 0o755); err != nil {
		return ResultadoGates{}, err
	}
	seq := contadorGates.Add(1)
	logPath := filepath.Join(dirLogs, fmt.Sprintf("fase-%s-gates-%s-%d.log", fase.Codigo, r.agoraTS(), seq))
	logFile, err := os.Create(logPath)
	if err != nil {
		return ResultadoGates{}, err
	}
	defer logFile.Close()
	res := ResultadoGates{Ok: true, LogPath: logPath}

	rodarBloco := func(nome, d string, comandos []string) bool {
		for _, comando := range comandos {
			fmt.Fprintf(logFile, "\n===== [%s] %s (em %s)\n", nome, comando, d)
			saida, err := r.exec(ctx, d, comando, r.timeout())
			logFile.Write(saida)
			if err != nil {
				res.Ok = false
				res.Gate = nome + ": " + comando
				res.Erro = ultimasLinhas(string(saida), 200)
				res.Ambiente = falhaDeAmbiente(saida, err)
				return false
			}
		}
		return true
	}

	for _, g := range r.Gates {
		d := resolverDirGate(dir, g.Dir)
		if g.SomenteSeMudou {
			if limpo, err := gitops.Limpo(d); err == nil && limpo {
				fmt.Fprintf(logFile, "\n===== [%s] sem mudancas em %s, pulado\n", g.Nome, d)
				continue
			}
		}
		if !rodarBloco(g.Nome, d, g.Comandos) {
			return res, nil
		}
	}

	if fase.GateExtra != "" {
		var extra *GateExtra
		for i := range r.GatesExtra {
			if r.GatesExtra[i].Nome == fase.GateExtra {
				extra = &r.GatesExtra[i]
				break
			}
		}
		if extra == nil {
			fmt.Fprintf(logFile, "\n===== AVISO: gate_extra %q nao configurado — ignorado\n", fase.GateExtra)
			return res, nil
		}
		if !rodarBloco(extra.Nome, resolverDirGate(dir, extra.Dir), extra.Comandos) {
			return res, nil
		}
	}
	return res, nil
}

func (r *RunnerGates) exec(ctx context.Context, dir, comando string, timeout time.Duration) ([]byte, error) {
	if r.Exec != nil {
		return r.Exec(ctx, dir, comando, timeout)
	}
	return execShell(ctx, dir, comando, timeout)
}

func (r *RunnerGates) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 30 * time.Minute
}

func (r *RunnerGates) agoraTS() string {
	agora := time.Now
	if r.Agora != nil {
		agora = r.Agora
	}
	return agora().Format("20060102-150405")
}

// resolverDirGate transforma o dir de um gate em caminho utilizavel a partir do
// worktree. Portado de resolverDir (config.go). Vazio/"." → o proprio worktree.
func resolverDirGate(raiz, dir string) string {
	if dir == "" || dir == "." {
		return raiz
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(raiz, dir)
}

// falhaDeAmbiente detecta quando um gate falhou porque o COMANDO nao pode ser
// executado (binario ausente, PATH errado, sintaxe incompativel com o shell) —
// e nao porque o codigo esta reprovado. E um problema de configuracao do gate
// ou do ambiente que o corretor (contexto limpo, sem mexer no PATH) nao conserta
// a tempo: melhor parar e pedir intervencao humana do que gastar tentativas de
// correcao num falso negativo. Portado de gates.go do Praxis atual.
func falhaDeAmbiente(saida []byte, err error) bool {
	s := strings.ToLower(string(saida))
	marcadores := []string{
		"não é reconhecido como um comando",                    // cmd.exe PT-BR
		"nao e reconhecido como um comando",                    // idem, sem acento
		"is not recognized as an internal or external command", // cmd.exe EN
		"command not found",                                    // sh/bash
		"não é reconhecido como nome de cmdlet",                // powershell PT-BR
		"is not recognized as the name of a cmdlet",            // powershell EN
	}
	for _, m := range marcadores {
		if strings.Contains(s, m) {
			return true
		}
	}
	// exit 9009 (cmd: comando nao encontrado) ou 127 (sh: idem)
	if ee, ok := err.(*exec.ExitError); ok {
		switch ee.ExitCode() {
		case 127, 9009:
			return true
		}
	}
	return false
}

// execShell roda um comando via shell do sistema (cmd /c no Windows, sh -c nos
// demais), com timeout, devolvendo stdout+stderr combinados. O ctx pai propaga
// o cancelamento (pausa da demanda) ao processo. Portado de gates.go.
func execShell(parent context.Context, dir, comando string, timeout time.Duration) ([]byte, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", comando)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", comando)
	}
	cmd.Dir = dir
	saida, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timeout de %v: %s", timeout, comando)
	}
	return saida, err
}

// ultimasLinhas devolve as ultimas n linhas de s (portado de util.go).
func ultimasLinhas(s string, n int) string {
	linhas := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(linhas) > n {
		linhas = linhas[len(linhas)-n:]
	}
	return strings.Join(linhas, "\n")
}
