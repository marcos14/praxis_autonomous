// Command praxis é o binário único do Praxis Autonomous.
//
// Uso diário: apenas `praxis serve` sobe o serviço (HTTP + scheduler + worker
// pool). Todo o resto da operação acontece na interface web. Nesta Fase 1b o
// `serve` já inicializa o banco (Fase 1a) e sobe o servidor HTTP com health
// check e shutdown gracioso; scheduler e worker pool entram nas fases seguintes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/api"
	"github.com/marcos14/praxis-autonomous/internal/db"
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
		fmt.Fprintln(errOut, "  serve   sobe o serviço (HTTP + scheduler)")
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

	srv := api.Novo(api.Opcoes{Banco: banco, Log: logger})
	return servirHTTP(ctx, *addr, srv.Handler(), out, logger)
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
