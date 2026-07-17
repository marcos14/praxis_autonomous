//go:build windows

package procs

import (
	"os/exec"
	"strconv"
)

// MatarArvore mata o processo pid e TODA a sua subarvore de filhos no Windows via
// `taskkill /F /T /PID <pid>`. O `/T` percorre a arvore de processos (registrada
// pelo SO via parentesco) — nao depende de grupo de processos, por isso
// ConfigurarGrupoProcesso e no-op no Windows. Devolve nil quando o taskkill
// conclui (processo ja morto tambem devolve erro do taskkill, tratado como
// best-effort pelo chamador).
func MatarArvore(pid int) error {
	if pid <= 0 {
		return nil
	}
	return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
}

// ConfigurarGrupoProcesso e no-op no Windows: `taskkill /T` ja mata a arvore sem
// precisar de um grupo de processos dedicado.
func ConfigurarGrupoProcesso(cmd *exec.Cmd) {}
