// Command praxis é o binário único do Praxis Autonomous.
//
// Uso diário: apenas `praxis serve` sobe o serviço (HTTP + scheduler + worker
// pool). Todo o resto da operação acontece na interface web. Nesta Fase 0 o
// subcomando `serve` é um stub — o servidor é implementado a partir da Fase 1b.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// versao é a versão do binário. Substituível em build via -ldflags.
var versao = "0.0.0-dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

// run é o ponto de entrada testável: interpreta as flags globais e despacha
// para o subcomando correspondente.
func run(args []string, out, errOut io.Writer) error {
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
		return serve(rest[1:], out)
	default:
		return fmt.Errorf("subcomando desconhecido: %q", rest[0])
	}
}

// serve sobe o serviço. Stub da Fase 0: o servidor HTTP, o scheduler e o worker
// pool são adicionados a partir da Fase 1b.
func serve(args []string, out io.Writer) error {
	fmt.Fprintln(out, "praxis serve: stub — servidor ainda não implementado (a partir da Fase 1b)")
	return nil
}
