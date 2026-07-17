//go:build !windows

package procs

import (
	"os/exec"
	"syscall"
)

// MatarArvore mata o processo pid e seus descendentes enviando SIGKILL ao GRUPO
// de processos (kill(-pgid)). Depende de o processo ter sido iniciado como lider
// de grupo (ConfigurarGrupoProcesso, via Setpgid), o que faz pid == pgid. Se o
// processo nao estiver num grupo proprio, cai para matar so o pid.
func MatarArvore(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// ConfigurarGrupoProcesso poe o processo filho no seu proprio grupo (Setpgid),
// tornando-o lider — assim MatarArvore(-pid) alcanca toda a subarvore. Chamar
// ANTES de Start.
func ConfigurarGrupoProcesso(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
