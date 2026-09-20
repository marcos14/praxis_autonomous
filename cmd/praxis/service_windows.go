//go:build windows

package main

// Casca do Windows (ADR 0001, §1.2): o binário fala o protocolo do Service
// Control Manager por conta própria — sem wrapper (NSSM/WinSW). `service run`
// conecta ao dispatcher do SCM, reporta START_PENDING → RUNNING, responde a
// STOP/SHUTDOWN/INTERROGATE e cancela o contexto do serve com prazo. As ações
// (install/remove/start/stop) exigem Administrador; as consultas (status,
// "está instalado?") pedem só direitos de query e funcionam sem elevação (D4).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// homeServicoPadrao é o diretório de estado do serviço: %ProgramData%\praxis
// (D5). Nunca o perfil de um usuário — o serviço roda antes de logins e sob
// outra conta; o %LOCALAPPDATA% do LocalSystem cairia em System32\config.
func homeServicoPadrao() string {
	pd := strings.TrimSpace(os.Getenv("ProgramData"))
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "praxis")
}

// dirBinarioPadrao é a pasta estável do executável: %ProgramFiles%\Praxis. O
// instalador copia o binário para cá — registrar o exe da pasta Downloads morre
// quando o usuário limpa a pasta (D5).
func dirBinarioPadrao() string {
	pf := strings.TrimSpace(os.Getenv("ProgramFiles"))
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "Praxis")
}

func caminhoBinarioInstalado(dir string) string { return filepath.Join(dir, "praxis.exe") }

func flagsServicoPlataforma(fs *flag.FlagSet, o *opcoesServico) {
	fs.StringVar(&o.Conta, "conta", "LocalSystem",
		"conta do serviço: LocalSystem, LocalService, NetworkService ou DOMINIO\\usuario (com -senha). "+
			"Rodar como o usuário que já autenticou os CLIs dos motores reaproveita esses logins")
	fs.StringVar(&o.Senha, "senha", "", "senha da conta de usuário informada em -conta")
	fs.BoolVar(&o.Logon, "logon", false,
		"em vez de serviço do sistema, tarefa de logon do usuário atual: sem senha e sem Administrador, "+
			"com o seu perfil (PATH, logins dos motores, repositórios); roda enquanto você estiver logado. "+
			"Defaults: -home %LOCALAPPDATA%\\praxis, -dir %LOCALAPPDATA%\\Programs\\Praxis")
}

// aplicarDefaultsLogon troca os defaults de máquina pelos de usuário quando
// -logon foi pedido e o operador não fixou -home/-dir.
func aplicarDefaultsLogon(o *opcoesServico) {
	if !o.Logon {
		return
	}
	if !o.explicitas["home"] {
		o.Home = homeLogonPadrao()
	}
	if !o.explicitas["dir"] {
		o.Dir = dirLogonPadrao()
	}
}

// artefatoRegistro imprime os comandos sc.exe equivalentes ao install (§3 da
// ADR) — ou, com -logon, o XML da tarefa de logon (schtasks /Create /XML).
func artefatoRegistro(exe string, o *opcoesServico) string {
	if o.Logon {
		usuario, _ := usuarioAtual()
		home, _ := filepath.Abs(o.Home)
		args := append([]string{"service", "run", "-home", home, "-log", filepath.Join(home, nomeArquivoLogServico)}, o.argsServe()...)
		return xmlTarefaLogon(usuario, exe, args)
	}
	return strings.Join([]string{
		"# Equivalente sc.exe do `praxis service install` (execute como Administrador):",
		comandoServicoWindows(nomeServico, exe, o),
		"sc.exe description " + nomeServico + " \"" + descricaoServico + "\"",
		"# Rede de segurança para crash (D3): reinicia após 5s, 5s e 30s; zera a contagem após 1 dia.",
		"sc.exe failure " + nomeServico + " reset= 86400 actions= restart/5000/restart/5000/restart/30000",
		"sc.exe failureflag " + nomeServico + " 1",
		"sc.exe start " + nomeServico,
		"",
	}, "\n")
}

// normalizarConta traduz apelidos para o nome que o SCM espera. Vazio significa
// LocalSystem (default do CreateService).
func normalizarConta(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "", "localsystem", "system", `nt authority\system`, `.\localsystem`:
		return ""
	case "localservice", `nt authority\localservice`:
		return `NT AUTHORITY\LocalService`
	case "networkservice", `nt authority\networkservice`:
		return `NT AUTHORITY\NetworkService`
	}
	return strings.TrimSpace(c)
}

func exibirConta(c string) string {
	if c == "" {
		return "LocalSystem"
	}
	return c
}

// contaInterna informa se a conta é uma das contas de serviço do sistema (sem senha).
func contaInterna(c string) bool {
	return c == "" || strings.HasPrefix(strings.ToUpper(c), `NT AUTHORITY\`)
}

// ---------------------------------------------------------------------------
// Consultas sem elevação (D4)
// ---------------------------------------------------------------------------

// infoServico é o retrato do serviço obtido só com direitos de consulta.
type infoServico struct {
	Nome      string
	Estado    svc.State
	BinPath   string
	Conta     string
	StartType uint32
}

// consultarServico abre o SCM com SC_MANAGER_CONNECT e o serviço com
// SERVICE_QUERY_STATUS|SERVICE_QUERY_CONFIG — nunca ALL_ACCESS. Por isso
// funciona de qualquer processo, elevado ou não; misturar os caminhos produz o
// bug clássico de "não há serviço" quando se roda sem UAC (§1.2 da ADR).
func consultarServico(nome string) (info infoServico, instalado bool, err error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return info, false, fmt.Errorf("conectar ao SCM: %w", err)
	}
	defer windows.CloseServiceHandle(h)

	nomePtr, err := windows.UTF16PtrFromString(nome)
	if err != nil {
		return info, false, err
	}
	sh, err := windows.OpenService(h, nomePtr, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return info, false, nil
		}
		return info, false, fmt.Errorf("abrir serviço %q: %w", nome, err)
	}
	s := &mgr.Service{Name: nome, Handle: sh}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return info, true, fmt.Errorf("consultar estado de %q: %w", nome, err)
	}
	cfg, err := s.Config()
	if err != nil {
		return info, true, fmt.Errorf("consultar configuração de %q: %w", nome, err)
	}
	return infoServico{
		Nome:      nome,
		Estado:    st.State,
		BinPath:   cfg.BinaryPathName,
		Conta:     exibirConta(normalizarConta(cfg.ServiceStartName)),
		StartType: cfg.StartType,
	}, true, nil
}

// nomeServicoInstalado resolve sob qual nome esta máquina registrou o serviço:
// o atual ou um dos legados (D6). Se nenhum, devolve o nome atual e false —
// instalar usa o nome novo.
func nomeServicoInstalado() (string, bool, error) {
	for _, n := range append([]string{nomeServico}, nomesServicoLegados...) {
		_, ok, err := consultarServico(n)
		if err != nil {
			return "", false, err
		}
		if ok {
			return n, true, nil
		}
	}
	return nomeServico, false, nil
}

// ---------------------------------------------------------------------------
// Ações (exigem Administrador)
// ---------------------------------------------------------------------------

// conectarSCM abre o SCM com direitos de administração. Sem elevação o SCM
// devolve ERROR_ACCESS_DENIED — traduzido para uma mensagem que diz o que fazer.
func conectarSCM() (*mgr.Mgr, error) {
	m, err := mgr.Connect()
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, errors.New("acesso negado ao Gerenciador de Serviços: execute o terminal como Administrador")
		}
		return nil, fmt.Errorf("conectar ao SCM: %w", err)
	}
	return m, nil
}

// montarBinPath compõe a linha de comando registrada no SCM exatamente como
// mgr.CreateService faz (EscapeArg em cada parte), para que a comparação com o
// BinaryPathName lido de volta seja exata (D7).
func montarBinPath(exe string, args []string) string {
	s := syscall.EscapeArg(exe)
	for _, a := range args {
		s += " " + syscall.EscapeArg(a)
	}
	return s
}

// registroDifere diz se a configuração registrada no SCM diverge da desejada —
// o instalador confere o binário registrado, não só a existência (D7).
func registroDifere(atual, quer mgr.Config) bool {
	return !strings.EqualFold(atual.BinaryPathName, quer.BinaryPathName) ||
		normalizarConta(atual.ServiceStartName) != normalizarConta(quer.ServiceStartName) ||
		atual.StartType != quer.StartType ||
		atual.DisplayName != quer.DisplayName ||
		atual.Description != quer.Description
}

func serviceInstall(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(errOut)
	o := flagsServico(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.registrarExplicitas(fs)
	aplicarDefaultsLogon(o)
	if o.Logon {
		if o.explicitas["conta"] || o.explicitas["senha"] {
			return errors.New("-logon roda com o usuário atual; não combine com -conta/-senha")
		}
		return instalarTarefaLogon(o, out, errOut)
	}
	o.Conta = normalizarConta(o.Conta)
	if !contaInterna(o.Conta) && o.Senha == "" {
		return errors.New("-senha é obrigatória quando -conta é uma conta de usuário (conta Microsoft/Windows Hello sem senha local? use -logon)")
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

	m, err := conectarSCM()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// Uma tarefa de logon instalada antes brigaria pela porta com o serviço do
	// sistema: migra removendo-a (mesmo espírito da D6 — não conviver).
	if _, existe, err := consultarTarefaLogon(); err == nil && existe {
		fmt.Fprintf(out, "tarefa de logon %q encontrada: removendo antes de registrar o serviço do sistema\n", nomeTarefaLogon)
		if err := removerTarefaLogon(out); err != nil {
			return err
		}
	}

	// D6: um serviço sob nome legado é removido, não deixado conviver — dois
	// serviços apontando para o mesmo PRAXIS_HOME brigariam pelo mesmo banco.
	nomeAtual, instalado, err := nomeServicoInstalado()
	if err != nil {
		return err
	}
	if instalado && nomeAtual != nomeServico {
		fmt.Fprintf(out, "serviço legado %q encontrado: removendo antes de registrar %q\n", nomeAtual, nomeServico)
		if err := removerServico(m, nomeAtual, out); err != nil {
			return err
		}
		instalado = false
	}

	// Serviço existente rodando: para antes de trocar o binário (um exe em uso
	// não pode ser sobrescrito) e para que a versão nova suba no start (D7).
	var s *mgr.Service
	if instalado {
		s, err = m.OpenService(nomeServico)
		if err != nil {
			return fmt.Errorf("abrir serviço %q: %w", nomeServico, err)
		}
		defer s.Close()
		if err := pararServico(s, out); err != nil {
			return err
		}
	}

	// Porta livre? Confere antes de tocar em binário e registro (D7). Se o
	// serviço antigo estava no ar, volta com ele — a instalação falhou, não a
	// máquina.
	if err := verificarPorta(o.Addr); err != nil {
		if s != nil {
			_ = iniciarServico(s, home, out)
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
	if o.Conta != "" {
		concederAcesso(home, o.Conta, errOut)
	}

	argsRun := append([]string{"service", "run", "-home", home}, o.argsServe()...)
	cfg := mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		BinaryPathName:   montarBinPath(destino, argsRun),
		ServiceStartName: o.Conta,
		Password:         o.Senha,
		DisplayName:      nomeExibicaoServico,
		Description:      descricaoServico,
	}
	if s == nil {
		s, err = m.CreateService(nomeServico, destino, cfg, argsRun...)
		if err != nil {
			return fmt.Errorf("registrar serviço %q: %w", nomeServico, err)
		}
		defer s.Close()
		fmt.Fprintf(out, "serviço %q registrado\n", nomeServico)
	} else {
		atual, err := s.Config()
		if err != nil {
			return fmt.Errorf("ler registro de %q: %w", nomeServico, err)
		}
		if registroDifere(atual, cfg) {
			cfg.SidType = atual.SidType
			if err := s.UpdateConfig(cfg); err != nil {
				return fmt.Errorf("atualizar registro de %q: %w", nomeServico, err)
			}
			fmt.Fprintf(out, "registro de %q atualizado (binPath/conta/início)\n", nomeServico)
		} else {
			fmt.Fprintf(out, "registro de %q já correto\n", nomeServico)
		}
	}

	// Rede de segurança para crash (D3): o SCM não reinicia nada por default.
	// Reinicia após 5s, 5s e 30s; a contagem zera após um dia. Com o flag de
	// "non-crash failures", saída com código ≠ 0 também conta como falha.
	acoes := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := s.SetRecoveryActions(acoes, 86400); err != nil {
		fmt.Fprintf(errOut, "aviso: configurar reinício após falha: %v\n", err)
	} else if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		fmt.Fprintf(errOut, "aviso: configurar reinício após saída com erro: %v\n", err)
	}

	if err := iniciarServico(s, home, out); err != nil {
		return err
	}
	// "Rodando" no SCM só diz que o processo não morreu; confirma que o HTTP
	// está respondendo de fato.
	if err := esperarSaude(o.Addr, o.comTLS(), 20*time.Second); err != nil {
		fmt.Fprintf(errOut, "aviso: serviço rodando mas ainda sem resposta HTTP (%v); veja %s\n", err, filepath.Join(home, nomeArquivoLogServico))
	} else {
		fmt.Fprintf(out, "serviço respondendo em %s\n", o.urlAcesso())
	}
	fmt.Fprintf(out, "\nserviço:  %s (%s)\nbinário:  %s\nhome:     %s\nconta:    %s\nendereço: %s (TLS: %s)\nacesso:   %s\nlog:      %s\nstatus:   praxis service status\n",
		nomeServico, nomeExibicaoServico, destino, home, exibirConta(o.Conta), o.Addr, simNao(o.comTLS()), o.urlAcesso(),
		filepath.Join(home, nomeArquivoLogServico))
	return nil
}

// concederAcesso dá à conta do serviço permissão de modificação sobre o
// PRAXIS_HOME (best-effort via icacls) — LocalSystem já tem; LocalService e
// contas de usuário não necessariamente.
func concederAcesso(dir, conta string, errOut io.Writer) {
	cmd := exec.Command("icacls", dir, "/grant", conta+":(OI)(CI)M", "/T", "/Q")
	if saida, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(errOut, "aviso: conceder acesso de %q a %s: %v\n%s", conta, dir, err, saida)
	}
}

func serviceRemove(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service remove", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	nome, instalado, err := nomeServicoInstalado()
	if err != nil {
		return err
	}
	if !instalado {
		// Sem serviço do SCM: talvez seja a tarefa de logon.
		if _, existe, err := consultarTarefaLogon(); err == nil && existe {
			if err := removerTarefaLogon(out); err != nil {
				return err
			}
			fmt.Fprintf(out, "os dados em %s e o binário em %s foram mantidos\n", homeLogonPadrao(), caminhoBinarioInstalado(dirLogonPadrao()))
			return nil
		}
		fmt.Fprintf(out, "serviço %q não está instalado (nem como tarefa de logon) — nada a fazer\n", nomeServico)
		return nil
	}
	m, err := conectarSCM()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if err := removerServico(m, nome, out); err != nil {
		return err
	}
	fmt.Fprintf(out, "os dados em PRAXIS_HOME (por padrão %s) foram mantidos; o binário em %s também\n",
		homeServicoPadrao(), caminhoBinarioInstalado(dirBinarioPadrao()))
	return nil
}

// removerServico para e apaga o serviço. DeleteService só marca para exclusão:
// o serviço some quando estiver parado e todos os handles (um services.msc
// aberto, por exemplo) forem fechados — o estado "marcado" é tolerado.
func removerServico(m *mgr.Mgr, nome string, out io.Writer) error {
	s, err := m.OpenService(nome)
	if err != nil {
		return fmt.Errorf("abrir serviço %q: %w", nome, err)
	}
	defer s.Close()
	if err := pararServico(s, out); err != nil {
		return err
	}
	if err := s.Delete(); err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			fmt.Fprintf(out, "serviço %q já marcado para exclusão (some quando todos os handles fecharem — feche o services.msc)\n", nome)
			return nil
		}
		return fmt.Errorf("remover serviço %q: %w", nome, err)
	}
	fmt.Fprintf(out, "serviço %q removido\n", nome)
	return nil
}

func serviceStart(_ context.Context, args []string, out, errOut io.Writer) error {
	if tarefa, err := modoTarefaLogon(); err != nil {
		return err
	} else if tarefa {
		fmt.Fprintf(out, "iniciando tarefa %q…\n", nomeTarefaLogon)
		return iniciarTarefaLogon()
	}
	s, m, err := abrirServicoParaAcao(args, errOut)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	return iniciarServico(s, homeServicoPadrao(), out)
}

func serviceStop(_ context.Context, args []string, out, errOut io.Writer) error {
	if tarefa, err := modoTarefaLogon(); err != nil {
		return err
	} else if tarefa {
		fmt.Fprintf(out, "parando tarefa %q…\n", nomeTarefaLogon)
		return pararTarefaLogon()
	}
	s, m, err := abrirServicoParaAcao(args, errOut)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	return pararServico(s, out)
}

func serviceRestart(_ context.Context, args []string, out, errOut io.Writer) error {
	if tarefa, err := modoTarefaLogon(); err != nil {
		return err
	} else if tarefa {
		fmt.Fprintf(out, "reiniciando tarefa %q…\n", nomeTarefaLogon)
		if err := pararTarefaLogon(); err != nil {
			return err
		}
		return iniciarTarefaLogon()
	}
	s, m, err := abrirServicoParaAcao(args, errOut)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := pararServico(s, out); err != nil {
		return err
	}
	return iniciarServico(s, homeServicoPadrao(), out)
}

// modoTarefaLogon informa se o que está instalado é a tarefa de logon (e não o
// serviço do SCM). O serviço do SCM tem precedência quando os dois existem.
func modoTarefaLogon() (bool, error) {
	if _, instalado, err := nomeServicoInstalado(); err != nil || instalado {
		return false, err
	}
	_, existe, err := consultarTarefaLogon()
	return existe, err
}

// abrirServicoParaAcao resolve o nome instalado (atual ou legado) e abre o
// serviço com direitos de administração.
func abrirServicoParaAcao(args []string, errOut io.Writer) (*mgr.Service, *mgr.Mgr, error) {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	nome, instalado, err := nomeServicoInstalado()
	if err != nil {
		return nil, nil, err
	}
	if !instalado {
		return nil, nil, fmt.Errorf("serviço %q não está instalado (praxis service install)", nomeServico)
	}
	m, err := conectarSCM()
	if err != nil {
		return nil, nil, err
	}
	s, err := m.OpenService(nome)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("abrir serviço %q: %w", nome, err)
	}
	return s, m, nil
}

// pararServico manda Stop e espera o serviço reportar STOPPED (até 30s). Já
// parado é sucesso.
func pararServico(s *mgr.Service, out io.Writer) error {
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("consultar %q: %w", s.Name, err)
	}
	if st.State == svc.Stopped {
		return nil
	}
	fmt.Fprintf(out, "parando serviço %q…\n", s.Name)
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return fmt.Errorf("parar %q: %w", s.Name, err)
	}
	if err := esperarEstado(s, svc.Stopped, 30*time.Second); err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço %q parado\n", s.Name)
	return nil
}

// iniciarServico manda Start e espera RUNNING (até 30s). Já rodando é sucesso.
// Um serviço que para logo após iniciar é reportado com o caminho do log.
func iniciarServico(s *mgr.Service, home string, out io.Writer) error {
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("consultar %q: %w", s.Name, err)
	}
	if st.State == svc.Running {
		fmt.Fprintf(out, "serviço %q já está rodando\n", s.Name)
		return nil
	}
	fmt.Fprintf(out, "iniciando serviço %q…\n", s.Name)
	if err := s.Start(); err != nil {
		switch {
		case errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING):
		case errors.Is(err, windows.ERROR_SERVICE_LOGON_FAILED):
			return fmt.Errorf("iniciar %q: logon da conta falhou — confira a senha e conceda à conta o direito "+
				"\"Fazer logon como serviço\" (secpol.msc → Atribuição de direitos de usuário)", s.Name)
		default:
			return fmt.Errorf("iniciar %q: %w", s.Name, err)
		}
	}
	if err := esperarEstado(s, svc.Running, 30*time.Second); err != nil {
		return fmt.Errorf("%w — veja %s", err, filepath.Join(home, nomeArquivoLogServico))
	}
	fmt.Fprintf(out, "serviço %q rodando\n", s.Name)
	return nil
}

// esperarEstado consulta o serviço até atingir `quer` ou o prazo vencer. Sair
// para STOPPED enquanto se espera RUNNING é falha imediata.
func esperarEstado(s *mgr.Service, quer svc.State, prazo time.Duration) error {
	limite := time.Now().Add(prazo)
	for {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("consultar %q: %w", s.Name, err)
		}
		if st.State == quer {
			return nil
		}
		if quer == svc.Running && st.State == svc.Stopped {
			return fmt.Errorf("serviço %q parou logo após iniciar (código %d)", s.Name, st.Win32ExitCode)
		}
		if time.Now().After(limite) {
			return fmt.Errorf("serviço %q não chegou a %s em %s (estado atual: %s)", s.Name, nomeEstado(quer), prazo, nomeEstado(st.State))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func nomeEstado(st svc.State) string {
	switch st {
	case svc.Stopped:
		return "parado"
	case svc.StartPending:
		return "iniciando"
	case svc.StopPending:
		return "parando"
	case svc.Running:
		return "rodando"
	case svc.ContinuePending:
		return "retomando"
	case svc.PausePending:
		return "pausando"
	case svc.Paused:
		return "pausado"
	}
	return fmt.Sprintf("estado %d", st)
}

func nomeInicio(t uint32) string {
	switch t {
	case mgr.StartAutomatic:
		return "automático"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "desabilitado"
	}
	return fmt.Sprintf("tipo %d", t)
}

// serviceStatus mostra o retrato do serviço. Não exige elevação (D4).
func serviceStatus(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service status", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	nome, instalado, err := nomeServicoInstalado()
	if err != nil {
		return err
	}
	if !instalado {
		if existe, err := statusTarefaLogon(out); err != nil {
			return err
		} else if existe {
			return nil
		}
		fmt.Fprintf(out, "serviço %q: não instalado (nem sob nomes legados, nem como tarefa de logon)\n", nomeServico)
		return nil
	}
	info, _, err := consultarServico(nome)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço:  %s (%s)\nestado:   %s\nbinPath:  %s\nconta:    %s\ninício:   %s\n",
		info.Nome, nomeExibicaoServico, nomeEstado(info.Estado), info.BinPath, info.Conta, nomeInicio(info.StartType))
	if nome != nomeServico {
		fmt.Fprintf(out, "aviso:    registrado sob nome legado; `praxis service install` migra para %q\n", nomeServico)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Corpo do serviço
// ---------------------------------------------------------------------------

// serviceRun é o modo registrado no binPath. Detecta o contexto (D9): iniciado
// pelo SCM, fala o protocolo e loga em arquivo; num console, roda como `serve`
// (útil para depurar exatamente a linha de comando registrada).
func serviceRun(ctx context.Context, args []string, out, errOut io.Writer) error {
	home, resto := extrairFlagHome(args)
	logArq, resto := extrairFlag(resto, "log")
	if err := definirHome(home); err != nil {
		return err
	}
	sobSCM, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("detectar contexto de execução: %w", err)
	}
	if !sobSCM && logArq == "" {
		fmt.Fprintln(out, "praxis service run: não iniciado pelo SCM — rodando em console (equivale a `praxis serve`)")
		return serve(ctx, resto, out, errOut)
	}

	// Sem console útil (SCM) ou com -log (tarefa de logon do Agendador): log em
	// arquivo rotativo sob PRAXIS_HOME (D8). O SCM inicia o processo em
	// System32 — muda para o home para nenhum caminho relativo cair lá.
	homeReal, err := db.PraxisHome()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(homeReal, 0o755); err != nil {
		return fmt.Errorf("criar PRAXIS_HOME %q: %w", homeReal, err)
	}
	_ = os.Chdir(homeReal)
	if logArq == "" {
		logArq = filepath.Join(homeReal, nomeArquivoLogServico)
	}
	logw, err := abrirArquivoRotativo(logArq, 10<<20, 5)
	if err != nil {
		return err
	}
	defer logw.Close()
	log.SetOutput(logw)
	// Pânico do runtime vai para um arquivo próprio — senão a falha some sem rastro.
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(logArq), "servico-crash.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	}

	if !sobSCM {
		// Tarefa de logon: o Agendador abre uma janela de console para um
		// programa de console. Com o log já em arquivo, solta o console — a
		// janela some. Sinais de console deixam de chegar; o stop é pelo Agendador.
		fecharConsole()
		fmt.Fprintf(logw, "--- praxis %s iniciado como tarefa de logon (PRAXIS_HOME=%s)\n", versao, homeReal)
		err := serve(ctx, resto, logw, logw)
		if err != nil {
			fmt.Fprintf(logw, "serve terminou com erro: %v\n", err)
		}
		return err
	}

	fmt.Fprintf(logw, "--- praxis %s iniciado pelo SCM (PRAXIS_HOME=%s)\n", versao, homeReal)
	h := &handlerSCM{ctx: ctx, args: resto, out: logw}
	if err := svc.Run(nomeServico, h); err != nil {
		fmt.Fprintf(logw, "dispatcher do SCM: %v\n", err)
		return err
	}
	fmt.Fprintln(logw, "--- serviço encerrado")
	return nil
}

// fecharConsole desanexa o processo do console (FreeConsole); se ele era o
// único cliente, a janela fecha. Best-effort.
func fecharConsole() {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole")
	if err := proc.Find(); err == nil {
		_, _, _ = proc.Call()
	}
}

// handlerSCM é a casca do SCM em volta do serve (D2): reporta START_PENDING
// antes de qualquer trabalho, RUNNING logo após lançar o corpo, e trata Stop e
// Shutdown (desligar a máquina não manda Stop) cancelando o contexto com prazo.
type handlerSCM struct {
	ctx  context.Context
	args []string
	out  io.Writer
}

func (h *handlerSCM) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending, WaitHint: uint32(timeoutShutdown / time.Millisecond)}

	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, h.args, h.out, h.out) }()

	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending, WaitHint: uint32(timeoutParadaServico / time.Millisecond)}
				cancel() // pedido de parada graciosa…
				select {
				case err := <-done: // …com prazo: o SCM não espera
					if err != nil {
						fmt.Fprintf(h.out, "serve encerrou com erro: %v\n", err)
					}
				case <-time.After(timeoutParadaServico):
					fmt.Fprintf(h.out, "parada graciosa não terminou em %s; encerrando mesmo assim\n", timeoutParadaServico)
				}
				return false, 0
			}
		case err := <-done:
			// O corpo terminou sozinho (porta ocupada, banco inacessível…):
			// reporta falha ao SCM para as recovery actions agirem.
			if err != nil {
				fmt.Fprintf(h.out, "serve terminou com erro: %v\n", err)
				return true, 1
			}
			return false, 0
		}
	}
}
