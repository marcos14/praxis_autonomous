package procs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// comandoDormir devolve um comando portavel que dorme por seg segundos, para
// exercitar o encerramento de processos vivos.
func comandoDormir(seg int) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// ping -n N espera ~N-1 segundos; +2 garante folga.
		return exec.Command("cmd", "/c", fmt.Sprintf("ping -n %d 127.0.0.1 >NUL", seg+2))
	}
	return exec.Command("sleep", strconv.Itoa(seg))
}

// vivo informa se o processo ainda esta rodando (Wait ainda nao retornou).
func esperarMorte(t *testing.T, cmd *exec.Cmd, prazo time.Duration) bool {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return true
	case <-time.After(prazo):
		return false
	}
}

func TestMatarArvore(t *testing.T) {
	cmd := comandoDormir(30)
	ConfigurarGrupoProcesso(cmd)
	if err := cmd.Start(); err != nil {
		t.Skipf("nao consegui iniciar processo de teste: %v", err)
	}
	if err := MatarArvore(cmd.Process.Pid); err != nil {
		t.Fatalf("MatarArvore: %v", err)
	}
	if !esperarMorte(t, cmd, 5*time.Second) {
		t.Fatal("o processo continuou vivo apos MatarArvore")
	}
}

func TestRegistroCicloDeVida(t *testing.T) {
	dir := t.TempDir()
	r, err := NovoRegistro(dir)
	if err != nil {
		t.Fatal(err)
	}
	remover := r.Registrar(4242)
	if _, err := os.Stat(filepath.Join(dir, "4242.pid")); err != nil {
		t.Fatalf("pidfile deveria existir apos Registrar: %v", err)
	}
	remover()
	if _, err := os.Stat(filepath.Join(dir, "4242.pid")); !os.IsNotExist(err) {
		t.Fatalf("pidfile deveria sumir apos a funcao de remocao: %v", err)
	}
}

func TestMatarOrfaos(t *testing.T) {
	dir := t.TempDir()
	r, err := NovoRegistro(dir)
	if err != nil {
		t.Fatal(err)
	}

	cmd := comandoDormir(30)
	ConfigurarGrupoProcesso(cmd)
	if err := cmd.Start(); err != nil {
		t.Skipf("nao consegui iniciar processo de teste: %v", err)
	}
	// simula um orfao: PID registrado mas nunca desregistrado (queda do servico).
	r.Registrar(cmd.Process.Pid)

	mortos, err := r.MatarOrfaos()
	if err != nil {
		t.Fatalf("MatarOrfaos: %v", err)
	}
	if mortos != 1 {
		t.Fatalf("esperava 1 arvore morta, veio %d", mortos)
	}
	if !esperarMorte(t, cmd, 5*time.Second) {
		t.Fatal("o orfao continuou vivo apos MatarOrfaos")
	}
	// o pidfile deve ter sido removido.
	entradas, _ := os.ReadDir(dir)
	if len(entradas) != 0 {
		t.Fatalf("esperava o registro vazio apos MatarOrfaos, sobraram %d arquivos", len(entradas))
	}
}

func TestMatarOrfaosDirVazioOuInexistente(t *testing.T) {
	// registro nil e no-op.
	var r *Registro
	if n, err := r.MatarOrfaos(); err != nil || n != 0 {
		t.Fatalf("registro nil deveria ser no-op: n=%d err=%v", n, err)
	}
	// dir vazio: nada a matar.
	r2, err := NovoRegistro(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r2.MatarOrfaos(); err != nil || n != 0 {
		t.Fatalf("dir vazio: n=%d err=%v", n, err)
	}
}
