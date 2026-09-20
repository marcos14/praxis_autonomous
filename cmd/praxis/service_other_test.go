//go:build !windows

package main

import (
	"strings"
	"testing"
)

// TestAplicarDefaultsLogonLinux: -logon troca os defaults de máquina pelos do
// usuário (mesmo home do `serve` manual), salvo -home/-dir explícitos.
func TestAplicarDefaultsLogonLinux(t *testing.T) {
	t.Setenv("HOME", "/home/teste")
	t.Setenv("XDG_CONFIG_HOME", "")
	o := parseServico(t, "-logon")
	aplicarDefaultsLogon(o)
	if o.Home != "/home/teste/.config/praxis" || o.Dir != "/home/teste/.local/bin" {
		t.Errorf("defaults do modo usuário: home=%q dir=%q", o.Home, o.Dir)
	}
	o = parseServico(t, "-logon", "-home", "/srv/praxis")
	aplicarDefaultsLogon(o)
	if o.Home != "/srv/praxis" || o.Dir != "/home/teste/.local/bin" {
		t.Errorf("-home explícito deve prevalecer: home=%q dir=%q", o.Home, o.Dir)
	}
	o = parseServico(t)
	aplicarDefaultsLogon(o)
	if o.Home != "/var/lib/praxis" || o.Dir != "/usr/local/bin" {
		t.Errorf("sem -logon valem os defaults de máquina: home=%q dir=%q", o.Home, o.Dir)
	}
}

func TestModoSystemdArgs(t *testing.T) {
	if got := strings.Join(modoUsuario.args("restart", "praxis"), " "); got != "--user restart praxis" {
		t.Errorf("modo usuário: %q", got)
	}
	if got := strings.Join(modoSistema.args("restart", "praxis"), " "); got != "restart praxis" {
		t.Errorf("modo sistema: %q", got)
	}
	if modoUsuario.caminhoUnit("praxis") == modoSistema.caminhoUnit("praxis") {
		t.Error("os dois modos não podem apontar para o mesmo unit")
	}
	if !strings.Contains(modoUsuario.journalctl("praxis"), "--user") || strings.Contains(modoSistema.journalctl("praxis"), "--user") {
		t.Error("journalctl deve refletir o modo")
	}
}

// TestArtefatoRegistroLogon: `service unit -logon` imprime o unit de usuário
// com as instruções sem root e o lembrete do linger.
func TestArtefatoRegistroLogon(t *testing.T) {
	t.Setenv("HOME", "/home/teste")
	o := parseServico(t, "-logon")
	aplicarDefaultsLogon(o)
	s := artefatoRegistro("/home/teste/.local/bin/praxis", o)
	for _, quer := range []string{"systemctl --user daemon-reload", "loginctl enable-linger", "WantedBy=default.target", "/home/teste/.config/systemd/user/praxis.service"} {
		if !strings.Contains(s, quer) {
			t.Errorf("artefato -logon não contém %q:\n%s", quer, s)
		}
	}
}

func TestModeloEnvUsuarioGravaPATH(t *testing.T) {
	s := modeloEnvUsuario("/home/teste/.local/bin:/usr/bin")
	if !strings.Contains(s, "\nPATH=/home/teste/.local/bin:/usr/bin\n") {
		t.Errorf("PATH não gravado:\n%s", s)
	}
}
