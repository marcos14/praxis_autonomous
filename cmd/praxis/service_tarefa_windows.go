//go:build windows

package main

// Modo "tarefa de logon" (Windows): o problema DIFERENTE que a ADR 0001 (§4)
// separa do agente de máquina — um processo residente POR USUÁRIO. Contas
// Microsoft com Windows Hello não têm senha local utilizável, então o logon de
// serviço com -conta não é possível; e rodar como LocalSystem quebra PATH dos
// CLIs dos motores, os logins deles e o dono dos repositórios git. A tarefa de
// logon do Agendador roda com o token interativo do usuário (sem senha), sobe
// junto com o login e usa o PRAXIS_HOME do perfil — nada a migrar. O custo:
// só roda enquanto o usuário estiver logado, e o stop é abrupto (o Agendador
// mata o processo; o SQLite em WAL tolera e os órfãos morrem no próximo boot).

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

// nomeTarefaLogon é o nome da tarefa no Agendador — contrato como o nome do
// serviço (D6).
const nomeTarefaLogon = "Praxis Autonomous"

// homeLogonPadrao é o PRAXIS_HOME do modo logon: o mesmo default do `serve`
// rodado à mão (%LOCALAPPDATA%\praxis), de propósito — quem migra do terminal
// para a tarefa continua com seus dados.
func homeLogonPadrao() string {
	if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
		return filepath.Join(la, "praxis")
	}
	return homeServicoPadrao()
}

// dirLogonPadrao é a pasta do binário no modo logon: por usuário, sem admin.
func dirLogonPadrao() string {
	if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
		return filepath.Join(la, "Programs", "Praxis")
	}
	return dirBinarioPadrao()
}

// powershell executa um comando no Windows PowerShell e devolve stdout.
func powershell(cmd string) (string, error) {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", cmd)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return out.String(), fmt.Errorf("powershell: %w\n%s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// citarPS cita uma string para o PowerShell (aspas simples).
func citarPS(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// infoTarefa é o retrato da tarefa de logon.
type infoTarefa struct {
	State     string `json:"State"`
	Command   string `json:"Command"`
	Arguments string `json:"Arguments"`
	UserId    string `json:"UserId"`
}

// consultarTarefaLogon consulta a tarefa; não exige elevação (a tarefa é do
// próprio usuário).
func consultarTarefaLogon() (info infoTarefa, existe bool, err error) {
	out, err := powershell(`$t = Get-ScheduledTask -TaskName ` + citarPS(nomeTarefaLogon) + ` -ErrorAction SilentlyContinue; ` +
		`if ($t) { [pscustomobject]@{ State = [string]$t.State; Command = $t.Actions[0].Execute; Arguments = $t.Actions[0].Arguments; UserId = $t.Principal.UserId } | ConvertTo-Json -Compress }; exit 0`)
	// `exit 0`: com -Command, o PowerShell devolve 1 quando o último comando
	// registrou erro — e o Get-ScheduledTask de uma tarefa inexistente registra,
	// mesmo silenciado. Ausência não é erro aqui.
	if err != nil {
		return info, false, fmt.Errorf("consultar tarefa %q: %w", nomeTarefaLogon, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return info, false, nil
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return info, true, fmt.Errorf("ler tarefa %q: %w (%s)", nomeTarefaLogon, err, out)
	}
	return info, true, nil
}

// xmlTarefaLogon gera a definição da tarefa (schema do Task Scheduler). Token
// interativo do usuário (sem senha), disparo no logon desse usuário, sem limite
// de execução, reinício em falha (rede de segurança — D3) e uma instância só.
func xmlTarefaLogon(usuario, exe string, args []string) string {
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var argStr []string
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		argStr = append(argStr, a)
	}
	return strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-16"?>`,
		`<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">`,
		`  <RegistrationInfo>`,
		`    <Author>Praxis Autonomous</Author>`,
		`    <Description>` + esc(descricaoServico) + ` — tarefa de logon do usuário (praxis service install -logon)</Description>`,
		`  </RegistrationInfo>`,
		`  <Triggers>`,
		`    <LogonTrigger><Enabled>true</Enabled><UserId>` + esc(usuario) + `</UserId></LogonTrigger>`,
		`  </Triggers>`,
		`  <Principals>`,
		`    <Principal id="Author"><UserId>` + esc(usuario) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal>`,
		`  </Principals>`,
		`  <Settings>`,
		`    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`,
		`    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`    <AllowHardTerminate>true</AllowHardTerminate>`,
		`    <StartWhenAvailable>true</StartWhenAvailable>`,
		`    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>`,
		`    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>`,
		`    <AllowStartOnDemand>true</AllowStartOnDemand>`,
		`    <Enabled>true</Enabled>`,
		`    <Hidden>false</Hidden>`,
		`    <RunOnlyIfIdle>false</RunOnlyIfIdle>`,
		`    <WakeToRun>false</WakeToRun>`,
		`    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`    <Priority>7</Priority>`,
		`    <RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure>`,
		`  </Settings>`,
		`  <Actions Context="Author">`,
		`    <Exec><Command>` + esc(exe) + `</Command><Arguments>` + esc(strings.Join(argStr, " ")) + `</Arguments></Exec>`,
		`  </Actions>`,
		`</Task>`,
		``,
	}, "\r\n")
}

// gravarUTF16 grava s como UTF-16LE com BOM — o formato que o Agendador espera
// no XML de tarefa.
func gravarUTF16(caminho, s string) error {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2, 2+2*len(u))
	b[0], b[1] = 0xFF, 0xFE
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return os.WriteFile(caminho, b, 0o600)
}

// usuarioAtual devolve DOMÍNIO\usuário do processo.
func usuarioAtual() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("identificar o usuário atual: %w", err)
	}
	return u.Username, nil
}

// instalarTarefaLogon registra (ou atualiza) a tarefa de logon do usuário atual
// e a inicia. Idempotente (D7): reinstalar para a tarefa, troca o binário,
// reescreve a definição e reinicia. Não exige elevação.
func instalarTarefaLogon(o *opcoesServico, out, errOut io.Writer) error {
	if _, instalado, err := nomeServicoInstalado(); err == nil && instalado {
		return errors.New("já existe o serviço do sistema \"" + nomeServico + "\"; remova-o antes (como Administrador: praxis service remove) — os dois brigariam pela mesma porta")
	}
	usuario, err := usuarioAtual()
	if err != nil {
		return err
	}
	home, err := filepath.Abs(o.Home)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	origem, err := executavelAtual()
	if err != nil {
		return err
	}
	destino := caminhoBinarioInstalado(dir)

	info, existia, err := consultarTarefaLogon()
	if err != nil {
		return err
	}
	estavaRodando := existia && strings.EqualFold(info.State, "Running")
	if estavaRodando {
		fmt.Fprintf(out, "parando tarefa %q…\n", nomeTarefaLogon)
		if err := pararTarefaLogon(); err != nil {
			return err
		}
	}
	if err := verificarPorta(o.Addr); err != nil {
		if estavaRodando {
			_ = iniciarTarefaLogon()
		}
		return err
	}

	copiado, err := instalarBinario(origem, destino)
	if err != nil {
		return err
	}
	if copiado {
		fmt.Fprintf(out, "binário copiado para %s\n", destino)
	} else {
		fmt.Fprintf(out, "binário já em %s\n", destino)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("criar PRAXIS_HOME %q: %w", home, err)
	}

	logArq := filepath.Join(home, nomeArquivoLogServico)
	args := append([]string{"service", "run", "-home", home, "-log", logArq}, o.argsServe()...)
	xmlPath := filepath.Join(os.TempDir(), "praxis-tarefa-logon.xml")
	if err := gravarUTF16(xmlPath, xmlTarefaLogon(usuario, destino, args)); err != nil {
		return fmt.Errorf("gravar definição da tarefa: %w", err)
	}
	defer os.Remove(xmlPath)
	if _, err := powershell(`Register-ScheduledTask -TaskName ` + citarPS(nomeTarefaLogon) +
		` -Xml (Get-Content -Raw -LiteralPath ` + citarPS(xmlPath) + `) -Force | Out-Null`); err != nil {
		return fmt.Errorf("registrar tarefa %q: %w", nomeTarefaLogon, err)
	}
	if existia {
		fmt.Fprintf(out, "tarefa %q atualizada\n", nomeTarefaLogon)
	} else {
		fmt.Fprintf(out, "tarefa %q registrada (dispara no logon de %s)\n", nomeTarefaLogon, usuario)
	}

	fmt.Fprintf(out, "iniciando tarefa %q…\n", nomeTarefaLogon)
	if err := iniciarTarefaLogon(); err != nil {
		return err
	}
	if err := esperarSaude(o.Addr, o.comTLS(), 20*time.Second); err != nil {
		fmt.Fprintf(errOut, "aviso: tarefa iniciada mas ainda sem resposta HTTP (%v); veja %s\n", err, logArq)
	} else {
		fmt.Fprintf(out, "serviço respondendo em %s\n", o.urlAcesso())
	}
	fmt.Fprintf(out, "\nmodo:     tarefa de logon (roda enquanto %s estiver logado)\ntarefa:   %s\nbinário:  %s\nhome:     %s\nendereço: %s (TLS: %s)\nacesso:   %s\nlog:      %s\nstatus:   praxis service status\n",
		usuario, nomeTarefaLogon, destino, home, o.Addr, simNao(o.comTLS()), o.urlAcesso(), logArq)
	return nil
}

func iniciarTarefaLogon() error {
	if _, err := powershell(`Start-ScheduledTask -TaskName ` + citarPS(nomeTarefaLogon)); err != nil {
		return fmt.Errorf("iniciar tarefa %q: %w", nomeTarefaLogon, err)
	}
	return nil
}

// pararTarefaLogon manda o Agendador encerrar a tarefa (mata o processo) e
// espera o estado sair de Running.
func pararTarefaLogon() error {
	if _, err := powershell(`Stop-ScheduledTask -TaskName ` + citarPS(nomeTarefaLogon)); err != nil {
		return fmt.Errorf("parar tarefa %q: %w", nomeTarefaLogon, err)
	}
	limite := time.Now().Add(15 * time.Second)
	for {
		info, existe, err := consultarTarefaLogon()
		if err != nil || !existe || !strings.EqualFold(info.State, "Running") {
			return err
		}
		if time.Now().After(limite) {
			return fmt.Errorf("tarefa %q continua em execução após 15s", nomeTarefaLogon)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func removerTarefaLogon(out io.Writer) error {
	info, existe, err := consultarTarefaLogon()
	if err != nil {
		return err
	}
	if !existe {
		return nil
	}
	if strings.EqualFold(info.State, "Running") {
		fmt.Fprintf(out, "parando tarefa %q…\n", nomeTarefaLogon)
		if err := pararTarefaLogon(); err != nil {
			return err
		}
	}
	if _, err := powershell(`Unregister-ScheduledTask -TaskName ` + citarPS(nomeTarefaLogon) + ` -Confirm:$false`); err != nil {
		return fmt.Errorf("remover tarefa %q: %w", nomeTarefaLogon, err)
	}
	fmt.Fprintf(out, "tarefa %q removida\n", nomeTarefaLogon)
	return nil
}

// statusTarefaLogon imprime o retrato da tarefa; devolve false se não existe.
func statusTarefaLogon(out io.Writer) (bool, error) {
	info, existe, err := consultarTarefaLogon()
	if err != nil || !existe {
		return existe, err
	}
	estado := info.State
	switch strings.ToLower(estado) {
	case "running":
		estado = "rodando"
	case "ready":
		estado = "parada (pronta para o próximo logon)"
	case "disabled":
		estado = "desabilitada"
	}
	fmt.Fprintf(out, "modo:     tarefa de logon\ntarefa:   %s\nestado:   %s\nusuário:  %s\ncomando:  %s %s\n",
		nomeTarefaLogon, estado, info.UserId, info.Command, info.Arguments)
	return true, nil
}
