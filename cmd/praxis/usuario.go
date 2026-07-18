package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// usuarioCmd é o subcomando de recuperação/administração de usuários pela linha
// de comando — uma saída de emergência quando não há como usar a UI (ex.: esqueceu
// a senha do único admin). O uso diário é pela interface web.
//
//	praxis usuario add    -nome "Fulano" -email f@x.com -senha ... [-admin=false]
//	praxis usuario reset-senha -email f@x.com -senha nova
//	praxis usuario list
func usuarioCmd(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "uso: praxis usuario <add|reset-senha|list> [flags]")
		return errors.New("subcomando de usuario obrigatório")
	}
	banco, err := db.AbrirPadrao()
	if err != nil {
		return fmt.Errorf("abrir banco: %w", err)
	}
	defer banco.Fechar()

	switch args[0] {
	case "add":
		return usuarioAdd(ctx, banco, args[1:], out, errOut)
	case "reset-senha":
		return usuarioResetSenha(ctx, banco, args[1:], out, errOut)
	case "list":
		return usuarioList(ctx, banco, out)
	default:
		return fmt.Errorf("subcomando de usuario desconhecido: %q", args[0])
	}
}

func usuarioAdd(ctx context.Context, banco *db.DB, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("usuario add", flag.ContinueOnError)
	fs.SetOutput(errOut)
	nome := fs.String("nome", "", "nome do usuário")
	email := fs.String("email", "", "e-mail (login)")
	senha := fs.String("senha", "", "senha inicial")
	admin := fs.Bool("admin", true, "vincula o papel de sistema admin (acesso total)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" || strings.TrimSpace(*senha) == "" {
		return errors.New("-email e -senha são obrigatórios")
	}
	if strings.TrimSpace(*nome) == "" {
		*nome = *email // nome é obrigatório no banco; usa o e-mail como padrão amigável
	}
	var papeis []int64
	if *admin {
		id, err := banco.IDPapelPorNome(ctx, "admin")
		if err != nil {
			return fmt.Errorf("obter papel admin: %w", err)
		}
		papeis = []int64{id}
	}
	u, err := banco.CriarUsuario(ctx, *nome, *email, *senha, papeis)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "usuário criado: #%d %s <%s>%s\n", u.ID, u.Nome, u.Email, sufixoAdmin(*admin))
	return nil
}

func usuarioResetSenha(ctx context.Context, banco *db.DB, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("usuario reset-senha", flag.ContinueOnError)
	fs.SetOutput(errOut)
	email := fs.String("email", "", "e-mail do usuário")
	senha := fs.String("senha", "", "nova senha")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" || strings.TrimSpace(*senha) == "" {
		return errors.New("-email e -senha são obrigatórios")
	}
	id, err := banco.IDUsuarioPorEmail(ctx, *email)
	if err != nil {
		return fmt.Errorf("localizar usuário: %w", err)
	}
	if err := banco.DefinirSenha(ctx, id, *senha); err != nil {
		return err
	}
	fmt.Fprintf(out, "senha redefinida para %s\n", *email)
	return nil
}

func usuarioList(ctx context.Context, banco *db.DB, out io.Writer) error {
	usuarios, err := banco.ListarUsuarios(ctx)
	if err != nil {
		return err
	}
	if len(usuarios) == 0 {
		fmt.Fprintln(out, "(nenhum usuário cadastrado)")
		return nil
	}
	for _, u := range usuarios {
		nomes := make([]string, 0, len(u.Papeis))
		for _, p := range u.Papeis {
			nomes = append(nomes, p.Nome)
		}
		estado := "ativo"
		if !u.Ativo {
			estado = "inativo"
		}
		fmt.Fprintf(out, "#%d  %-30s  %-8s  papéis: %s\n", u.ID, u.Email, estado, strings.Join(nomes, ", "))
	}
	return nil
}

func sufixoAdmin(admin bool) string {
	if admin {
		return " (admin)"
	}
	return ""
}
