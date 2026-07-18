// Command praxis é o binário único do Praxis Autonomous.
//
// Uso diário: apenas `praxis serve` sobe o serviço (HTTP + scheduler + worker
// pool). Todo o resto da operação acontece na interface web. Nesta Fase 1b o
// `serve` já inicializa o banco (Fase 1a) e sobe o servidor HTTP com health
// check e shutdown gracioso; scheduler e worker pool entram nas fases seguintes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/api"
	"github.com/marcos14/praxis-autonomous/internal/consultor"
	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/intake"
	"github.com/marcos14/praxis-autonomous/internal/manutencao"
	"github.com/marcos14/praxis-autonomous/internal/notify"
	"github.com/marcos14/praxis-autonomous/internal/procs"
	"github.com/marcos14/praxis-autonomous/internal/scheduler"
)

// versao é a versão do binário. Substituível em build via -ldflags.
var versao = "0.0.0-dev"

// enderecoPadrao é o bind default do servidor HTTP. Restrito ao loopback: o
// acesso externo se dá por túnel/reverse-proxy, como no Praxis atual.
const enderecoPadrao = "127.0.0.1:7799"

// timeoutShutdown é o prazo para o servidor drenar conexões em andamento antes
// de forçar o encerramento.
const timeoutShutdown = 10 * time.Second

func main() {
	// Cancela o contexto no primeiro SIGINT/SIGTERM, disparando o shutdown
	// gracioso. Um segundo sinal encerra o processo abruptamente (stop restaura
	// o comportamento default do sinal).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

// run é o ponto de entrada testável: interpreta as flags globais e despacha
// para o subcomando correspondente.
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("praxis", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprintln(errOut, "uso: praxis [-version] <subcomando> [flags]")
		fmt.Fprintln(errOut, "subcomandos:")
		fmt.Fprintln(errOut, "  serve    sobe o serviço (HTTP + scheduler)")
		fmt.Fprintln(errOut, "  usuario  administra usuários pela CLI (add|reset-senha|list)")
		fmt.Fprintln(errOut)
		fs.PrintDefaults()
	}
	mostrarVersao := fs.Bool("version", false, "exibe a versão e sai")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *mostrarVersao {
		fmt.Fprintln(out, versao)
		return nil
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("subcomando obrigatório (ex.: serve)")
	}

	switch rest[0] {
	case "serve":
		return serve(ctx, rest[1:], out, errOut)
	case "service":
		return service(rest[1:], out, errOut)
	case "import":
		return importarCmd(ctx, rest[1:], out, errOut)
	case "usuario":
		return usuarioCmd(ctx, rest[1:], out, errOut)
	default:
		return fmt.Errorf("subcomando desconhecido: %q", rest[0])
	}
}

// serve inicializa o banco e sobe o servidor HTTP, bloqueando até que ctx seja
// cancelado (SIGINT/SIGTERM) — quando faz um shutdown gracioso — ou o servidor
// falhe. Retorna nil quando o encerramento é limpo.
func serve(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	addr := fs.String("addr", enderecoPadrao, "endereço TCP de bind do servidor HTTP")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(errOut, nil))
	api.Versao = versao

	banco, err := db.AbrirPadrao()
	if err != nil {
		return fmt.Errorf("abrir banco: %w", err)
	}
	defer func() {
		if err := banco.Fechar(); err != nil {
			logger.Error("fechar banco", "erro", err)
		}
	}()
	logger.Info("banco aberto", "caminho", banco.Caminho)

	// Garante que o segredo de assinatura do JWT exista já no boot (gerado e
	// persistido na primeira vez), evitando latência/erro na primeira autenticação.
	// Best-effort: uma falha aqui não impede subir (o segredo é resolvido de novo
	// preguiçosamente no middleware).
	if _, err := banco.ObterOuGerarJWTSecret(ctx); err != nil {
		logger.Warn("preparar segredo do jwt", "erro", err)
	}

	// Dependências compartilhadas do ciclo de execução: operações git (mutex por
	// projeto) e o registro de PIDs dos harnesses (para matar órfãos no boot).
	git := gitops.Novo()
	registro := registroPIDs(logger)

	// Recuperação pós-restart (Fase 2i): antes de servir, faz o prune dos
	// worktrees, mata processos de harness órfãos de uma queda anterior e
	// re-enfileira as demandas presas em `executando`. Best-effort: uma falha aqui
	// não impede o serviço de subir.
	recuperarPosRestart(ctx, banco, git, registro, logger)

	// Intake (Fases 3b/3c): dispara o analista readonly (perguntas) e o planejador
	// (plano + fases) em background. Roda com o ctx de vida do serviço (cancelado
	// no shutdown); Aguardar drena as análises/planejamentos em voo antes de fechar
	// o banco.
	intakeSvc := novoIntake(ctx, banco, git, logger)
	defer intakeSvc.Aguardar()

	// Consultas: dispara o consultor (chat de análise para produto/suporte) e a
	// geração de overview de projeto em background — mesma vida do intake.
	consultorSvc := novoConsultor(ctx, banco, git, logger)
	defer consultorSvc.Aguardar()

	// Scheduler (fecha 2g.n1): executa as demandas em background — a demanda
	// aprovada "anda sozinha" (executor→gates→corretor→revisor→commit por fase).
	// É passado à API como ControladorExecucao para pausar/cancelar interromperem
	// o worker ao vivo.
	sched := iniciarScheduler(ctx, banco, git, registro, logger)

	// Notificações (Fase 4e): despachante em background que tail-a os eventos do
	// banco e envia para os canais configurados (config global "notificacoes").
	iniciarNotificacoes(ctx, banco, logger)

	// Manutenção (Fase 5d): backup periódico do banco, rotação e retenção de
	// logs/eventos, em background ligado ao ctx de vida do serviço.
	iniciarManutencao(ctx, banco, logger)

	opts := api.Opcoes{Banco: banco, Log: logger, Git: git, Intake: intakeSvc,
		Planejamento: intakeSvc, Consultas: consultorSvc}
	if sched != nil {
		opts.Exec = sched
	}
	srv := api.Novo(opts)
	return servirHTTP(ctx, *addr, srv.Handler(), out, logger)
}

// registroPIDs cria o registro de PIDs dos harnesses em PRAXIS_HOME/pids. Falha
// (PRAXIS_HOME indisponível) vira aviso e nil — o serviço sobe sem registro (os
// órfãos não serão mortos no boot, mas o resto opera).
func registroPIDs(logger *slog.Logger) *procs.Registro {
	home, err := db.PraxisHome()
	if err != nil {
		logger.Warn("registro de PIDs: resolver PRAXIS_HOME", "erro", err)
		return nil
	}
	registro, err := procs.NovoRegistro(filepath.Join(home, "pids"))
	if err != nil {
		logger.Warn("registro de PIDs", "erro", err)
		return nil
	}
	return registro
}

// novoIntake monta o serviço de intake (Fase 3b) com a pasta de logs em
// PRAXIS_HOME/logs e o ctx de vida do serviço. Falha ao resolver PRAXIS_HOME não
// impede subir: o intake grava os .jsonl no diretório de trabalho (o analista
// ainda funciona).
func novoIntake(ctx context.Context, banco *db.DB, git *gitops.Ops, logger *slog.Logger) *intake.Servico {
	dirLogs := ""
	if home, err := db.PraxisHome(); err == nil {
		dirLogs = filepath.Join(home, "logs")
	} else {
		logger.Warn("intake: resolver PRAXIS_HOME para logs", "erro", err)
	}
	return intake.NovoServico(intake.OpcoesServico{
		Store:   banco,
		DirLogs: dirLogs,
		Ctx:     ctx,
		Log:     func(msg string) { logger.Info(msg) },
		Git:     git,
	})
}

// novoConsultor monta o serviço de consultas (chat de análise de código para
// produto/suporte) com a mesma pasta de logs e ctx de vida do intake. O Ops de
// git é o mesmo do scheduler — o mutex por projeto serializa pull e worktree.
func novoConsultor(ctx context.Context, banco *db.DB, git *gitops.Ops, logger *slog.Logger) *consultor.Servico {
	dirLogs := ""
	if home, err := db.PraxisHome(); err == nil {
		dirLogs = filepath.Join(home, "logs")
	} else {
		logger.Warn("consultor: resolver PRAXIS_HOME para logs", "erro", err)
	}
	return consultor.NovoServico(consultor.OpcoesServico{
		Store:   banco,
		DirLogs: dirLogs,
		Ctx:     ctx,
		Log:     func(msg string) { logger.Info(msg) },
		Git:     git,
	})
}

// iniciarNotificacoes sobe o despachante de notificações (Fase 4e) em uma
// goroutine ligada ao ctx de vida do serviço. A config de canais/eventos é lida
// da config global (chave "notificacoes") a cada ciclo — mudanças na tela de
// Configurações valem sem reiniciar. Sem canal ativo, o despachante só avança o
// cursor (nenhum envio).
func iniciarNotificacoes(ctx context.Context, banco *db.DB, logger *slog.Logger) {
	provedor := func(ctx context.Context) (notify.Config, error) {
		entradas, err := banco.ObterConfigGlobal(ctx)
		if err != nil {
			return notify.Config{}, err
		}
		bruto, ok := entradas["notificacoes"]
		if !ok || len(bruto) == 0 {
			return notify.Config{}, nil
		}
		var cfg notify.Config
		if err := json.Unmarshal(bruto, &cfg); err != nil {
			return notify.Config{}, err
		}
		return cfg, nil
	}
	desp := notify.NovoDespachante(notify.OpcoesDespachante{
		Fonte:  banco,
		Config: provedor,
		Log:    func(msg string) { logger.Info(msg) },
	})
	go desp.Rodar(ctx)
}

// iniciarManutencao sobe a rotina de manutenção (Fase 5d) em background: backup
// periódico do banco em PRAXIS_HOME/backups, rotação e retenção de logs/eventos.
// Sem PRAXIS_HOME resolvido, não sobe (só loga um aviso).
func iniciarManutencao(ctx context.Context, banco *db.DB, logger *slog.Logger) {
	home, err := db.PraxisHome()
	if err != nil {
		logger.Warn("manutenção: resolver PRAXIS_HOME", "erro", err)
		return
	}
	m := manutencao.Nova(manutencao.Opcoes{
		Store:      banco,
		DirBackups: filepath.Join(home, "backups"),
		DirLogs:    filepath.Join(home, "logs"),
		Log:        func(msg string) { logger.Info(msg) },
	})
	go m.Rodar(ctx)
}

// recuperarPosRestart executa a recuperação de boot da Fase 2i (prune de
// worktrees, morte de processos órfãos e refila das demandas `executando`). É
// best-effort: qualquer falha vira log e o serviço sobe mesmo assim. Recebe as
// dependências compartilhadas (git/registro) para usar as mesmas instâncias do
// scheduler (mesmo mutex por projeto, mesmo registro de PIDs).
func recuperarPosRestart(ctx context.Context, banco *db.DB, git *gitops.Ops, registro *procs.Registro, logger *slog.Logger) {
	rec, err := scheduler.RecuperarPosRestart(ctx, banco, git, registro, func(msg string) { logger.Info(msg) })
	if err != nil {
		logger.Warn("recuperação pós-restart", "erro", err)
		return
	}
	logger.Info("recuperação pós-restart concluída",
		"projetos", rec.ProjetosPreparados, "orfaos", rec.OrfaosMortos, "demandas_refiladas", rec.DemandasRefiladas)
}

// servirHTTP abre o listener em addr e delega a servirListener. Separar a
// abertura do listener permite testar o ciclo servir/shutdown com um listener
// controlado pelo teste.
func servirHTTP(ctx context.Context, addr string, h http.Handler, out io.Writer, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("escutar em %s: %w", addr, err)
	}
	return servirListener(ctx, ln, h, out, logger)
}

// servirListener serve h em ln até ctx ser cancelado, quando faz o shutdown
// gracioso (drena conexões por até timeoutShutdown). Retorna nil no encerramento
// limpo.
func servirListener(ctx context.Context, ln net.Listener, h http.Handler, out io.Writer, logger *slog.Logger) error {
	servidor := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	fmt.Fprintf(out, "praxis serve: ouvindo em http://%s\n", ln.Addr())
	logger.Info("servidor no ar", "addr", ln.Addr().String())

	errServe := make(chan error, 1)
	go func() { errServe <- servidor.Serve(ln) }()

	select {
	case err := <-errServe:
		// Serve só retorna com erro real aqui (nunca ErrServerClosed, que só
		// vem após Shutdown/Close).
		return err
	case <-ctx.Done():
		logger.Info("sinal de encerramento recebido, drenando conexões")
		ctxShutdown, cancel := context.WithTimeout(context.Background(), timeoutShutdown)
		defer cancel()
		if err := servidor.Shutdown(ctxShutdown); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		// Aguarda o Serve retornar (deve ser ErrServerClosed após o Shutdown).
		if err := <-errServe; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		logger.Info("servidor encerrado")
		return nil
	}
}
