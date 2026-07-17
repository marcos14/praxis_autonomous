package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/importador"
)

// importar é o subcomando opcional que traz projetos do Praxis clássico
// (automacao/autopilot.json + fases.csv) para o banco (Fase 5e). Idempotente:
// reimportar a mesma pasta não duplica. Uso: praxis import <pasta> [<pasta>...]
func importarCmd(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	pastas := fs.Args()
	if len(pastas) == 0 {
		return fmt.Errorf("informe ao menos uma pasta de projeto (com automacao/autopilot.json)")
	}

	banco, err := db.AbrirPadrao()
	if err != nil {
		return fmt.Errorf("abrir banco: %w", err)
	}
	defer banco.Fechar()

	for _, pasta := range pastas {
		res, err := importador.Importar(ctx, banco, pasta)
		if err != nil {
			fmt.Fprintf(errOut, "erro ao importar %q: %v\n", pasta, err)
			continue
		}
		if !res.Criado {
			fmt.Fprintf(out, "já importado: %s (projeto #%d) — nada a fazer\n", res.Nome, res.ProjectID)
			continue
		}
		fmt.Fprintf(out, "importado: %s (projeto #%d), %d fase(s) pendente(s)\n", res.Nome, res.ProjectID, res.Fases)
	}
	return nil
}
