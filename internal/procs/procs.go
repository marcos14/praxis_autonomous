// Package procs oferece o encerramento de ARVORES de processos (o harness e seus
// filhos) e um registro em disco dos PIDs vivos, para a recuperacao pos-restart
// da Fase 2i: no boot, os processos de harness que sobreviveram a uma queda do
// servico (orfaos) precisam ser mortos antes de reutilizar/limpar os worktrees
// (Risco 5 do plano, agravado no Windows).
//
// Duas responsabilidades:
//   - MatarArvore/ConfigurarGrupoProcesso: matam nao so o filho direto, mas toda
//     a subarvore (no Unix via process group; no Windows via `taskkill /T`). Sao
//     usados tanto na interrupcao ao vivo (pausar/cancelar cancela o ctx e o
//     motor mata a arvore) quanto no boot (matar orfaos).
//   - Registro: grava um arquivo <pid>.pid por processo vivo; o que sobrar no
//     boot e orfao e sua arvore e morta por MatarOrfaos.
package procs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Registro persiste, em disco, os PIDs dos processos de harness vivos. Cada
// processo vira um arquivo <pid>.pid em Dir enquanto roda; o arquivo e removido
// quando o processo termina normalmente (via a funcao devolvida por Registrar).
// Os arquivos que sobrarem apos uma queda do servico apontam processos orfaos,
// mortos por MatarOrfaos no boot.
//
// E best-effort: uma falha de escrita apenas faz a recuperacao perder a chance
// de matar aquele orfao especifico (o `git worktree prune` + `--force` ainda
// limpam o worktree). Um PID pode, em tese, ter sido reciclado pelo SO antes do
// boot — por isso o registro so guarda PIDs de processos que o proprio servico
// iniciou e que ficaram sem o encerramento limpo.
type Registro struct {
	dir string
	mu  sync.Mutex
}

// NovoRegistro cria (garantindo o diretorio) um registro de PIDs em dir.
func NovoRegistro(dir string) (*Registro, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("registro de processos sem diretorio")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("criar dir de PIDs %q: %w", dir, err)
	}
	return &Registro{dir: dir}, nil
}

// Registrar grava o PID e devolve uma funcao que o remove (para `defer` apos o
// processo terminar). No-op (funcao vazia) quando o registro e nil ou o pid e
// invalido, para o chamador nao precisar checar.
func (r *Registro) Registrar(pid int) func() {
	if r == nil || pid <= 0 {
		return func() {}
	}
	caminho := r.caminho(pid)
	r.mu.Lock()
	_ = os.WriteFile(caminho, []byte(strconv.Itoa(pid)), 0o644)
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		_ = os.Remove(caminho)
		r.mu.Unlock()
	}
}

// MatarOrfaos le todos os PIDs registrados, mata a arvore de cada um e remove o
// arquivo. Devolve quantas arvores foram efetivamente mortas. E chamada no boot,
// antes de reutilizar/limpar os worktrees. Um diretorio inexistente nao e erro
// (nunca houve execucao).
func (r *Registro) MatarOrfaos() (int, error) {
	if r == nil {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entradas, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	mortos := 0
	for _, e := range entradas {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		caminho := filepath.Join(r.dir, e.Name())
		if pid := lerPID(caminho); pid > 0 {
			if MatarArvore(pid) == nil {
				mortos++
			}
		}
		_ = os.Remove(caminho)
	}
	return mortos, nil
}

// caminho e o arquivo de um PID dentro do registro.
func (r *Registro) caminho(pid int) string {
	return filepath.Join(r.dir, strconv.Itoa(pid)+".pid")
}

// lerPID le o PID de um arquivo do registro (0 se ilegivel).
func lerPID(caminho string) int {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}
