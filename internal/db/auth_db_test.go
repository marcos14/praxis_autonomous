package db

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// idPapelAdmin resolve o id do papel de sistema `admin` (semeado na migração 6).
func idPapelAdmin(t *testing.T, d *DB) int64 {
	t.Helper()
	papeis, err := d.ListarPapeis(context.Background())
	if err != nil {
		t.Fatalf("ListarPapeis: %v", err)
	}
	for _, p := range papeis {
		if p.Nome == "admin" {
			return p.ID
		}
	}
	t.Fatal("papel admin não foi semeado pela migração")
	return 0
}

func TestSeedPapelAdmin(t *testing.T) {
	d := abrirTemp(t)
	id := idPapelAdmin(t, d)
	p, err := d.ObterPapel(context.Background(), id)
	if err != nil {
		t.Fatalf("ObterPapel: %v", err)
	}
	if !p.Sistema {
		t.Error("papel admin deveria ter sistema=true")
	}
	if len(p.Permissoes) != 1 || p.Permissoes[0] != PermCuringa {
		t.Errorf("permissões do admin = %v, quero [%q]", p.Permissoes, PermCuringa)
	}
}

func TestCriarAutenticarUsuario(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	admin := idPapelAdmin(t, d)

	u, err := d.CriarUsuario(ctx, "Teste", "Teste@Exemplo.com.BR", "segredo123", []int64{admin})
	if err != nil {
		t.Fatalf("CriarUsuario: %v", err)
	}
	if u.Email != "teste@exemplo.com.br" {
		t.Errorf("email não foi normalizado: %q", u.Email)
	}
	if len(u.Papeis) != 1 || u.Papeis[0].Nome != "admin" {
		t.Errorf("papéis = %+v, quero [admin]", u.Papeis)
	}

	// Login case-insensitive no e-mail, senha correta.
	got, err := d.AutenticarUsuario(ctx, "teste@exemplo.com.br", "segredo123")
	if err != nil {
		t.Fatalf("AutenticarUsuario válido: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("id autenticado = %d, quero %d", got.ID, u.ID)
	}

	// Senha errada → ErrCredenciais.
	if _, err := d.AutenticarUsuario(ctx, "teste@exemplo.com.br", "errada"); !errors.Is(err, ErrCredenciais) {
		t.Errorf("senha errada: erro = %v, quero ErrCredenciais", err)
	}
	// E-mail inexistente → ErrCredenciais (não vaza que não existe).
	if _, err := d.AutenticarUsuario(ctx, "ninguem@x.com", "x"); !errors.Is(err, ErrCredenciais) {
		t.Errorf("inexistente: erro = %v, quero ErrCredenciais", err)
	}
}

func TestUsuarioInativoNaoAutentica(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	u, err := d.CriarUsuario(ctx, "Ana", "ana@x.com", "senha-forte", nil)
	if err != nil {
		t.Fatalf("CriarUsuario: %v", err)
	}
	if _, err := d.AtualizarUsuario(ctx, u.ID, "Ana", "ana@x.com", false, nil); err != nil {
		t.Fatalf("AtualizarUsuario: %v", err)
	}
	if _, err := d.AutenticarUsuario(ctx, "ana@x.com", "senha-forte"); !errors.Is(err, ErrCredenciais) {
		t.Errorf("inativo: erro = %v, quero ErrCredenciais", err)
	}
}

func TestEmailDuplicado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	if _, err := d.CriarUsuario(ctx, "A", "dup@x.com", "senha-longa", nil); err != nil {
		t.Fatalf("CriarUsuario 1: %v", err)
	}
	// Mesmo e-mail em outra caixa → normaliza e colide.
	if _, err := d.CriarUsuario(ctx, "B", "DUP@x.com", "senha-longa", nil); !errors.Is(err, ErrEmailDuplicado) {
		t.Errorf("erro = %v, quero ErrEmailDuplicado", err)
	}
}

func TestPermissoesDoUsuarioUniao(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	p1, err := d.CriarPapel(ctx, "Criador", "", []string{PermDemandasCriar})
	if err != nil {
		t.Fatalf("CriarPapel 1: %v", err)
	}
	p2, err := d.CriarPapel(ctx, "Respondedor", "", []string{PermDemandasResponder, PermDemandasOperar})
	if err != nil {
		t.Fatalf("CriarPapel 2: %v", err)
	}
	u, err := d.CriarUsuario(ctx, "Uni", "uni@x.com", "senha-longa", []int64{p1.ID, p2.ID})
	if err != nil {
		t.Fatalf("CriarUsuario: %v", err)
	}
	perms, err := d.PermissoesDoUsuario(ctx, u.ID)
	if err != nil {
		t.Fatalf("PermissoesDoUsuario: %v", err)
	}
	for _, q := range []string{PermDemandasCriar, PermDemandasResponder, PermDemandasOperar} {
		if !perms[q] {
			t.Errorf("faltou permissão %q em %v", q, perms)
		}
	}
	if perms[PermCuringa] {
		t.Errorf("não deveria ter curinga: %v", perms)
	}
}

func TestPapelPermissaoInvalida(t *testing.T) {
	d := abrirTemp(t)
	if _, err := d.CriarPapel(context.Background(), "X", "", []string{"nao.existe"}); !errors.Is(err, ErrPermissaoInvalida) {
		t.Errorf("erro = %v, quero ErrPermissaoInvalida", err)
	}
}

func TestPapelSistemaProtegido(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	admin := idPapelAdmin(t, d)
	if _, err := d.AtualizarPapel(ctx, admin, "root", "", []string{PermCuringa}); !errors.Is(err, ErrPapelSistema) {
		t.Errorf("atualizar sistema: erro = %v, quero ErrPapelSistema", err)
	}
	if err := d.ExcluirPapel(ctx, admin); !errors.Is(err, ErrPapelSistema) {
		t.Errorf("excluir sistema: erro = %v, quero ErrPapelSistema", err)
	}
}

func TestContarUsuariosEAdmins(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	if n, _ := d.ContarUsuarios(ctx); n != 0 {
		t.Fatalf("ContarUsuarios inicial = %d, quero 0", n)
	}
	admin := idPapelAdmin(t, d)
	if _, err := d.CriarUsuario(ctx, "Root", "root@x.com", "senha-longa", []int64{admin}); err != nil {
		t.Fatalf("CriarUsuario admin: %v", err)
	}
	if _, err := d.CriarUsuario(ctx, "Leitor", "leitor@x.com", "senha-longa", nil); err != nil {
		t.Fatalf("CriarUsuario leitor: %v", err)
	}
	if n, _ := d.ContarUsuarios(ctx); n != 2 {
		t.Errorf("ContarUsuarios = %d, quero 2", n)
	}
	if n, _ := d.ContarAdmins(ctx); n != 1 {
		t.Errorf("ContarAdmins = %d, quero 1", n)
	}
}

func TestPapelInexistenteAoCriarUsuario(t *testing.T) {
	d := abrirTemp(t)
	if _, err := d.CriarUsuario(context.Background(), "X", "x@x.com", "senha-longa", []int64{99999}); !errors.Is(err, ErrPapelInexistente) {
		t.Errorf("erro = %v, quero ErrPapelInexistente", err)
	}
}

func TestJWTSecretPersistenteEEstavel(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	s1, err := d.ObterOuGerarJWTSecret(ctx)
	if err != nil {
		t.Fatalf("ObterOuGerarJWTSecret 1: %v", err)
	}
	if len(s1) == 0 {
		t.Fatal("segredo vazio")
	}
	s2, err := d.ObterOuGerarJWTSecret(ctx)
	if err != nil {
		t.Fatalf("ObterOuGerarJWTSecret 2: %v", err)
	}
	if !bytes.Equal(s1, s2) {
		t.Error("segredo mudou entre chamadas — deveria ser estável")
	}
}

func TestJWTSecretEnvOverride(t *testing.T) {
	d := abrirTemp(t)
	t.Setenv(nomeEnvJWTSecret, "segredo-do-ambiente")
	s, err := d.ObterOuGerarJWTSecret(context.Background())
	if err != nil {
		t.Fatalf("ObterOuGerarJWTSecret: %v", err)
	}
	if string(s) != "segredo-do-ambiente" {
		t.Errorf("segredo = %q, quero o do ambiente", s)
	}
}

func TestHashSenhaVerifica(t *testing.T) {
	h, err := HashSenha("minha-senha")
	if err != nil {
		t.Fatalf("HashSenha: %v", err)
	}
	if !VerificarSenha(h, "minha-senha") {
		t.Error("senha correta deveria verificar")
	}
	if VerificarSenha(h, "outra") {
		t.Error("senha errada não deveria verificar")
	}
	if _, err := HashSenha("   "); !errors.Is(err, ErrSenhaVazia) {
		t.Errorf("senha vazia: erro = %v, quero ErrSenhaVazia", err)
	}
}
