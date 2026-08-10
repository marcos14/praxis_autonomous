//go:build windows

package servico

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// timeoutParada é quanto esperamos o serviço chegar a Stopped antes de desistir.
// O `serve` drena as conexões em até 10s (timeoutShutdown) e ainda encerra os
// harnesses, então a margem é generosa de propósito.
const timeoutParada = 45 * time.Second

// abrirGerenciador conecta ao SCM pedindo APENAS os direitos necessários.
//
// mgr.Connect() não serve para consultar: ele pede SC_MANAGER_ALL_ACCESS, que
// falha sem elevação — e aí todo processo não elevado veria "serviço não
// instalado" mesmo com o serviço registrado e rodando. Direito de consulta é
// concedido a qualquer usuário; criar serviço é que exige Administrador.
func abrirGerenciador(direitos uint32) (*mgr.Mgr, error) {
	h, err := windows.OpenSCManager(nil, nil, direitos)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, errors.New("acesso negado ao Gerenciador de Serviços: abra o PowerShell como Administrador")
		}
		return nil, fmt.Errorf("abrir o Gerenciador de Serviços: %w", err)
	}
	return &mgr.Mgr{Handle: h}, nil
}

// abrirServico abre um serviço já registrado com os direitos pedidos. Devolve
// (nil, nil) quando o serviço simplesmente não existe — situação normal, distinta
// de um erro de acesso, que vem como erro.
func abrirServico(nome string, direitos uint32) (*mgr.Service, error) {
	m, err := abrirGerenciador(windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, err
	}
	defer m.Disconnect()
	p, err := windows.UTF16PtrFromString(nome)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenService(m.Handle, p, direitos)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, nil
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, fmt.Errorf("acesso negado ao serviço %q: abra o PowerShell como Administrador", nome)
		}
		return nil, fmt.Errorf("abrir o serviço %q: %w", nome, err)
	}
	return &mgr.Service{Name: nome, Handle: h}, nil
}

// Consultar devolve o estado do serviço. Funciona sem elevação (só direitos de
// consulta), porque `praxis service status` tem de responder a mesma coisa
// rodando como usuário comum e como Administrador.
func Consultar(nome string) (Estado, error) {
	e := Estado{Nome: nome}
	s, err := abrirServico(nome, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return e, err
	}
	if s == nil {
		return e, nil
	}
	defer s.Close()
	e.Instalado = true
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(s.Handle, &st); err == nil {
		e.Rodando = st.CurrentState == windows.SERVICE_RUNNING || st.CurrentState == windows.SERVICE_START_PENDING
		e.Detalhe = estadoEmTexto(st.CurrentState)
	}
	if cfg, err := s.Config(); err == nil {
		e.LinhaComando = cfg.BinaryPathName
		e.Exe = ExeDoComando(cfg.BinaryPathName)
		e.Conta = cfg.ServiceStartName
		e.Habilitado = cfg.StartType == mgr.StartAutomatic
	}
	return e, nil
}

func estadoEmTexto(estado uint32) string {
	switch estado {
	case windows.SERVICE_STOPPED:
		return "parado"
	case windows.SERVICE_START_PENDING:
		return "iniciando"
	case windows.SERVICE_STOP_PENDING:
		return "parando"
	case windows.SERVICE_RUNNING:
		return "rodando"
	case windows.SERVICE_PAUSED:
		return "pausado"
	default:
		return fmt.Sprintf("estado %d", estado)
	}
}

// Instalar registra (ou re-registra) o serviço e o deixa rodando.
//
// É idempotente de propósito: se já existe um serviço com esse nome ele é parado
// e removido antes, e só então recriado com a configuração desta chamada. Manter
// o registro antigo é justamente o que fazia uma máquina continuar rodando um
// binário velho para sempre — reinstalar parecia funcionar, o serviço na verdade
// nunca se mexia.
func Instalar(o Opcoes, log Log) error {
	o = o.ComPadroes()
	if !filepath.IsAbs(o.Exe) {
		return fmt.Errorf("o executável do serviço precisa de caminho absoluto: %q", o.Exe)
	}
	if st, err := Consultar(o.Nome); err == nil && st.Instalado {
		log.avisar("serviço %q já registrado (%s); removendo para registrar de novo", o.Nome, st.LinhaComando)
		if err := Remover(o.Nome, log); err != nil {
			return fmt.Errorf("remover o registro anterior: %w", err)
		}
		// O Windows só solta o registro quando o último handle a ele fecha;
		// recriar com o mesmo nome antes disso falha com "marcado para exclusão".
		esperarSumir(o.Nome, 30*time.Second)
	}

	m, err := abrirGerenciador(windows.SC_MANAGER_CONNECT | windows.SC_MANAGER_CREATE_SERVICE)
	if err != nil {
		return err
	}
	defer m.Disconnect()

	cfg := mgr.Config{
		DisplayName:      o.Display,
		Description:      o.Descricao,
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		ServiceStartName: o.Usuario,
		Password:         o.Senha,
	}
	s, err := m.CreateService(o.Nome, o.Exe, cfg, o.Args...)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			return fmt.Errorf("o serviço %q ainda está sendo removido pelo Windows; feche o services.msc e tente de novo", o.Nome)
		}
		return fmt.Errorf("criar o serviço %q: %w", o.Nome, err)
	}
	defer s.Close()

	// Equivalente ao Restart=on-failure da unit systemd: se o processo morrer sem
	// reportar parada ao SCM, o Windows o sobe de novo em vez de deixar a máquina
	// sem orquestrador até alguém notar.
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400); err != nil {
		// Não é motivo para desfazer a instalação: o serviço está registrado e
		// sobe no boot; só perde o reinício automático em caso de queda.
		log.avisar("serviço %q criado, mas sem reinício automático em caso de falha: %v", o.Nome, err)
	}
	return Iniciar(o.Nome)
}

// Remover para e apaga o registro do serviço. Não mexe em PRAXIS_HOME: o banco,
// os backups e os logs continuam onde estão.
//
// A parada vem antes da exclusão porque o SCM só marca o serviço para remoção
// enquanto ele está no ar — sem parar, o processo continuaria rodando (e segurando
// o binário) depois de um "removido" bem-sucedido.
func Remover(nome string, log Log) error {
	if err := Parar(nome); err != nil {
		log.avisar("parar o serviço %q antes de remover: %v", nome, err)
	}
	s, err := abrirServico(nome, windows.DELETE)
	if err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("serviço %q não está instalado", nome)
	}
	defer s.Close()
	if err := s.Delete(); err != nil {
		return fmt.Errorf("remover o serviço %q: %w", nome, err)
	}
	return nil
}

// Iniciar sobe o serviço. Um serviço já rodando não é erro.
func Iniciar(nome string) error {
	s, err := abrirServico(nome, windows.SERVICE_START|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("serviço %q não está instalado (rode `praxis service install`)", nome)
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return nil
		}
		if errors.Is(err, windows.ERROR_SERVICE_LOGON_FAILED) {
			return fmt.Errorf("iniciar o serviço %q: a conta configurada não tem o direito "+
				"\"Fazer logon como serviço\". Conceda-o em secpol.msc → Políticas locais → "+
				"Atribuição de direitos de usuário, ou reinstale sem -usuario (roda como LocalSystem): %w", nome, err)
		}
		return fmt.Errorf("iniciar o serviço %q: %w", nome, err)
	}
	return nil
}

// Parar pede a parada e espera o serviço chegar a Stopped, para que quem chamou
// possa substituir o binário em seguida. Um serviço já parado não é erro.
func Parar(nome string) error {
	s, err := abrirServico(nome, windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("serviço %q não está instalado", nome)
	}
	defer s.Close()
	st, err := s.Control(svc.Stop)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil
		}
		return fmt.Errorf("parar o serviço %q: %w", nome, err)
	}
	prazo := time.Now().Add(timeoutParada)
	for st.State != svc.Stopped {
		if time.Now().After(prazo) {
			return fmt.Errorf("o serviço %q não parou em %s", nome, timeoutParada)
		}
		time.Sleep(300 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return fmt.Errorf("consultar o serviço %q: %w", nome, err)
		}
	}
	return nil
}

// esperarSumir bloqueia até o registro do serviço desaparecer, ou até o prazo —
// caso em que quem chamou tenta criar de todo modo e reporta o erro real, em vez
// de travar esperando.
func esperarSumir(nome string, prazo time.Duration) {
	limite := time.Now().Add(prazo)
	for {
		st, err := Consultar(nome)
		if err != nil || !st.Instalado || time.Now().After(limite) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// Privilegiado informa se este processo pode registrar/remover serviços e, quando
// não pode, o que falta fazer.
func Privilegiado() (bool, string) {
	if windows.GetCurrentProcessToken().IsElevated() {
		return true, ""
	}
	return false, "abra o PowerShell (ou o Prompt de Comando) com \"Executar como administrador\""
}

// ContaServicoPadrao existe para simetria de API com o Linux; no Windows o
// padrão sem -usuario é LocalSystem e nenhuma conta é criada.
const ContaServicoPadrao = ""

// GarantirContaSistema não se aplica ao Windows (LocalSystem é o default do
// SCM; a restrição de root do Claude é do Linux). Existe para o chamador não
// precisar de build tags.
func GarantirContaSistema() (conta string, criada bool, err error) {
	return "", false, nil
}

// DestinoPadrao é onde o binário do serviço fica: Program Files, e não a pasta de
// onde alguém rodou o instalador — um serviço apontando para Downloads para de
// subir no dia em que a pasta é limpa.
func DestinoPadrao() string {
	base := os.Getenv("ProgramFiles")
	if base == "" {
		base = `C:\Program Files`
	}
	return filepath.Join(base, "Praxis")
}

// EhServicoSCM informa se este processo foi iniciado pelo Gerenciador de Serviços
// do Windows. É a pergunta que o `serve` faz para decidir se roda sob o handler
// de controle: sem isso o SCM derruba o processo com o erro 1053.
func EhServicoSCM() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// manipulador liga o ciclo de vida do SCM ao ctx do corpo do serviço: Stop e
// Shutdown cancelam o ctx, o que dispara o shutdown gracioso do `serve`.
type manipulador struct {
	corpo func(context.Context) error
	err   error
}

func (h *manipulador) Execute(_ []string, pedidos <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const aceita = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	ctx, cancelar := context.WithCancel(context.Background())
	defer cancelar()
	fim := make(chan struct{})
	go func() {
		h.err = h.corpo(ctx)
		close(fim)
	}()

	status <- svc.Status{State: svc.Running, Accepts: aceita}
	for {
		select {
		case p := <-pedidos:
			switch p.Cmd {
			case svc.Interrogate:
				status <- p.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancelar()
				select {
				case <-fim:
				case <-time.After(timeoutParada):
				}
				return false, 0
			}
		case <-fim:
			// O corpo terminou sozinho. Se foi por erro, reporta código de saída
			// específico para o SCM aplicar as ações de recuperação (reiniciar).
			if h.err != nil {
				return true, 1
			}
			return false, 0
		}
	}
}

// RodarComoServico roda corpo sob o controle do SCM até o serviço ser parado.
//
// O nome passado ao SCM é irrelevante para um serviço de processo próprio (o
// Windows ignora o nome da tabela de despacho nesse caso), então um serviço
// registrado com -nome diferente do padrão continua funcionando.
func RodarComoServico(nome string, corpo func(context.Context) error) error {
	h := &manipulador{corpo: corpo}
	if err := svc.Run(nome, h); err != nil {
		return fmt.Errorf("rodar como serviço do Windows: %w", err)
	}
	return h.err
}
